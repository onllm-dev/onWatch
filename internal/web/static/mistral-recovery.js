// Shared by the dashboard and quick view. No credentials are handled here.
(() => {
  const escape = value => String(value || '').replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
  class MistralRecovery {
    constructor({root, endpoint, refresh, render, grant}) {
      Object.assign(this, {root, endpoint, refresh, render, grant, granting:false, busy:false, feedback:'', timer:null, controller:null, deadline:0, suspended:false});
      this.click = event => {
        if (event.target.closest('[data-mistral-grant]')) {
          event.preventDefault(); event.stopPropagation(); this.requestGrant(); return;
        }
        if (!event.target.closest('[data-mistral-retry]')) return;
        event.preventDefault(); event.stopPropagation();
        void this.retry();
      };
      root.addEventListener('click', this.click);
      this.hide = () => { this.suspended = true; this.stop(); };
      this.visibility = () => { this.suspended = document.hidden; if (document.hidden) this.stop(); else if (this.connection?.retrying) this.watch(); };
      window.addEventListener('pagehide', this.hide);
      document.addEventListener('visibilitychange', this.visibility);
    }
    markup(connection, status) {
      this.connection = connection || {};
      const c = this.connection;
      if (status === 'ok' && !c.reason && !c.retrying && !this.busy && !this.granting) this.feedback = '';
      if (c.retrying && !document.hidden && !this.suspended && !this.deadline) this.watch();
      let message = c.message || (status === 'reconnect' ? 'Reconnect to Mistral, then retry connection.' : status === 'stale' ? 'Mistral updates are unavailable.' : status === 'partial' ? 'Some Mistral data is unavailable.' : status === 'waiting' ? 'Waiting for Mistral connection.' : '');
      if (this.grant && c.reason === 'browser_access_denied') message = 'Choose Grant Browser Access and confirm the browser folder. onWatch will then retry automatically.';
      if (!message && !this.busy && !this.granting && !c.retrying && !this.feedback) return '';
      const waiting = this.busy || c.retrying || this.granting;
      if (waiting) message = 'Checking Mistral connection. Allow a browser credential prompt if one appears.';
      if (this.granting) message = 'Waiting for browser access…';
      const next = !waiting && c.nextRetryAt ? ` Next automatic attempt: ${new Date(c.nextRetryAt).toLocaleTimeString()}.` : '';
      const grantButton = this.grant && c.reason === 'browser_access_denied' ? `<button type="button" class="mistral-retry" data-mistral-grant ${waiting ? 'disabled' : ''}>Grant Browser Access</button>` : '';
      return `<div class="mistral-recovery"><span role="status">${escape(this.feedback || message)}${escape(next)}</span><div class="mistral-recovery-actions">${grantButton}<button type="button" class="mistral-retry" data-mistral-retry ${waiting || !c.canRetry ? 'disabled' : ''}>${waiting && !this.granting ? 'Checking connection...' : 'Retry connection'}</button></div></div>`;
    }
    requestGrant() {
      if (!this.grant || this.granting || this.busy || this.connection?.retrying || this.connection?.reason !== 'browser_access_denied') return;
      this.stop(); this.suspended = false; this.granting = true; this.feedback = ''; this.render();
      try { if (!this.grant()) this.grantResult('unavailable'); }
      catch (_) { this.grantResult('unavailable'); }
    }
    grantResult(result) {
      // No picker is open; a page that missed the completion must not keep waiting.
      if (result === 'idle') { if (this.granting) { this.granting = false; this.render(); } return; }
      this.granting = result === 'pending';
      const messages = {
        cancelled: 'Browser access request cancelled.',
        unavailable: 'Browser access is still unavailable. Try Grant Browser Access again.',
        busy: 'A browser access request is already open. Complete that request first.',
        retry_failed: 'Could not start the retry. Check connection status, then use Retry connection.',
      };
      this.feedback = messages[result] || '';
      this.render();
      if (result === 'retrying' && !this.suspended && !document.hidden) this.watch();
    }
    async request(url, options = {}) {
      const controller = new AbortController();
      this.controller = controller;
      const timeout = setTimeout(() => controller.abort(), 10000);
      try { return await fetch(url, {...options, signal:controller.signal, credentials:'same-origin'}); }
      finally { clearTimeout(timeout); if (this.controller === controller) this.controller = null; }
    }
    async retry() {
      if (this.granting || this.busy || this.connection?.retrying || !this.connection?.canRetry) return;
      this.suspended = false;
      this.busy = true; this.feedback = ''; this.render();
      try {
        const response = await this.request(this.endpoint, {method:'POST', headers:{'X-Requested-With':'XMLHttpRequest'}});
        if (response.status !== 202) {
          this.feedback = response.status === 429 ? `Please wait ${response.headers.get('Retry-After') || '30'} seconds before retrying.` : response.status === 401 ? 'Sign in to onWatch to retry Mistral.' : response.status === 409 ? 'Mistral polling is unavailable. Check provider settings.' : 'Could not request a retry. Try again.';
        }
      } catch (error) {
        if (!this.suspended) this.feedback = error.name === 'AbortError' ? 'Retry request timed out. Check connection status before trying again.' : 'Could not contact onWatch. Try again.';
      } finally {
        this.busy = false; this.render();
        if (!document.hidden && !this.suspended) this.watch();
      }
    }
    watch() {
      if (this.timer || this.controller || this.suspended) return;
      if (!this.deadline) this.deadline = Date.now() + 210000;
      this.timer = setTimeout(() => void this.tick(), 2000);
    }
    async tick() {
      clearTimeout(this.timer);
      this.timer = null;
      if (this.suspended || document.hidden || Date.now() >= this.deadline) { this.stop(); return; }
      const controller = new AbortController();
      this.controller = controller;
      const timeout = setTimeout(() => controller.abort(), 10000);
      try {
        const c = await this.refresh(controller.signal);
        if (c && !c.retrying && (c.canRetry || !c.reason)) {
          this.feedback = ''; this.deadline = 0; this.render(); return;
        }
      } catch (error) {
        if (error.name !== 'AbortError') { this.feedback = 'Could not refresh connection status.'; this.render(); }
      } finally {
        clearTimeout(timeout);
        if (this.controller === controller) this.controller = null;
      }
      if (this.deadline) this.watch();
    }
    stop() {
      clearTimeout(this.timer); this.timer = null; this.deadline = 0;
      if (this.controller) this.controller.abort();
      this.controller = null;
    }
    dispose() {
      this.hide(); this.root.removeEventListener('click', this.click);
      window.removeEventListener('pagehide', this.hide);
      document.removeEventListener('visibilitychange', this.visibility);
    }
  }
  window.MistralRecovery = MistralRecovery;
})();
