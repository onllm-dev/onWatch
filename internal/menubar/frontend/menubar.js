(function () {
  const bridge = createBridge();

  function createBridge() {
    if (window.go && window.go.menubar && window.go.menubar.App) {
      const app = window.go.menubar.App;
      return {
        mode: 'wails',
        getSnapshot: () => app.GetSnapshot(),
        getSettings: () => app.GetSettings(),
        openExternal: (url) => app.OpenExternal(url),
        refresh: () => app.Refresh(),
      };
    }

    const browserBridge = window.__ONWATCH_MENUBAR_BRIDGE__ || {};
    return {
      mode: browserBridge.mode || 'browser',
      requestedView: browserBridge.view || '',
      getSettings: async () => {
        const settings = Object.assign({}, browserBridge.settings || {});
        if (browserBridge.view) {
          settings.default_view = browserBridge.view;
        }
        return settings;
      },
      getSnapshot: async () => {
        const view = encodeURIComponent(browserBridge.view || 'standard');
        const resp = await fetch(`/api/menubar/summary?view=${view}`, { credentials: 'same-origin' });
        if (!resp.ok) {
          const err = new Error(`menubar summary failed: ${resp.status}`);
          err.status = resp.status;
          throw err;
        }
        return resp.json();
      },
      openExternal: (url) => window.open(url, '_blank', 'noopener,noreferrer'),
      refresh: async () => {},
    };
  }

  const icons = {
    github: '<svg viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><path d="M12 1.5A10.5 10.5 0 0 0 8.68 22c.53.1.72-.23.72-.5v-1.95c-2.95.64-3.57-1.26-3.57-1.26-.48-1.22-1.17-1.54-1.17-1.54-.96-.66.07-.64.07-.64 1.06.08 1.62 1.08 1.62 1.08.95 1.62 2.48 1.15 3.08.88.1-.68.37-1.15.67-1.42-2.36-.27-4.84-1.18-4.84-5.26 0-1.16.42-2.1 1.08-2.84-.1-.27-.46-1.38.1-2.87 0 0 .9-.29 2.95 1.08a10.2 10.2 0 0 1 5.37 0c2.05-1.37 2.95-1.08 2.95-1.08.56 1.5.2 2.6.1 2.87.68.74 1.08 1.68 1.08 2.84 0 4.09-2.48 4.98-4.86 5.24.38.33.72.98.72 1.98v2.94c0 .28.2.6.73.5A10.5 10.5 0 0 0 12 1.5Z"/></svg>',
    support: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true"><path d="M9.1 9a3 3 0 1 1 5.8 1c-.3.85-1 1.3-1.7 1.77-.82.53-1.65 1.08-1.65 2.23"/><circle cx="12" cy="17" r="1"/></svg>',
    globe: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true"><circle cx="12" cy="12" r="9"/><path d="M3 12h18"/><path d="M12 3c2.6 2.9 4 5.9 4 9s-1.4 6.1-4 9c-2.6-2.9-4-5.9-4-9s1.4-6.1 4-9z"/></svg>'
  };

  function escapeHTML(value) {
    return String(value || '')
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;')
      .replace(/'/g, '&#39;');
  }

  function severityClass(status) {
    return status || 'healthy';
  }

  function circumference(radius) {
    return 2 * Math.PI * radius;
  }

  function meterMarkup(quota) {
    const radius = 24;
    const length = circumference(radius);
    const percent = Math.max(0, Math.min(100, Number(quota.percent || 0)));
    const dashOffset = length - (length * percent / 100);
    return `
      <div class="quota-meter status-${severityClass(quota.status)}">
        <div class="meter-shell">
          <svg class="meter-svg" viewBox="0 0 64 64" aria-hidden="true">
            <circle class="meter-track" cx="32" cy="32" r="${radius}"></circle>
            <circle class="meter-ring" cx="32" cy="32" r="${radius}" stroke-dasharray="${length}" stroke-dashoffset="${dashOffset}"></circle>
          </svg>
          <div class="meter-value">${escapeHTML(quota.display_value || `${percent.toFixed(0)}%`)}</div>
        </div>
        <div class="meter-label">${escapeHTML(quota.label)}</div>
      </div>
    `;
  }

  function trendMarkup(series) {
    const points = Array.isArray(series.points) ? series.points : [];
    if (!points.length) {
      return '';
    }
    const max = Math.max(...points, 100);
    const min = Math.min(...points, 0);
    const range = Math.max(max - min, 1);
    const coords = points.map((value, index) => {
      const x = points.length === 1 ? 0 : (index / (points.length - 1)) * 100;
      const y = 22 - (((value - min) / range) * 22);
      return `${x},${y.toFixed(2)}`;
    }).join(' ');
    return `
      <div class="trend-row status-${severityClass(series.status)}">
        <div class="trend-label">${escapeHTML(series.label)}</div>
        <svg class="trend-line" viewBox="0 0 100 24" preserveAspectRatio="none" aria-hidden="true">
          <polyline points="${coords}"></polyline>
        </svg>
      </div>
    `;
  }

  function providerCardMarkup(provider, view) {
    const quotas = (provider.quotas || []).map(meterMarkup).join('');
    const showTrends = view === 'detailed';
    const trends = showTrends ? (provider.trends || []).map(trendMarkup).join('') : '';
    const percent = Number(provider.highest_percent || 0).toFixed(0);
    const meta = [
      provider.subtitle,
      provider.updated_at ? `Updated ${provider.updated_at}` : '',
    ].filter(Boolean).join(' · ');
    return `
      <details class="provider-card status-${severityClass(provider.status)}" data-view="${escapeHTML(view)}" ${view === 'detailed' ? 'open' : ''}>
        <summary>
          <div>
            <div class="provider-name-row">
              <span class="menubar-status-dot"></span>
              <span class="provider-name">${escapeHTML(provider.label)}</span>
            </div>
            ${meta ? `<div class="provider-meta">${escapeHTML(meta)}</div>` : ''}
          </div>
          <div class="provider-percent">${percent}%</div>
        </summary>
        <div class="provider-body">
          <div class="provider-quotas">${quotas || '<div class="provider-empty">No quota data available yet.</div>'}</div>
          ${trends ? `<div class="provider-trends">${trends}</div>` : ''}
        </div>
      </details>
    `;
  }

  function aggregateLabel(snapshot) {
    if (snapshot.aggregate && snapshot.aggregate.label) {
      return snapshot.aggregate.label;
    }
    return 'Watching your active providers';
  }

  function summaryMarkup(snapshot, providers) {
    const status = severityClass(snapshot.aggregate && snapshot.aggregate.status);
    const aggregate = snapshot.aggregate || {};
    return `
      <section class="menubar-summary">
        <div class="aggregate-card status-${status}">
          <div class="aggregate-percent">${Number(aggregate.highest_percent || 0).toFixed(0)}%</div>
          <div class="aggregate-label">${escapeHTML(snapshot.updated_ago || 'Waiting for quota data')}</div>
        </div>
        <div class="aggregate-stats">
          <div class="aggregate-stat-list">
            <div class="aggregate-stat">
              <strong>${escapeHTML(String(aggregate.provider_count || providers.length || 0))}</strong>
              <span class="aggregate-label">Providers</span>
            </div>
            <div class="aggregate-stat">
              <strong>${escapeHTML(String(aggregate.warning_count || 0))}</strong>
              <span class="aggregate-label">Warnings</span>
            </div>
            <div class="aggregate-stat">
              <strong>${escapeHTML(String(aggregate.critical_count || 0))}</strong>
              <span class="aggregate-label">Critical</span>
            </div>
          </div>
        </div>
      </section>
    `;
  }

  function minimalMarkup(snapshot, providers) {
    const aggregate = snapshot.aggregate || {};
    const status = severityClass(aggregate.status);
    return `
      <section class="minimal-view">
        <div class="aggregate-circle status-${status}">
          <span class="aggregate-percent">${Number(aggregate.highest_percent || 0).toFixed(0)}%</span>
        </div>
        <div class="aggregate-label">${escapeHTML(snapshot.updated_ago || 'Waiting for quota data')}</div>
        <div class="aggregate-status status-${status}">${escapeHTML(aggregate.label || 'All Good')}</div>
        <div class="minimal-stats">
          <span>${escapeHTML(String(aggregate.provider_count || providers.length || 0))} providers</span>
          <span>${escapeHTML(String(aggregate.warning_count || 0))} warnings</span>
          <span>${escapeHTML(String(aggregate.critical_count || 0))} critical</span>
        </div>
      </section>
    `;
  }

  function providerListMarkup(providers, view) {
    if (!providers.length) {
      return '<section class="provider-list"><div class="provider-empty-state">No provider quota data is available yet.</div></section>';
    }
    return `
      <section class="provider-list">
        ${providers.map((provider) => providerCardMarkup(provider, view)).join('')}
      </section>
    `;
  }

  function footerMarkup() {
    return `
      <footer class="menubar-footer" id="footer">
        <div class="menubar-subtitle">Status is computed from the highest-pressure quota in each provider card.</div>
        <div class="footer-links">
          <a href="https://github.com/onllm-dev/onwatch" data-external="true" data-url="https://github.com/onllm-dev/onwatch" aria-label="View on GitHub">${icons.github}</a>
          <a href="https://github.com/onllm-dev/onwatch/issues" data-external="true" data-url="https://github.com/onllm-dev/onwatch/issues" aria-label="Get Support">${icons.support}</a>
          <a href="https://onllm.dev" data-external="true" data-url="https://onllm.dev" aria-label="Visit onllm.dev">${icons.globe}</a>
        </div>
      </footer>
    `;
  }

  function render(snapshot, settings) {
    const root = document.getElementById('menubar-root');
    const status = severityClass(snapshot.aggregate && snapshot.aggregate.status);
    const providers = Array.isArray(snapshot.providers) ? snapshot.providers : [];
    const view = settings.default_view || bridge.requestedView || 'standard';
    let bodyMarkup = '';
    if (view === 'minimal') {
      bodyMarkup = minimalMarkup(snapshot, providers);
    } else if (view === 'detailed') {
      bodyMarkup = summaryMarkup(snapshot, providers) + providerListMarkup(providers, view);
    } else {
      bodyMarkup = summaryMarkup(snapshot, providers) + providerListMarkup(providers, view);
    }

    root.innerHTML = `
      <section class="menubar-panel menubar-view menubar-view-${escapeHTML(view)}">
        <header class="menubar-header status-${status}">
          <div>
            <div class="menubar-title">onWatch</div>
            <div class="menubar-subtitle">${escapeHTML(aggregateLabel(snapshot))}</div>
          </div>
          <div class="menubar-status-badge">
            <span class="menubar-status-dot"></span>
            <span>${escapeHTML((snapshot.aggregate && snapshot.aggregate.label) || 'All Good')}</span>
          </div>
        </header>
        ${bodyMarkup}
        ${footerMarkup()}
      </section>
    `;

    root.querySelectorAll('[data-external="true"]').forEach((el) => {
      el.addEventListener('click', (event) => {
        event.preventDefault();
        bridge.openExternal(el.dataset.url);
      });
    });
  }

  function renderError(error) {
    const root = document.getElementById('menubar-root');
    root.innerHTML = `
      <section class="menubar-panel menubar-error">
        <div>
          <div>Menubar data is temporarily unavailable.</div>
          <div class="menubar-subtitle">${escapeHTML(error && error.message ? error.message : 'Unable to reach the local onWatch data source.')}</div>
          <button type="button" id="menubar-retry">Retry</button>
        </div>
      </section>
    `;
    const retry = document.getElementById('menubar-retry');
    if (retry) {
      retry.addEventListener('click', () => init());
    }
  }

  let refreshTimer = null;

  async function init() {
    const settings = await bridge.getSettings();
    try {
      const snapshot = await bridge.getSnapshot();
      render(snapshot, settings || {});
      const intervalSeconds = Number(settings && settings.refresh_seconds ? settings.refresh_seconds : 60);
      if (refreshTimer) {
        clearInterval(refreshTimer);
      }
      refreshTimer = setInterval(async () => {
        try {
          const nextSnapshot = await bridge.getSnapshot();
          render(nextSnapshot, settings || {});
        } catch (error) {
          renderError(error);
        }
      }, Math.max(intervalSeconds, 10) * 1000);
    } catch (error) {
      renderError(error);
    }
  }

  document.addEventListener('DOMContentLoaded', init);
}());
