// SynTrack Dashboard JavaScript

const API_BASE = '';
const REFRESH_INTERVAL = 60000;

let chart = null;
let countdownInterval = null;
let refreshInterval = null;
let currentQuotas = {};

const statusConfig = {
  healthy: { label: 'Healthy', icon: 'M20 6L9 17l-5-5' },
  warning: { label: 'Warning', icon: 'M10.29 3.86L1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0zM12 9v4M12 17h.01' },
  danger: { label: 'Danger', icon: 'M10.29 3.86L1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0zM12 9v4M12 17h.01' },
  critical: { label: 'Critical', icon: 'M12 9v4M12 17h.01M10.29 3.86L1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z' }
};

function initTheme() {
  const toggle = document.getElementById('theme-toggle');
  if (!toggle) return;
  
  toggle.addEventListener('click', () => {
    const current = document.documentElement.getAttribute('data-theme');
    const next = current === 'light' ? 'dark' : 'light';
    document.documentElement.setAttribute('data-theme', next);
    localStorage.setItem('syntrack-theme', next);
    if (chart) updateChartTheme();
  });
}

function formatDuration(seconds) {
  if (seconds < 0) return 'Resetting...';
  const h = Math.floor(seconds / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  const s = seconds % 60;
  if (h > 0) return `${h}h ${m}m`;
  if (m > 0) return `${m}m ${s}s`;
  return '< 1m';
}

function formatNumber(num) {
  return num.toLocaleString('en-US', { maximumFractionDigits: 1 });
}

function formatDateTime(isoString) {
  const d = new Date(isoString);
  return d.toLocaleString('en-US', { month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit' });
}

function updateCard(quotaType, data) {
  currentQuotas[quotaType] = data;
  
  const progressEl = document.getElementById(`progress-${quotaType}`);
  const fractionEl = document.getElementById(`fraction-${quotaType}`);
  const percentEl = document.getElementById(`percent-${quotaType}`);
  const statusEl = document.getElementById(`status-${quotaType}`);
  const resetEl = document.getElementById(`reset-${quotaType}`);
  const countdownEl = document.getElementById(`countdown-${quotaType}`);
  
  if (progressEl) {
    progressEl.style.width = `${data.percent}%`;
    progressEl.setAttribute('data-status', data.status);
    progressEl.parentElement.setAttribute('aria-valuenow', Math.round(data.percent));
  }
  
  if (fractionEl) {
    fractionEl.textContent = `${formatNumber(data.usage)} / ${formatNumber(data.limit)}`;
  }
  
  if (percentEl) {
    percentEl.textContent = `${data.percent.toFixed(1)}%`;
  }
  
  if (statusEl) {
    const config = statusConfig[data.status] || statusConfig.healthy;
    statusEl.setAttribute('data-status', data.status);
    statusEl.innerHTML = `<svg class="status-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="${config.icon}"/></svg>${config.label}`;
  }
  
  if (resetEl && data.renewsAt) {
    resetEl.textContent = `Resets: ${formatDateTime(data.renewsAt)}`;
  }
  
  if (countdownEl) {
    countdownEl.textContent = formatDuration(data.timeUntilResetSeconds);
    countdownEl.classList.toggle('imminent', data.timeUntilResetSeconds < 1800);
  }
}

function startCountdowns() {
  if (countdownInterval) clearInterval(countdownInterval);
  countdownInterval = setInterval(() => {
    Object.keys(currentQuotas).forEach(type => {
      const data = currentQuotas[type];
      if (data && data.timeUntilResetSeconds > 0) {
        data.timeUntilResetSeconds--;
        const el = document.getElementById(`countdown-${type}`);
        if (el) {
          el.textContent = formatDuration(data.timeUntilResetSeconds);
          el.classList.toggle('imminent', data.timeUntilResetSeconds < 1800);
        }
      }
    });
  }, 1000);
}

async function fetchCurrent() {
  try {
    const res = await fetch(`${API_BASE}/api/current`);
    if (!res.ok) throw new Error('Failed to fetch');
    const data = await res.json();
    
    requestAnimationFrame(() => {
      updateCard('subscription', data.subscription);
      updateCard('search', data.search);
      updateCard('toolCalls', data.toolCalls);
      
      const lastUpdated = document.getElementById('last-updated');
      if (lastUpdated) {
        lastUpdated.textContent = `Last updated: ${new Date().toLocaleTimeString()}`;
      }
      
      const statusDot = document.getElementById('status-dot');
      if (statusDot) statusDot.classList.remove('stale');
      
      updateInsights(data);
    });
  } catch (err) {
    console.error('Fetch error:', err);
    const statusDot = document.getElementById('status-dot');
    if (statusDot) statusDot.classList.add('stale');
  }
}

function updateInsights(data) {
  const container = document.getElementById('insights-content');
  if (!container) return;
  
  const insights = [];
  ['subscription', 'search', 'toolCalls'].forEach(type => {
    const q = data[type];
    if (q && q.insight) insights.push(`<p class="insight-text"><strong>${q.name}:</strong> ${q.insight}</p>`);
  });
  
  container.innerHTML = insights.join('') || '<p class="insight-text">No insights available.</p>';
}

function initChart() {
  const ctx = document.getElementById('usage-chart');
  if (!ctx || typeof Chart === 'undefined') return;
  
  const isDark = document.documentElement.getAttribute('data-theme') === 'dark';
  const gridColor = isDark ? '#49454F' : '#CAC4D0';
  const textColor = isDark ? '#CAC4D0' : '#49454F';
  
  chart = new Chart(ctx, {
    type: 'line',
    data: {
      labels: [],
      datasets: [
        { label: 'Subscription', data: [], borderColor: '#D0BCFF', backgroundColor: 'rgba(208, 188, 255, 0.1)', fill: true, tension: 0.3 },
        { label: 'Search', data: [], borderColor: '#4ADE80', backgroundColor: 'rgba(74, 222, 128, 0.1)', fill: true, tension: 0.3 },
        { label: 'Tool Calls', data: [], borderColor: '#38BDF8', backgroundColor: 'rgba(56, 189, 248, 0.1)', fill: true, tension: 0.3 }
      ]
    },
    options: {
      responsive: true,
      maintainAspectRatio: false,
      interaction: { mode: 'index', intersect: false },
      plugins: {
        legend: { labels: { color: textColor, usePointStyle: true, boxWidth: 8 } }
      },
      scales: {
        x: { grid: { color: gridColor, drawBorder: false }, ticks: { color: textColor, maxTicksLimit: 6 } },
        y: { grid: { color: gridColor, drawBorder: false }, ticks: { color: textColor, callback: v => v + '%' }, min: 0, max: 100 }
      }
    }
  });
}

function updateChartTheme() {
  if (!chart) return;
  const isDark = document.documentElement.getAttribute('data-theme') === 'dark';
  const gridColor = isDark ? '#49454F' : '#CAC4D0';
  const textColor = isDark ? '#CAC4D0' : '#49454F';
  
  chart.options.scales.x.grid.color = gridColor;
  chart.options.scales.x.ticks.color = textColor;
  chart.options.scales.y.grid.color = gridColor;
  chart.options.scales.y.ticks.color = textColor;
  chart.options.plugins.legend.labels.color = textColor;
  chart.update('none');
}

async function fetchHistory(range = '6h') {
  try {
    const res = await fetch(`${API_BASE}/api/history?range=${range}`);
    if (!res.ok) throw new Error('Failed to fetch history');
    const data = await res.json();
    
    if (!chart) initChart();
    if (!chart) return;
    
    chart.data.labels = data.map(d => new Date(d.capturedAt).toLocaleTimeString('en-US', { hour: '2-digit', minute: '2-digit' }));
    chart.data.datasets[0].data = data.map(d => d.subscriptionPercent);
    chart.data.datasets[1].data = data.map(d => d.searchPercent);
    chart.data.datasets[2].data = data.map(d => d.toolCallsPercent);
    chart.update();
  } catch (err) {
    console.error('History fetch error:', err);
  }
}

async function fetchCycles(quotaType = 'subscription') {
  try {
    const res = await fetch(`${API_BASE}/api/cycles?type=${quotaType}`);
    if (!res.ok) throw new Error('Failed to fetch cycles');
    const data = await res.json();
    
    const tbody = document.getElementById('cycles-tbody');
    if (!tbody) return;
    
    if (data.length === 0) {
      tbody.innerHTML = '<tr><td colspan="7" class="empty-state">No cycle data yet. Tracking begins on first poll.</td></tr>';
      return;
    }
    
    tbody.innerHTML = data.map((cycle, i) => {
      const isActive = !cycle.cycleEnd;
      const start = new Date(cycle.cycleStart);
      const end = cycle.cycleEnd ? new Date(cycle.cycleEnd) : new Date();
      const duration = Math.round((end - start) / 60000);
      const hours = Math.floor(duration / 60);
      const mins = duration % 60;
      
      return `<tr>
        <td>#${cycle.id}${isActive ? ' <span class="badge">Active</span>' : ''}</td>
        <td>${start.toLocaleString('en-US', { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })}</td>
        <td>${cycle.cycleEnd ? new Date(cycle.cycleEnd).toLocaleString('en-US', { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' }) : 'Active'}</td>
        <td>${hours}h ${mins}m</td>
        <td>${formatNumber(cycle.peakRequests)}</td>
        <td>${formatNumber(cycle.totalDelta)}</td>
        <td>${duration > 0 ? formatNumber(cycle.totalDelta / (duration / 60)) + '/hr' : '-'}</td>
      </tr>`;
    }).join('');
  } catch (err) {
    console.error('Cycles fetch error:', err);
  }
}

async function fetchSessions() {
  try {
    const res = await fetch(`${API_BASE}/api/sessions`);
    if (!res.ok) throw new Error('Failed to fetch sessions');
    const data = await res.json();
    
    const tbody = document.getElementById('sessions-tbody');
    if (!tbody) return;
    
    if (data.length === 0) {
      tbody.innerHTML = '<tr><td colspan="7" class="empty-state">No sessions recorded yet.</td></tr>';
      return;
    }
    
    tbody.innerHTML = data.map(session => {
      const isActive = !session.endedAt;
      const start = new Date(session.startedAt);
      const end = session.endedAt ? new Date(session.endedAt) : new Date();
      const duration = Math.round((end - start) / 60000);
      const hours = Math.floor(duration / 60);
      const mins = duration % 60;
      
      return `<tr>
        <td>${session.id.slice(0, 8)}${isActive ? ' <span class="badge">Active</span>' : ''}</td>
        <td>${start.toLocaleString('en-US', { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })}</td>
        <td>${session.endedAt ? new Date(session.endedAt).toLocaleString('en-US', { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' }) : 'Active'}</td>
        <td>${hours}h ${mins}m</td>
        <td>${formatNumber(session.maxSubRequests)}</td>
        <td>${formatNumber(session.maxSearchRequests)}</td>
        <td>${formatNumber(session.maxToolRequests)}</td>
      </tr>`;
    }).join('');
  } catch (err) {
    console.error('Sessions fetch error:', err);
  }
}

function setupRangeSelector() {
  const buttons = document.querySelectorAll('.range-btn');
  buttons.forEach(btn => {
    btn.addEventListener('click', () => {
      buttons.forEach(b => b.classList.remove('active'));
      btn.classList.add('active');
      fetchHistory(btn.dataset.range);
    });
  });
}

function setupCycleSelector() {
  const select = document.getElementById('cycle-quota-select');
  if (select) {
    select.addEventListener('change', (e) => fetchCycles(e.target.value));
  }
}

function setupPasswordToggle() {
  const toggle = document.querySelector('.toggle-password');
  const input = document.getElementById('password');
  if (toggle && input) {
    toggle.addEventListener('click', () => {
      const isVisible = input.type === 'text';
      input.type = isVisible ? 'password' : 'text';
      toggle.classList.toggle('showing', !isVisible);
    });
  }
}

function startAutoRefresh() {
  if (refreshInterval) clearInterval(refreshInterval);
  refreshInterval = setInterval(() => {
    fetchCurrent();
    const activeRange = document.querySelector('.range-btn.active');
    fetchHistory(activeRange?.dataset.range || '6h');
    const cycleSelect = document.getElementById('cycle-quota-select');
    fetchCycles(cycleSelect?.value || 'subscription');
    fetchSessions();
  }, REFRESH_INTERVAL);
}

document.addEventListener('DOMContentLoaded', () => {
  initTheme();
  setupRangeSelector();
  setupCycleSelector();
  setupPasswordToggle();
  
  if (document.getElementById('usage-chart')) {
    initChart();
    fetchCurrent();
    fetchHistory('6h');
    fetchCycles('subscription');
    fetchSessions();
    startCountdowns();
    startAutoRefresh();
  }
});
