// onWatch Dashboard JavaScript

const BASE_PATH = (document.querySelector('meta[name="base-path"]') || {}).content || '';
const API_BASE = BASE_PATH;
const REFRESH_INTERVAL = 120000;

// ── Lazy Loading via IntersectionObserver ──
const _lazyLoaded = new Set();
function lazyLoadOnVisible(selector, callback) {
  const el = document.querySelector(selector);
  if (!el) return;
  if (typeof IntersectionObserver === 'undefined') { callback(); return; }
  const observer = new IntersectionObserver((entries) => {
    entries.forEach(entry => {
      if (entry.isIntersecting && !_lazyLoaded.has(selector)) {
        _lazyLoaded.add(selector);
        observer.unobserve(entry.target);
        callback();
      }
    });
  }, { rootMargin: '200px' });
  observer.observe(el);
}

// ── Auth helper: redirect to login on 401 ──
async function authFetch(url, options) {
  // Add CSRF protection header for state-changing requests
  options = options || {};
  const method = (options.method || 'GET').toUpperCase();
  if (method !== 'GET' && method !== 'HEAD') {
    options.headers = options.headers || {};
    if (!options.headers['X-Requested-With']) {
      options.headers['X-Requested-With'] = 'XMLHttpRequest';
    }
  }
  const res = await fetch(url, options);
  if (res.status === 401) {
    // Don't redirect if already on the login page (avoids infinite refresh loop)
    if (window.location.pathname !== BASE_PATH + '/login') {
      window.location.href = BASE_PATH + '/login';
    }
    throw new Error('Session expired');
  }
  return res;
}

// ── Provider State ──
function getCurrentProvider() {
  const bothView = document.getElementById('both-view') || document.getElementById('all-providers-container');
  if (bothView) return 'both';
  const anthropicGrid = document.getElementById('quota-grid-anthropic');
  if (anthropicGrid) return 'anthropic';
  const copilotGrid = document.getElementById('quota-grid-copilot');
  if (copilotGrid) return 'copilot';
  const codexGrid = document.getElementById('quota-grid-codex')
    || document.getElementById('codex-accounts-container-both')
    || document.getElementById('codex-accounts-container');
  if (codexGrid) return 'codex';
  const antigravityGrid = document.getElementById('quota-grid-antigravity');
  if (antigravityGrid) return 'antigravity';
  const minimaxGrid = document.getElementById('quota-grid-minimax');
  if (minimaxGrid) return 'minimax';
  const openrouterGrid = document.getElementById('quota-grid-openrouter');
  if (openrouterGrid) return 'openrouter';
  const geminiGrid = document.getElementById('quota-grid-gemini');
  if (geminiGrid) return 'gemini';
  const grid = document.getElementById('quota-grid');
  return (grid && grid.dataset.provider) || 'synthetic';
}

function providerParam() {
  const provider = getCurrentProvider();
  let param = `provider=${provider}`;
  // Append account parameter for multi-account providers
  if (provider === 'codex') {
    param += codexAccountParam();
  } else if (provider === 'minimax') {
    param += minimaxAccountParam();
  }
  return param;
}

function shouldShowHistoryTables(provider = getCurrentProvider()) {
  return provider !== 'both';
}

function getBothViewProviders() {
  const tabs = document.querySelectorAll('#provider-tabs .provider-tab[data-provider]');
  if (tabs.length > 0) {
    return [...tabs]
      .map(el => el.dataset.provider)
      .filter((provider) => provider && provider !== 'both');
  }
  return [];
}

// ── Global State ──
const State = {
  chart: null,
  chartSyn: null,
  chartZai: null,
  chartAnth: null,
  chartCodex: null,
  chartCodexByAccount: {},
  providerCharts: {},
  modalChart: null,
  countdownInterval: null,
  refreshInterval: null,
  currentQuotas: {},
  // Table data caches
  allCyclesData: [],
  allSessionsData: [],
  // Cycles table state
  cyclesSort: { key: null, dir: 'desc' },
  cyclesPage: 1,
  cyclesPageSize: 10,
  cyclesRange: 259200000,   // 3 days in ms (default)
  cyclesBucket: 2,          // Polling history grouping bucket in minutes
  cyclesQuotaNames: [],     // dynamic quota column names
  // Sessions table state
  sessionsSort: { key: null, dir: 'desc' },
  sessionsPage: 1,
  sessionsPageSize: 10,
  // Expanded session
  expandedSessionId: null,
  // Dynamic Y-axis max (preserved across theme changes)
  chartYMax: 100,
  // Hidden quota datasets (persisted in localStorage)
  hiddenQuotas: new Set(),
  // Hidden insight keys (persisted in DB via settings API)
  hiddenInsights: new Set(),
  // Insights time range (1d / 7d / 30d)
  insightsRange: '7d',
  // Anthropic session column names (sorted, max 3 - mirrors backend positional mapping)
  anthropicSessionQuotas: [],
  // Cycle Overview state
  allOverviewData: [],
  overviewQuotaNames: [],
  overviewGroupBy: null,
  overviewSort: { key: null, dir: 'desc' },
  overviewPage: 1,
  overviewPageSize: 10,
  // Codex profile selection (multi-account beta)
  codexAccount: 1,
  codexProfiles: [],
  codexPlanType: '',
  codexQuotaNames: [],
  allProvidersCurrent: null,
  allProvidersInsights: null,
  allProvidersHistory: null,
  providerVisibility: {},
  providerSettings: {},
  menubarCapabilities: null,
  menubarProviderOrder: [],
  menubarProviders: [],
  menubarVisibleProviders: [],
  menubarStatusDisplay: { mode: 'multi_provider', selected_quotas: [] },
  currentRequestSeq: 0,
  insightsRequestSeq: 0,
  historyRequestSeq: 0,
  cyclesRequestSeq: 0,
  sessionsRequestSeq: 0,
  overviewRequestSeq: 0,
};

// ── Persistence ──

function loadHiddenQuotas() {
  try {
    const stored = localStorage.getItem('onwatch-hidden-quotas');
    if (stored) {
      State.hiddenQuotas = new Set(JSON.parse(stored));
    }
  } catch (e) {
    // silent - localStorage read failure is non-critical
    State.hiddenQuotas = new Set();
  }
}

function saveHiddenQuotas() {
  try {
    localStorage.setItem('onwatch-hidden-quotas', JSON.stringify([...State.hiddenQuotas]));
  } catch (e) {
    // silent
  }
}

// ── Codex Profile Persistence (multi-account beta) ──

function loadCodexAccount() {
  try {
    const stored = localStorage.getItem('onwatch-codex-account');
    if (stored) {
      const parsed = parseInt(stored, 10);
      State.codexAccount = isNaN(parsed) ? 1 : parsed;
    }
  } catch (e) {
    State.codexAccount = 1;
  }
}

function saveCodexAccount(account) {
  State.codexAccount = account;
  try {
    localStorage.setItem('onwatch-codex-account', account);
  } catch (e) {
    // silent
  }
}

async function loadCodexProfiles() {
  try {
    const res = await authFetch(`${API_BASE}/api/codex/profiles`);
    if (!res.ok) return;
    const data = await res.json();
    if (data.profiles && data.profiles.length > 0) {
      // Filter out deleted profiles and "default" profile for dashboard tabs
      const activeProfiles = data.profiles.filter(p => !p.deletedAt);
      const customProfiles = activeProfiles.filter(p => p.name !== 'default');
      if (customProfiles.length > 0) {
        State.codexProfiles = customProfiles;
      } else if (activeProfiles.length > 0) {
        State.codexProfiles = activeProfiles;
      } else {
        // All profiles deleted - no tabs needed
        State.codexProfiles = [];
      }
      populateCodexProfileTabs();
      updateCodexProfileTabsVisibility();
    }
  } catch (e) {
    // silent - profiles endpoint may not exist on older versions
  }
}

function populateCodexProfileTabs() {
  const dropdown = document.getElementById('codex-profile-dropdown');
  const menu = document.getElementById('codex-profile-menu');
  if (!dropdown || !menu) return;

  // Only show dropdown if there are multiple profiles
  if (State.codexProfiles.length <= 1) {
    dropdown.style.display = 'none';
    return;
  }

  menu.innerHTML = '';

  for (const profile of State.codexProfiles) {
    const li = document.createElement('li');
    li.className = 'codex-profile-item' + (profile.id === State.codexAccount ? ' active' : '');
    li.dataset.accountId = profile.id;
    li.textContent = profile.name;
    li.setAttribute('role', 'option');
    li.setAttribute('aria-selected', profile.id === State.codexAccount ? 'true' : 'false');
    li.addEventListener('click', () => {
      switchCodexProfile(profile.id);
      closeCodexProfileDropdown();
    });
    menu.appendChild(li);
  }

  // If current account not in list, reset to first profile
  if (!State.codexProfiles.find(p => p.id === State.codexAccount)) {
    State.codexAccount = State.codexProfiles[0].id;
    saveCodexAccount(State.codexAccount);
    updateProfileTabsActive();
  }

  updateProfileTabsActive();
}

function switchCodexProfile(accountId) {
  if (State.codexAccount === accountId) return;
  State.codexAccount = accountId;
  saveCodexAccount(accountId);
  updateProfileTabsActive();
  refreshAll();
}

function updateProfileTabsActive() {
  const label = document.getElementById('codex-profile-label');
  const menu = document.getElementById('codex-profile-menu');
  if (!menu) return;

  const activeProfile = State.codexProfiles.find(p => p.id === State.codexAccount);
  if (label && activeProfile) {
    label.textContent = activeProfile.name;
  }

  menu.querySelectorAll('.codex-profile-item').forEach(item => {
    const isActive = parseInt(item.dataset.accountId, 10) === State.codexAccount;
    item.classList.toggle('active', isActive);
    item.setAttribute('aria-selected', isActive ? 'true' : 'false');
  });
}

function closeCodexProfileDropdown() {
  const trigger = document.getElementById('codex-profile-trigger');
  const menu = document.getElementById('codex-profile-menu');
  if (trigger) trigger.setAttribute('aria-expanded', 'false');
  if (menu) menu.classList.remove('open');
}

// Show/hide profile dropdown based on current provider and profile count
function updateCodexProfileTabsVisibility() {
  const dropdown = document.getElementById('codex-profile-dropdown');
  if (!dropdown) return;

  const provider = getCurrentProvider();
  const show = provider === 'codex' && State.codexProfiles.length > 1;
  dropdown.style.display = show ? '' : 'none';
}

// Refresh all dashboard data (used when switching profiles)
function refreshAll() {
  const refreshBtn = document.getElementById('refresh-btn');
  if (refreshBtn) refreshBtn.classList.add('spinning');
  const tasks = [fetchCurrent(), fetchDeepInsights(), fetchHistory()];
  if (shouldShowHistoryTables()) {
    tasks.push(fetchCycles(), fetchSessions(), fetchCycleOverview());
  }
  Promise.all(tasks).finally(() => {
    if (refreshBtn) setTimeout(() => refreshBtn.classList.remove('spinning'), 600);
  });
}

function initCodexProfileTabs() {
  // Set up dropdown toggle behavior
  const trigger = document.getElementById('codex-profile-trigger');
  const menu = document.getElementById('codex-profile-menu');
  if (!trigger || !menu) return;

  trigger.addEventListener('click', (e) => {
    e.stopPropagation();
    const isOpen = menu.classList.toggle('open');
    trigger.setAttribute('aria-expanded', isOpen ? 'true' : 'false');
  });

  // Close on outside click
  document.addEventListener('click', (e) => {
    if (!e.target.closest('.codex-profile-dropdown')) {
      closeCodexProfileDropdown();
    }
  });

  // Close on Escape
  document.addEventListener('keydown', (e) => {
    if (e.key === 'Escape') closeCodexProfileDropdown();
  });
}

function codexAccountParam() {
  return State.codexAccount ? `&account=${encodeURIComponent(State.codexAccount)}` : '';
}

// ── MiniMax Account Persistence (multi-account) ──

function loadMiniMaxAccount() {
  try {
    const stored = localStorage.getItem('onwatch-minimax-account');
    if (stored) {
      const parsed = parseInt(stored, 10);
      State.minimaxAccount = isNaN(parsed) ? null : parsed;
    }
  } catch (e) {
    State.minimaxAccount = null;
  }
}

function saveMiniMaxAccount(account) {
  State.minimaxAccount = account;
  try {
    localStorage.setItem('onwatch-minimax-account', account);
  } catch (e) {
    // silent
  }
}

async function loadMiniMaxAccounts() {
  try {
    const res = await authFetch(`${API_BASE}/api/minimax/accounts`);
    if (!res.ok) return;
    const data = await res.json();
    if (data.accounts && data.accounts.length > 0) {
      const activeAccounts = data.accounts.filter(a => !a.deletedAt);
      State.minimaxAccounts = activeAccounts;
      populateMiniMaxAccountTabs();
      updateMiniMaxAccountTabsVisibility();
    }
  } catch (e) {
    // silent
  }
}

function populateMiniMaxAccountTabs() {
  const dropdown = document.getElementById('minimax-profile-dropdown');
  const menu = document.getElementById('minimax-profile-menu');
  if (!dropdown || !menu) return;

  if (!State.minimaxAccounts || State.minimaxAccounts.length <= 1) {
    dropdown.style.display = 'none';
    return;
  }

  menu.innerHTML = '';

  for (const account of State.minimaxAccounts) {
    const li = document.createElement('li');
    li.className = 'codex-profile-item' + (account.id === State.minimaxAccount ? ' active' : '');
    li.dataset.accountId = account.id;
    li.textContent = account.name;
    li.setAttribute('role', 'option');
    li.setAttribute('aria-selected', account.id === State.minimaxAccount ? 'true' : 'false');
    li.addEventListener('click', () => {
      switchMiniMaxAccount(account.id);
      closeMiniMaxAccountDropdown();
    });
    menu.appendChild(li);
  }

  if (!State.minimaxAccounts.find(a => a.id === State.minimaxAccount)) {
    State.minimaxAccount = State.minimaxAccounts[0].id;
    saveMiniMaxAccount(State.minimaxAccount);
    updateMiniMaxAccountTabsActive();
  }

  updateMiniMaxAccountTabsActive();
}

function switchMiniMaxAccount(accountId) {
  if (State.minimaxAccount === accountId) return;
  State.minimaxAccount = accountId;
  saveMiniMaxAccount(accountId);
  updateMiniMaxAccountTabsActive();
  refreshAll();
}

function updateMiniMaxAccountTabsActive() {
  const label = document.getElementById('minimax-profile-label');
  const menu = document.getElementById('minimax-profile-menu');
  if (!menu) return;

  const active = State.minimaxAccounts && State.minimaxAccounts.find(a => a.id === State.minimaxAccount);
  if (label && active) {
    label.textContent = active.name;
  }

  menu.querySelectorAll('.codex-profile-item').forEach(item => {
    const isActive = parseInt(item.dataset.accountId, 10) === State.minimaxAccount;
    item.classList.toggle('active', isActive);
    item.setAttribute('aria-selected', isActive ? 'true' : 'false');
  });
}

function closeMiniMaxAccountDropdown() {
  const trigger = document.getElementById('minimax-profile-trigger');
  const menu = document.getElementById('minimax-profile-menu');
  if (trigger) trigger.setAttribute('aria-expanded', 'false');
  if (menu) menu.classList.remove('open');
}

function updateMiniMaxAccountTabsVisibility() {
  const dropdown = document.getElementById('minimax-profile-dropdown');
  if (!dropdown) return;

  const provider = getCurrentProvider();
  const show = provider === 'minimax' && State.minimaxAccounts && State.minimaxAccounts.length > 1;
  dropdown.style.display = show ? '' : 'none';
}

function initMiniMaxAccountTabs() {
  const trigger = document.getElementById('minimax-profile-trigger');
  const menu = document.getElementById('minimax-profile-menu');
  if (!trigger || !menu) return;

  trigger.addEventListener('click', (e) => {
    e.stopPropagation();
    const isOpen = menu.classList.toggle('open');
    trigger.setAttribute('aria-expanded', isOpen ? 'true' : 'false');
  });

  document.addEventListener('click', (e) => {
    if (!e.target.closest('#minimax-profile-dropdown')) {
      closeMiniMaxAccountDropdown();
    }
  });

  document.addEventListener('keydown', (e) => {
    if (e.key === 'Escape') closeMiniMaxAccountDropdown();
  });
}

function minimaxAccountParam() {
  return State.minimaxAccount ? `&account=${encodeURIComponent(State.minimaxAccount)}` : '';
}

// ── Insight Visibility (DB-persisted) ──

// Cross-provider insight correlation map (mirrors backend)
const insightCorrelations = [
  ['cycle_utilization', 'token_budget'],
  ['tool_share', 'tool_breakdown'],
  ['trend', 'trend_24h'],
  ['weekly_pace', 'usage_7d'],
];

// Expand a set of hidden keys with all correlated keys
function expandCorrelatedKeys(keys) {
  const expanded = new Set(keys);
  for (const group of insightCorrelations) {
    if (group.some(k => expanded.has(k))) {
      group.forEach(k => expanded.add(k));
    }
  }
  return expanded;
}

// Get all correlated keys for a given key (returns array including the key itself)
function getCorrelatedKeys(key) {
  const related = [key];
  for (const group of insightCorrelations) {
    if (group.includes(key)) {
      group.forEach(k => { if (!related.includes(k)) related.push(k); });
    }
  }
  return related;
}

async function loadHiddenInsights() {
  try {
    const res = await authFetch(`${API_BASE}/api/settings`);
    if (res.ok) {
      const data = await res.json();
      if (data.hidden_insights && Array.isArray(data.hidden_insights)) {
        State.hiddenInsights = new Set(data.hidden_insights);
      }
      if (data.provider_visibility && typeof data.provider_visibility === 'object') {
        State.providerVisibility = data.provider_visibility;
      } else {
        State.providerVisibility = {};
      }

      if (getCurrentProvider() === 'both' && (State.allProvidersCurrent || State.allProvidersInsights || State.allProvidersHistory)) {
        renderAllProvidersView();
      }
    }
  } catch (e) {
    // silent
  }
}

async function saveHiddenInsights() {
  try {
    await authFetch(`${API_BASE}/api/settings`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ hidden_insights: [...State.hiddenInsights] })
    });
  } catch (e) {
    // silent
  }
}

async function toggleInsightVisibility(key) {
  const related = getCorrelatedKeys(key);
  const isHidden = State.hiddenInsights.has(key);
  related.forEach(k => {
    if (isHidden) State.hiddenInsights.delete(k);
    else State.hiddenInsights.add(k);
  });
  await saveHiddenInsights();
  fetchDeepInsights(); // Re-fetch (backend will filter)
}

async function unhideInsight(key) {
  const related = getCorrelatedKeys(key);
  related.forEach(k => State.hiddenInsights.delete(k));
  await saveHiddenInsights();
  fetchDeepInsights();
}

// ── Provider Persistence ──

function loadDefaultProvider() {
  try {
    return localStorage.getItem('onwatch-default-provider') || '';
  } catch (e) {
    return '';
  }
}

function saveDefaultProvider(provider) {
  try {
    localStorage.setItem('onwatch-default-provider', provider);
  } catch (e) {
    // silent
  }
}

function toggleQuotaVisibility(quotaType) {
  if (State.hiddenQuotas.has(quotaType)) {
    State.hiddenQuotas.delete(quotaType);
  } else {
    State.hiddenQuotas.add(quotaType);
  }
  saveHiddenQuotas();
  
  // Update chart if it exists
  if (State.chart) {
    updateChartVisibility();
  }
}

function updateChartVisibility() {
  if (getCurrentProvider() === 'both') return; // Both mode uses separate charts
  if (!State.chart) return;
  
  const provider = getCurrentProvider();
  const quotaMap = provider === 'zai'
    ? { 0: 'tokensLimit', 1: 'timeLimit', 2: 'toolCalls' }
    : { 0: 'subscription', 1: 'search', 2: 'toolCalls' };
  
  State.chart.data.datasets.forEach((ds, index) => {
    const quotaType = quotaMap[index];
    if (quotaType) {
      ds.hidden = State.hiddenQuotas.has(quotaType);
    }
  });
  
  // Recompute Y-axis based on visible datasets only
  State.chartYMax = computeYMax(State.chart.data.datasets, State.chart);
  State.chart.options.scales.y.max = State.chartYMax;
  State.chart.update('none'); // Update without animation
}

const statusConfig = {
  healthy: { label: 'Healthy', icon: 'M20 6L9 17l-5-5' },
  warning: { label: 'Warning', icon: 'M10.29 3.86L1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0zM12 9v4M12 17h.01' },
  danger: { label: 'Danger', icon: 'M10.29 3.86L1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0zM12 9v4M12 17h.01' },
  critical: { label: 'Critical', icon: 'M12 9v4M12 17h.01M10.29 3.86L1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z' }
};

const quotaNames = {
  subscription: 'Subscription Quota',
  search: 'Search (Hourly)',
  toolCalls: 'Tool Call Discounts',
  coding_plan: 'MiniMax Coding Plan'
};

// freshnessLabel returns a human-readable label for per-quota data freshness.
function freshnessLabel(quota) {
  const age = quota.ageSeconds || 0;
  const src = quota.source === 'statusline' ? 'Live' : 'API';
  if (age < 60) return `${src} - just now`;
  if (age < 3600) return `${src} - ${Math.floor(age / 60)}m ago`;
  if (age < 86400) return `${src} - ${Math.floor(age / 3600)}h ago`;
  return `${src} - ${Math.floor(age / 86400)}d ago`;
}

// Anthropic display names (mirrors backend anthropicDisplayNames)
const anthropicDisplayNames = {
  five_hour: '5-Hour Limit',
  seven_day: 'Weekly All-Model',
  seven_day_sonnet: 'Weekly Sonnet',
  monthly_limit: 'Monthly Limit',
  extra_usage: 'Extra Usage'
};

// Anthropic quota icons (mapped by key)
const anthropicQuotaIcons = {
  five_hour: '<circle cx="12" cy="12" r="10"/><polyline points="12 6 12 12 16 14"/>',       // clock
  seven_day: '<rect x="3" y="4" width="18" height="18" rx="2"/><line x1="16" y1="2" x2="16" y2="6"/><line x1="8" y1="2" x2="8" y2="6"/><line x1="3" y1="10" x2="21" y2="10"/>', // calendar
  seven_day_sonnet: '<path d="M12 2L2 7l10 5 10-5-10-5zM2 17l10 5 10-5M2 12l10 5 10-5"/>', // layers
  monthly_limit: '<rect x="2" y="7" width="20" height="14" rx="2" ry="2"/><path d="M16 21V5a2 2 0 0 0-2-2h-4a2 2 0 0 0-2 2v16"/>', // briefcase
  extra_usage: '<path d="M21.21 15.89A10 10 0 1 1 8 2.83"/><path d="M22 12A10 10 0 0 0 12 2v10z"/>' // pie-chart
};

// Anthropic chart colors keyed by quota name (stable regardless of which quotas exist)
const anthropicChartColorMap = {
  five_hour:        { border: '#D97757', bg: 'rgba(217, 119, 87, 0.08)' },   // coral
  seven_day:        { border: '#10B981', bg: 'rgba(16, 185, 129, 0.08)' },   // emerald
  seven_day_sonnet: { border: '#3B82F6', bg: 'rgba(59, 130, 246, 0.08)' },   // blue
  monthly_limit:    { border: '#A855F7', bg: 'rgba(168, 85, 247, 0.08)' },   // violet
  extra_usage:      { border: '#F59E0B', bg: 'rgba(245, 158, 11, 0.08)' }    // amber
};
const anthropicChartColorFallback = [
  { border: '#14B8A6', bg: 'rgba(20, 184, 166, 0.08)' },
  { border: '#EC4899', bg: 'rgba(236, 72, 153, 0.08)' }
];

// ── Copilot display names (mirrors backend CopilotDisplayName) ──
const copilotDisplayNames = {
  premium_interactions: 'Premium Requests',
  chat: 'Chat',
  completions: 'Completions'
};

const codexDisplayNames = {
  five_hour: '5-Hour Limit',
  seven_day: 'Weekly All-Model',
  code_review: 'Review Requests'
};

const codexSessionLabels = {
  sub: '5-Hour Limit',
  search: 'Weekly All-Model'
};

function getCodexSessionLabel(index) {
  const names = State.codexQuotaNames || [];
  const key = names[index];
  if (key) {
    return codexDisplayNames[key] || key;
  }
  return index === 0 ? codexSessionLabels.sub : codexSessionLabels.search;
}

function updateCodexSessionHeaders() {
  for (let i = 0; i < 2; i++) {
    const el = document.getElementById(`codex-session-col-${i}`);
    if (!el) continue;
    const label = getCodexSessionLabel(i).replace(/ Limit$/, '');
    el.innerHTML = `${label} <span class="sort-arrow"></span>`;
  }
}

function normalizeCodexPlanType(planType) {
  return typeof planType === 'string' ? planType.trim().toLowerCase() : '';
}

function isCodexFreePlan(planType) {
  return normalizeCodexPlanType(planType) === 'free';
}

function codexVisibleQuotaNames(planType) {
  return isCodexFreePlan(planType)
    ? ['seven_day', 'code_review']
    : ['five_hour', 'seven_day', 'code_review'];
}

const anthropicQuotaOrder = ['five_hour', 'seven_day', 'seven_day_sonnet', 'monthly_limit', 'extra_usage'];
const codexQuotaOrder = ['five_hour', 'seven_day', 'code_review'];

function quotaOrderForProvider(provider) {
  if (provider === 'anthropic') return anthropicQuotaOrder;
  if (provider === 'codex') return codexQuotaOrder;
  return [];
}

function sortQuotaKeysForProvider(keys, provider) {
  const sorted = Array.isArray(keys) ? [...keys] : Array.from(keys || []);
  const preferred = quotaOrderForProvider(provider);
  if (preferred.length === 0) {
    return sorted.sort();
  }
  const rank = new Map(preferred.map((name, index) => [name, index]));
  return sorted.sort((left, right) => {
    const leftRank = rank.has(left) ? rank.get(left) : Number.MAX_SAFE_INTEGER;
    const rightRank = rank.has(right) ? rank.get(right) : Number.MAX_SAFE_INTEGER;
    if (leftRank !== rightRank) return leftRank - rightRank;
    return String(left).localeCompare(String(right));
  });
}

function sortQuotaEntriesForProvider(quotas, provider) {
  if (!Array.isArray(quotas)) return [];
  const preferred = quotaOrderForProvider(provider);
  if (preferred.length === 0) return [...quotas];
  return sortItemsByPreference(quotas, preferred, (quota) => quota && quota.name);
}

function setCodexPlanType(planType) {
  const normalized = normalizeCodexPlanType(planType);
  if (!normalized) return false;
  const changed = State.codexPlanType !== normalized;
  State.codexPlanType = normalized;
  return changed;
}

function filterCodexQuotasForPlan(quotas, planType) {
  if (!Array.isArray(quotas)) return [];
  const preferred = new Set(codexVisibleQuotaNames(planType));
  let filtered = quotas
    .filter(q => q && q.name && preferred.has(q.name));

  if (filtered.length === 0) {
    if (isCodexFreePlan(planType)) {
      // Free plans should never render five_hour even if backend reports it.
      filtered = quotas.filter(q => q && q.name && q.name !== 'five_hour');
    } else {
      filtered = quotas.filter(q => q && q.name);
    }
  }

  return sortQuotaEntriesForProvider(filtered, 'codex');
}

// Codex chart colors keyed by quota name
const codexChartColorMap = {
  five_hour: { border: '#0EA5E9', bg: 'rgba(14, 165, 233, 0.08)' },
  seven_day: { border: '#22C55E', bg: 'rgba(34, 197, 94, 0.08)' },
  code_review: { border: '#F59E0B', bg: 'rgba(245, 158, 11, 0.08)' }
};
const codexChartColorFallback = [
  { border: '#F97316', bg: 'rgba(249, 115, 22, 0.08)' },
  { border: '#A855F7', bg: 'rgba(168, 85, 247, 0.08)' }
];

// Copilot quota icons (mapped by key)
const copilotQuotaIcons = {
  premium_interactions: '<path d="M12 2L2 7l10 5 10-5-10-5zM2 17l10 5 10-5M2 12l10 5 10-5"/>', // layers
  chat: '<path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"/>', // message-square
  completions: '<polyline points="20 6 9 17 4 12"/>' // check
};

// Copilot chart colors keyed by quota name
const copilotChartColorMap = {
  premium_interactions: { border: '#6e40c9', bg: 'rgba(110, 64, 201, 0.08)' }, // GitHub purple
  chat:                 { border: '#2ea043', bg: 'rgba(46, 160, 67, 0.08)' },  // GitHub green
  completions:          { border: '#58a6ff', bg: 'rgba(88, 166, 255, 0.08)' }  // GitHub blue
};
const copilotChartColorFallback = [
  { border: '#f78166', bg: 'rgba(247, 129, 102, 0.08)' },
  { border: '#a371f7', bg: 'rgba(163, 113, 247, 0.08)' }
];

const antigravityChartColorMap = {
  antigravity_claude_gpt: { border: '#D97757', bg: 'rgba(217, 119, 87, 0.08)' },
  antigravity_gemini_pro: { border: '#10B981', bg: 'rgba(16, 185, 129, 0.08)' },
  antigravity_gemini_flash: { border: '#3B82F6', bg: 'rgba(59, 130, 246, 0.08)' },
};
const antigravityChartColorFallback = [
  { border: '#F59E0B', bg: 'rgba(245, 158, 11, 0.08)' },
  { border: '#8B5CF6', bg: 'rgba(139, 92, 246, 0.08)' },
];

const minimaxDisplayNames = {
  'MiniMax Coding Plan': 'MiniMax Coding Plan',
  'MiniMax-M2': 'MiniMax M2',
  'MiniMax-M2.1': 'MiniMax M2.1',
  'MiniMax-M2.5': 'MiniMax M2.5',
  'MiniMax-M2.5-highspeed': 'MiniMax M2.5 Highspeed',
};

const minimaxChartColorMap = {
  'MiniMax Coding Plan': { border: '#F97316', bg: 'rgba(249, 115, 22, 0.08)' },
  'MiniMax-M2': { border: '#FF6B6B', bg: 'rgba(255, 107, 107, 0.08)' },
  'MiniMax-M2.1': { border: '#4ECDC4', bg: 'rgba(78, 205, 196, 0.08)' },
  'MiniMax-M2.5': { border: '#4ECDC4', bg: 'rgba(78, 205, 196, 0.08)' },
  'MiniMax-M2.5-highspeed': { border: '#45B7D1', bg: 'rgba(69, 183, 209, 0.08)' },
};
const minimaxChartColorFallback = [
  { border: '#F7DC6F', bg: 'rgba(247, 220, 111, 0.08)' },
  { border: '#BB8FCE', bg: 'rgba(187, 143, 206, 0.08)' },
];

const geminiDisplayNames = {
  'pro': 'Gemini Pro',
  'flash': 'Gemini Flash',
  'flash_lite': 'Gemini Flash Lite',
};

const geminiChartColorMap = {
  'pro': { border: '#4285F4', bg: 'rgba(66, 133, 244, 0.08)' },
  'flash': { border: '#34A853', bg: 'rgba(52, 168, 83, 0.08)' },
  'flash_lite': { border: '#FBBC04', bg: 'rgba(251, 188, 4, 0.08)' },
};

const geminiChartColorFallback = [
  { border: '#4285F4', bg: 'rgba(66, 133, 244, 0.08)' },
  { border: '#34A853', bg: 'rgba(52, 168, 83, 0.08)' },
  { border: '#FBBC04', bg: 'rgba(251, 188, 4, 0.08)' },
  { border: '#EA4335', bg: 'rgba(234, 67, 53, 0.08)' },
  { border: '#8AB4F8', bg: 'rgba(138, 180, 248, 0.08)' },
  { border: '#81C995', bg: 'rgba(129, 201, 149, 0.08)' },
];

// ── Renewal Categories for Cycle Overview ──

const renewalCategories = {
  anthropic: [
    { label: '5-Hour', groupBy: 'five_hour' },
    { label: 'Weekly All', groupBy: 'seven_day' },
    { label: 'Weekly Sonnet', groupBy: 'seven_day_sonnet' },
    { label: 'Extra', groupBy: 'extra_usage' }
  ],
  synthetic: [
    { label: 'Subscription', groupBy: 'subscription' },
    { label: 'Tool Calls', groupBy: 'toolcall' }
  ],
  zai: [
    { label: 'Tokens', groupBy: 'tokens' },
    { label: 'Time', groupBy: 'time' }
  ],
  copilot: [
    { label: 'Premium', groupBy: 'premium_interactions' },
    { label: 'Chat', groupBy: 'chat' },
    { label: 'Completions', groupBy: 'completions' }
  ],
  codex: [
    { label: '5-Hour', groupBy: 'five_hour' },
    { label: 'Weekly All', groupBy: 'seven_day' },
    { label: 'Review Requests', groupBy: 'code_review' }
  ],
  antigravity: [
    { label: 'Claude+GPT', groupBy: 'antigravity_claude_gpt' },
    { label: 'Gemini Pro', groupBy: 'antigravity_gemini_pro' },
    { label: 'Gemini Flash', groupBy: 'antigravity_gemini_flash' }
  ],
  minimax: [
    { label: 'Coding Plan', groupBy: 'coding_plan' }
  ],
  openrouter: [
    { label: 'Credits', groupBy: 'credits' }
  ],
  gemini: []
};

const overviewQuotaDisplayNames = {
  subscription: 'Subscription',
  toolcall: 'Tool Calls',
  tokens: 'Tokens',
  time: 'Time',
  five_hour: '5-Hour', // Default for Anthropic
  seven_day: 'Weekly All',
  code_review: 'Review Requests',
  seven_day_sonnet: 'Weekly Sonnet',
  monthly_limit: 'Monthly',
  extra_usage: 'Extra',
  premium_interactions: 'Premium',
  chat: 'Chat',
  completions: 'Completions',
  coding_plan: 'MiniMax Coding Plan',
  antigravity_claude_gpt: 'Claude + GPT Quota',
  antigravity_gemini_pro: 'Gemini Pro Quota',
  antigravity_gemini_flash: 'Gemini Flash Quota',
  credits: 'Credits'
};

// Provider-specific display name overrides
const providerQuotaDisplayOverrides = {
  codex: {
    five_hour: '5-Hour Limit'
  },
  minimax: {
    coding_plan: 'MiniMax Coding Plan'
  },
  gemini: {
    'pro': 'Gemini Pro',
    'flash': 'Gemini Flash',
    'flash_lite': 'Gemini Flash Lite',
  }
};

// Get quota display name with provider context
function getQuotaDisplayName(quotaKey, provider) {
  // Check for provider-specific override
  if (provider && providerQuotaDisplayOverrides[provider]) {
    const override = providerQuotaDisplayOverrides[provider][quotaKey];
    if (override) return override;
  }
  // Fall back to generic display name
  return overviewQuotaDisplayNames[quotaKey] || quotaKey;
};

// ── Anthropic Dynamic Card Rendering ──

// ── Anthropic Promo ──

function isAnthropicPeakHours(promo) {
  const now = new Date();
  const etStr = now.toLocaleString('en-US', { timeZone: 'America/New_York' });
  const et = new Date(etStr);
  const hour = et.getHours();
  const day = et.getDay();
  const isWeekday = day >= 1 && day <= 5;
  return promo.peakWeekdaysOnly ? (isWeekday && hour >= promo.peakStartHourET && hour < promo.peakEndHourET) : false;
}

// Compute minutes until the next peak/off-peak transition in ET
function promoMinutesUntilTransition(promo) {
  const now = new Date();
  const etStr = now.toLocaleString('en-US', { timeZone: 'America/New_York' });
  const et = new Date(etStr);
  const hour = et.getHours();
  const min = et.getMinutes();
  const day = et.getDay(); // 0=Sun, 6=Sat
  const isWeekday = day >= 1 && day <= 5;
  const isPeak = promo.peakWeekdaysOnly ? (isWeekday && hour >= promo.peakStartHourET && hour < promo.peakEndHourET) : false;

  let totalMin;
  if (isPeak) {
    // Currently peak - countdown to peak end (peakEndHourET today)
    totalMin = (promo.peakEndHourET - hour - 1) * 60 + (60 - min);
  } else if (isWeekday && hour < promo.peakStartHourET) {
    // Before peak today - countdown to peak start
    totalMin = (promo.peakStartHourET - hour - 1) * 60 + (60 - min);
  } else {
    // After peak on weekday, or weekend - countdown to next weekday peak start
    let daysToAdd;
    if (day === 5) daysToAdd = 3;      // Fri after peak -> Mon
    else if (day === 6) daysToAdd = 2;  // Sat -> Mon
    else if (day === 0) daysToAdd = 1;  // Sun -> Mon
    else daysToAdd = 1;                 // Weekday after peak -> next day
    totalMin = (daysToAdd - 1) * 1440 + (24 - hour - 1) * 60 + (60 - min) + promo.peakStartHourET * 60;
  }

  // Cap at promo end date if available
  if (promo.endsAt) {
    const promoEndMin = Math.floor((new Date(promo.endsAt) - now) / 60000);
    if (promoEndMin > 0 && promoEndMin < totalMin) totalMin = promoEndMin;
  }

  return Math.max(0, totalMin);
}

// Format minutes into a compact countdown string
function formatPromoCountdown(minutes) {
  if (minutes <= 0) return '';
  if (minutes >= 1440) {
    const d = Math.floor(minutes / 1440);
    const h = Math.floor((minutes % 1440) / 60);
    return h > 0 ? d + 'd ' + h + 'h' : d + 'd';
  }
  if (minutes >= 60) {
    const h = Math.floor(minutes / 60);
    const m = minutes % 60;
    return m > 0 ? h + 'h ' + m + 'm' : h + 'h';
  }
  return minutes + 'm';
}

// Build the full promo label text with countdown
function promoLabelWithCountdown(promo) {
  const isPeak = isAnthropicPeakHours(promo);
  const label = isPeak ? 'Standard limits' : '2x limits';
  const mins = promoMinutesUntilTransition(promo);
  const countdown = formatPromoCountdown(mins);
  return countdown ? label + ' till ' + countdown : label;
}

// Store current promo for card rendering
let _anthropicPromo = null;

function updateAnthropicPromoState(promo) {
  _anthropicPromo = promo || null;
  refreshPromoTags();
  // Re-evaluate every 30s for countdown updates (no API call)
  if (window._promoInterval) clearInterval(window._promoInterval);
  if (_anthropicPromo) {
    window._promoInterval = setInterval(refreshPromoTags, 30000);
  }
}

function refreshPromoTags() {
  document.querySelectorAll('.promo-tag-inline').forEach(el => {
    if (!_anthropicPromo) { el.remove(); return; }
    const isPeak = isAnthropicPeakHours(_anthropicPromo);
    el.className = 'promo-tag-inline ' + (isPeak ? 'promo-peak' : 'promo-offpeak');
    el.textContent = promoLabelWithCountdown(_anthropicPromo);
  });
}

function promoTagHTML() {
  if (!_anthropicPromo) return '';
  const isPeak = isAnthropicPeakHours(_anthropicPromo);
  const cls = isPeak ? 'promo-peak' : 'promo-offpeak';
  const text = promoLabelWithCountdown(_anthropicPromo);
  return `<a class="promo-tag-inline ${cls}" href="https://support.claude.com/en/articles/14063676-claude-march-2026-usage-promotion" target="_blank" rel="noopener noreferrer" onclick="event.stopPropagation()" title="${escapeHTML(_anthropicPromo.description)}">${text}</a>`;
}

function renderAnthropicQuotaCards(quotas, containerId) {
  const container = document.getElementById(containerId);
  if (!container) return;

  // Build cards for each quota
  container.innerHTML = quotas.map((q, i) => {
    const icon = anthropicQuotaIcons[q.name] || '<circle cx="12" cy="12" r="10"/><path d="M12 16v-4M12 8h.01"/>';
    const displayName = q.displayName || anthropicDisplayNames[q.name] || q.name;
    const utilPct = (q.utilization || 0).toFixed(1);
    const status = q.status || 'healthy';
    const statusCfg = statusConfig[status] || statusConfig.healthy;
    const countdownId = `countdown-anth-${q.name}`;
    const progressId = `progress-anth-${q.name}`;
    const percentId = `percent-anth-${q.name}`;
    const statusId = `status-anth-${q.name}`;
    const resetId = `reset-anth-${q.name}`;

    return `<article class="quota-card anthropic-card${q.isStale ? ' stale-card' : ''}" data-quota="${q.name}" data-provider="anthropic" role="button" tabindex="0" aria-label="View ${displayName} details" style="animation-delay: ${i * 60}ms">
      <header class="card-header">
        <h2 class="quota-title">
          <svg class="quota-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">${icon}</svg>
          ${displayName}
        </h2>
        <span class="countdown" id="${countdownId}">${q.timeUntilResetSeconds > 0 ? formatDuration(q.timeUntilResetSeconds) : '--:--'}</span>
      </header>
      <div class="progress-stats">
        <span class="usage-percent" id="${percentId}">${utilPct}%</span>
        <span class="usage-fraction">Utilization</span>
      </div>
      <div class="progress-wrapper">
        <div class="progress-bar" role="progressbar" aria-valuenow="${Math.round(q.utilization || 0)}" aria-valuemin="0" aria-valuemax="100">
          <div class="progress-fill" id="${progressId}" style="width: ${utilPct}%" data-status="${status}"></div>
        </div>
      </div>
      ${q.ageSeconds != null ? `<div class="card-freshness${q.isStale ? ' stale' : ''}">${freshnessLabel(q)}</div>` : ''}
      <footer class="card-footer">
        <span class="status-badge" id="${statusId}" data-status="${status}">
          <svg class="status-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="${statusCfg.icon}"/></svg>
          ${statusCfg.label}
        </span>
        ${promoTagHTML()}
        <span class="reset-time" id="${resetId}">${q.resetsAt ? 'Resets: ' + formatDateTime(q.resetsAt) : ''}</span>
      </footer>
    </article>`;
  }).join('');

  // Re-attach modal click handlers for new cards
  container.querySelectorAll('.quota-card[role="button"]').forEach(card => {
    const handler = () => {
      const providerCol = card.closest('.provider-column');
      const providerOverride = providerCol ? providerCol.dataset.provider : 'anthropic';
      openAnthropicModal(card.dataset.quota, providerOverride);
    };
    card.addEventListener('click', handler);
    card.addEventListener('keydown', e => {
      if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); handler(); }
    });
  });
}

function updateAnthropicCard(quota) {
  const key = `anth-${quota.name}`;
  const prev = State.currentQuotas[key];
  State.currentQuotas[key] = {
    percent: quota.utilization || 0,
    usage: quota.utilization || 0,
    limit: 100,
    status: quota.status || 'healthy',
    renewsAt: quota.resetsAt,
    timeUntilReset: quota.timeUntilReset,
    timeUntilResetSeconds: quota.timeUntilResetSeconds || 0,
    name: quota.name,
    displayName: quota.displayName,
    source: quota.source || '',
    ageSeconds: quota.ageSeconds || 0,
    isStale: quota.isStale || false
  };

  const progressEl = document.getElementById(`progress-anth-${quota.name}`);
  const percentEl = document.getElementById(`percent-anth-${quota.name}`);
  const statusEl = document.getElementById(`status-anth-${quota.name}`);
  const resetEl = document.getElementById(`reset-anth-${quota.name}`);
  const countdownEl = document.getElementById(`countdown-anth-${quota.name}`);

  const utilPct = (quota.utilization || 0).toFixed(1);
  const status = quota.status || 'healthy';

  if (progressEl) {
    progressEl.style.width = `${utilPct}%`;
    progressEl.setAttribute('data-status', status);
  }
  if (percentEl) {
    const oldVal = prev ? prev.percent : 0;
    if (Math.abs(oldVal - quota.utilization) > 0.2) {
      animateValue(percentEl, oldVal, quota.utilization, 400, v => `${v.toFixed(1)}%`);
    } else {
      percentEl.textContent = `${utilPct}%`;
    }
  }
  if (statusEl) {
    const config = statusConfig[status] || statusConfig.healthy;
    statusEl.setAttribute('data-status', status);
    statusEl.innerHTML = `<svg class="status-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="${config.icon}"/></svg>${config.label}`;
  }
  if (resetEl) {
    resetEl.textContent = quota.resetsAt ? `Resets: ${formatDateTime(quota.resetsAt)}` : '';
  }
  if (countdownEl) {
    if (quota.timeUntilResetSeconds > 0) {
      countdownEl.textContent = formatDuration(quota.timeUntilResetSeconds);
      countdownEl.classList.toggle('imminent', quota.timeUntilResetSeconds < 1800);
      countdownEl.style.display = '';
    } else {
      countdownEl.style.display = 'none';
    }
  }
}

// Anthropic quota detail modal
function openAnthropicModal(quotaName, providerOverride) {
  const key = `anth-${quotaName}`;
  const data = State.currentQuotas[key];
  if (!data) return;

  const modal = document.getElementById('detail-modal');
  const titleEl = document.getElementById('modal-title');
  const bodyEl = document.getElementById('modal-body');
  if (!modal || !bodyEl) return;

  const displayName = data.displayName || anthropicDisplayNames[quotaName] || quotaName;
  titleEl.textContent = displayName;

  const statusCfg = statusConfig[data.status] || statusConfig.healthy;
  const timeLeft = data.timeUntilResetSeconds > 0 ? formatDuration(data.timeUntilResetSeconds) : 'N/A';
  const sourceKpi = data.source ? `
      <div class="modal-kpi">
        <div class="modal-kpi-value">${data.source === 'statusline' ? '\u{1F7E2} Live' : '\u{1F310} API'}</div>
        <div class="modal-kpi-label">Source${data.ageSeconds ? ' \u00B7 ' + freshnessLabel({source: data.source, ageSeconds: data.ageSeconds}) : ''}</div>
      </div>` : '';

  bodyEl.innerHTML = `
    <div class="modal-kpi-row">
      <div class="modal-kpi">
        <div class="modal-kpi-value">${data.percent.toFixed(1)}%</div>
        <div class="modal-kpi-label">Utilization</div>
      </div>
      <div class="modal-kpi">
        <div class="modal-kpi-value"><span class="status-badge" data-status="${data.status}"><svg class="status-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="${statusCfg.icon}"/></svg>${statusCfg.label}</span></div>
        <div class="modal-kpi-label">Status</div>
      </div>
      <div class="modal-kpi">
        <div class="modal-kpi-value">${timeLeft}</div>
        <div class="modal-kpi-label">Until Reset</div>
      </div>
      ${sourceKpi}
    </div>
    <h3 class="modal-section-title">Usage History</h3>
    <div class="modal-chart-container">
      <canvas id="modal-chart"></canvas>
    </div>
    <h3 class="modal-section-title">Recent Cycles</h3>
    <div class="table-wrapper">
      <table class="data-table" id="modal-cycles-table">
        <thead><tr><th>Cycle</th><th>Duration</th><th>Peak %</th><th>Total %</th></tr></thead>
        <tbody id="modal-cycles-tbody"><tr><td colspan="4" class="empty-state">Loading...</td></tr></tbody>
      </table>
    </div>
  `;

  modal.hidden = false;
  document.getElementById('modal-close').focus();

  // Load chart and cycles for this Anthropic quota
  loadAnthropicModalChart(quotaName);
  loadAnthropicModalCycles(quotaName);
}

async function loadAnthropicModalChart(quotaName) {
  const ctx = document.getElementById('modal-chart');
  if (!ctx || typeof Chart === 'undefined') return;
  if (State.modalChart) { State.modalChart.destroy(); State.modalChart = null; }

  const range = State.currentRange || '6h';
  const rangeKey = range.toLowerCase();
  const timeUnit = ['7d', '30d', '15d'].includes(rangeKey) ? 'day' : 'hour';

  try {
    const res = await authFetch(`${API_BASE}/api/history?range=${range}&provider=anthropic`);
    if (!res.ok) return;
    const data = await res.json();
    if (!Array.isArray(data) || data.length === 0) return;

    const colors = getThemeColors();
    const rawData = data.map(d => ({ x: new Date(d.capturedAt), y: d[quotaName] || 0 }));
    const processed = processDataWithGaps(rawData, range);
    const maxVal = Math.max(...data.map(d => d[quotaName] || 0), 0);
    let yMax = maxVal <= 0 ? 10 : maxVal < 5 ? 10 : Math.min(Math.max(Math.ceil((maxVal * 1.2) / 5) * 5, 10), 100);

    State.modalChart = new Chart(ctx, {
      type: 'line',
      data: {
        datasets: [(() => { const c = anthropicChartColorMap[quotaName] || { border: '#D97706', bg: 'rgba(217, 119, 6, 0.08)' }; return {
          label: anthropicDisplayNames[quotaName] || quotaName,
          data: processed.data,
          borderColor: c.border,
          backgroundColor: c.bg,
          fill: true,
          tension: 0.3,
          borderWidth: 2.5,
          pointRadius: processed.pointRadii,
          pointHoverRadius: 5,
          spanGaps: true,
          segment: getSegmentStyle(processed.gapSegments, c.border)
        }; })()]
      },
      options: {
        responsive: true, maintainAspectRatio: false,
        plugins: {
          legend: { display: false },
          tooltip: { backgroundColor: colors.surfaceContainer, titleColor: colors.onSurface, bodyColor: colors.text, borderColor: colors.outline, borderWidth: 1, callbacks: { label: c => `${c.parsed.y.toFixed(1)}%` } }
        },
        scales: {
          x: { type: 'time', time: { unit: timeUnit, displayFormats: { minute: 'HH:mm', hour: ['7d', '30d', '15d', '24h', '3d'].includes(rangeKey) ? 'MMM d, HH:mm' : 'HH:mm', day: 'MMM d' } }, grid: { color: colors.grid, drawBorder: false }, ticks: { color: colors.text, maxTicksLimit: 6, source: 'auto' } },
          y: { grid: { color: colors.grid, drawBorder: false }, ticks: { color: colors.text, callback: v => v + '%' }, min: 0, max: yMax }
        }
      }
    });
  } catch (err) { /* modal chart error - non-critical */ }
}

async function loadAnthropicModalCycles(quotaName) {
  try {
    const res = await authFetch(`${API_BASE}/api/cycles?type=${quotaName}&provider=anthropic`);
    if (!res.ok) return;
    const cycles = await res.json();
    const tbody = document.getElementById('modal-cycles-tbody');
    if (!tbody) return;
    const recent = cycles.slice(0, 5);
    if (recent.length === 0) {
      tbody.innerHTML = '<tr><td colspan="4" class="empty-state">No cycles yet.</td></tr>';
      return;
    }
    tbody.innerHTML = recent.map(cycle => {
      const start = new Date(cycle.cycleStart);
      const end = cycle.cycleEnd ? new Date(cycle.cycleEnd) : new Date();
      const durationMins = Math.round((end - start) / 60000);
      const isActive = !cycle.cycleEnd;
      return `<tr>
        <td>#${cycle.id}${isActive ? ' <span class="badge">Active</span>' : ''}</td>
        <td>${formatDurationMins(durationMins)}</td>
        <td>${formatNumber(cycle.peakUtilization || 0)}%</td>
        <td>${formatNumber(cycle.totalDelta || 0)}%</td>
      </tr>`;
    }).join('');
  } catch (err) { /* modal cycles error - non-critical */ }
}

// ── Copilot Dynamic Card Rendering ──

function renderCopilotQuotaCards(quotas, containerId) {
  const container = document.getElementById(containerId);
  if (!container) return;

  // Build cards for each quota
  container.innerHTML = quotas.map((q, i) => {
    const icon = copilotQuotaIcons[q.name] || '<circle cx="12" cy="12" r="10"/><path d="M12 16v-4M12 8h.01"/>';
    const displayName = q.displayName || copilotDisplayNames[q.name] || q.name;
    const usagePct = (q.usagePercent || 0).toFixed(1);
    const status = q.status || 'healthy';
    const statusCfg = statusConfig[status] || statusConfig.healthy;
    const countdownId = `countdown-copilot-${q.name}`;
    const progressId = `progress-copilot-${q.name}`;
    const percentId = `percent-copilot-${q.name}`;
    const fractionId = `fraction-copilot-${q.name}`;
    const statusId = `status-copilot-${q.name}`;
    const resetId = `reset-copilot-${q.name}`;

    // Format the usage fraction (remaining / entitlement or ∞ for unlimited)
    let fractionText = '';
    if (q.unlimited) {
      fractionText = '∞ Unlimited';
    } else {
      const used = q.entitlement - q.remaining;
      fractionText = `${formatNumber(used)} / ${formatNumber(q.entitlement)}`;
    }

    return `<article class="quota-card copilot-card" data-quota="${q.name}" data-provider="copilot" role="button" tabindex="0" aria-label="View ${displayName} details" style="animation-delay: ${i * 60}ms">
      <header class="card-header">
        <h2 class="quota-title">
          <svg class="quota-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">${icon}</svg>
          ${displayName}
          ${q.unlimited ? '<span class="unlimited-badge">∞</span>' : ''}
        </h2>
        <span class="countdown" id="${countdownId}">${q.timeUntilResetSeconds > 0 ? formatDuration(q.timeUntilResetSeconds) : '--:--'}</span>
      </header>
      <div class="progress-stats">
        <span class="usage-percent" id="${percentId}">${q.unlimited ? '0' : usagePct}%</span>
        <span class="usage-fraction" id="${fractionId}">${fractionText}</span>
      </div>
      <div class="progress-wrapper">
        <div class="progress-bar" role="progressbar" aria-valuenow="${q.unlimited ? 0 : Math.round(q.usagePercent || 0)}" aria-valuemin="0" aria-valuemax="100">
          <div class="progress-fill" id="${progressId}" style="width: ${q.unlimited ? 0 : usagePct}%" data-status="${status}"></div>
        </div>
      </div>
      <footer class="card-footer">
        <span class="status-badge" id="${statusId}" data-status="${status}">
          <svg class="status-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="${statusCfg.icon}"/></svg>
          ${statusCfg.label}
        </span>
        <span class="reset-time" id="${resetId}">${q.resetDate ? 'Resets: ' + formatDateTime(q.resetDate) : ''}</span>
      </footer>
    </article>`;
  }).join('');

  // Re-attach modal click handlers for new cards
  container.querySelectorAll('.quota-card[role="button"]').forEach(card => {
    const handler = () => {
      const providerCol = card.closest('.provider-column');
      const providerOverride = providerCol ? providerCol.dataset.provider : 'copilot';
      openCopilotModal(card.dataset.quota, providerOverride);
    };
    card.addEventListener('click', handler);
    card.addEventListener('keydown', e => {
      if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); handler(); }
    });
  });
}

function updateCopilotCard(quota) {
  const key = `copilot-${quota.name}`;
  const prev = State.currentQuotas[key];
  State.currentQuotas[key] = {
    percent: quota.usagePercent || 0,
    usage: quota.entitlement - quota.remaining,
    limit: quota.entitlement,
    remaining: quota.remaining,
    unlimited: quota.unlimited,
    status: quota.status || 'healthy',
    renewsAt: quota.resetDate,
    timeUntilReset: quota.timeUntilReset,
    timeUntilResetSeconds: quota.timeUntilResetSeconds || 0,
    name: quota.name,
    displayName: quota.displayName
  };

  const progressEl = document.getElementById(`progress-copilot-${quota.name}`);
  const percentEl = document.getElementById(`percent-copilot-${quota.name}`);
  const fractionEl = document.getElementById(`fraction-copilot-${quota.name}`);
  const statusEl = document.getElementById(`status-copilot-${quota.name}`);
  const resetEl = document.getElementById(`reset-copilot-${quota.name}`);
  const countdownEl = document.getElementById(`countdown-copilot-${quota.name}`);

  const usagePct = quota.unlimited ? 0 : (quota.usagePercent || 0).toFixed(1);
  const status = quota.status || 'healthy';

  if (progressEl) {
    progressEl.style.width = `${usagePct}%`;
    progressEl.setAttribute('data-status', status);
  }
  if (percentEl) {
    const oldVal = prev ? prev.percent : 0;
    if (!quota.unlimited && Math.abs(oldVal - quota.usagePercent) > 0.2) {
      animateValue(percentEl, oldVal, quota.usagePercent, 400, v => `${v.toFixed(1)}%`);
    } else {
      percentEl.textContent = quota.unlimited ? '0%' : `${usagePct}%`;
    }
  }
  if (fractionEl) {
    if (quota.unlimited) {
      fractionEl.textContent = '∞ Unlimited';
    } else {
      const used = quota.entitlement - quota.remaining;
      fractionEl.textContent = `${formatNumber(used)} / ${formatNumber(quota.entitlement)}`;
    }
  }
  if (statusEl) {
    const config = statusConfig[status] || statusConfig.healthy;
    statusEl.setAttribute('data-status', status);
    statusEl.innerHTML = `<svg class="status-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="${config.icon}"/></svg>${config.label}`;
  }
  if (resetEl) {
    resetEl.textContent = quota.resetDate ? `Resets: ${formatDateTime(quota.resetDate)}` : '';
  }
  if (countdownEl) {
    if (quota.timeUntilResetSeconds > 0) {
      countdownEl.textContent = formatDuration(quota.timeUntilResetSeconds);
      countdownEl.classList.toggle('imminent', quota.timeUntilResetSeconds < 1800);
      countdownEl.style.display = '';
    } else {
      countdownEl.style.display = 'none';
    }
  }
}

function minimaxCardKey(name) {
  return String(name || '').replace(/[^a-zA-Z0-9_-]/g, '-');
}

function minimaxSharedSubtitle(sharedModels) {
  if (!Array.isArray(sharedModels) || sharedModels.length === 0) return '';
  const labels = sharedModels
    .map(name => String(name || '').replace(/^MiniMax-/, ''))
    .filter(Boolean);
  return labels.length > 0 ? `Shared: ${labels.join(', ')}` : '';
}

function renderMiniMaxQuotaCards(quotas, containerId) {
  const container = document.getElementById(containerId);
  if (!container) return;

  container.innerHTML = quotas.map((q, i) => {
    const cardKey = minimaxCardKey(q.name);
    const displayName = q.displayName || minimaxDisplayNames[q.name] || q.name;
    const subtitle = minimaxSharedSubtitle(q.sharedModels);
    const usagePct = (q.usagePercent || 0).toFixed(1);
    const status = q.status || 'healthy';
    const statusCfg = statusConfig[status] || statusConfig.healthy;
    const countdownId = `countdown-minimax-${cardKey}`;
    const progressId = `progress-minimax-${cardKey}`;
    const percentId = `percent-minimax-${cardKey}`;
    const fractionId = `fraction-minimax-${cardKey}`;
    const statusId = `status-minimax-${cardKey}`;
    const resetId = `reset-minimax-${cardKey}`;
    const subtitleId = `subtitle-minimax-${cardKey}`;

    const isWeekly = q.isWeekly || false;
    const weeklyBadge = isWeekly ? ' <span class="weekly-badge">Weekly</span>' : '';

    return `<article class="quota-card minimax-card${isWeekly ? ' minimax-weekly' : ''}" data-quota="${q.name}" data-provider="minimax" style="animation-delay: ${i * 60}ms">
      <header class="card-header">
        <div class="quota-title-block">
          <h2 class="quota-title">
            <svg class="quota-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M12 2L2 7l10 5 10-5-10-5zM2 17l10 5 10-5M2 12l10 5 10-5"/></svg>
            ${escapeHTML(displayName)}${weeklyBadge}
          </h2>
          <div class="quota-subtitle" id="${subtitleId}"${subtitle ? '' : ' hidden'}>${escapeHTML(subtitle)}</div>
        </div>
        <span class="countdown" id="${countdownId}">${q.timeUntilResetSeconds > 0 ? formatDuration(q.timeUntilResetSeconds) : '--:--'}</span>
      </header>
      <div class="progress-stats">
        <span class="usage-percent" id="${percentId}">${usagePct}%</span>
        <span class="usage-fraction" id="${fractionId}">${formatNumber(q.used || 0)} / ${formatNumber(q.total || 0)}</span>
      </div>
      <div class="progress-wrapper">
        <div class="progress-bar" role="progressbar" aria-valuenow="${Math.round(q.usagePercent || 0)}" aria-valuemin="0" aria-valuemax="100">
          <div class="progress-fill" id="${progressId}" style="width: ${usagePct}%" data-status="${status}"></div>
        </div>
      </div>
      <footer class="card-footer">
        <span class="status-badge" id="${statusId}" data-status="${status}">
          <svg class="status-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="${statusCfg.icon}"/></svg>
          ${statusCfg.label}
        </span>
        <span class="reset-time" id="${resetId}">${q.resetAt ? 'Resets: ' + formatDateTime(q.resetAt) : ''}</span>
      </footer>
    </article>`;
  }).join('');
}

function updateMiniMaxCard(quota) {
  const cardKey = minimaxCardKey(quota.name);
  const key = `minimax-${quota.name}`;
  State.currentQuotas[key] = {
    percent: quota.usagePercent || 0,
    usage: quota.used || 0,
    limit: quota.total || 0,
    status: quota.status || 'healthy',
    renewsAt: quota.resetAt,
    timeUntilResetSeconds: quota.timeUntilResetSeconds || 0,
    name: quota.name,
    displayName: quota.displayName,
    sharedModels: quota.sharedModels || []
  };

  const usagePct = (quota.usagePercent || 0).toFixed(1);
  const status = quota.status || 'healthy';
  const progressEl = document.getElementById(`progress-minimax-${cardKey}`);
  const percentEl = document.getElementById(`percent-minimax-${cardKey}`);
  const fractionEl = document.getElementById(`fraction-minimax-${cardKey}`);
  const statusEl = document.getElementById(`status-minimax-${cardKey}`);
  const resetEl = document.getElementById(`reset-minimax-${cardKey}`);
  const countdownEl = document.getElementById(`countdown-minimax-${cardKey}`);
  const subtitleEl = document.getElementById(`subtitle-minimax-${cardKey}`);
  const subtitle = minimaxSharedSubtitle(quota.sharedModels);

  if (progressEl) {
    progressEl.style.width = `${usagePct}%`;
    progressEl.setAttribute('data-status', status);
  }
  if (percentEl) percentEl.textContent = `${usagePct}%`;
  if (fractionEl) fractionEl.textContent = `${formatNumber(quota.used || 0)} / ${formatNumber(quota.total || 0)}`;
  if (statusEl) {
    const config = statusConfig[status] || statusConfig.healthy;
    statusEl.setAttribute('data-status', status);
    statusEl.innerHTML = `<svg class="status-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="${config.icon}"/></svg>${config.label}`;
  }
  if (resetEl) resetEl.textContent = quota.resetAt ? `Resets: ${formatDateTime(quota.resetAt)}` : '';
  if (subtitleEl) {
    subtitleEl.textContent = subtitle;
    subtitleEl.hidden = !subtitle;
  }
  if (countdownEl) {
    if (quota.timeUntilResetSeconds > 0) {
      countdownEl.textContent = formatDuration(quota.timeUntilResetSeconds);
      countdownEl.classList.toggle('imminent', quota.timeUntilResetSeconds < 1800);
      countdownEl.style.display = '';
    } else {
      countdownEl.style.display = 'none';
    }
  }
}

// Copilot quota detail modal
function openCopilotModal(quotaName, providerOverride) {
  const key = `copilot-${quotaName}`;
  const data = State.currentQuotas[key];
  if (!data) return;

  const modal = document.getElementById('detail-modal');
  const titleEl = document.getElementById('modal-title');
  const bodyEl = document.getElementById('modal-body');
  if (!modal || !bodyEl) return;

  const displayName = data.displayName || copilotDisplayNames[quotaName] || quotaName;
  titleEl.textContent = displayName;

  const usagePct = data.unlimited ? 0 : (data.percent || 0).toFixed(1);
  const used = data.unlimited ? 0 : (data.limit - data.remaining);

  bodyEl.innerHTML = `
    <div class="modal-stats-grid">
      <div class="modal-stat">
        <span class="modal-stat-label">Usage</span>
        <span class="modal-stat-value">${usagePct}%</span>
      </div>
      <div class="modal-stat">
        <span class="modal-stat-label">Used / Total</span>
        <span class="modal-stat-value">${data.unlimited ? '∞' : formatNumber(used) + ' / ' + formatNumber(data.limit)}</span>
      </div>
      <div class="modal-stat">
        <span class="modal-stat-label">Status</span>
        <span class="modal-stat-value" data-status="${data.status}">${(statusConfig[data.status] || statusConfig.healthy).label}</span>
      </div>
      <div class="modal-stat">
        <span class="modal-stat-label">Resets In</span>
        <span class="modal-stat-value">${data.timeUntilResetSeconds > 0 ? formatDuration(data.timeUntilResetSeconds) : '--'}</span>
      </div>
    </div>
    <div class="modal-chart-container">
      <canvas id="modal-chart" height="200"></canvas>
    </div>
    <h4 class="modal-section-title">Recent Cycles</h4>
    <table class="modal-cycles-table">
      <thead><tr><th>Cycle</th><th>Duration</th><th>Peak Used</th><th>Total Delta</th></tr></thead>
      <tbody id="modal-cycles-tbody"><tr><td colspan="4">Loading...</td></tr></tbody>
    </table>
  `;

  modal.classList.add('open');
  document.body.classList.add('modal-open');
  modal.querySelector('.modal-close')?.focus();

  loadCopilotModalChart(quotaName);
  loadCopilotModalCycles(quotaName);
}

async function loadCopilotModalChart(quotaName) {
  const range = State.currentRange || '6h';
  const rangeKey = range.toLowerCase();
  const timeUnit = ['7d', '30d', '15d'].includes(rangeKey) ? 'day' : 'hour';
  try {
    const res = await authFetch(`${API_BASE}/api/history?range=${range}&provider=copilot`);
    if (!res.ok) return;
    const history = await res.json();
    if (!history || history.length === 0) return;

    // Extract data points for this quota
    const data = history.filter(h => {
      if (!h.quotas) return false;
      return h.quotas.some(q => q.name === quotaName);
    }).map(h => {
      const q = h.quotas.find(q => q.name === quotaName);
      return { capturedAt: h.capturedAt, usagePercent: q ? q.usagePercent : 0 };
    });

    if (data.length === 0) return;

    const canvas = document.getElementById('modal-chart');
    if (!canvas) return;
    const ctx = canvas.getContext('2d');

    // Clean up existing chart if any
    if (State.modalChart) {
      State.modalChart.destroy();
      State.modalChart = null;
    }

    const colors = getThemeColors();
    const rawData = data.map(d => ({ x: new Date(d.capturedAt), y: d.usagePercent }));
    const processed = processDataWithGaps(rawData, range);
    const maxVal = Math.max(...data.map(d => d.usagePercent), 10);
    const yMax = Math.min(Math.ceil(maxVal / 10) * 10 + 10, 110);

    State.modalChart = new Chart(ctx, {
      type: 'line',
      data: {
        datasets: [(() => { const c = copilotChartColorMap[quotaName] || { border: '#6e40c9', bg: 'rgba(110, 64, 201, 0.08)' }; return {
          label: copilotDisplayNames[quotaName] || quotaName,
          data: processed.data,
          borderColor: c.border,
          backgroundColor: c.bg,
          fill: true,
          tension: 0.3,
          borderWidth: 2.5,
          pointRadius: processed.pointRadii,
          pointHoverRadius: 5,
          spanGaps: true,
          segment: getSegmentStyle(processed.gapSegments, c.border)
        }; })()]
      },
      options: {
        responsive: true, maintainAspectRatio: false,
        plugins: {
          legend: { display: false },
          tooltip: { backgroundColor: colors.surfaceContainer, titleColor: colors.onSurface, bodyColor: colors.text, borderColor: colors.outline, borderWidth: 1, callbacks: { label: c => `${c.parsed.y.toFixed(1)}%` } }
        },
        scales: {
          x: { type: 'time', time: { unit: timeUnit, displayFormats: { minute: 'HH:mm', hour: ['7d', '30d', '15d', '24h', '3d'].includes(rangeKey) ? 'MMM d, HH:mm' : 'HH:mm', day: 'MMM d' } }, grid: { color: colors.grid, drawBorder: false }, ticks: { color: colors.text, maxTicksLimit: 6, source: 'auto' } },
          y: { grid: { color: colors.grid, drawBorder: false }, ticks: { color: colors.text, callback: v => v + '%' }, min: 0, max: yMax }
        }
      }
    });
  } catch (err) { /* modal chart error - non-critical */ }
}

async function loadCopilotModalCycles(quotaName) {
  try {
    const res = await authFetch(`${API_BASE}/api/cycles?type=${quotaName}&provider=copilot`);
    if (!res.ok) return;
    const cycles = await res.json();
    const tbody = document.getElementById('modal-cycles-tbody');
    if (!tbody) return;
    const recent = cycles.slice(0, 5);
    if (recent.length === 0) {
      tbody.innerHTML = '<tr><td colspan="4" class="empty-state">No cycles yet.</td></tr>';
      return;
    }
    tbody.innerHTML = recent.map(cycle => {
      const start = new Date(cycle.cycleStart);
      const end = cycle.cycleEnd ? new Date(cycle.cycleEnd) : new Date();
      const durationMins = Math.round((end - start) / 60000);
      const isActive = !cycle.cycleEnd;
      return `<tr>
        <td>#${cycle.id}${isActive ? ' <span class="badge">Active</span>' : ''}</td>
        <td>${formatDurationMins(durationMins)}</td>
        <td>${formatNumber(cycle.peakUsed || 0)}</td>
        <td>${formatNumber(cycle.totalDelta || 0)}</td>
      </tr>`;
    }).join('');
  } catch (err) { /* modal cycles error - non-critical */ }
}

// ── Antigravity Dynamic Card Rendering ──

// Antigravity model icons by model ID pattern
const antigravityQuotaIcons = {
  'claude': '<path d="M12 2L2 7l10 5 10-5-10-5zM2 17l10 5 10-5M2 12l10 5 10-5"/>',
  'gemini': '<polygon points="12 2 15.09 8.26 22 9.27 17 14.14 18.18 21.02 12 17.77 5.82 21.02 7 14.14 2 9.27 8.91 8.26 12 2"/>',
  'gpt': '<circle cx="12" cy="12" r="10"/><path d="M12 16v-4M12 8h.01"/>',
  'default': '<path d="M12 2L2 7l10 5 10-5-10-5zM2 17l10 5 10-5M2 12l10 5 10-5"/>'
};

function getAntigravityIcon(modelId) {
  const lower = (modelId || '').toLowerCase();
  if (lower.includes('claude')) return antigravityQuotaIcons['claude'];
  if (lower.includes('gpt')) return antigravityQuotaIcons['gpt'];
  if (lower.includes('gemini')) return antigravityQuotaIcons['gemini'];
  return antigravityQuotaIcons['default'];
}

function getAntigravityGroupColumns(quota) {
  const labels = Array.isArray(quota.modelLabels) ? quota.modelLabels : [];
  return [
    labels[0] || '--',
    labels[1] || '--',
    labels[2] || '--'
  ];
}

function renderAntigravityQuotaCards(quotas, containerId) {
  const container = document.getElementById(containerId);
  if (!container) return;

  container.innerHTML = quotas.map((q, i) => {
    const icon = getAntigravityIcon(q.modelId);
    const displayName = q.displayName || q.label || q.modelId;
    const usagePct = (q.usagePercent || 0).toFixed(1);
    const status = q.status || 'healthy';
    const statusCfg = statusConfig[status] || statusConfig.healthy;
    const countdownId = `countdown-antigravity-${q.modelId}`;
    const progressId = `progress-antigravity-${q.modelId}`;
    const percentId = `percent-antigravity-${q.modelId}`;
    const fractionId = `fraction-antigravity-${q.modelId}`;
    const statusId = `status-antigravity-${q.modelId}`;
    const resetId = `reset-antigravity-${q.modelId}`;

    // Format the remaining percent
    const remainingPct = (q.remainingPercent || 0).toFixed(1);
    const fractionText = `${remainingPct}% remaining`;

    return `<article class="quota-card antigravity-card" data-quota="${q.modelId}" data-provider="antigravity" role="button" tabindex="0" aria-label="View ${displayName} details" style="animation-delay: ${i * 60}ms">
      <header class="card-header">
        <h2 class="quota-title">
          <svg class="quota-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">${icon}</svg>
          ${displayName}
          ${q.isExhausted ? '<span class="exhausted-badge">Exhausted</span>' : ''}
        </h2>
        <span class="countdown" id="${countdownId}">${q.timeUntilResetSeconds > 0 ? formatDuration(q.timeUntilResetSeconds) : '--:--'}</span>
      </header>
      <div class="progress-stats">
        <span class="usage-percent" id="${percentId}">${usagePct}%</span>
        <span class="usage-fraction" id="${fractionId}">${fractionText}</span>
      </div>
      <div class="progress-wrapper">
        <div class="progress-bar" role="progressbar" aria-valuenow="${Math.round(q.usagePercent || 0)}" aria-valuemin="0" aria-valuemax="100">
          <div class="progress-fill" id="${progressId}" style="width: ${usagePct}%" data-status="${status}"></div>
        </div>
      </div>
      <footer class="card-footer">
        <span class="status-badge" id="${statusId}" data-status="${status}">
          <svg class="status-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="${statusCfg.icon}"/></svg>
          ${statusCfg.label}
        </span>
        <span class="reset-time" id="${resetId}">${q.resetTime ? 'Resets: ' + formatDateTime(q.resetTime) : ''}</span>
      </footer>
    </article>`;
  }).join('');

  // Re-attach modal click handlers for new cards
  container.querySelectorAll('.quota-card[role="button"]').forEach(card => {
    const handler = () => {
      const providerCol = card.closest('.provider-column');
      const providerOverride = providerCol ? providerCol.dataset.provider : 'antigravity';
      openAntigravityModal(card.dataset.quota, providerOverride);
    };
    card.addEventListener('click', handler);
    card.addEventListener('keydown', e => {
      if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); handler(); }
    });
  });
}

function updateAntigravityCard(quota) {
  const key = `antigravity-${quota.modelId}`;
  const prev = State.currentQuotas[key];
  State.currentQuotas[key] = {
    percent: quota.usagePercent || 0,
    remainingFraction: quota.remainingFraction,
    remainingPercent: quota.remainingPercent,
    isExhausted: quota.isExhausted,
    status: quota.status || 'healthy',
    resetTime: quota.resetTime,
    timeUntilReset: quota.timeUntilReset,
    timeUntilResetSeconds: quota.timeUntilResetSeconds || 0,
    modelId: quota.modelId,
    label: quota.label,
    displayName: quota.displayName
  };

  const progressEl = document.getElementById(`progress-antigravity-${quota.modelId}`);
  const percentEl = document.getElementById(`percent-antigravity-${quota.modelId}`);
  const fractionEl = document.getElementById(`fraction-antigravity-${quota.modelId}`);
  const statusEl = document.getElementById(`status-antigravity-${quota.modelId}`);
  const resetEl = document.getElementById(`reset-antigravity-${quota.modelId}`);
  const countdownEl = document.getElementById(`countdown-antigravity-${quota.modelId}`);

  const usagePct = (quota.usagePercent || 0).toFixed(1);
  const status = quota.status || 'healthy';

  if (progressEl) {
    progressEl.style.width = `${usagePct}%`;
    progressEl.setAttribute('data-status', status);
  }
  if (percentEl) {
    const oldVal = prev ? prev.percent : 0;
    if (Math.abs(oldVal - quota.usagePercent) > 0.2) {
      animateValue(percentEl, oldVal, quota.usagePercent, 400, v => `${v.toFixed(1)}%`);
    } else {
      percentEl.textContent = `${usagePct}%`;
    }
  }
  if (fractionEl) {
    const remainingPct = (quota.remainingPercent || 0).toFixed(1);
    fractionEl.textContent = `${remainingPct}% remaining`;
  }
  if (statusEl) {
    const config = statusConfig[status] || statusConfig.healthy;
    statusEl.setAttribute('data-status', status);
    statusEl.innerHTML = `<svg class="status-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="${config.icon}"/></svg>${config.label}`;
  }
  if (resetEl) {
    resetEl.textContent = quota.resetTime ? `Resets: ${formatDateTime(quota.resetTime)}` : '';
  }
  if (countdownEl) {
    if (quota.timeUntilResetSeconds > 0) {
      countdownEl.textContent = formatDuration(quota.timeUntilResetSeconds);
      countdownEl.classList.toggle('imminent', quota.timeUntilResetSeconds < 1800);
      countdownEl.style.display = '';
    } else {
      countdownEl.style.display = 'none';
    }
  }
}

// ── Gemini Quota Cards ──

function renderGeminiQuotaCards(quotas, containerId) {
  const container = document.getElementById(containerId);
  if (!container) return;

  if (!quotas || quotas.length === 0) {
    container.innerHTML = '<p class="empty-state">No Gemini quota data available.</p>';
    return;
  }

  container.innerHTML = quotas.map((q, i) => {
    const displayName = q.displayName || geminiDisplayNames[q.modelId] || q.modelId;
    const membersText = q.members && q.members.length > 0 ? q.members.join(', ') : '';
    const usagePct = (q.usagePercent || 0).toFixed(1);
    const status = q.status || 'healthy';
    const statusCfg = statusConfig[status] || statusConfig.healthy;
    const countdownId = `countdown-gemini-${q.modelId}`;
    const progressId = `progress-gemini-${q.modelId}`;
    const percentId = `percent-gemini-${q.modelId}`;
    const fractionId = `fraction-gemini-${q.modelId}`;
    const statusId = `status-gemini-${q.modelId}`;
    const resetId = `reset-gemini-${q.modelId}`;

    const remainingPct = (q.remainingPercent || (q.remainingFraction != null ? q.remainingFraction * 100 : 0)).toFixed(1);
    const fractionText = `${remainingPct}% remaining`;

    return `<article class="quota-card gemini-card" data-quota="${q.modelId}" data-provider="gemini" role="button" tabindex="0" aria-label="View ${displayName} details" style="animation-delay: ${i * 60}ms">
      <header class="card-header">
        <h2 class="quota-title">
          <svg class="quota-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="10"/><path d="M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z"/></svg>
          ${displayName}
          ${q.isExhausted ? '<span class="exhausted-badge">Exhausted</span>' : ''}
        </h2>
        <span class="countdown" id="${countdownId}">${q.timeUntilResetSeconds > 0 ? formatDuration(q.timeUntilResetSeconds) : '--:--'}</span>
      </header>
      ${membersText ? `<div class="family-members" style="font-size:0.75rem;opacity:0.7;margin:-4px 0 4px 0;padding:0 16px">${membersText}</div>` : ''}
      <div class="progress-stats">
        <span class="usage-percent" id="${percentId}">${usagePct}%</span>
        <span class="usage-fraction" id="${fractionId}">${fractionText}</span>
      </div>
      <div class="progress-wrapper">
        <div class="progress-bar" role="progressbar" aria-valuenow="${Math.round(q.usagePercent || 0)}" aria-valuemin="0" aria-valuemax="100">
          <div class="progress-fill" id="${progressId}" style="width: ${usagePct}%" data-status="${status}"></div>
        </div>
      </div>
      <footer class="card-footer">
        <span class="status-badge" id="${statusId}" data-status="${status}">
          <svg class="status-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="${statusCfg.icon}"/></svg>
          ${statusCfg.label}
        </span>
        <span class="reset-time" id="${resetId}">${q.resetTime ? 'Resets: ' + formatDateTime(q.resetTime) : ''}</span>
      </footer>
    </article>`;
  }).join('');

  // Re-attach modal click handlers for new cards
  container.querySelectorAll('.quota-card[role="button"]').forEach(card => {
    const handler = () => {
      openGeminiModal(card.dataset.quota);
    };
    card.addEventListener('click', handler);
    card.addEventListener('keydown', e => {
      if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); handler(); }
    });
  });
}

function updateGeminiCard(q) {
  const key = `gemini-${q.modelId}`;
  const prev = State.currentQuotas[key];
  State.currentQuotas[key] = {
    percent: q.usagePercent || 0,
    remainingFraction: q.remainingFraction,
    remainingPercent: q.remainingPercent,
    isExhausted: q.isExhausted,
    status: q.status || 'healthy',
    resetTime: q.resetTime,
    timeUntilReset: q.timeUntilReset,
    timeUntilResetSeconds: q.timeUntilResetSeconds || 0,
    modelId: q.modelId,
    label: q.label,
    displayName: q.displayName
  };

  const progressEl = document.getElementById(`progress-gemini-${q.modelId}`);
  const percentEl = document.getElementById(`percent-gemini-${q.modelId}`);
  const fractionEl = document.getElementById(`fraction-gemini-${q.modelId}`);
  const statusEl = document.getElementById(`status-gemini-${q.modelId}`);
  const resetEl = document.getElementById(`reset-gemini-${q.modelId}`);
  const countdownEl = document.getElementById(`countdown-gemini-${q.modelId}`);

  const usagePct = (q.usagePercent || 0).toFixed(1);
  const status = q.status || 'healthy';

  if (progressEl) {
    progressEl.style.width = `${usagePct}%`;
    progressEl.setAttribute('data-status', status);
  }
  if (percentEl) {
    const oldVal = prev ? prev.percent : 0;
    if (Math.abs(oldVal - (q.usagePercent || 0)) > 0.2) {
      animateValue(percentEl, oldVal, q.usagePercent || 0, 400, v => `${v.toFixed(1)}%`);
    } else {
      percentEl.textContent = `${usagePct}%`;
    }
  }
  if (fractionEl) {
    const remainingPct = (q.remainingPercent || (q.remainingFraction != null ? q.remainingFraction * 100 : 0)).toFixed(1);
    fractionEl.textContent = `${remainingPct}% remaining`;
  }
  if (statusEl) {
    const config = statusConfig[status] || statusConfig.healthy;
    statusEl.setAttribute('data-status', status);
    statusEl.innerHTML = `<svg class="status-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="${config.icon}"/></svg>${config.label}`;
  }
  if (resetEl) {
    resetEl.textContent = q.resetTime ? `Resets: ${formatDateTime(q.resetTime)}` : '';
  }
  if (countdownEl) {
    if (q.timeUntilResetSeconds > 0) {
      countdownEl.textContent = formatDuration(q.timeUntilResetSeconds);
      countdownEl.classList.toggle('imminent', q.timeUntilResetSeconds < 1800);
      countdownEl.style.display = '';
    } else {
      countdownEl.style.display = 'none';
    }
  }
}

function openGeminiModal(modelId) {
  openAntigravityModal(modelId, 'gemini');
}

// OpenRouter quota card rendering
function renderOpenRouterCard(credits, containerId) {
  const container = document.getElementById(containerId);
  if (!container) return;

  const usagePct = (credits.percent || 0).toFixed(1);
  const status = getQuotaStatus(credits.percent || 0);
  const statusCfg = statusConfig[status] || statusConfig.healthy;
  const hasLimit = credits.limit != null && credits.limit > 0;
  const usageStr = '$' + (credits.usage || 0).toFixed(4);
  const limitStr = hasLimit ? '$' + credits.limit.toFixed(4) : 'Unlimited';
  const remainStr = credits.remaining != null ? '$' + credits.remaining.toFixed(4) : '--';

  container.innerHTML = `<article class="quota-card openrouter-card" data-quota="credits" data-provider="openrouter" style="animation-delay: 0ms">
    <header class="card-header">
      <div class="quota-title-block">
        <h2 class="quota-title">
          <svg class="quota-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><line x1="12" y1="1" x2="12" y2="23"/><path d="M17 5H9.5a3.5 3.5 0 0 0 0 7h5a3.5 3.5 0 0 1 0 7H6"/></svg>
          Credits
        </h2>
        ${credits.isFreeTier ? '<div class="quota-subtitle">Free Tier</div>' : ''}
      </div>
      <span class="countdown" id="countdown-openrouter-credits" style="display: none">--:--</span>
    </header>
    <div class="progress-stats">
      <span class="usage-percent" id="percent-openrouter-credits">${usagePct}%</span>
      <span class="usage-fraction" id="fraction-openrouter-credits">${usageStr} / ${limitStr}</span>
    </div>
    <div class="progress-wrapper">
      <div class="progress-bar" role="progressbar" aria-valuenow="${Math.round(credits.percent || 0)}" aria-valuemin="0" aria-valuemax="100">
        <div class="progress-fill" id="progress-openrouter-credits" style="width: ${usagePct}%" data-status="${status}"></div>
      </div>
    </div>
    <footer class="card-footer">
      <span class="status-badge" id="status-openrouter-credits" data-status="${status}">
        <svg class="status-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="${statusCfg.icon}"/></svg>
        ${statusCfg.label}
      </span>
      <span class="reset-time" id="reset-openrouter-credits">${hasLimit ? 'Remaining: ' + remainStr : ''}</span>
    </footer>
  </article>`;
}

function updateOpenRouterCard(credits) {
  const key = 'openrouter-credits';
  const prev = State.currentQuotas[key];
  State.currentQuotas[key] = {
    percent: credits.percent || 0,
    usage: credits.usage || 0,
    limit: credits.limit,
    remaining: credits.remaining,
    status: getQuotaStatus(credits.percent || 0),
    isFreeTier: credits.isFreeTier
  };

  const usagePct = (credits.percent || 0).toFixed(1);
  const status = getQuotaStatus(credits.percent || 0);
  const hasLimit = credits.limit != null && credits.limit > 0;
  const usageStr = '$' + (credits.usage || 0).toFixed(4);
  const limitStr = hasLimit ? '$' + credits.limit.toFixed(4) : 'Unlimited';
  const remainStr = credits.remaining != null ? '$' + credits.remaining.toFixed(4) : '--';

  const progressEl = document.getElementById('progress-openrouter-credits');
  const percentEl = document.getElementById('percent-openrouter-credits');
  const fractionEl = document.getElementById('fraction-openrouter-credits');
  const statusEl = document.getElementById('status-openrouter-credits');
  const resetEl = document.getElementById('reset-openrouter-credits');

  if (progressEl) {
    progressEl.style.width = `${usagePct}%`;
    progressEl.setAttribute('data-status', status);
  }
  if (percentEl) {
    const oldVal = prev ? prev.percent : 0;
    if (Math.abs(oldVal - (credits.percent || 0)) > 0.2) {
      animateValue(percentEl, oldVal, credits.percent || 0, 400, v => `${v.toFixed(1)}%`);
    } else {
      percentEl.textContent = `${usagePct}%`;
    }
  }
  if (fractionEl) fractionEl.textContent = `${usageStr} / ${limitStr}`;
  if (statusEl) {
    const config = statusConfig[status] || statusConfig.healthy;
    statusEl.setAttribute('data-status', status);
    statusEl.innerHTML = `<svg class="status-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="${config.icon}"/></svg>${config.label}`;
  }
  if (resetEl) resetEl.textContent = hasLimit ? 'Remaining: ' + remainStr : '';
}

function getQuotaStatus(percent) {
  if (percent >= 90) return 'critical';
  if (percent >= 75) return 'warning';
  return 'healthy';
}

// Antigravity quota detail modal
function openAntigravityModal(groupKey, providerOverride) {
  const key = `antigravity-${groupKey}`;
  const data = State.currentQuotas[key];
  if (!data) return;

  const modal = document.getElementById('detail-modal');
  const titleEl = document.getElementById('modal-title');
  const bodyEl = document.getElementById('modal-body');
  if (!modal || !bodyEl) return;

  const displayName = data.displayName || data.label || groupKey;
  titleEl.textContent = displayName;

  const usagePct = (data.percent || 0).toFixed(1);
  const remainingPct = (data.remainingPercent || 0).toFixed(1);

  bodyEl.innerHTML = `
    <div class="modal-stats-grid">
      <div class="modal-stat">
        <span class="modal-stat-label">Usage</span>
        <span class="modal-stat-value">${usagePct}%</span>
      </div>
      <div class="modal-stat">
        <span class="modal-stat-label">Remaining</span>
        <span class="modal-stat-value">${remainingPct}%</span>
      </div>
      <div class="modal-stat">
        <span class="modal-stat-label">Status</span>
        <span class="modal-stat-value" data-status="${data.status}">${(statusConfig[data.status] || statusConfig.healthy).label}</span>
      </div>
      <div class="modal-stat">
        <span class="modal-stat-label">Resets In</span>
        <span class="modal-stat-value">${data.timeUntilResetSeconds > 0 ? formatDuration(data.timeUntilResetSeconds) : '--'}</span>
      </div>
    </div>
    <div class="modal-chart-container">
      <canvas id="modal-chart" height="200"></canvas>
    </div>
    <h4 class="modal-section-title">Recent Cycles</h4>
    <table class="modal-cycles-table">
      <thead><tr><th>Cycle</th><th>Duration</th><th>Peak Used</th><th>Total Delta</th></tr></thead>
      <tbody id="modal-cycles-tbody"><tr><td colspan="4">Loading...</td></tr></tbody>
    </table>
  `;

  modal.classList.add('open');
  document.body.classList.add('modal-open');
  modal.querySelector('.modal-close')?.focus();

  loadAntigravityModalChart(groupKey);
  loadAntigravityModalCycles(groupKey);
}

async function loadAntigravityModalChart(groupKey) {
  const range = State.currentRange || '6h';
  const rangeKey = range.toLowerCase();
  const timeUnit = ['7d', '30d', '15d'].includes(rangeKey) ? 'day' : 'hour';
  const colors = getThemeColors();
  try {
    const res = await authFetch(`${API_BASE}/api/history?range=${range}&provider=antigravity`);
    if (!res.ok) return;
    const data = await res.json();

    const ctx = document.getElementById('modal-chart')?.getContext('2d');
    if (!ctx || !data.datasets) return;

    // Filter to the selected logical quota group dataset
    const modelDataset = data.datasets.find(ds => ds.modelId === groupKey);
    if (!modelDataset) return;

    if (State.modalChart) State.modalChart.destroy();

    const labels = data.labels || [];
    const rawData = (modelDataset.data || []).map((y, i) => ({ x: new Date(labels[i]), y }));
    const processed = processDataWithGaps(rawData, range);
    const borderColor = '#6e40c9';

    State.modalChart = new Chart(ctx, {
      type: 'line',
      data: {
        datasets: [{
          label: modelDataset.label || groupKey,
          data: processed.data,
          borderColor: borderColor,
          backgroundColor: 'rgba(110, 64, 201, 0.1)',
          tension: 0.3,
          borderWidth: 2.5,
          pointRadius: processed.pointRadii,
          pointHoverRadius: 5,
          fill: true,
          spanGaps: true,
          segment: getSegmentStyle(processed.gapSegments, borderColor)
        }]
      },
      options: {
        responsive: true,
        maintainAspectRatio: false,
        plugins: { legend: { display: false } },
        scales: {
          x: { type: 'time', time: { unit: timeUnit, displayFormats: { minute: 'HH:mm', hour: ['7d', '30d', '15d', '24h', '3d'].includes(rangeKey) ? 'MMM d, HH:mm' : 'HH:mm', day: 'MMM d' } }, grid: { color: colors.grid, drawBorder: false }, ticks: { color: colors.text, maxTicksLimit: 6, source: 'auto' }, title: { display: true, text: 'Time' } },
          y: { beginAtZero: true, max: 100, title: { display: true, text: 'Usage %' } }
        }
      }
    });
  } catch (err) { /* modal chart error - non-critical */ }
}

async function loadAntigravityModalCycles(groupKey) {
  try {
    const res = await authFetch(`${API_BASE}/api/cycle-overview?groupBy=${groupKey}&provider=antigravity`);
    if (!res.ok) return;
    const data = await res.json();
    const tbody = document.getElementById('modal-cycles-tbody');
    if (!tbody || !data.cycles) return;

    if (data.cycles.length === 0) {
      tbody.innerHTML = '<tr><td colspan="4" class="empty-state">No cycles recorded yet</td></tr>';
      return;
    }

    tbody.innerHTML = data.cycles.slice(0, 5).map((cycle, i) => {
      const start = cycle.cycleStart ? new Date(cycle.cycleStart) : null;
      const end = cycle.cycleEnd ? new Date(cycle.cycleEnd) : null;
      const durationMins = start
        ? Math.max(0, Math.round(((end || new Date()) - start) / 60000))
        : 0;
      const primary = (cycle.crossQuotas || []).find(cq => cq.name === groupKey);
      const peak = primary ? primary.percent : 0;
      return `<tr>
        <td>#${data.cycles.length - i}</td>
        <td>${formatDurationMins(durationMins)}</td>
        <td>${formatNumber(peak)}%</td>
        <td>${formatNumber(cycle.totalDelta || 0)}%</td>
      </tr>`;
    }).join('');
  } catch (err) { /* modal cycles error - non-critical */ }
}

// ── Codex Dynamic Card Rendering ──

function renderCodexQuotaCards(quotas, containerId, planType) {
  const container = document.getElementById(containerId);
  if (!container) return;
  const visibleQuotas = filterCodexQuotasForPlan(quotas, planType);
  if (visibleQuotas.length === 0) {
    container.innerHTML = '<p class="empty-state">No Codex quota data available yet.</p>';
    return;
  }

  container.innerHTML = visibleQuotas.map((q, i) => {
    const icon = anthropicQuotaIcons[q.name] || '<circle cx="12" cy="12" r="10"/><path d="M12 16v-4M12 8h.01"/>';
    const displayName = q.displayName || codexDisplayNames[q.name] || q.name;
    const cardPercent = q.cardPercent != null ? q.cardPercent : (q.utilization || 0);
    const utilPct = cardPercent.toFixed(1);
    const cardLabel = q.cardLabel || 'Utilization';
    const status = q.status || 'healthy';
    const statusCfg = statusConfig[status] || statusConfig.healthy;
    const countdownId = `countdown-codex-${q.name}`;
    const progressId = `progress-codex-${q.name}`;
    const percentId = `percent-codex-${q.name}`;
    const statusId = `status-codex-${q.name}`;
    const resetId = `reset-codex-${q.name}`;

    return `<article class="quota-card codex-card" data-quota="${q.name}" data-provider="codex" role="button" tabindex="0" aria-label="View ${displayName} details" style="animation-delay: ${i * 60}ms">
      <header class="card-header">
        <h2 class="quota-title">
          <svg class="quota-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">${icon}</svg>
          ${displayName}
        </h2>
        <span class="countdown" id="${countdownId}">${q.timeUntilResetSeconds > 0 ? formatDuration(q.timeUntilResetSeconds) : '--:--'}</span>
      </header>
      <div class="progress-stats">
        <span class="usage-percent" id="${percentId}">${utilPct}%</span>
        <span class="usage-fraction">${cardLabel}</span>
      </div>
      <div class="progress-wrapper">
        <div class="progress-bar" role="progressbar" aria-valuenow="${Math.round(cardPercent)}" aria-valuemin="0" aria-valuemax="100">
          <div class="progress-fill" id="${progressId}" style="width: ${utilPct}%" data-status="${status}"></div>
        </div>
      </div>
      <footer class="card-footer">
        <span class="status-badge" id="${statusId}" data-status="${status}">
          <svg class="status-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="${statusCfg.icon}"/></svg>
          ${statusCfg.label}
        </span>
        <span class="reset-time" id="${resetId}">${q.resetsAt ? 'Resets: ' + formatDateTime(q.resetsAt) : ''}</span>
      </footer>
    </article>`;
  }).join('');

  container.querySelectorAll('.quota-card[role="button"]').forEach(card => {
    const handler = () => {
      const providerCol = card.closest('.provider-column');
      const providerOverride = providerCol ? providerCol.dataset.provider : 'codex';
      openCodexModal(card.dataset.quota, providerOverride);
    };
    card.addEventListener('click', handler);
    card.addEventListener('keydown', e => {
      if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); handler(); }
    });
  });
}

function formatCodexPlan(planType) {
  const normalized = normalizeCodexPlanType(planType);
  if (!normalized) return 'Unknown Plan';
  return normalized.charAt(0).toUpperCase() + normalized.slice(1);
}

// Render Codex cards for a specific account (used in "both" view with multiple accounts)
function renderCodexQuotaCardsForAccount(quotas, container, accountName, planType, accountId) {
  const visibleQuotas = filterCodexQuotasForPlan(quotas, planType);
  const safeAccountId = String(accountId || accountName || 'default').replace(/[^a-zA-Z0-9_-]/g, '-');

  const header = document.createElement('div');
  header.className = 'codex-account-header';
  header.innerHTML = `
    <span class="codex-account-name">${accountName}</span>
    <span class="codex-account-plan">${formatCodexPlan(planType)}</span>
  `;
  container.appendChild(header);

  if (visibleQuotas.length === 0) {
    const empty = document.createElement('p');
    empty.className = 'empty-state';
    empty.textContent = 'No Codex quota data available for this account yet.';
    container.appendChild(empty);
    return;
  }

  const cardsDiv = document.createElement('div');
  cardsDiv.className = 'codex-account-cards';
  cardsDiv.innerHTML = visibleQuotas.map((q, i) => {
    const icon = anthropicQuotaIcons[q.name] || '<circle cx="12" cy="12" r="10"/><path d="M12 16v-4M12 8h.01"/>';
    const displayName = q.displayName || codexDisplayNames[q.name] || q.name;
    const cardPercent = q.cardPercent != null ? q.cardPercent : (q.utilization || 0);
    const utilPct = cardPercent.toFixed(1);
    const cardLabel = q.cardLabel || 'Utilization';
    const status = q.status || 'healthy';
    const statusCfg = statusConfig[status] || statusConfig.healthy;
    const cardKey = `codex-${safeAccountId}-${q.name}`;

    return `<article class="quota-card codex-card" id="card-${cardKey}" data-quota="${q.name}" data-provider="codex" data-account-id="${accountId}" aria-label="${accountName} ${displayName}" style="animation-delay: ${i * 60}ms">
      <header class="card-header">
        <h2 class="quota-title">
          <svg class="quota-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">${icon}</svg>
          ${displayName}
        </h2>
        <span class="countdown" id="countdown-${cardKey}">${q.timeUntilResetSeconds > 0 ? formatDuration(q.timeUntilResetSeconds) : '--:--'}</span>
      </header>
      <div class="progress-stats">
        <span class="usage-percent" id="percent-${cardKey}">${utilPct}%</span>
        <span class="usage-fraction">${cardLabel}</span>
      </div>
      <div class="progress-wrapper">
        <div class="progress-bar" role="progressbar" aria-valuenow="${Math.round(cardPercent)}" aria-valuemin="0" aria-valuemax="100">
          <div class="progress-fill" id="progress-${cardKey}" style="width: ${utilPct}%" data-status="${status}"></div>
        </div>
      </div>
      <footer class="card-footer">
        <span class="status-badge" id="status-${cardKey}" data-status="${status}">
          <svg class="status-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="${statusCfg.icon}"/></svg>
          ${statusCfg.label}
        </span>
        <span class="reset-time" id="reset-${cardKey}">${q.resetsAt ? 'Resets: ' + formatDateTime(q.resetsAt) : ''}</span>
      </footer>
    </article>`;
  }).join('');

  container.appendChild(cardsDiv);
}

function renderCodexAccountSections(accounts) {
  const container = document.getElementById('codex-accounts-container-both');
  if (!container) return;

  container.innerHTML = '';
  if (!Array.isArray(accounts) || accounts.length === 0) {
    container.innerHTML = '<p class="empty-state">No Codex account usage found yet.</p>';
    return;
  }

  accounts.forEach((account) => {
    const accountId = account.accountId || account.id || 1;
    const accountName = account.accountName || account.name || `Account ${accountId}`;
    const section = document.createElement('section');
    section.className = 'codex-account-section';
    section.dataset.accountId = String(accountId);
    renderCodexQuotaCardsForAccount(account.quotas || [], section, accountName, account.planType, accountId);
    container.appendChild(section);
  });
}

function updateCodexCard(quota) {
  const key = `codex-${quota.name}`;
  const prev = State.currentQuotas[key];
  const cardPercent = quota.cardPercent != null ? quota.cardPercent : (quota.utilization || 0);
  const cardLabel = quota.cardLabel || 'Utilization';
  State.currentQuotas[key] = {
    percent: cardPercent,
    usage: quota.utilization || 0,
    limit: 100,
    headroom: quota.headroom || Math.max(0, 100 - (quota.utilization || 0)),
    currentRate: quota.currentRate || 0,
    projectedUtil: quota.projectedUtil || 0,
    status: quota.status || 'healthy',
    renewsAt: quota.resetsAt,
    timeUntilReset: quota.timeUntilReset,
    timeUntilResetSeconds: quota.timeUntilResetSeconds || 0,
    cardLabel,
    name: quota.name,
    displayName: quota.displayName
  };

  const progressEl = document.getElementById(`progress-codex-${quota.name}`);
  const percentEl = document.getElementById(`percent-codex-${quota.name}`);
  const statusEl = document.getElementById(`status-codex-${quota.name}`);
  const resetEl = document.getElementById(`reset-codex-${quota.name}`);
  const countdownEl = document.getElementById(`countdown-codex-${quota.name}`);

  const utilPct = cardPercent.toFixed(1);
  const status = quota.status || 'healthy';

  if (progressEl) {
    progressEl.style.width = `${utilPct}%`;
    progressEl.setAttribute('data-status', status);
  }
  if (percentEl) {
    const oldVal = prev ? prev.percent : 0;
    if (Math.abs(oldVal - cardPercent) > 0.2) {
      animateValue(percentEl, oldVal, cardPercent, 400, v => `${v.toFixed(1)}%`);
    } else {
      percentEl.textContent = `${utilPct}%`;
    }
  }
  if (statusEl) {
    const config = statusConfig[status] || statusConfig.healthy;
    statusEl.setAttribute('data-status', status);
    statusEl.innerHTML = `<svg class="status-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="${config.icon}"/></svg>${config.label}`;
  }
  if (resetEl) {
    resetEl.textContent = quota.resetsAt ? `Resets: ${formatDateTime(quota.resetsAt)}` : '';
  }
  if (countdownEl) {
    if (quota.timeUntilResetSeconds > 0) {
      countdownEl.textContent = formatDuration(quota.timeUntilResetSeconds);
      countdownEl.classList.toggle('imminent', quota.timeUntilResetSeconds < 1800);
      countdownEl.style.display = '';
    } else {
      countdownEl.style.display = 'none';
    }
  }
}

function openCodexModal(quotaName, providerOverride) {
  const key = `codex-${quotaName}`;
  const data = State.currentQuotas[key];
  if (!data) return;

  const modal = document.getElementById('detail-modal');
  const titleEl = document.getElementById('modal-title');
  const bodyEl = document.getElementById('modal-body');
  if (!modal || !bodyEl) return;

  const displayName = data.displayName || codexDisplayNames[quotaName] || quotaName;
  titleEl.textContent = displayName;

  const statusCfg = statusConfig[data.status] || statusConfig.healthy;
  const timeLeft = data.timeUntilResetSeconds > 0 ? formatDuration(data.timeUntilResetSeconds) : 'N/A';
  const modalLabel = data.cardLabel || 'Utilization';

  bodyEl.innerHTML = `
    <div class="modal-kpi-row">
      <div class="modal-kpi">
        <div class="modal-kpi-value">${data.percent.toFixed(1)}%</div>
        <div class="modal-kpi-label">${modalLabel}</div>
      </div>
      <div class="modal-kpi">
        <div class="modal-kpi-value"><span class="status-badge" data-status="${data.status}"><svg class="status-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="${statusCfg.icon}"/></svg>${statusCfg.label}</span></div>
        <div class="modal-kpi-label">Status</div>
      </div>
      <div class="modal-kpi">
        <div class="modal-kpi-value">${timeLeft}</div>
        <div class="modal-kpi-label">Until Reset</div>
      </div>
    </div>
    <h3 class="modal-section-title">Usage History</h3>
    <div class="modal-chart-container">
      <canvas id="modal-chart"></canvas>
    </div>
    <h3 class="modal-section-title">Recent Cycles</h3>
    <div class="table-wrapper">
      <table class="data-table" id="modal-cycles-table">
        <thead><tr><th>Cycle</th><th>Duration</th><th>Peak %</th><th>Total %</th></tr></thead>
        <tbody id="modal-cycles-tbody"><tr><td colspan="4" class="empty-state">Loading...</td></tr></tbody>
      </table>
    </div>
  `;

  modal.hidden = false;
  document.getElementById('modal-close').focus();

  loadCodexModalChart(quotaName);
  loadCodexModalCycles(quotaName);
}

async function loadCodexModalChart(quotaName) {
  const ctx = document.getElementById('modal-chart');
  if (!ctx || typeof Chart === 'undefined') return;
  if (State.modalChart) { State.modalChart.destroy(); State.modalChart = null; }

  const range = State.currentRange || '6h';
  const rangeKey = range.toLowerCase();
  const timeUnit = ['7d', '30d', '15d'].includes(rangeKey) ? 'day' : 'hour';

  try {
    const res = await authFetch(`${API_BASE}/api/history?range=${range}&provider=codex${codexAccountParam()}`);
    if (!res.ok) return;
    const data = await res.json();
    if (!Array.isArray(data) || data.length === 0) return;

    const colors = getThemeColors();
    const rawData = data.map(d => ({ x: new Date(d.capturedAt), y: d[quotaName] || 0 }));
    const processed = processDataWithGaps(rawData, range);
    const maxVal = Math.max(...data.map(d => d[quotaName] || 0), 0);
    const yMax = maxVal <= 0 ? 10 : maxVal < 5 ? 10 : Math.min(Math.max(Math.ceil((maxVal * 1.2) / 5) * 5, 10), 100);

    State.modalChart = new Chart(ctx, {
      type: 'line',
      data: {
        datasets: [(() => { const c = codexChartColorMap[quotaName] || { border: '#0EA5E9', bg: 'rgba(14, 165, 233, 0.08)' }; return {
          label: codexDisplayNames[quotaName] || quotaName,
          data: processed.data,
          borderColor: c.border,
          backgroundColor: c.bg,
          fill: true,
          tension: 0.3,
          borderWidth: 2.5,
          pointRadius: processed.pointRadii,
          pointHoverRadius: 5,
          spanGaps: true,
          segment: getSegmentStyle(processed.gapSegments, c.border)
        }; })()]
      },
      options: {
        responsive: true, maintainAspectRatio: false,
        plugins: {
          legend: { display: false },
          tooltip: { backgroundColor: colors.surfaceContainer, titleColor: colors.onSurface, bodyColor: colors.text, borderColor: colors.outline, borderWidth: 1, callbacks: { label: c => `${c.parsed.y.toFixed(1)}%` } }
        },
        scales: {
          x: { type: 'time', time: { unit: timeUnit, displayFormats: { minute: 'HH:mm', hour: ['7d', '30d', '15d', '24h', '3d'].includes(rangeKey) ? 'MMM d, HH:mm' : 'HH:mm', day: 'MMM d' } }, grid: { color: colors.grid, drawBorder: false }, ticks: { color: colors.text, maxTicksLimit: 6, source: 'auto' } },
          y: { grid: { color: colors.grid, drawBorder: false }, ticks: { color: colors.text, callback: v => v + '%' }, min: 0, max: yMax }
        }
      }
    });
  } catch (err) { /* modal chart error - non-critical */ }
}

async function loadCodexModalCycles(quotaName) {
  try {
    const res = await authFetch(`${API_BASE}/api/cycles?type=${quotaName}&provider=codex${codexAccountParam()}`);
    if (!res.ok) return;
    const cycles = await res.json();
    const tbody = document.getElementById('modal-cycles-tbody');
    if (!tbody) return;
    const recent = cycles.slice(0, 5);
    if (recent.length === 0) {
      tbody.innerHTML = '<tr><td colspan="4" class="empty-state">No cycles yet.</td></tr>';
      return;
    }
    tbody.innerHTML = recent.map(cycle => {
      const start = new Date(cycle.cycleStart);
      const end = cycle.cycleEnd ? new Date(cycle.cycleEnd) : new Date();
      const durationMins = Math.round((end - start) / 60000);
      const isActive = !cycle.cycleEnd;
      return `<tr>
        <td>#${cycle.id}${isActive ? ' <span class="badge">Active</span>' : ''}</td>
        <td>${formatDurationMins(durationMins)}</td>
        <td>${formatNumber(cycle.peakUtilization || 0)}%</td>
        <td>${formatNumber(cycle.totalDelta || 0)}%</td>
      </tr>`;
    }).join('');
  } catch (err) { /* modal cycles error - non-critical */ }
}

// ── Utilities ──

function formatDuration(seconds) {
  if (seconds < 0) return 'Resetting...';
  const totalHours = Math.floor(seconds / 3600);
  const d = Math.floor(totalHours / 24);
  const h = totalHours % 24;
  const m = Math.floor((seconds % 3600) / 60);
  const s = seconds % 60;
  if (d > 0 && h > 0) return `${d}d ${h}h`;
  if (d > 0) return `${d}d ${m}m`;
  if (h > 0) return `${h}h ${m}m`;
  if (m > 0) return `${m}m ${s}s`;
  return '< 1m';
}

function formatDurationMins(durationMins) {
  const days = Math.floor(durationMins / 1440);
  const hours = Math.floor((durationMins % 1440) / 60);
  const mins = durationMins % 60;
  if (days > 0 && hours > 0) return `${days}d ${hours}h`;
  if (days > 0) return `${days}d ${mins}m`;
  return `${hours}h ${mins}m`;
}

function formatNumber(num) {
  return num.toLocaleString('en-US', { maximumFractionDigits: 1 });
}

function formatDateTime(isoString) {
  const d = new Date(isoString);
  const opts = { month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit' };
  if (typeof getEffectiveTimezone === 'function') {
    opts.timeZone = getEffectiveTimezone();
  }
  return d.toLocaleString('en-US', opts);
}

function formatChartXAxisLabel(isoOrLabel, range) {
  if (!isoOrLabel) return '';

  const d = new Date(isoOrLabel);
  if (Number.isNaN(d.getTime())) {
    return isoOrLabel;
  }

  const tz = typeof getEffectiveTimezone === 'function' ? getEffectiveTimezone() : undefined;
  const rangeKey = (range || '').toLowerCase();
  const showDate = ['24h', '7d', '30d', '3d', '15d'].includes(rangeKey);

  const opts = showDate
    ? { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' }
    : { hour: '2-digit', minute: '2-digit' };

  if (tz) opts.timeZone = tz;
  return d.toLocaleString('en-US', opts);
}


function getThemeColors() {
  const style = getComputedStyle(document.documentElement);
  const isDark = document.documentElement.getAttribute('data-theme') !== 'light';
  return {
    grid: style.getPropertyValue('--border-light').trim() || (isDark ? '#2A2E37' : '#F0F1F3'),
    text: style.getPropertyValue('--text-muted').trim() || (isDark ? '#8891A0' : '#6B7280'),
    outline: style.getPropertyValue('--border-default').trim(),
    surfaceContainer: style.getPropertyValue('--surface-card').trim(),
    onSurface: style.getPropertyValue('--text-primary').trim(),
    isDark
  };
}

// ── Timezone Badge & Selector ──

// Active timezone (empty = browser default)
let activeTimezone = '';

// Legacy → canonical timezone aliases
const TZ_ALIASES = {
  'Asia/Calcutta': 'Asia/Kolkata',
  'US/Eastern': 'America/New_York',
  'US/Central': 'America/Chicago',
  'US/Mountain': 'America/Denver',
  'US/Pacific': 'America/Los_Angeles',
};

function normalizeTz(tz) { return TZ_ALIASES[tz] || tz; }

// Curated timezone list sorted by UTC offset (descending: east → west).
// India (Asia/Kolkata) is always present.
const TZ_LIST = (() => {
  const base = [
    { tz: 'Pacific/Auckland', label: 'Auckland' },
    { tz: 'Australia/Sydney', label: 'Sydney' },
    { tz: 'Asia/Tokyo', label: 'Tokyo' },
    { tz: 'Asia/Shanghai', label: 'Shanghai' },
    { tz: 'Asia/Singapore', label: 'Singapore' },
    { tz: 'Asia/Kolkata', label: 'India' },
    { tz: 'Asia/Dubai', label: 'Dubai' },
    { tz: 'Europe/Moscow', label: 'Moscow' },
    { tz: 'Europe/Istanbul', label: 'Istanbul' },
    { tz: 'Europe/Berlin', label: 'Berlin' },
    { tz: 'Europe/Paris', label: 'Paris' },
    { tz: 'Europe/London', label: 'London' },
    { tz: 'UTC', label: 'UTC' },
    { tz: 'America/Sao_Paulo', label: 'Sao Paulo' },
    { tz: 'America/New_York', label: 'New York' },
    { tz: 'America/Chicago', label: 'Chicago' },
    { tz: 'America/Denver', label: 'Denver' },
    { tz: 'America/Los_Angeles', label: 'Los Angeles' },
    { tz: 'Pacific/Honolulu', label: 'Honolulu' },
  ];
  // Insert user's browser timezone if not already in list (after normalization)
  const browserTz = normalizeTz(Intl.DateTimeFormat().resolvedOptions().timeZone);
  if (!base.some(e => e.tz === browserTz)) {
    const label = browserTz.split('/').pop().replace(/_/g, ' ');
    const off = tzOffsetMin(browserTz);
    let inserted = false;
    for (let i = 0; i < base.length; i++) {
      if (tzOffsetMin(base[i].tz) < off) {
        base.splice(i, 0, { tz: browserTz, label });
        inserted = true;
        break;
      }
    }
    if (!inserted) base.push({ tz: browserTz, label });
  }
  return base;
})();

function tzOffsetMin(tz) {
  try {
    const d = new Date();
    const parts = d.toLocaleString('en-US', { timeZone: tz, timeZoneName: 'shortOffset' }).split('GMT');
    if (parts.length < 2 || !parts[1]) return 0;
    const str = parts[1].trim();
    const m = str.match(/^([+-]?)(\d{1,2})(?::(\d{2}))?$/);
    if (!m) return 0;
    const sign = m[1] === '-' ? -1 : 1;
    return sign * (parseInt(m[2]) * 60 + parseInt(m[3] || '0'));
  } catch (e) { return 0; }
}

function getEffectiveTimezone() {
  return activeTimezone || normalizeTz(Intl.DateTimeFormat().resolvedOptions().timeZone);
}

function tzAbbr(tz) {
  try {
    return new Date().toLocaleTimeString('en-US', { timeZone: tz, timeZoneName: 'short' }).split(' ').pop();
  } catch (e) {
    return tz.split('/').pop();
  }
}

function findTzIndex(tz) {
  const normalized = normalizeTz(tz);
  const idx = TZ_LIST.findIndex(e => e.tz === normalized);
  return idx >= 0 ? idx : 0;
}

function initTimezoneBadge() {
  const badge = document.getElementById('timezone-badge');
  if (!badge) return;

  loadTimezoneFromAPI().then(() => {
    updateBadgeText(badge);
    badge.style.cursor = 'pointer';
    badge.addEventListener('click', (e) => {
      e.stopPropagation();
      toggleTzPicker(badge);
    });
  });
}

async function loadTimezoneFromAPI() {
  try {
    const res = await authFetch(`${API_BASE}/api/settings`);
    if (!res.ok) return;
    const data = await res.json();
    if (data.timezone) {
      activeTimezone = normalizeTz(data.timezone);
    }
  } catch (e) {}
}

function updateBadgeText(badge) {
  if (!badge) badge = document.getElementById('timezone-badge');
  if (!badge) return;
  const tz = getEffectiveTimezone();
  const entry = TZ_LIST.find(e => e.tz === tz);
  const label = entry ? entry.label : tz.split('/').pop().replace(/_/g, ' ');
  badge.textContent = `${label} (${tzAbbr(tz)})`;
  badge.title = tz;
}

function toggleTzPicker(badge) {
  let existing = document.getElementById('tz-picker');
  if (existing) { existing.remove(); return; }

  const picker = document.createElement('div');
  picker.id = 'tz-picker';
  picker.className = 'tz-picker';

  const list = document.createElement('div');
  list.className = 'tz-picker-list';

  const ITEM_H = 36;
  const VISIBLE = 7;
  const COPIES = 3;
  const totalItems = TZ_LIST.length;

  // Render 3 copies for infinite scroll illusion
  for (let copy = 0; copy < COPIES; copy++) {
    TZ_LIST.forEach((entry, i) => {
      const item = document.createElement('div');
      item.className = 'tz-picker-item';
      if (entry.tz === getEffectiveTimezone()) item.classList.add('active');
      item.dataset.tz = entry.tz;
      item.dataset.idx = i;
      const abbr = tzAbbr(entry.tz);
      item.innerHTML = `<span class="tz-picker-label">${entry.label}</span><span class="tz-picker-abbr">${abbr}</span>`;
      item.addEventListener('click', () => selectTz(entry.tz, picker, badge));
      list.appendChild(item);
    });
  }

  list.style.height = (VISIBLE * ITEM_H) + 'px';
  picker.appendChild(list);

  // Position below badge
  const rect = badge.getBoundingClientRect();
  picker.style.top = (rect.bottom + 4) + 'px';
  picker.style.right = (window.innerWidth - rect.right) + 'px';

  document.body.appendChild(picker);

  // Scroll to center current timezone in middle copy
  const activeIdx = findTzIndex(getEffectiveTimezone());
  const midStart = totalItems; // start of middle copy
  const targetScroll = (midStart + activeIdx) * ITEM_H - Math.floor(VISIBLE / 2) * ITEM_H;
  list.scrollTop = targetScroll;

  // Infinite scroll: snap to middle copy when reaching edges
  list.addEventListener('scroll', () => {
    const maxScroll = totalItems * COPIES * ITEM_H - list.clientHeight;
    if (list.scrollTop < totalItems * ITEM_H * 0.25) {
      list.scrollTop += totalItems * ITEM_H;
    } else if (list.scrollTop > totalItems * ITEM_H * 1.75) {
      list.scrollTop -= totalItems * ITEM_H;
    }
  });

  // Close on outside click
  function closeOutside(e) {
    if (!picker.contains(e.target) && e.target !== badge) {
      picker.remove();
      document.removeEventListener('click', closeOutside);
      document.removeEventListener('keydown', closeEsc);
    }
  }
  function closeEsc(e) {
    if (e.key === 'Escape') {
      picker.remove();
      document.removeEventListener('click', closeOutside);
      document.removeEventListener('keydown', closeEsc);
    }
  }
  setTimeout(() => {
    document.addEventListener('click', closeOutside);
    document.addEventListener('keydown', closeEsc);
  }, 0);
}

async function selectTz(tz, picker, badge) {
  activeTimezone = tz;
  updateBadgeText(badge);
  if (picker) picker.remove();
  try {
    await authFetch(`${API_BASE}/api/settings`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ timezone: tz })
    });
  } catch (e) {
    // silent
  }
}

// ── Theme ──

function initTheme() {
  const toggle = document.getElementById('theme-toggle');
  if (!toggle) return;
  toggle.addEventListener('click', () => {
    const current = document.documentElement.getAttribute('data-theme');
    const next = current === 'light' ? 'dark' : 'light';
    document.documentElement.setAttribute('data-theme', next);
    localStorage.setItem('onwatch-theme', next);
    updateChartTheme();
  });
}

function setLayoutDensity(mode) {
  const aliases = { default: 'normal' };
  const normalized = aliases[mode] || mode;
  const valid = new Set(['compact', 'normal', 'wide']);
  const next = valid.has(normalized) ? normalized : 'normal';

  if (document.body) {
    document.body.classList.remove('layout-compact', 'layout-normal', 'layout-default', 'layout-wide');
    document.body.classList.add(`layout-${next}`);
  }

  const toggle = document.getElementById('layout-toggle');
  if (toggle) {
    toggle.querySelectorAll('.layout-btn').forEach((btn) => {
      const active = btn.dataset.layout === next;
      btn.classList.toggle('active', active);
      btn.setAttribute('aria-pressed', active ? 'true' : 'false');
    });
  }

  // Keep settings select in sync (if on settings page)
  const settingsSelect = document.getElementById('settings-layout-density');
  if (settingsSelect && settingsSelect.value !== next) {
    settingsSelect.value = next;
  }

  try {
    localStorage.setItem('onwatch-layout', next);
  } catch (e) {
    // silent
  }

  requestAnimationFrame(() => {
    if (State.chart && typeof State.chart.resize === 'function') {
      State.chart.resize();
    }
    Object.values(State.providerCharts || {}).forEach((chart) => {
      if (chart && typeof chart.resize === 'function') {
        chart.resize();
      }
    });
  });
}

function initLayoutToggle() {
  let saved = 'normal';
  try {
    const stored = localStorage.getItem('onwatch-layout');
    if (stored) saved = stored;
  } catch (e) {
    // silent
  }
  setLayoutDensity(saved);

  // Dashboard navbar toggle (if present)
  const toggle = document.getElementById('layout-toggle');
  if (toggle) {
    toggle.addEventListener('click', (e) => {
      const btn = e.target.closest('.layout-btn');
      if (!btn) return;
      setLayoutDensity(btn.dataset.layout);
    });
  }

  // Settings page select (if present)
  const settingsSelect = document.getElementById('settings-layout-density');
  if (settingsSelect) {
    settingsSelect.value = saved;
    settingsSelect.addEventListener('change', () => {
      setLayoutDensity(settingsSelect.value);
    });
  }
}

// ── Card Updates ──

function updateCard(quotaType, data, suffix) {
  const key = suffix ? `${quotaType}_${suffix}` : quotaType;
  const prev = State.currentQuotas[key];
  State.currentQuotas[key] = data;

  const idSuffix = suffix ? `${quotaType}-${suffix}` : quotaType;
  const progressEl = document.getElementById(`progress-${idSuffix}`);
  const fractionEl = document.getElementById(`fraction-${idSuffix}`);
  const percentEl = document.getElementById(`percent-${idSuffix}`);
  const statusEl = document.getElementById(`status-${idSuffix}`);
  const resetEl = document.getElementById(`reset-${idSuffix}`);
  const countdownEl = document.getElementById(`countdown-${idSuffix}`);

  if (progressEl) {
    progressEl.style.width = `${data.percent}%`;
    progressEl.setAttribute('data-status', data.status);
    progressEl.parentElement.setAttribute('aria-valuenow', Math.round(data.percent));
  }

  if (fractionEl) {
    fractionEl.textContent = `${formatNumber(data.usage)} / ${formatNumber(data.limit)}`;
  }

  if (percentEl) {
    // Animate percentage from old to new
    const oldVal = prev ? prev.percent : 0;
    const newVal = data.percent;
    if (Math.abs(oldVal - newVal) > 0.2) {
      animateValue(percentEl, oldVal, newVal, 400, v => `${v.toFixed(1)}%`);
    } else {
      percentEl.textContent = `${data.percent.toFixed(1)}%`;
    }
  }

  if (statusEl) {
    const config = statusConfig[data.status] || statusConfig.healthy;
    const prevStatus = statusEl.getAttribute('data-status');
    statusEl.setAttribute('data-status', data.status);
    statusEl.innerHTML = `<svg class="status-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="${config.icon}"/></svg>${config.label}`;
  }

  if (resetEl) {
    if (data.renewsAt && data.timeUntilReset !== 'N/A') {
      resetEl.textContent = `Resets: ${formatDateTime(data.renewsAt)}`;
      resetEl.style.display = '';
    } else {
      resetEl.textContent = '';
      resetEl.style.display = 'none';
    }
  }

  if (countdownEl) {
    if (data.timeUntilResetSeconds > 0) {
      countdownEl.textContent = formatDuration(data.timeUntilResetSeconds);
      countdownEl.classList.toggle('imminent', data.timeUntilResetSeconds < 1800);
      countdownEl.style.display = '';
    } else if (data.timeUntilReset === 'N/A') {
      countdownEl.style.display = 'none';
    } else {
      countdownEl.textContent = '< 1m';
      countdownEl.style.display = '';
    }
  }

  // Render per-tool breakdown for Z.ai Time Limit card
  const detailsEl = document.getElementById(`usage-details-${idSuffix}`);
  if (detailsEl && data.usageDetails && data.usageDetails.length > 0) {
    detailsEl.innerHTML = data.usageDetails.map(d =>
      `<div class="usage-detail-row">
        <span class="usage-detail-model">${d.modelCode || d.ModelCode}</span>
        <span class="usage-detail-count">${formatNumber(d.usage || d.Usage)}</span>
      </div>`
    ).join('');
    detailsEl.style.display = '';
  } else if (detailsEl) {
    detailsEl.style.display = 'none';
  }
}

function animateValue(el, from, to, duration, formatter) {
  const start = performance.now();
  function step(now) {
    const progress = Math.min((now - start) / duration, 1);
    const eased = 1 - Math.pow(1 - progress, 3); // ease-out cubic
    const val = from + (to - from) * eased;
    el.textContent = formatter(val);
    if (progress < 1) requestAnimationFrame(step);
  }
  requestAnimationFrame(step);
}

function startCountdowns() {
  if (State.countdownInterval) clearInterval(State.countdownInterval);
  State.countdownInterval = setInterval(() => {
    Object.keys(State.currentQuotas).forEach(type => {
      const data = State.currentQuotas[type];
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

// ── Data Fetching ──

async function fetchCurrent() {
  const requestProvider = getCurrentProvider();
  const requestAccount = requestProvider === 'codex' ? State.codexAccount : null;
  const requestSeq = (State.currentRequestSeq || 0) + 1;
  State.currentRequestSeq = requestSeq;

  try {
    const res = await authFetch(`${API_BASE}/api/current?${providerParam()}`);
    if (!res.ok) throw new Error('Failed to fetch');
    const data = await res.json();

    requestAnimationFrame(() => {
      if (State.currentRequestSeq !== requestSeq) return;
      if (getCurrentProvider() !== requestProvider) return;
      if (requestProvider === 'codex' && State.codexAccount !== requestAccount) return;

      const provider = requestProvider;
      if (provider === 'both') {
        State.allProvidersCurrent = data;
        renderAllProvidersView();
      } else if (provider === 'copilot') {
        // Copilot response: { capturedAt: ..., quotas: [...] }
        if (data.quotas) {
          const container = document.getElementById('quota-grid-copilot');
          if (container && container.children.length === 0) {
            renderCopilotQuotaCards(data.quotas, 'quota-grid-copilot');
          }
          data.quotas.forEach(q => updateCopilotCard(q));
        }
      } else if (provider === 'anthropic') {
        // Anthropic response: { capturedAt: ..., quotas: [...], promo?: {...} }
        // Set promo state before rendering cards so promoTagHTML() works
        updateAnthropicPromoState(data.promo || null);
        if (data.quotas) {
          const container = document.getElementById('quota-grid-anthropic');
          if (container && container.children.length === 0) {
            renderAnthropicQuotaCards(data.quotas, 'quota-grid-anthropic');
          }
          data.quotas.forEach(q => updateAnthropicCard(q));
          // Store quota names for session table headers using Anthropic display order.
          if (State.anthropicSessionQuotas.length === 0) {
            State.anthropicSessionQuotas = sortQuotaKeysForProvider(data.quotas.map(q => q.name), 'anthropic').slice(0, 3);
            updateAnthropicSessionHeaders();
          }
        }
      } else if (provider === 'codex') {
        fetchCodexUsage({ mode: 'codex', data });
      } else if (provider === 'antigravity') {
        // Antigravity response: { capturedAt: ..., quotas: [...] }
        if (data.quotas) {
          const container = document.getElementById('quota-grid-antigravity');
          if (container && container.children.length === 0) {
            renderAntigravityQuotaCards(data.quotas, 'quota-grid-antigravity');
          }
          data.quotas.forEach(q => updateAntigravityCard(q));
        }
      } else if (provider === 'minimax') {
        if (data.quotas) {
          const container = document.getElementById('quota-grid-minimax');
          if (container && container.children.length !== data.quotas.length) {
            renderMiniMaxQuotaCards(data.quotas, 'quota-grid-minimax');
          } else if (container && container.children.length === 0) {
            renderMiniMaxQuotaCards(data.quotas, 'quota-grid-minimax');
          } else {
            data.quotas.forEach(q => updateMiniMaxCard(q));
          }
        }
      } else if (provider === 'gemini') {
        if (data.quotas) {
          const container = document.getElementById('quota-grid-gemini');
          if (container && container.children.length === 0) {
            renderGeminiQuotaCards(data.quotas, 'quota-grid-gemini');
          }
          data.quotas.forEach(q => updateGeminiCard(q));
        }
      } else if (provider === 'openrouter') {
        if (data.credits) {
          const container = document.getElementById('quota-grid-openrouter');
          if (container && container.children.length === 0) {
            renderOpenRouterCard(data.credits, 'quota-grid-openrouter');
          } else {
            updateOpenRouterCard(data.credits);
          }
        }
      } else if (provider === 'zai') {
        updateCard('tokensLimit', data.tokensLimit);
        updateCard('timeLimit', data.timeLimit);
        updateCard('toolCalls', data.toolCalls);
      } else {
        updateCard('subscription', data.subscription);
        updateCard('search', data.search);
        updateCard('toolCalls', data.toolCalls);
      }

      const lastUpdated = document.getElementById('last-updated');
      if (lastUpdated) {
        lastUpdated.textContent = `Last updated: ${new Date().toLocaleTimeString()}`;
      }

      const statusDot = document.getElementById('status-dot');
      if (statusDot) statusDot.classList.remove('stale');

    });
  } catch (err) {
    // fetch error - cards show fallback state
    if (State.currentRequestSeq !== requestSeq) return;
    const statusDot = document.getElementById('status-dot');
    if (statusDot) statusDot.classList.add('stale');
  }
}

async function fetchCodexUsage(options = {}) {
  const mode = options.mode || getCurrentProvider();
  let payload = options.data || null;

  try {
    if (mode === 'both') {
      let accounts = [];
      if (Array.isArray(payload)) {
        accounts = payload;
      } else if (payload && Array.isArray(payload.accounts)) {
        accounts = payload.accounts;
      } else {
        const res = await authFetch(`${API_BASE}/api/codex/accounts/usage`);
        if (!res.ok) throw new Error('Failed to fetch Codex account usage');
        const data = await res.json();
        accounts = Array.isArray(data.accounts) ? data.accounts : [];
      }
      renderCodexAccountSections(accounts);
      return;
    }

    if (!payload || !Array.isArray(payload.quotas)) {
      const accountID = State.codexAccount || 1;
      const res = await authFetch(`${API_BASE}/api/codex/usage?account=${encodeURIComponent(accountID)}`);
      if (!res.ok) throw new Error('Failed to fetch Codex usage');
      payload = await res.json();
    }

    const planChanged = setCodexPlanType(payload.planType);

    const visibleQuotas = filterCodexQuotasForPlan(payload.quotas, State.codexPlanType);
    const nextQuotaNames = visibleQuotas.map(q => q.name);
    const prevQuotaNames = Array.isArray(State.codexQuotaNames) ? State.codexQuotaNames : [];
    const quotaNamesChanged = nextQuotaNames.length !== prevQuotaNames.length ||
      nextQuotaNames.some((name, idx) => name !== prevQuotaNames[idx]);
    if (quotaNamesChanged) {
      State.codexQuotaNames = nextQuotaNames;
    }
    if (planChanged || quotaNamesChanged) {
      syncCodexOverviewControls();
    }
    if (quotaNamesChanged) {
      updateCodexSessionHeaders();
    }

    const container = document.getElementById('quota-grid-codex');
    if (!container) return;

    const renderedCount = container.querySelectorAll('.quota-card.codex-card').length;
    if (container.children.length === 0 || renderedCount !== visibleQuotas.length || planChanged) {
      renderCodexQuotaCards(visibleQuotas, 'quota-grid-codex', State.codexPlanType);
    }

    visibleQuotas.forEach(q => updateCodexCard(q));
  } catch (err) {
    // codex usage fetch error - non-critical
  }
}


// ── Anthropic Session Table Header Updates ──

// Mapping from sorted quota API keys to the 3 positional session columns (sub, search, tool)
// Backend sorts ActiveQuotaNames() alphabetically and maps first 3 to these DB columns.
const anthropicSessionSlots = ['sub', 'search', 'tool'];

function updateAnthropicSessionHeaders() {
  const quotas = State.anthropicSessionQuotas;
  if (!quotas || quotas.length === 0) return;

  for (let i = 0; i < 3; i++) {
    const el = document.getElementById(`anth-session-col-${i}`);
    if (el && quotas[i]) {
      const shortName = anthropicDisplayNames[quotas[i]] || quotas[i];
      // Remove trailing " Limit" for compact table headers
      const label = shortName.replace(/ Limit$/, '');
      el.innerHTML = `${label} % <span class="sort-arrow"></span>`;
    }
  }
}

// Get the display label for Anthropic session column by positional index (0, 1, 2)
function getAnthropicSessionLabel(idx) {
  const quotas = State.anthropicSessionQuotas;
  if (quotas && quotas[idx]) {
    const name = anthropicDisplayNames[quotas[idx]] || quotas[idx];
    return name.replace(/ Limit$/, '');
  }
  // Fallback labels if quota data hasn't loaded yet
  const fallbacks = ['5-Hour', 'Weekly', 'Weekly Sonnet'];
  return fallbacks[idx] || `Quota ${idx + 1}`;
}

// ── Deep Insights (Interactive Cards) ──

// Title-specific icons for insight cards (Feather/Lucide style)
const insightTitleIcons = {
  'Avg Cycle Utilization': '<circle cx="12" cy="12" r="10"/><path d="M12 6v6l4 2"/>', // clock/gauge
  '30-Day Usage': '<rect x="3" y="4" width="18" height="18" rx="2"/><line x1="16" y1="2" x2="16" y2="6"/><line x1="8" y1="2" x2="8" y2="6"/><line x1="3" y1="10" x2="21" y2="10"/>', // calendar
  'Weekly Pace': '<polyline points="23 6 13.5 15.5 8.5 10.5 1 18"/><polyline points="17 6 23 6 23 12"/>', // trending-up
  'Tool Call Share': '<path d="M21.21 15.89A10 10 0 1 1 8 2.83"/><path d="M22 12A10 10 0 0 0 12 2v10z"/>', // pie-chart
  'Session Avg': '<line x1="18" y1="20" x2="18" y2="10"/><line x1="12" y1="20" x2="12" y2="4"/><line x1="6" y1="20" x2="6" y2="14"/>', // bar-chart
  'Coverage': '<path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z"/>', // shield
  'High Variance': '<polyline points="22 12 18 12 15 21 9 3 6 12 2 12"/>', // activity
  'Usage Spread': '<line x1="12" y1="20" x2="12" y2="10"/><line x1="18" y1="20" x2="18" y2="4"/><line x1="6" y1="20" x2="6" y2="16"/>', // bar-chart-2
  'Consistent': '<line x1="5" y1="12" x2="19" y2="12"/>', // minus (steady)
  'Trend': '<polyline points="23 6 13.5 15.5 8.5 10.5 1 18"/><polyline points="17 6 23 6 23 12"/>', // trending-up
  'Getting Started': '<circle cx="12" cy="12" r="10"/><path d="M12 16v-4M12 8h.01"/>', // info
  // Z.ai-specific insight icons
  'Token Budget': '<circle cx="12" cy="12" r="10"/><path d="M12 6v6l4 2"/>', // clock/gauge
  'Token Rate': '<polyline points="23 6 13.5 15.5 8.5 10.5 1 18"/><polyline points="17 6 23 6 23 12"/>', // trending-up
  'Projected Usage': '<path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z"/>', // shield
  'Tool Breakdown': '<path d="M14.7 6.3a1 1 0 0 0 0 1.4l1.6 1.6a1 1 0 0 0 1.4 0l3.77-3.77a6 6 0 0 1-7.94 7.94l-6.91 6.91a2.12 2.12 0 0 1-3-3l6.91-6.91a6 6 0 0 1 7.94-7.94l-3.76 3.76z"/>', // wrench
  'Time Budget': '<circle cx="12" cy="12" r="10"/><polyline points="12 6 12 12 16 14"/>', // clock
  '24h Trend': '<polyline points="23 6 13.5 15.5 8.5 10.5 1 18"/><polyline points="17 6 23 6 23 12"/>', // trending-up
  '7-Day Usage': '<rect x="3" y="4" width="18" height="18" rx="2"/><line x1="16" y1="2" x2="16" y2="6"/><line x1="8" y1="2" x2="8" y2="6"/><line x1="3" y1="10" x2="21" y2="10"/>', // calendar
  'Plan Capacity': '<path d="M2 20h.01"/><path d="M7 20v-4"/><path d="M12 20v-8"/><path d="M17 20V8"/><path d="M22 4v16"/>', // signal/tiers
  'Tokens Per Call': '<path d="M12 2L2 7l10 5 10-5-10-5zM2 17l10 5 10-5M2 12l10 5 10-5"/>', // layers
  'Top Tool': '<polygon points="12 2 15.09 8.26 22 9.27 17 14.14 18.18 21.02 12 17.77 5.82 21.02 7 14.14 2 9.27 8.91 8.26 12 2"/>', // star
};

// Quota-type icons (used for live quota insight cards)
const quotaIcons = {
  subscription: '<rect x="3" y="3" width="18" height="18" rx="2"/><path d="M3 9h18M9 21V9"/>', // credit-card/subscription
  search: '<circle cx="11" cy="11" r="8"/><path d="M21 21l-4.35-4.35"/>', // search
  toolCalls: '<path d="M14.7 6.3a1 1 0 0 0 0 1.4l1.6 1.6a1 1 0 0 0 1.4 0l3.77-3.77a6 6 0 0 1-7.94 7.94l-6.91 6.91a2.12 2.12 0 0 1-3-3l6.91-6.91a6 6 0 0 1 7.94-7.94l-3.76 3.76z"/>', // wrench
  tokensLimit: '<path d="M12 2L2 7l10 5 10-5-10-5zM2 17l10 5 10-5M2 12l10 5 10-5"/>', // layers
  timeLimit: '<circle cx="12" cy="12" r="10"/><polyline points="12 6 12 12 16 14"/>', // clock
  session: '<line x1="18" y1="20" x2="18" y2="10"/><line x1="12" y1="20" x2="12" y2="4"/><line x1="6" y1="20" x2="6" y2="14"/>', // bar-chart
};

// Severity fallback icons
const insightIcons = {
  positive: '<path d="M20 6L9 17l-5-5"/>',
  warning: '<path d="M10.29 3.86L1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0zM12 9v4M12 17h.01"/>',
  negative: '<circle cx="12" cy="12" r="10"/><path d="M15 9l-6 6M9 9l6 6"/>',
  info: '<circle cx="12" cy="12" r="10"/><path d="M12 16v-4M12 8h.01"/>'
};

async function fetchDeepInsights() {
  const provider = getCurrentProvider();
  const requestProvider = provider;
  const requestAccount = requestProvider === 'codex' ? State.codexAccount : null;
  const requestRange = State.insightsRange;
  const requestSeq = (State.insightsRequestSeq || 0) + 1;
  State.insightsRequestSeq = requestSeq;
  const panel = document.querySelector('.insights-panel');
  const statsEl = document.getElementById('insights-stats');
  const cardsEl = document.getElementById('insights-cards');
  if (provider !== 'both' && !cardsEl) return;

  // Render range selector pills in the insights header (once)
  if (provider !== 'both') {
    renderInsightsRangePills();
  }

  try {
    const res = await authFetch(`${API_BASE}/api/insights?${providerParam()}&range=${requestRange}`);
    if (!res.ok) throw new Error('Failed to fetch insights');
    const data = await res.json();

    if (State.insightsRequestSeq !== requestSeq) return;
    if (getCurrentProvider() !== requestProvider) return;
    if (requestProvider === 'codex' && State.codexAccount !== requestAccount) return;
    if (State.insightsRange !== requestRange) return;

    if (requestProvider === 'both') {
      State.allProvidersInsights = data;
      renderAllProvidersView();
      return;
    } else {
      // Single provider mode
      let allStats = data.stats || [];
      let allInsights = data.insights || [];

      allStats = getSingleViewInsightStats(requestProvider, allStats);
      allInsights = getSingleViewInsightCards(requestProvider, allInsights);

      // Filter out hidden insights
      const expandedHidden = expandCorrelatedKeys(State.hiddenInsights);
      allInsights = allInsights.filter(i => !i.key || !expandedHidden.has(i.key));

      // Render stats
      if (statsEl) {
        statsEl.innerHTML = allStats.length > 0 ? allStats.map(s =>
          `<div class="insight-stat">
            <div class="insight-stat-value">${s.value}</div>
            <div class="insight-stat-label">${s.label}</div>
            ${s.sublabel ? `<div class="insight-stat-sublabel">${s.sublabel}</div>` : ''}
          </div>`
        ).join('') : '';
      }

      // Render insight cards
      renderInsightCards(cardsEl, allInsights);
    }

    // Render hidden insights badge
    renderHiddenInsightsBadge();
  } catch (err) {
    // insights fetch error - panel shows fallback state
    if (State.insightsRequestSeq !== requestSeq) return;
    if (getCurrentProvider() !== requestProvider) return;
    if (requestProvider === 'both') return;
    if (statsEl) statsEl.innerHTML = '';
    cardsEl.innerHTML = '<p class="insight-text">Unable to load insights.</p>';
  }
}

function renderInsightsRangePills() {
  const header = document.querySelector('.insights-panel .section-header');
  if (!header || header.querySelector('.insights-range-selector')) return;

  const selector = document.createElement('div');
  selector.className = 'range-selector insights-range-selector';
  selector.setAttribute('role', 'group');
  selector.setAttribute('aria-label', 'Insights time range');

  const ranges = [
    { value: '1d', label: '1d' },
    { value: '7d', label: '7d' },
    { value: '30d', label: '30d' },
  ];

  selector.innerHTML = ranges.map(r =>
    `<button class="range-btn ${r.value === State.insightsRange ? 'active' : ''}" data-insights-range="${r.value}">${r.label}</button>`
  ).join('');

  selector.addEventListener('click', (e) => {
    const btn = e.target.closest('[data-insights-range]');
    if (!btn) return;
    State.insightsRange = btn.dataset.insightsRange;
    selector.querySelectorAll('.range-btn').forEach(b => b.classList.remove('active'));
    btn.classList.add('active');
    fetchDeepInsights();
  });

  header.appendChild(selector);
}

function renderBothInsights(data, statsEl, cardsEl) {
  // Clear the single-mode containers
  if (statsEl) statsEl.innerHTML = '';

  const expandedHidden = expandCorrelatedKeys(State.hiddenInsights);
  const activeProviders = new Set(getBothViewProviders());

  const renderProviderBox = (providerKey, label, payload) => {
    if (!payload) return '';
    const providerStats = payload.stats || [];
    const providerInsights = (payload.insights || []).filter(i => !i.key || !expandedHidden.has(i.key));
    return `<div class="provider-insights-box" data-provider="${providerKey}">
      <h4 class="provider-insights-label">${label}</h4>
      <div class="insights-stats">${providerStats.map(s =>
        `<div class="insight-stat">
          <div class="insight-stat-value">${s.value}</div>
          <div class="insight-stat-label">${s.label}</div>
          ${s.sublabel ? `<div class="insight-stat-sublabel">${s.sublabel}</div>` : ''}
        </div>`
      ).join('')}</div>
      <div class="insights-cards">${buildInsightCardsHTML(providerInsights)}</div>
    </div>`;
  };

  let html = '';

  if (activeProviders.has('synthetic') && data.synthetic) {
    html += renderProviderBox('synthetic', 'Synthetic', data.synthetic);
  }

  if (activeProviders.has('zai') && data.zai) {
    html += renderProviderBox('zai', 'Z.ai', data.zai);
  }

  if (activeProviders.has('anthropic') && data.anthropic) {
    html += renderProviderBox('anthropic', 'Anthropic', data.anthropic);
  }

  if (activeProviders.has('copilot') && data.copilot) {
    html += renderProviderBox('copilot', 'Copilot <span class="beta-badge">Beta</span>', data.copilot);
  }

	if (activeProviders.has('antigravity') && data.antigravity) {
		html += renderProviderBox('antigravity', 'Antigravity', data.antigravity);
	}

	if (activeProviders.has('minimax') && data.minimax) {
		html += renderProviderBox('minimax', 'MiniMax', data.minimax);
	}

	if (activeProviders.has('gemini') && data.gemini) {
		html += renderProviderBox('gemini', 'Gemini', data.gemini);
	}

	if (activeProviders.has('codex')) {
    if (Array.isArray(data.codexAccounts) && data.codexAccounts.length > 0) {
      data.codexAccounts.forEach(acc => {
        const label = `Codex · ${acc.accountName || `Account ${acc.accountId || ''}`.trim()}`;
        html += renderProviderBox('codex', label, acc);
      });
    } else if (data.codex) {
      html += renderProviderBox('codex', 'Codex', data.codex);
    }
  }

  cardsEl.innerHTML = html || '<p class="insight-text">No insights available.</p>';

  // Attach events to all insight cards within both boxes
  cardsEl.querySelectorAll('.insight-card').forEach(card => {
    attachInsightCardEvents(card, cardsEl);
  });
  cardsEl.querySelectorAll('.insight-eye-btn').forEach(btn => {
    btn.addEventListener('click', (e) => {
      e.stopPropagation();
      toggleInsightVisibility(btn.dataset.key);
    });
  });
}

function buildInsightCardsHTML(insights) {
  if (insights.length === 0) return '<p class="insight-text">Keep tracking to see deep analytics.</p>';
  return insights.map((i, idx) => {
    const icon = insightTitleIcons[i.title] || (i.quotaType && quotaIcons[i.quotaType]) || insightIcons[i.severity] || insightIcons.info;
    const hideBtn = i.key ? `<button class="insight-eye-btn" data-key="${i.key}" aria-label="Hide this insight" title="Hide this insight">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"/><circle cx="12" cy="12" r="3"/></svg>
      </button>` : '';
    return `<div class="insight-card severity-${i.severity}" data-insight-idx="${idx}" data-key="${i.key || ''}" role="button" tabindex="0">
      <div class="insight-card-header">
        <svg class="insight-card-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">${icon}</svg>
        <span class="insight-card-title">${i.title}</span>
        ${i.metric || i.sublabel ? `<span class="insight-card-values">${i.metric ? `<span class="insight-card-metric">${i.metric}</span>` : ''}${i.sublabel ? `<span class="insight-card-sublabel">${i.sublabel}</span>` : ''}</span>` : ''}
        ${hideBtn}
        <svg class="insight-card-chevron" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M6 9l6 6 6-6"/></svg>
      </div>
      <div class="insight-card-detail">
        <div class="insight-card-desc">${i.description}</div>
      </div>
    </div>`;
  }).join('');
}

function renderInsightCards(container, insights) {
  if (insights.length > 0) {
    container.innerHTML = buildInsightCardsHTML(insights);
    container.querySelectorAll('.insight-card').forEach(card => {
      attachInsightCardEvents(card, container);
    });
    container.querySelectorAll('.insight-eye-btn').forEach(btn => {
      btn.addEventListener('click', (e) => {
        e.stopPropagation();
        toggleInsightVisibility(btn.dataset.key);
      });
    });
  } else {
    container.innerHTML = '<p class="insight-text">Keep tracking to see deep analytics.</p>';
  }
}

function attachInsightCardEvents(card, container) {
  const toggle = (e) => {
    if (e.target.closest('.insight-eye-btn')) return;
    const wasExpanded = card.classList.contains('expanded');
    // Only collapse siblings within the same parent container
    const parent = card.closest('.insights-cards') || container;
    parent.querySelectorAll('.insight-card.expanded').forEach(c => c.classList.remove('expanded'));
    if (!wasExpanded) card.classList.add('expanded');
  };
  card.addEventListener('click', toggle);
  card.addEventListener('keydown', e => {
    if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); toggle(e); }
  });
}

// ── Hidden Insights Badge ──

function renderHiddenInsightsBadge() {
  const panel = document.querySelector('.insights-panel');
  if (!panel) return;

  // Remove existing badge
  const existing = panel.querySelector('.hidden-insights-badge');
  if (existing) existing.remove();

  const hiddenCount = State.hiddenInsights.size;
  if (hiddenCount === 0) return;

  const badge = document.createElement('div');
  badge.className = 'hidden-insights-badge';
  badge.innerHTML = `
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" class="hidden-badge-icon">
      <path d="M17.94 17.94A10.07 10.07 0 0 1 12 20c-7 0-11-8-11-8a18.45 18.45 0 0 1 5.06-5.94"/>
      <path d="M9.9 4.24A9.12 9.12 0 0 1 12 4c7 0 11 8 11 8a18.5 18.5 0 0 1-2.16 3.19"/>
      <line x1="1" y1="1" x2="23" y2="23"/>
    </svg>
    <span>${hiddenCount} hidden</span>
    <button class="hidden-badge-show" title="Show all hidden insights">Show all</button>
  `;

  badge.querySelector('.hidden-badge-show').addEventListener('click', async () => {
    State.hiddenInsights.clear();
    await saveHiddenInsights();
    fetchDeepInsights();
  });

  // Insert after section header
  const header = panel.querySelector('.section-header');
  if (header) {
    header.after(badge);
  } else {
    panel.prepend(badge);
  }
}

// ── Chart: Crosshair Plugin ──

const crosshairPlugin = {
  id: 'crosshair',
  afterDraw(chart, args, options) {
    const { ctx, chartArea, tooltip } = chart;
    if (!tooltip || !tooltip.opacity || tooltip.dataPoints.length === 0) return;
    const x = tooltip.dataPoints[0].element.x;
    ctx.save();
    ctx.beginPath();
    ctx.setLineDash([4, 4]);
    ctx.strokeStyle = getComputedStyle(document.documentElement).getPropertyValue('--border-default').trim() || '#E5E7EB';
    ctx.lineWidth = 1;
    ctx.moveTo(x, chartArea.top);
    ctx.lineTo(x, chartArea.bottom);
    ctx.stroke();
    ctx.restore();
  }
};

// ── Chart Init & Update ──

function computeYMax(datasets, chart) {
  // Filter out hidden datasets - check both ds.hidden and chart metadata visibility
  const visibleDatasets = datasets.filter((ds, i) => {
    if (ds.hidden) return false;
    if (chart && chart.getDatasetMeta(i).hidden) return false;
    return ds.data && ds.data.length > 0;
  });

  // If no visible datasets, return default 10%
  if (visibleDatasets.length === 0) return 10;

  let maxVal = 0;
  visibleDatasets.forEach(ds => {
    ds.data.forEach(v => {
      // Handle both {x, y} objects and raw numbers
      const val = typeof v === 'number' ? v : (v && typeof v.y === 'number' ? v.y : 0);
      if (val > maxVal) maxVal = val;
    });
  });
  
  // If max is 0 or very low, show up to 10% to give visual context
  if (maxVal <= 0) return 10;
  if (maxVal < 5) return 10;
  
  // Add 30% headroom above the max value for better visualization
  // Round up to nearest 5 for cleaner axis labels
  const paddedMax = maxVal * 1.3;
  const yMax = Math.min(Math.max(Math.ceil(paddedMax / 5) * 5, 10), 100);

  return yMax;
}

function initChart() {
  if (getCurrentProvider() === 'both') return; // Both mode uses dual charts
  const ctx = document.getElementById('usage-chart');
  if (!ctx || typeof Chart === 'undefined') return;

  Chart.register(crosshairPlugin);

  const colors = getThemeColors();

  // Map dataset indices to quota types for visibility toggle
  const provider = getCurrentProvider();
  let defaultDatasets;
  if (provider === 'antigravity') {
    defaultDatasets = []; // Antigravity datasets are dynamic - populated when history data arrives
  } else if (provider === 'minimax') {
    defaultDatasets = []; // MiniMax datasets are dynamic - populated when history data arrives
  } else if (provider === 'gemini') {
    defaultDatasets = []; // Gemini datasets are dynamic - populated when history data arrives
  } else if (provider === 'openrouter') {
    defaultDatasets = []; // OpenRouter datasets are dynamic - populated when history data arrives
  } else if (provider === 'zai') {
    defaultDatasets = [
      { label: 'Tokens Limit', data: [], borderColor: getComputedStyle(document.documentElement).getPropertyValue('--chart-subscription').trim() || '#0D9488', backgroundColor: 'rgba(13, 148, 136, 0.06)', fill: true, tension: 0.4, borderWidth: 2, pointRadius: 0, pointHoverRadius: 4, hidden: State.hiddenQuotas.has('tokensLimit') },
      { label: 'Time Limit', data: [], borderColor: getComputedStyle(document.documentElement).getPropertyValue('--chart-search').trim() || '#F59E0B', backgroundColor: 'rgba(245, 158, 11, 0.06)', fill: true, tension: 0.4, borderWidth: 2, pointRadius: 0, pointHoverRadius: 4, hidden: State.hiddenQuotas.has('timeLimit') },
      { label: 'Tool Calls', data: [], borderColor: getComputedStyle(document.documentElement).getPropertyValue('--chart-toolcalls').trim() || '#3B82F6', backgroundColor: 'rgba(59, 130, 246, 0.06)', fill: true, tension: 0.4, borderWidth: 2, pointRadius: 0, pointHoverRadius: 4, hidden: State.hiddenQuotas.has('toolCalls') }
    ];
  } else {
    defaultDatasets = [
      { label: 'Subscription', data: [], borderColor: getComputedStyle(document.documentElement).getPropertyValue('--chart-subscription').trim() || '#0D9488', backgroundColor: 'rgba(13, 148, 136, 0.06)', fill: true, tension: 0.4, borderWidth: 2, pointRadius: 0, pointHoverRadius: 4, hidden: State.hiddenQuotas.has('subscription') },
      { label: 'Search', data: [], borderColor: getComputedStyle(document.documentElement).getPropertyValue('--chart-search').trim() || '#F59E0B', backgroundColor: 'rgba(245, 158, 11, 0.06)', fill: true, tension: 0.4, borderWidth: 2, pointRadius: 0, pointHoverRadius: 4, hidden: State.hiddenQuotas.has('search') },
      { label: 'Tool Calls', data: [], borderColor: getComputedStyle(document.documentElement).getPropertyValue('--chart-toolcalls').trim() || '#3B82F6', backgroundColor: 'rgba(59, 130, 246, 0.06)', fill: true, tension: 0.4, borderWidth: 2, pointRadius: 0, pointHoverRadius: 4, hidden: State.hiddenQuotas.has('toolCalls') }
    ];
  }
  const quotaMap = provider === 'zai'
    ? ['tokensLimit', 'timeLimit', 'toolCalls']
    : provider === 'minimax'
      ? []
    : provider === 'gemini'
      ? []
    : provider === 'openrouter'
      ? []
    : ['subscription', 'search', 'toolCalls'];

  State.chart = new Chart(ctx, {
    type: 'line',
    data: {
      labels: [],
      datasets: defaultDatasets
    },
    options: {
      responsive: true,
      maintainAspectRatio: false,
      interaction: { mode: 'index', intersect: false },
      plugins: {
        legend: {
          labels: { color: colors.text, usePointStyle: true, boxWidth: 8 },
          onClick: function(e, legendItem, legend) {
            // Default toggle behavior
            const index = legendItem.datasetIndex;
            const ci = legend.chart;
            const meta = ci.getDatasetMeta(index);
            meta.hidden = meta.hidden === null ? !ci.data.datasets[index].hidden : null;
            ci.update('none');
            // Recalculate Y-axis based on visible datasets
            State.chartYMax = computeYMax(ci.data.datasets, ci);
            ci.options.scales.y.max = State.chartYMax;
            ci.update();
          }
        },
        tooltip: {
          mode: 'index',
          intersect: false,
          backgroundColor: colors.surfaceContainer || '#1E1E1E',
          titleColor: colors.onSurface || '#E6E1E5',
          bodyColor: colors.text || '#CAC4D0',
          borderColor: colors.outline || '#938F99',
          borderWidth: 1,
          padding: 12,
          displayColors: true,
          usePointStyle: true,
          callbacks: {
            label: function(ctx) {
              return `${ctx.dataset.label}: ${ctx.parsed.y.toFixed(1)}%`;
            }
          }
        }
      },
      scales: {
        x: {
          type: 'time',
          time: { unit: 'hour', displayFormats: { minute: 'HH:mm', hour: 'HH:mm', day: 'MMM d' } },
          grid: { color: colors.grid, drawBorder: false },
          ticks: { color: colors.text, maxTicksLimit: 6, source: 'auto' }
        },
        y: { grid: { color: colors.grid, drawBorder: false }, ticks: { color: colors.text, callback: v => v + '%' }, min: 0, max: State.chartYMax }
      }
    }
  });
}

function updateChartTheme() {
  if (getCurrentProvider() === 'both') {
    // Re-render both-mode provider cards so Chart.js picks up updated theme tokens.
    if (State.allProvidersCurrent || State.allProvidersInsights || State.allProvidersHistory) {
      renderAllProvidersView();
    } else {
      fetchHistory();
    }
    return;
  }
  if (!State.chart) return;
  const colors = getThemeColors();
  const style = getComputedStyle(document.documentElement);

  // Update line colors for theme
  const chartColors = [
    style.getPropertyValue('--chart-subscription').trim() || '#0D9488',
    style.getPropertyValue('--chart-search').trim() || '#F59E0B',
    style.getPropertyValue('--chart-toolcalls').trim() || '#3B82F6'
  ];
  State.chart.data.datasets.forEach((ds, i) => {
    if (chartColors[i]) ds.borderColor = chartColors[i];
  });

  State.chart.options.scales.x.grid.color = colors.grid;
  State.chart.options.scales.x.ticks.color = colors.text;
  State.chart.options.scales.y.grid.color = colors.grid;
  State.chart.options.scales.y.ticks.color = colors.text;
  State.chart.options.scales.y.max = State.chartYMax;
  State.chart.options.plugins.legend.labels.color = colors.text;
  State.chart.options.plugins.tooltip.backgroundColor = colors.surfaceContainer;
  State.chart.options.plugins.tooltip.titleColor = colors.onSurface;
  State.chart.options.plugins.tooltip.bodyColor = colors.text;
  State.chart.options.plugins.tooltip.borderColor = colors.outline;
  State.chart.update('none');
}

async function fetchHistory(range) {
  if (range === undefined) {
    const activeBtn = document.querySelector('.range-btn[data-range].active');
    range = activeBtn ? activeBtn.dataset.range : '6h';
  }
  State.currentRange = range;
  const requestProvider = getCurrentProvider();
  const requestAccount = requestProvider === 'codex' ? State.codexAccount : null;
  const requestRange = range;
  const requestSeq = (State.historyRequestSeq || 0) + 1;
  State.historyRequestSeq = requestSeq;

  try {
    const res = await authFetch(`${API_BASE}/api/history?range=${range}&${providerParam()}`);
    if (!res.ok) throw new Error('Failed to fetch history');
    const data = await res.json();

    if (State.historyRequestSeq !== requestSeq) return;
    if (getCurrentProvider() !== requestProvider) return;
    if (requestProvider === 'codex' && State.codexAccount !== requestAccount) return;
    if (State.currentRange !== requestRange) return;

    const provider = requestProvider;

    if (provider === 'both') {
      State.allProvidersHistory = data;
      renderAllProvidersView();
      return;
    }

    if (!State.chart) initChart();
    if (!State.chart) return;

    if (provider === 'antigravity') {
      // Antigravity history: { labels: [...], datasets: [...] }
      const defaultColors = ['#D97757', '#10B981', '#3B82F6'];
      const labels = data.labels || [];
      const datasets = [];
      (data.datasets || []).forEach((ds, idx) => {
        const rawData = (ds.data || []).map((y, i) => ({ x: new Date(labels[i]), y }));
        const color = ds.borderColor || defaultColors[idx % defaultColors.length];
        const { data: processedData, gapSegments, pointRadii } = processDataWithGaps(rawData, range);
        const mainDataset = {
          label: ds.label || ds.modelId,
          data: processedData,
          borderColor: color,
          backgroundColor: color + '15',
          fill: true,
          tension: 0.4,
          borderWidth: 2,
          pointRadius: pointRadii,
          pointHoverRadius: 4,
          hidden: State.hiddenQuotas.has(ds.modelId),
          spanGaps: true,
          segment: getSegmentStyle(gapSegments, color)
        };
        datasets.push(mainDataset);
      });
      State.chart.data.datasets = datasets;
      updateTimeScale(State.chart, range);
      State.chartYMax = computeYMax(State.chart.data.datasets, State.chart);
      State.chart.options.scales.y.max = State.chartYMax;
      State.chart.update();
      return;
    }

    const historyRows = Array.isArray(data) ? data : [];

    if (provider === 'anthropic') {
      // Anthropic history: array of { capturedAt, five_hour, seven_day, ... }
      // Dynamic datasets based on available quota keys
      const quotaKeys = new Set();
      historyRows.forEach(d => {
        Object.keys(d).forEach(k => { if (k !== 'capturedAt') quotaKeys.add(k); });
      });
      const sortedKeys = sortQuotaKeysForProvider(quotaKeys, 'anthropic');
      let fallbackIdx = 0;
      const datasets = [];
      sortedKeys.forEach((key) => {
        const color = anthropicChartColorMap[key] || anthropicChartColorFallback[fallbackIdx++ % anthropicChartColorFallback.length];
        const rawData = historyRows.map(d => ({ x: new Date(d.capturedAt), y: d[key] != null ? d[key] : null }));
        const { data, gapSegments, pointRadii } = processDataWithGaps(rawData, range);
        const mainDataset = {
          label: anthropicDisplayNames[key] || key,
          data: data,
          borderColor: color.border,
          backgroundColor: color.bg,
          fill: true, tension: 0.4, borderWidth: 2,
          pointRadius: pointRadii,
          pointHoverRadius: 4,
          hidden: State.hiddenQuotas.has(key),
          spanGaps: true,
          segment: getSegmentStyle(gapSegments, color.border)
        };
        datasets.push(mainDataset);
      });
      State.chart.data.datasets = datasets;
      updateTimeScale(State.chart, range);
      State.chartYMax = computeYMax(State.chart.data.datasets, State.chart);
      State.chart.options.scales.y.max = State.chartYMax;
      State.chart.update();
      return;
    }

    if (provider === 'copilot') {
      // Copilot history: array of { capturedAt, quotas: [...] } → transform to flat object
      // Extract quota keys and build datasets
      const quotaKeys = new Set();
      historyRows.forEach(d => {
        if (d.quotas) d.quotas.forEach(q => quotaKeys.add(q.name));
      });
      const sortedKeys = [...quotaKeys].sort();
      let fallbackIdx = 0;
      const datasets = [];
      sortedKeys.forEach((key) => {
        const color = copilotChartColorMap[key] || copilotChartColorFallback[fallbackIdx++ % copilotChartColorFallback.length];
        const rawData = historyRows.map(d => {
          const q = d.quotas ? d.quotas.find(q => q.name === key) : null;
          return { x: new Date(d.capturedAt), y: q ? (q.usagePercent || 0) : 0 };
        });
        const { data, gapSegments, pointRadii } = processDataWithGaps(rawData, range);
        const mainDataset = {
          label: copilotDisplayNames[key] || key,
          data: data,
          borderColor: color.border,
          backgroundColor: color.bg,
          fill: true, tension: 0.4, borderWidth: 2,
          pointRadius: pointRadii,
          pointHoverRadius: 4,
          hidden: State.hiddenQuotas.has(key),
          spanGaps: true,
          segment: getSegmentStyle(gapSegments, color.border)
        };
        datasets.push(mainDataset);
      });
      State.chart.data.datasets = datasets;
      updateTimeScale(State.chart, range);
      State.chartYMax = computeYMax(State.chart.data.datasets, State.chart);
      State.chart.options.scales.y.max = State.chartYMax;
      State.chart.update();
      return;
    }

    if (provider === 'minimax') {
      const quotaKeys = new Set();
      historyRows.forEach(d => {
        Object.keys(d).forEach(k => { if (k !== 'capturedAt') quotaKeys.add(k); });
      });
      const sortedKeys = [...quotaKeys].sort();
      let fallbackIdx = 0;
      const datasets = [];
      sortedKeys.forEach((key) => {
        const color = minimaxChartColorMap[key] || minimaxChartColorFallback[fallbackIdx++ % minimaxChartColorFallback.length];
        const rawData = historyRows.map(d => ({ x: new Date(d.capturedAt), y: d[key] || 0 }));
        const { data, gapSegments, pointRadii } = processDataWithGaps(rawData, range);
        const mainDataset = {
          label: minimaxDisplayNames[key] || key,
          data: data,
          borderColor: color.border,
          backgroundColor: color.bg,
          fill: true,
          tension: 0.4,
          borderWidth: 2,
          pointRadius: pointRadii,
          pointHoverRadius: 4,
          hidden: State.hiddenQuotas.has(key),
          spanGaps: true,
          segment: getSegmentStyle(gapSegments, color.border)
        };
        datasets.push(mainDataset);
      });
      State.chart.data.datasets = datasets;
      updateTimeScale(State.chart, range);
      State.chartYMax = computeYMax(State.chart.data.datasets, State.chart);
      State.chart.options.scales.y.max = State.chartYMax;
      State.chart.update();
      return;
    }

    if (provider === 'gemini') {
      const quotaKeys = new Set();
      historyRows.forEach(d => {
        Object.keys(d).forEach(k => { if (k !== 'capturedAt') quotaKeys.add(k); });
      });
      const sortedKeys = [...quotaKeys].sort();
      let fallbackIdx = 0;
      const datasets = [];
      sortedKeys.forEach((key) => {
        const color = geminiChartColorMap[key] || geminiChartColorFallback[fallbackIdx++ % geminiChartColorFallback.length];
        const rawData = historyRows.map(d => ({ x: new Date(d.capturedAt), y: d[key] || 0 }));
        const { data, gapSegments, pointRadii } = processDataWithGaps(rawData, range);
        const mainDataset = {
          label: geminiDisplayNames[key] || key,
          data: data,
          borderColor: color.border,
          backgroundColor: color.bg,
          fill: true,
          tension: 0.4,
          borderWidth: 2,
          pointRadius: pointRadii,
          pointHoverRadius: 4,
          hidden: State.hiddenQuotas.has(key),
          spanGaps: true,
          segment: getSegmentStyle(gapSegments, color.border)
        };
        datasets.push(mainDataset);
      });
      State.chart.data.datasets = datasets;
      updateTimeScale(State.chart, range);
      State.chartYMax = computeYMax(State.chart.data.datasets, State.chart);
      State.chart.options.scales.y.max = State.chartYMax;
      State.chart.update();
      return;
    }

    if (provider === 'openrouter') {
      const quotaKeys = new Set();
      historyRows.forEach(d => {
        Object.keys(d).forEach(k => { if (k !== 'capturedAt') quotaKeys.add(k); });
      });
      const sortedKeys = [...quotaKeys].sort();
      const openrouterDisplayNames = { usage: 'Total Usage', usageDaily: 'Daily Usage', percent: 'Usage %' };
      const openrouterChartColors = {
        usage: { border: '#0D9488', bg: 'rgba(13, 148, 136, 0.06)' },
        usageDaily: { border: '#F59E0B', bg: 'rgba(245, 158, 11, 0.06)' },
        percent: { border: '#3B82F6', bg: 'rgba(59, 130, 246, 0.06)' }
      };
      const openrouterFallback = [
        { border: '#8B5CF6', bg: 'rgba(139, 92, 246, 0.06)' },
        { border: '#EC4899', bg: 'rgba(236, 72, 153, 0.06)' }
      ];
      let fallbackIdx = 0;
      const datasets = [];
      sortedKeys.forEach((key) => {
        const color = openrouterChartColors[key] || openrouterFallback[fallbackIdx++ % openrouterFallback.length];
        const rawData = historyRows.map(d => ({ x: new Date(d.capturedAt), y: d[key] || 0 }));
        const { data, gapSegments, pointRadii } = processDataWithGaps(rawData, range);
        datasets.push({
          label: openrouterDisplayNames[key] || key,
          data: data,
          borderColor: color.border,
          backgroundColor: color.bg,
          fill: true,
          tension: 0.4,
          borderWidth: 2,
          pointRadius: pointRadii,
          pointHoverRadius: 4,
          hidden: State.hiddenQuotas.has(key),
          spanGaps: true,
          segment: getSegmentStyle(gapSegments, color.border)
        });
      });
      State.chart.data.datasets = datasets;
      updateTimeScale(State.chart, range);
      State.chartYMax = computeYMax(State.chart.data.datasets, State.chart);
      State.chart.options.scales.y.max = State.chartYMax;
      State.chart.update();
      return;
    }

    if (provider === 'codex') {
      // Codex history: array of { capturedAt, five_hour, seven_day, ... }
      const quotaKeys = new Set();
      historyRows.forEach(d => {
        Object.keys(d).forEach(k => { if (k !== 'capturedAt') quotaKeys.add(k); });
      });
      const sortedKeys = sortQuotaKeysForProvider(quotaKeys, 'codex');
      let fallbackIdx = 0;
      const datasets = [];
      sortedKeys.forEach((key) => {
        const color = codexChartColorMap[key] || codexChartColorFallback[fallbackIdx++ % codexChartColorFallback.length];
        const rawData = historyRows.map(d => ({ x: new Date(d.capturedAt), y: d[key] || 0 }));
        const { data, gapSegments, pointRadii } = processDataWithGaps(rawData, range);
        const mainDataset = {
          label: codexDisplayNames[key] || key,
          data: data,
          borderColor: color.border,
          backgroundColor: color.bg,
          fill: true, tension: 0.4, borderWidth: 2, pointRadius: pointRadii, pointHoverRadius: 4,
          hidden: State.hiddenQuotas.has(key),
          spanGaps: true,
          segment: getSegmentStyle(gapSegments, color.border)
        };
        datasets.push(mainDataset);
      });
      State.chart.data.datasets = datasets;
      updateTimeScale(State.chart, range);
      State.chartYMax = computeYMax(State.chart.data.datasets, State.chart);
      State.chart.options.scales.y.max = State.chartYMax;
      State.chart.update();
      return;
    }

    if (provider === 'zai') {
      const style = getComputedStyle(document.documentElement);
      const datasets = [];
      const configs = [
        { label: 'Tokens', key: 'tokensPercent', hiddenKey: 'tokensLimit', color: style.getPropertyValue('--chart-subscription').trim() || '#0D9488', bg: 'rgba(13, 148, 136, 0.06)' },
        { label: 'Time', key: 'timePercent', hiddenKey: 'timeLimit', color: style.getPropertyValue('--chart-search').trim() || '#F59E0B', bg: 'rgba(245, 158, 11, 0.06)' },
        { label: 'Tool Calls', key: 'toolCallsPercent', hiddenKey: 'toolCalls', color: style.getPropertyValue('--chart-toolcalls').trim() || '#3B82F6', bg: 'rgba(59, 130, 246, 0.06)' }
      ];
      configs.forEach(cfg => {
        const rawData = historyRows.map(d => ({ x: new Date(d.capturedAt), y: d[cfg.key] }));
        const { data, gapSegments, pointRadii } = processDataWithGaps(rawData, range);
        const mainDataset = { label: cfg.label, data: data, borderColor: cfg.color, backgroundColor: cfg.bg, fill: true, tension: 0.4, borderWidth: 2, pointRadius: pointRadii, pointHoverRadius: 4, hidden: State.hiddenQuotas.has(cfg.hiddenKey), spanGaps: true, segment: getSegmentStyle(gapSegments, cfg.color) };
        datasets.push(mainDataset);
      });
      State.chart.data.datasets = datasets;
    } else {
      const datasets = [];
      const configs = [
        { label: 'Subscription', key: 'subscriptionPercent', hiddenKey: 'subscription', color: '#0D9488', bg: 'rgba(13, 148, 136, 0.06)' },
        { label: 'Search', key: 'searchPercent', hiddenKey: 'search', color: '#F59E0B', bg: 'rgba(245, 158, 11, 0.06)' },
        { label: 'Tool Calls', key: 'toolCallsPercent', hiddenKey: 'toolCalls', color: '#3B82F6', bg: 'rgba(59, 130, 246, 0.06)' }
      ];
      configs.forEach(cfg => {
        const rawData = historyRows.map(d => ({ x: new Date(d.capturedAt), y: d[cfg.key] }));
        const { data, gapSegments, pointRadii } = processDataWithGaps(rawData, range);
        const mainDataset = { label: cfg.label, data: data, borderColor: cfg.color, backgroundColor: cfg.bg, fill: true, tension: 0.4, borderWidth: 2, pointRadius: pointRadii, pointHoverRadius: 4, hidden: State.hiddenQuotas.has(cfg.hiddenKey), spanGaps: true, segment: getSegmentStyle(gapSegments, cfg.color) };
        datasets.push(mainDataset);
      });
      State.chart.data.datasets = datasets;
    }

    updateTimeScale(State.chart, range);
    State.chartYMax = computeYMax(State.chart.data.datasets, State.chart);
    State.chart.options.scales.y.max = State.chartYMax;
    State.chart.update();
  } catch (err) {
    // history fetch error - chart shows empty state
  }
}

// ── "Both" Mode: Provider Cards ──

const bothProviderNames = {
  synthetic: 'Synthetic',
  zai: 'Z.ai',
  anthropic: 'Anthropic',
  copilot: 'Copilot',
  codex: 'Codex',
  antigravity: 'Antigravity',
  minimax: 'MiniMax',
  gemini: 'Gemini',
};

function escapeHTML(value) {
  return String(value ?? '')
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;')
    .replaceAll("'", '&#39;');
}

function toTitleCase(value) {
  return String(value || '')
    .replaceAll('_', ' ')
    .trim()
    .split(/\s+/)
    .filter(Boolean)
    .map(part => part.charAt(0).toUpperCase() + part.slice(1).toLowerCase())
    .join(' ');
}

function sanitizeProviderCardKey(value) {
  return String(value || '')
    .toLowerCase()
    .replace(/[^a-z0-9_-]+/g, '-')
    .replace(/^-+|-+$/g, '');
}

function loadProviderCardCollapseState() {
  try {
    const raw = localStorage.getItem('onwatch-provider-card-collapsed');
    return raw ? JSON.parse(raw) : {};
  } catch (e) {
    return {};
  }
}

function saveProviderCardCollapseState(state) {
  try {
    localStorage.setItem('onwatch-provider-card-collapsed', JSON.stringify(state));
  } catch (e) {
    // silent
  }
}

function isProviderTelemetryEnabled(provider, accountID) {
  const visibility = State.providerVisibility && typeof State.providerVisibility === 'object'
    ? State.providerVisibility
    : {};

  if ((provider === 'codex' || provider === 'minimax') && accountID != null) {
    const accountKey = `${provider}:${accountID}`;
    const accountVis = visibility[accountKey];
    if (accountVis && typeof accountVis === 'object' && accountVis.polling === false) {
      return false;
    }
  }

  const providerVis = visibility[provider];
  if (providerVis && typeof providerVis === 'object' && providerVis.polling === false) {
    return false;
  }
  return true;
}

function normalizeBothQuotas(provider, payload) {
  if (!payload) return [];

  if (provider === 'synthetic') {
    const map = [
      { key: 'subscription', label: 'Subscription' },
      { key: 'search', label: 'Search (Hourly)' },
      { key: 'toolCalls', label: 'Tool Calls' },
    ];
    return map
      .map(({ key, label }) => {
        const item = payload[key];
        if (!item) return null;
        return {
          name: key,
          displayName: label,
          cardPercent: item.percent ?? 0,
          cardLabel: 'Utilization',
          status: item.status || 'healthy',
          timeUntilResetSeconds: item.timeUntilResetSeconds || 0,
          resetsAt: item.renewsAt || '',
        };
      })
      .filter(Boolean);
  }

  if (provider === 'zai') {
    const map = [
      { key: 'tokensLimit', label: 'Tokens Limit' },
      { key: 'timeLimit', label: 'Time Limit' },
      { key: 'toolCalls', label: 'Tool Calls' },
    ];
    return map
      .map(({ key, label }) => {
        const item = payload[key];
        if (!item) return null;
        return {
          name: key,
          displayName: label,
          cardPercent: item.percent ?? 0,
          cardLabel: 'Utilization',
          status: item.status || 'healthy',
          timeUntilResetSeconds: item.timeUntilResetSeconds || 0,
          resetsAt: item.renewsAt || '',
        };
      })
      .filter(Boolean);
  }

  if (!Array.isArray(payload.quotas)) return [];
  const rawQuotas = provider === 'codex'
    ? filterCodexQuotasForPlan(payload.quotas, payload.planType || State.codexPlanType)
    : sortQuotaEntriesForProvider(payload.quotas, provider);
  return rawQuotas.map((quota) => {
    const percent = quota.cardPercent != null
      ? quota.cardPercent
      : (quota.usagePercent != null
        ? quota.usagePercent
        : (quota.utilization != null ? quota.utilization : (quota.percent ?? 0)));
    return {
      ...quota,
      cardPercent: percent,
      displayName: quota.displayName
        || codexDisplayNames[quota.name]
        || anthropicDisplayNames[quota.name]
        || copilotDisplayNames[quota.name]
        || minimaxDisplayNames[quota.name]
        || geminiDisplayNames[quota.name]
        || getQuotaDisplayName(quota.name, provider),
      cardLabel: quota.cardLabel || 'Utilization',
      status: quota.status || 'healthy',
      timeUntilResetSeconds: quota.timeUntilResetSeconds || 0,
      resetsAt: quota.resetsAt || quota.renewsAt || '',
    };
  });
}

function buildAllProviderEntries() {
  const current = State.allProvidersCurrent || {};
  const insights = State.allProvidersInsights || {};
  const history = State.allProvidersHistory || {};
  const configuredOrder = getBothViewProviders();
  const providerSet = new Set(configuredOrder);
  const addProviderFromKey = (key) => {
    if (!key) return;
    if (key === 'codex' || key === 'codexAccounts') {
      providerSet.add('codex');
      return;
    }
    if (key === 'minimax' || key === 'minimaxAccounts') {
      providerSet.add('minimax');
      return;
    }
    if (bothProviderNames[key]) {
      providerSet.add(key);
    }
  };
  Object.keys(current).forEach(addProviderFromKey);
  Object.keys(insights).forEach(addProviderFromKey);
  Object.keys(history).forEach(addProviderFromKey);

  const order = [];
  const seen = new Set();
  configuredOrder.forEach((provider) => {
    if (providerSet.has(provider) && !seen.has(provider)) {
      seen.add(provider);
      order.push(provider);
    }
  });
  [...providerSet]
    .filter(provider => !seen.has(provider))
    .sort((a, b) => a.localeCompare(b))
    .forEach((provider) => {
      seen.add(provider);
      order.push(provider);
    });

  // Set Anthropic promo state so promoTagHTML() works in both view
  if (current.anthropic && current.anthropic.promo) {
    updateAnthropicPromoState(current.anthropic.promo);
  }

  const entries = [];

  const addProviderEntry = (provider) => {
    if (provider === 'codex') {
      const currentAccounts = Array.isArray(current.codexAccounts)
        ? current.codexAccounts
        : (current.codex ? [current.codex] : []);
      if (currentAccounts.length === 0) return;

      const insightAccounts = Array.isArray(insights.codexAccounts) ? insights.codexAccounts : [];
      const historyAccounts = Array.isArray(history.codexAccounts) ? history.codexAccounts : [];

      currentAccounts.forEach((account, idx) => {
        const accountID = account.accountId || account.id || idx + 1;
        if (!isProviderTelemetryEnabled('codex', accountID)) return;
        const accountName = account.accountName || account.name || `Account ${idx + 1}`;
        const cardKey = sanitizeProviderCardKey(`codex-${accountID}`);
        const insightPayload = insightAccounts.find(acc => String(acc.accountId || '') === String(accountID))
          || insights.codex
          || { stats: [], insights: [] };
        const historyPayload = historyAccounts.find(acc => String(acc.accountId || '') === String(accountID));
        entries.push({
          provider: 'codex',
          cardKey,
          title: `Codex - Account: ${accountName}`,
          badge: toTitleCase(account.planType || ''),
          planType: account.planType || '',
          quotas: normalizeBothQuotas('codex', account),
          insights: insightPayload,
          historyRows: Array.isArray(historyPayload?.history)
            ? historyPayload.history
            : (Array.isArray(history.codex) ? history.codex : []),
        });
      });
      return;
    }

    if (provider === 'minimax') {
      const currentAccounts = Array.isArray(current.minimaxAccounts)
        ? current.minimaxAccounts
        : (current.minimax ? [current.minimax] : []);
      if (currentAccounts.length === 0) return;

      const insightAccounts = Array.isArray(insights.minimaxAccounts) ? insights.minimaxAccounts : [];
      const historyAccounts = Array.isArray(history.minimaxAccounts) ? history.minimaxAccounts : [];

      // Single account - render as a normal provider entry
      if (currentAccounts.length === 1 && !currentAccounts[0].accountId) {
        const payload = currentAccounts[0];
        if (!isProviderTelemetryEnabled('minimax')) return;
        entries.push({
          provider: 'minimax',
          cardKey: sanitizeProviderCardKey('minimax'),
          title: bothProviderNames.minimax || 'MiniMax',
          badge: '',
          quotas: normalizeBothQuotas('minimax', payload),
          insights: insights.minimax || { stats: [], insights: [] },
          historyRows: Array.isArray(history.minimax) ? history.minimax : [],
        });
        return;
      }

      currentAccounts.forEach((account, idx) => {
        const accountID = account.accountId || account.id || idx + 1;
        if (!isProviderTelemetryEnabled('minimax', accountID)) return;
        const accountName = account.accountName || account.name || `Account ${idx + 1}`;
        const cardKey = sanitizeProviderCardKey(`minimax-${accountID}`);
        const insightPayload = insightAccounts.find(acc => String(acc.accountId || '') === String(accountID))
          || insights.minimax
          || { stats: [], insights: [] };
        const historyPayload = historyAccounts.find(acc => String(acc.accountId || '') === String(accountID));
        entries.push({
          provider: 'minimax',
          cardKey,
          title: `MiniMax - ${accountName}`,
          badge: '',
          quotas: normalizeBothQuotas('minimax', account),
          insights: insightPayload,
          historyRows: Array.isArray(historyPayload?.history)
            ? historyPayload.history
            : (Array.isArray(history.minimax) ? history.minimax : []),
        });
      });
      return;
    }

    const payload = current[provider];
    if (!payload) return;
    if (!isProviderTelemetryEnabled(provider)) return;
    entries.push({
      provider,
      cardKey: sanitizeProviderCardKey(provider),
      title: bothProviderNames[provider] || toTitleCase(provider),
      badge: provider === 'copilot' ? 'Beta' : toTitleCase(payload.planType || ''),
      promoHtml: provider === 'anthropic' && payload.promo ? promoTagHTML() : '',
      planType: payload.planType || '',
      quotas: normalizeBothQuotas(provider, payload),
      insights: insights[provider] || { stats: [], insights: [] },
      historyRows: Array.isArray(history[provider]) ? history[provider] : [],
    });
  };

  order.forEach(addProviderEntry);
  return entries;
}

function renderProviderKPIHTML(quotas) {
  if (!Array.isArray(quotas) || quotas.length === 0) {
    return '<p class="insight-text">No KPI data available yet.</p>';
  }
  return quotas.map((quota) => {
    const percent = Number(quota.cardPercent ?? 0);
    const status = quota.status || 'healthy';
    const statusCfg = statusConfig[status] || statusConfig.healthy;
    const displayName = quota.displayName || quota.name || 'Quota';
    const label = quota.cardLabel || 'Utilization';
    const subtitle = quota.subtitle || minimaxSharedSubtitle(quota.sharedModels);
    const usageFraction = Number.isFinite(Number(quota.used)) && Number.isFinite(Number(quota.total)) && Number(quota.total) > 0
      ? `${formatNumber(quota.used)} / ${formatNumber(quota.total)}`
      : label;
    const icon = anthropicQuotaIcons[quota.name]
      || quotaIcons[quota.name]
      || '<circle cx="12" cy="12" r="10"/><path d="M12 16v-4M12 8h.01"/>';
    const resetText = quota.resetsAt ? `Resets: ${formatDateTime(quota.resetsAt)}` : '';
    const countdown = quota.timeUntilResetSeconds > 0 ? formatDuration(quota.timeUntilResetSeconds) : '--:--';

    return `<article class="quota-card provider-kpi-card" data-quota="${escapeHTML(quota.name || '')}">
      <header class="card-header">
        <div class="quota-title-block">
          <h2 class="quota-title">
            <svg class="quota-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">${icon}</svg>
            ${escapeHTML(displayName)}
          </h2>
          ${subtitle ? `<div class="quota-subtitle">${escapeHTML(subtitle)}</div>` : ''}
        </div>
        <span class="countdown">${escapeHTML(countdown)}</span>
      </header>
      <div class="progress-stats">
        <span class="usage-percent">${percent.toFixed(1)}%</span>
        <span class="usage-fraction">${escapeHTML(usageFraction)}</span>
      </div>
      <div class="progress-wrapper">
        <div class="progress-bar" role="progressbar" aria-valuenow="${Math.round(percent)}" aria-valuemin="0" aria-valuemax="100">
          <div class="progress-fill" style="width: ${Math.max(0, Math.min(percent, 100)).toFixed(1)}%" data-status="${status}"></div>
        </div>
      </div>
      <footer class="card-footer">
        <span class="status-badge" data-status="${status}">
          <svg class="status-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="${statusCfg.icon}"/></svg>
          ${statusCfg.label}
        </span>
        <span class="reset-time">${escapeHTML(resetText)}</span>
      </footer>
    </article>`;
  }).join('');
}

function sortItemsByPreference(items, preferredKeys, valueSelector) {
  if (!Array.isArray(items) || items.length === 0) return [];
  const order = new Map(preferredKeys.map((value, index) => [value, index]));
  return [...items].sort((a, b) => {
    const aValue = valueSelector(a);
    const bValue = valueSelector(b);
    const aRank = order.has(aValue) ? order.get(aValue) : Number.MAX_SAFE_INTEGER;
    const bRank = order.has(bValue) ? order.get(bValue) : Number.MAX_SAFE_INTEGER;
    if (aRank !== bRank) return aRank - bRank;
    return 0;
  });
}

function compactInsightText(text, maxLength = 84) {
  const normalized = String(text || '').replace(/\s+/g, ' ').trim();
  if (!normalized) return '';
  const sentenceMatch = normalized.match(/^(.+?[.!?])(?:\s|$)/);
  const candidate = sentenceMatch && sentenceMatch[1] ? sentenceMatch[1].trim() : normalized;
  if (candidate.length <= maxLength) return candidate;
  return `${candidate.slice(0, maxLength - 3).trimEnd()}...`;
}

function getSingleViewInsightStats(provider, stats) {
  if (provider !== 'minimax' && provider !== 'gemini') return stats;
  return sortItemsByPreference(
    stats.filter((stat) => stat && stat.label !== 'Current Status'),
    ['Current Usage', 'Burn Rate', 'Resets In'],
    (stat) => stat.label
  );
}

function getSingleViewInsightCards(provider, insights) {
  if (provider !== 'minimax' && provider !== 'gemini' && provider !== 'openrouter') return insights;
  const filtered = insights.filter((insight) => !['shared_status', 'burn_rate'].includes(insight.key));
  return sortItemsByPreference(
    filtered.length > 0 ? filtered : insights,
    ['trend', 'efficiency', 'burn_rate', 'shared_status'],
    (insight) => insight.key
  );
}

function getCompactProviderStats(provider, stats) {
  const preferred = provider === 'minimax' || provider === 'gemini' || provider === 'openrouter'
    ? ['Burn Rate', 'Current Usage', 'Resets In', 'Current Status']
    : [];
  const ordered = preferred.length > 0
    ? sortItemsByPreference(stats, preferred, (stat) => stat.label)
    : stats;
  return ordered.slice(0, 2);
}

function getCompactProviderInsights(provider, insights) {
  const ordered = provider === 'minimax' || provider === 'gemini' || provider === 'openrouter'
    ? sortItemsByPreference(insights, ['efficiency', 'trend', 'burn_rate', 'shared_status'], (insight) => insight.key)
    : insights;
  const urgent = ordered.filter((insight) => ['warning', 'negative'].includes(insight.severity));
  return urgent.slice(0, 1);
}

function renderProviderInsightsHTML(provider, payload) {
  const stats = getCompactProviderStats(provider, Array.isArray(payload?.stats) ? payload.stats : []);
  const insights = getCompactProviderInsights(provider, Array.isArray(payload?.insights) ? payload.insights : []);
  const items = [];

  stats.forEach((stat) => {
    items.push(`<article class="insight-card provider-mini-insight severity-info">
      <div class="insight-card-header">
        <span class="insight-card-title">${escapeHTML(stat.label || 'Metric')}</span>
        <span class="insight-card-values"><span class="insight-card-metric">${escapeHTML(stat.value || '--')}</span></span>
      </div>
      ${stat.sublabel ? `<div class="provider-mini-insight-note">${escapeHTML(compactInsightText(stat.sublabel, 48))}</div>` : ''}
    </article>`);
  });

  insights.forEach((insight) => {
    const note = compactInsightText(insight.sublabel || insight.description || '', 72);
    items.push(`<article class="insight-card provider-mini-insight severity-${escapeHTML(insight.severity || 'info')}">
      <div class="insight-card-header">
        <span class="insight-card-title">${escapeHTML(insight.title || 'Insight')}</span>
        ${insight.metric ? `<span class="insight-card-values"><span class="insight-card-metric">${escapeHTML(insight.metric)}</span></span>` : ''}
      </div>
      ${note ? `<div class="provider-mini-insight-note">${escapeHTML(note)}</div>` : ''}
    </article>`);
  });

  if (items.length === 0) return '';
  return items.join('');
}

function buildFixedDatasetsForRows(rows, range, configs) {
  const datasets = [];
  configs.forEach((cfg) => {
    const rawData = rows.map(d => ({ x: new Date(d.capturedAt), y: d[cfg.key] }));
    const processed = processDataWithGaps(rawData, range);
    datasets.push({
      label: cfg.label,
      data: processed.data,
      borderColor: cfg.color,
      backgroundColor: cfg.bg,
      fill: true,
      tension: 0.4,
      borderWidth: 2,
      pointRadius: processed.pointRadii,
      pointHoverRadius: 4,
      spanGaps: true,
      segment: getSegmentStyle(processed.gapSegments, cfg.color),
    });
  });
  return datasets;
}

function buildDynamicDatasetsForRows(rows, range, labelMap, colorMap, colorFallback, providerKey) {
  const keys = new Set();
  rows.forEach((row) => {
    Object.keys(row).forEach((key) => {
      if (key !== 'capturedAt') keys.add(key);
    });
  });

  const datasets = [];
  let idx = 0;
  sortQuotaKeysForProvider(keys, providerKey).forEach((key) => {
    const color = colorMap[key] || colorFallback[idx++ % colorFallback.length];
    const rawData = rows.map(d => ({ x: new Date(d.capturedAt), y: d[key] || 0 }));
    const processed = processDataWithGaps(rawData, range);
    datasets.push({
      label: (labelMap[key] || getQuotaDisplayName(key, providerKey) || key),
      data: processed.data,
      borderColor: color.border,
      backgroundColor: color.bg,
      fill: true,
      tension: 0.4,
      borderWidth: 2,
      pointRadius: processed.pointRadii,
      pointHoverRadius: 4,
      spanGaps: true,
      segment: getSegmentStyle(processed.gapSegments, color.border),
    });
  });
  return datasets;
}

function buildProviderCardDatasets(provider, rows, range) {
  const style = getComputedStyle(document.documentElement);
  if (provider === 'synthetic') {
    return buildFixedDatasetsForRows(rows, range, [
      { label: 'Subscription', key: 'subscriptionPercent', color: style.getPropertyValue('--chart-subscription').trim() || '#0D9488', bg: 'rgba(13,148,136,0.06)' },
      { label: 'Search', key: 'searchPercent', color: style.getPropertyValue('--chart-search').trim() || '#F59E0B', bg: 'rgba(245,158,11,0.06)' },
      { label: 'Tool Calls', key: 'toolCallsPercent', color: style.getPropertyValue('--chart-toolcalls').trim() || '#3B82F6', bg: 'rgba(59,130,246,0.06)' },
    ]);
  }
  if (provider === 'zai') {
    return buildFixedDatasetsForRows(rows, range, [
      { label: 'Tokens', key: 'tokensPercent', color: style.getPropertyValue('--chart-subscription').trim() || '#0D9488', bg: 'rgba(13,148,136,0.06)' },
      { label: 'Time', key: 'timePercent', color: style.getPropertyValue('--chart-search').trim() || '#F59E0B', bg: 'rgba(245,158,11,0.06)' },
      { label: 'Tool Calls', key: 'toolCallsPercent', color: style.getPropertyValue('--chart-toolcalls').trim() || '#3B82F6', bg: 'rgba(59,130,246,0.06)' },
    ]);
  }
  if (provider === 'anthropic') {
    return buildDynamicDatasetsForRows(rows, range, anthropicDisplayNames, anthropicChartColorMap, anthropicChartColorFallback, 'anthropic');
  }
  if (provider === 'codex') {
    return buildDynamicDatasetsForRows(rows, range, codexDisplayNames, codexChartColorMap, codexChartColorFallback, 'codex');
  }
  if (provider === 'copilot') {
    return buildDynamicDatasetsForRows(rows, range, copilotDisplayNames, copilotChartColorMap, copilotChartColorFallback, 'copilot');
  }
  if (provider === 'antigravity') {
    return buildDynamicDatasetsForRows(rows, range, {}, antigravityChartColorMap, antigravityChartColorFallback, 'antigravity');
  }
  if (provider === 'minimax') {
    return buildDynamicDatasetsForRows(rows, range, minimaxDisplayNames, minimaxChartColorMap, minimaxChartColorFallback, 'minimax');
  }
  if (provider === 'gemini') {
    return buildDynamicDatasetsForRows(rows, range, geminiDisplayNames, geminiChartColorMap, geminiChartColorFallback, 'gemini');
  }
  if (provider === 'openrouter') {
    const orDisplayNames = { usage: 'Total Usage', usageDaily: 'Daily Usage', percent: 'Usage %' };
    const orColors = {
      usage: { border: '#0D9488', bg: 'rgba(13, 148, 136, 0.06)' },
      usageDaily: { border: '#F59E0B', bg: 'rgba(245, 158, 11, 0.06)' },
      percent: { border: '#3B82F6', bg: 'rgba(59, 130, 246, 0.06)' }
    };
    const orFallback = [{ border: '#8B5CF6', bg: 'rgba(139, 92, 246, 0.06)' }];
    return buildDynamicDatasetsForRows(rows, range, orDisplayNames, orColors, orFallback, 'openrouter');
  }
  return [];
}

function destroyProviderCardCharts() {
  Object.values(State.providerCharts || {}).forEach((chart) => {
    if (chart && typeof chart.destroy === 'function') {
      chart.destroy();
    }
  });
  State.providerCharts = {};
}

function renderAllProvidersView() {
  const container = document.getElementById('all-providers-container');
  if (!container) return;

  const entries = buildAllProviderEntries();
  const collapsedState = loadProviderCardCollapseState();
  destroyProviderCardCharts();

  if (entries.length === 0) {
    container.innerHTML = '<p class="insight-text">No provider data available yet.</p>';
    return;
  }

  container.innerHTML = entries.map((entry) => {
    const collapsed = Boolean(collapsedState[entry.cardKey]);
    const badge = entry.badge ? `<span class="provider-card-badge">${escapeHTML(entry.badge)}</span>` : '';
    const promo = entry.promoHtml || '';
    const hasChartData = Array.isArray(entry.historyRows) && entry.historyRows.length > 0;
    const chartSection = hasChartData
      ? `<div class="provider-chart">
          <canvas id="provider-chart-${entry.cardKey}"></canvas>
        </div>`
      : `<div class="provider-chart provider-chart-empty">
          <p class="insight-text">Collecting data...</p>
        </div>`;
    return `<section class="provider-card ${collapsed ? 'collapsed' : ''}" data-card-key="${entry.cardKey}" data-provider="${entry.provider}">
      <header class="provider-card-header">
        <div class="provider-card-title">
          <span>${escapeHTML(entry.title)}</span>
          ${badge}${promo}
        </div>
        <button class="provider-card-collapse-btn" type="button" data-card-key="${entry.cardKey}" aria-expanded="${collapsed ? 'false' : 'true'}" aria-label="${collapsed ? 'Expand' : 'Collapse'} ${escapeHTML(entry.title)}">
          <svg class="provider-card-collapse-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <path d="m9 6 6 6-6 6"/>
          </svg>
        </button>
      </header>
      <div class="provider-card-body">
        <div class="provider-kpis">${renderProviderKPIHTML(entry.quotas)}</div>
        ${(() => {
          const insightsHTML = renderProviderInsightsHTML(entry.provider, entry.insights);
          return insightsHTML ? `<div class="provider-insights">${insightsHTML}</div>` : '';
        })()}
        ${chartSection}
      </div>
    </section>`;
  }).join('');

  container.querySelectorAll('.provider-card-collapse-btn').forEach((btn) => {
    btn.addEventListener('click', () => {
      const cardKey = btn.dataset.cardKey;
      const card = container.querySelector(`.provider-card[data-card-key="${cardKey}"]`);
      if (!card) return;
      card.classList.toggle('collapsed');
      const collapsed = card.classList.contains('collapsed');
      btn.setAttribute('aria-expanded', collapsed ? 'false' : 'true');
      const title = card.querySelector('.provider-card-title span')?.textContent || 'provider card';
      btn.setAttribute('aria-label', `${collapsed ? 'Expand' : 'Collapse'} ${title}`);
      collapsedState[cardKey] = collapsed;
      saveProviderCardCollapseState(collapsedState);
    });
  });

  const chartRange = State.currentRange || '6h';
  const colors = getThemeColors();
  entries.forEach((entry) => {
    const chartHost = container.querySelector(`.provider-card[data-card-key="${entry.cardKey}"] .provider-chart`);
    const canvas = container.querySelector(`#provider-chart-${entry.cardKey}`);
    const rows = Array.isArray(entry.historyRows) ? entry.historyRows : [];
    if (!chartHost || !canvas) return;

    const datasets = buildProviderCardDatasets(entry.provider, rows, chartRange);
    if (!datasets.length) {
      chartHost.classList.add('provider-chart-empty');
      chartHost.innerHTML = '<p class="insight-text">Collecting data...</p>';
      return;
    }

    chartHost.classList.remove('provider-chart-empty');

    const chart = new Chart(canvas, {
      type: 'line',
      data: { datasets },
      options: buildChartOptions(colors, computeYMax(datasets), chartRange)
    });
    State.providerCharts[entry.cardKey] = chart;
  });
}

// ── "Both" Mode: Dual Charts (legacy fallback) ──

function updateBothCharts(data, range = '6h') {
  const container = document.querySelector('.chart-container');
  if (!container) return;

  const destroyChart = (chart) => {
    if (chart && typeof chart.destroy === 'function') chart.destroy();
  };
  destroyChart(State.chartSyn); State.chartSyn = null;
  destroyChart(State.chartZai); State.chartZai = null;
  destroyChart(State.chartAnth); State.chartAnth = null;
  destroyChart(State.chartCodex); State.chartCodex = null;
  Object.values(State.chartCodexByAccount || {}).forEach(destroyChart);
  State.chartCodexByAccount = {};

  const activeProviders = new Set(getBothViewProviders());
  const slots = [];

  if (activeProviders.has('synthetic') && Array.isArray(data.synthetic) && data.synthetic.length > 0) {
    slots.push({ id: 'syn', label: 'Synthetic', provider: 'synthetic', rows: data.synthetic });
  }
  if (activeProviders.has('zai') && Array.isArray(data.zai) && data.zai.length > 0) {
    slots.push({ id: 'zai', label: 'Z.ai', provider: 'zai', rows: data.zai });
  }
  if (activeProviders.has('anthropic') && Array.isArray(data.anthropic) && data.anthropic.length > 0) {
    slots.push({ id: 'anth', label: 'Anthropic', provider: 'anthropic', rows: data.anthropic });
  }
  if (activeProviders.has('copilot') && Array.isArray(data.copilot) && data.copilot.length > 0) {
    slots.push({ id: 'copilot', label: 'Copilot', provider: 'copilot', rows: data.copilot });
  }
  if (activeProviders.has('antigravity') && Array.isArray(data.antigravity) && data.antigravity.length > 0) {
    slots.push({ id: 'antigravity', label: 'Antigravity', provider: 'antigravity', rows: data.antigravity });
  }
  if (activeProviders.has('minimax') && Array.isArray(data.minimax) && data.minimax.length > 0) {
    slots.push({ id: 'minimax', label: 'MiniMax', provider: 'minimax', rows: data.minimax });
  }
  if (activeProviders.has('gemini') && Array.isArray(data.gemini) && data.gemini.length > 0) {
    slots.push({ id: 'gemini', label: 'Gemini', provider: 'gemini', rows: data.gemini });
  }
  if (activeProviders.has('codex')) {
    if (Array.isArray(data.codexAccounts) && data.codexAccounts.length > 0) {
      data.codexAccounts.forEach((account, idx) => {
        const accountID = String(account.accountId || idx + 1).replace(/[^a-zA-Z0-9_-]/g, '-');
        const history = Array.isArray(account.history) ? account.history : [];
        if (history.length === 0) return;
        slots.push({
          id: `codex-${accountID}`,
          label: `Codex · ${account.accountName || `Account ${idx + 1}`}`,
          provider: 'codex',
          rows: history,
          accountKey: accountID,
        });
      });
    } else if (Array.isArray(data.codex) && data.codex.length > 0) {
      slots.push({ id: 'codex', label: 'Codex', provider: 'codex', rows: data.codex });
    }
  }

  container.classList.add('both-charts');
  if (slots.length === 0) {
    container.innerHTML = '<p class="insight-text">No chart data available.</p>';
    return;
  }

  container.innerHTML = slots.map(slot =>
    `<div class="chart-half"><h4 class="chart-half-label">${slot.label}</h4><canvas id="usage-chart-${slot.id}"></canvas></div>`
  ).join('');

  const origCanvas = document.getElementById('usage-chart');
  if (origCanvas) origCanvas.style.display = 'none';

  const style = getComputedStyle(document.documentElement);
  const colors = getThemeColors();

  const createFixedDatasets = (rows, configs) => {
    const datasets = [];
    configs.forEach(cfg => {
      const rawData = rows.map(d => ({ x: new Date(d.capturedAt), y: d[cfg.key] }));
      const processed = processDataWithGaps(rawData, range);
      datasets.push({
        label: cfg.label,
        data: processed.data,
        borderColor: cfg.color,
        backgroundColor: cfg.bg,
        fill: true,
        tension: 0.4,
        borderWidth: 2,
        pointRadius: processed.pointRadii,
        pointHoverRadius: 4,
        spanGaps: true,
        segment: getSegmentStyle(processed.gapSegments, cfg.color),
      });
    });
    return datasets;
  };

  const createDynamicDatasets = (rows, labelMap, colorMap, colorFallback, providerKey) => {
    const keys = new Set();
    rows.forEach(d => {
      Object.keys(d).forEach(k => { if (k !== 'capturedAt') keys.add(k); });
    });
    const sorted = sortQuotaKeysForProvider(keys, providerKey);
    const datasets = [];
    let idx = 0;
    sorted.forEach((key) => {
      const color = colorMap[key] || colorFallback[idx++ % colorFallback.length];
      const rawData = rows.map(d => ({ x: new Date(d.capturedAt), y: d[key] || 0 }));
      const processed = processDataWithGaps(rawData, range);
      datasets.push({
        label: (labelMap[key] || getQuotaDisplayName(key, providerKey) || key),
        data: processed.data,
        borderColor: color.border,
        backgroundColor: color.bg,
        fill: true,
        tension: 0.4,
        borderWidth: 2,
        pointRadius: processed.pointRadii,
        pointHoverRadius: 4,
        spanGaps: true,
        segment: getSegmentStyle(processed.gapSegments, color.border),
      });
    });
    return datasets;
  };

  slots.forEach(slot => {
    const canvas = document.getElementById(`usage-chart-${slot.id}`);
    if (!canvas || !Array.isArray(slot.rows) || slot.rows.length === 0) return;

    let datasets = [];
    if (slot.provider === 'synthetic') {
      datasets = createFixedDatasets(slot.rows, [
        { label: 'Subscription', key: 'subscriptionPercent', color: style.getPropertyValue('--chart-subscription').trim() || '#0D9488', bg: 'rgba(13,148,136,0.06)' },
        { label: 'Search', key: 'searchPercent', color: style.getPropertyValue('--chart-search').trim() || '#F59E0B', bg: 'rgba(245,158,11,0.06)' },
        { label: 'Tool Calls', key: 'toolCallsPercent', color: style.getPropertyValue('--chart-toolcalls').trim() || '#3B82F6', bg: 'rgba(59,130,246,0.06)' },
      ]);
    } else if (slot.provider === 'zai') {
      datasets = createFixedDatasets(slot.rows, [
        { label: 'Tokens', key: 'tokensPercent', color: style.getPropertyValue('--chart-subscription').trim() || '#0D9488', bg: 'rgba(13,148,136,0.06)' },
        { label: 'Time', key: 'timePercent', color: style.getPropertyValue('--chart-search').trim() || '#F59E0B', bg: 'rgba(245,158,11,0.06)' },
        { label: 'Tool Calls', key: 'toolCallsPercent', color: style.getPropertyValue('--chart-toolcalls').trim() || '#3B82F6', bg: 'rgba(59,130,246,0.06)' },
      ]);
    } else if (slot.provider === 'anthropic') {
      datasets = createDynamicDatasets(slot.rows, anthropicDisplayNames, anthropicChartColorMap, anthropicChartColorFallback, 'anthropic');
    } else if (slot.provider === 'codex') {
      datasets = createDynamicDatasets(slot.rows, codexDisplayNames, codexChartColorMap, codexChartColorFallback, 'codex');
    } else if (slot.provider === 'copilot') {
      datasets = createDynamicDatasets(slot.rows, copilotDisplayNames, copilotChartColorMap, copilotChartColorFallback, 'copilot');
    } else if (slot.provider === 'antigravity') {
      datasets = createDynamicDatasets(slot.rows, {}, antigravityChartColorMap, antigravityChartColorFallback, 'antigravity');
    } else if (slot.provider === 'minimax') {
      datasets = createDynamicDatasets(slot.rows, minimaxDisplayNames, minimaxChartColorMap, minimaxChartColorFallback, 'minimax');
    } else if (slot.provider === 'gemini') {
      datasets = createDynamicDatasets(slot.rows, geminiDisplayNames, geminiChartColorMap, geminiChartColorFallback, 'gemini');
    } else if (slot.provider === 'openrouter') {
      const orDN = { usage: 'Total Usage', usageDaily: 'Daily Usage', percent: 'Usage %' };
      const orCM = { usage: { border: '#0D9488', bg: 'rgba(13, 148, 136, 0.06)' }, usageDaily: { border: '#F59E0B', bg: 'rgba(245, 158, 11, 0.06)' }, percent: { border: '#3B82F6', bg: 'rgba(59, 130, 246, 0.06)' } };
      const orFB = [{ border: '#8B5CF6', bg: 'rgba(139, 92, 246, 0.06)' }];
      datasets = createDynamicDatasets(slot.rows, orDN, orCM, orFB, 'openrouter');
    }

    if (datasets.length === 0) return;

    const chart = new Chart(canvas, {
      type: 'line',
      data: { datasets },
      options: buildChartOptions(colors, computeYMax(datasets), range)
    });

    if (slot.provider === 'synthetic') State.chartSyn = chart;
    if (slot.provider === 'zai') State.chartZai = chart;
    if (slot.provider === 'anthropic') State.chartAnth = chart;
    if (slot.provider === 'codex') {
      if (slot.accountKey) State.chartCodexByAccount[slot.accountKey] = chart;
      else State.chartCodex = chart;
    }
  });
}

function buildChartOptions(colors, yMax, range) {
  const rangeKey = (range || '6h').toLowerCase();
  const timeUnit = ['7d', '30d', '15d'].includes(rangeKey) ? 'day' : ['24h', '3d'].includes(rangeKey) ? 'hour' : 'hour';
  const displayFormats = {
    minute: 'HH:mm',
    hour: ['7d', '30d', '15d', '24h', '3d'].includes(rangeKey) ? 'MMM d, HH:mm' : 'HH:mm',
    day: 'MMM d'
  };
  return {
    responsive: true,
    maintainAspectRatio: false,
    interaction: { mode: 'index', intersect: false },
    plugins: {
      legend: { labels: { color: colors.text, usePointStyle: true, boxWidth: 8 } },
      tooltip: {
        mode: 'index', intersect: false,
        backgroundColor: colors.surfaceContainer || '#1E1E1E',
        titleColor: colors.onSurface || '#E6E1E5',
        bodyColor: colors.text || '#CAC4D0',
        borderColor: colors.outline || '#938F99',
        borderWidth: 1, padding: 12, displayColors: true, usePointStyle: true,
        callbacks: {
          label: ctx => ctx.parsed.y != null ? `${ctx.dataset.label}: ${ctx.parsed.y.toFixed(1)}%` : null
        }
      }
    },
    scales: {
      x: {
        type: 'time',
        time: { unit: timeUnit, displayFormats },
        grid: { color: colors.grid, drawBorder: false },
        ticks: { color: colors.text, maxTicksLimit: 6, source: 'auto' }
      },
      y: { grid: { color: colors.grid, drawBorder: false }, ticks: { color: colors.text, callback: v => v + '%' }, min: 0, max: yMax || 100 }
    }
  };
}

// Get configured poll interval from page (in seconds), default 120s
function getPollIntervalMs() {
  const app = document.querySelector('.app');
  const intervalSec = app ? parseInt(app.dataset.pollInterval, 10) : 120;
  return (intervalSec || 120) * 1000;
}

// Analyze data and return gap indices + point visibility
// Solid lines for continuous data, dotted/faded for gaps
function processDataWithGaps(dataPoints, range = '6h') {
  // Dynamic gap threshold: multiplier × poll interval
  // 1h: 3 polls, 6h: 5 polls, 24h: 10 polls, 7d: 30 polls, 30d: 60 polls
  const pollMs = getPollIntervalMs();
  const multipliers = {
    '1h': 3,
    '6h': 5,
    '24h': 10,
    '7d': 30,
    '30d': 60
  };
  const gapThresholdMs = pollMs * (multipliers[range] || 5);

  if (!dataPoints || dataPoints.length === 0) return { data: [], gapSegments: new Set(), pointRadii: [] };
  if (dataPoints.length === 1) return { data: dataPoints, gapSegments: new Set(), pointRadii: [2] };

  const gapSegments = new Set();
  const pointRadii = [];
  const getTime = pt => pt.x instanceof Date ? pt.x.getTime() : new Date(pt.x).getTime();

  for (let i = 0; i < dataPoints.length; i++) {
    const prevGap = i === 0 ? Infinity : getTime(dataPoints[i]) - getTime(dataPoints[i - 1]);
    const nextGap = i === dataPoints.length - 1 ? Infinity : getTime(dataPoints[i + 1]) - getTime(dataPoints[i]);

    // Mark segment as gap
    if (nextGap > gapThresholdMs) {
      gapSegments.add(i);
    }

    // Show point if isolated or at edge of a short burst
    const isIsolated = prevGap > gapThresholdMs && nextGap > gapThresholdMs;
    const isEdgeOfBurst = (prevGap > gapThresholdMs || nextGap > gapThresholdMs);
    pointRadii.push(isIsolated ? 2 : (isEdgeOfBurst ? 1 : 0));
  }

  return { data: dataPoints, gapSegments, pointRadii };
}

// Create segment styling: solid for continuous, dotted/faded for gaps
function getSegmentStyle(gapSegments, baseColor) {
  let fadedColor = baseColor;
  if (baseColor.startsWith('#')) {
    const r = parseInt(baseColor.slice(1, 3), 16);
    const g = parseInt(baseColor.slice(3, 5), 16);
    const b = parseInt(baseColor.slice(5, 7), 16);
    fadedColor = `rgba(${r}, ${g}, ${b}, 0.35)`;
  }
  return {
    borderColor: ctx => gapSegments.has(ctx.p0DataIndex) ? fadedColor : baseColor,
    borderDash: ctx => gapSegments.has(ctx.p0DataIndex) ? [5, 3] : []
  };
}


function updateTimeScale(chart, range) {
  if (!chart || !chart.options || !chart.options.scales || !chart.options.scales.x) return;
  const rangeKey = (range || '6h').toLowerCase();
  const timeUnit = ['7d', '30d', '15d'].includes(rangeKey) ? 'day' : 'hour';
  chart.options.scales.x.time = {
    unit: timeUnit,
    displayFormats: {
      minute: 'HH:mm',
      hour: ['7d', '30d', '15d', '24h', '3d'].includes(rangeKey) ? 'MMM d, HH:mm' : 'HH:mm',
      day: 'MMM d'
    }
  };
}

// ── Cycles Table (client-side search/sort/paginate) ──

async function fetchCycles() {
  if (!shouldShowHistoryTables()) return;
  const requestProvider = getCurrentProvider();
  const requestAccount = requestProvider === 'codex' ? State.codexAccount : null;
  const requestRange = State.cyclesRange;
  const requestSeq = (State.cyclesRequestSeq || 0) + 1;
  State.cyclesRequestSeq = requestSeq;
  const provider = requestProvider;
  const loggingHistoryProviders = new Set(['synthetic', 'zai', 'anthropic', 'copilot', 'codex', 'antigravity', 'minimax', 'gemini']);

  if (loggingHistoryProviders.has(provider)) {
    // Convert range from ms to days (min 1, max 30)
    const rangeDays = Math.min(30, Math.max(1, Math.ceil(State.cyclesRange / (24 * 60 * 60 * 1000))));
    // Calculate limit based on range: 1 minute polling = rangeDays * 24 * 60 records
    // Cap at 50000 for performance (enough for ~35 days of 1-minute data)
    const dynamicLimit = Math.min(50000, rangeDays * 24 * 60);
    const accountParam = provider === 'codex' ? codexAccountParam() : provider === 'minimax' ? minimaxAccountParam() : '';
    const url = `/api/logging-history?provider=${provider}&limit=${dynamicLimit}&range=${rangeDays}${accountParam}`;
    try {
      const res = await authFetch(url);
      if (!res.ok) throw new Error('Failed to fetch logging history');
      const data = await res.json();
      if (State.cyclesRequestSeq !== requestSeq) return;
      if (getCurrentProvider() !== requestProvider) return;
      if (requestProvider === 'codex' && State.codexAccount !== requestAccount) return;
      if (State.cyclesRange !== requestRange) return;

      State.allCyclesData = (data.logs || []).map(log => ({
        cycleId: log.id,
        cycleStart: log.capturedAt,
        cycleEnd: log.capturedAt,
        totalDelta: 0,
        crossQuotas: log.crossQuotas || [],
      }));
      State.cyclesQuotaNames = data.quotaNames || [];
      State.cyclesPage = 1;
      State.isLoggingHistory = true;
      renderCyclesTable();
    } catch (err) {
      // logging history fetch error - table shows empty state
    }
    return;
  }

  // For both-provider view, keep existing cycle-overview behavior
  let groupBy = 'five_hour';
  const url = `/api/cycle-overview?${providerParam()}&groupBy=${groupBy}&limit=200`;

  try {
    const res = await authFetch(url);
    if (!res.ok) throw new Error('Failed to fetch cycles');
    const data = await res.json();
    if (State.cyclesRequestSeq !== requestSeq) return;
    if (getCurrentProvider() !== requestProvider) return;
    if (requestProvider === 'codex' && State.codexAccount !== requestAccount) return;
    if (State.cyclesRange !== requestRange) return;

    State.allCyclesData = data.cycles || [];
    State.cyclesQuotaNames = data.quotaNames || [];
    State.cyclesPage = 1;
    State.isLoggingHistory = false;
    renderCyclesTable();
  } catch (err) {
    // cycles fetch error - table shows empty state
  }
}

function bucketStartISO(iso, bucketMinutes) {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return null;
  const bucketMs = Math.max(1, bucketMinutes) * 60 * 1000;
  return new Date(Math.floor(d.getTime() / bucketMs) * bucketMs).toISOString();
}

function aggregateCyclesByBucket(rows, bucketMinutes) {
  if (!Array.isArray(rows) || rows.length === 0 || bucketMinutes <= 1) return rows;

  const grouped = new Map();
  for (const row of rows) {
    const bucketISO = bucketStartISO(row.cycleStart, bucketMinutes);
    if (!bucketISO) continue;

    if (!grouped.has(bucketISO)) {
      grouped.set(bucketISO, {
        cycleId: row.cycleId,
        cycleStart: bucketISO,
        cycleEnd: row.cycleEnd || null,
        totalDelta: typeof row.totalDelta === 'number' ? row.totalDelta : 0,
        crossQuotas: Array.isArray(row.crossQuotas)
          ? row.crossQuotas.map(cq => ({
              name: cq.name,
              value: cq.value,
              limit: cq.limit,
              percent: cq.percent,
              startPercent: cq.startPercent,
              delta: cq.delta,
            }))
          : [],
      });
      continue;
    }

    const agg = grouped.get(bucketISO);
    if (agg.cycleEnd == null) {
      if (row.cycleEnd != null) {
        agg.cycleEnd = row.cycleEnd;
      }
    } else if (row.cycleEnd != null && new Date(row.cycleEnd).getTime() > new Date(agg.cycleEnd).getTime()) {
      agg.cycleEnd = row.cycleEnd;
    }

    if (typeof row.totalDelta === 'number') {
      agg.totalDelta += row.totalDelta;
    }

    const byName = new Map((agg.crossQuotas || []).map(cq => [cq.name, cq]));
    for (const cq of row.crossQuotas || []) {
      if (!byName.has(cq.name)) {
        byName.set(cq.name, {
          name: cq.name,
          value: cq.value,
          limit: cq.limit,
          percent: cq.percent,
          startPercent: cq.startPercent,
          delta: cq.delta,
        });
        continue;
      }
      const existing = byName.get(cq.name);
      if ((cq.percent ?? -1) > (existing.percent ?? -1)) {
        existing.value = cq.value;
        existing.limit = cq.limit;
        existing.percent = cq.percent;
      }
      if (typeof cq.delta === 'number') {
        existing.delta = (typeof existing.delta === 'number' ? existing.delta : 0) + cq.delta;
      }
      if (typeof cq.startPercent === 'number' && (typeof existing.startPercent !== 'number' || cq.startPercent < existing.startPercent)) {
        existing.startPercent = cq.startPercent;
      }
    }
    agg.crossQuotas = [...byName.values()];
  }

  return [...grouped.values()].sort((a, b) => new Date(b.cycleStart).getTime() - new Date(a.cycleStart).getTime());
}

function renderCyclesTable() {
  const thead = document.getElementById('cycles-thead');
  const tbody = document.getElementById('cycles-tbody');
  const infoEl = document.getElementById('cycles-info');
  const paginationEl = document.getElementById('cycles-pagination');
  if (!thead || !tbody) return;

  const provider = getCurrentProvider();
  const quotaNames = State.cyclesQuotaNames;
  const usePercent = provider === 'anthropic' || provider === 'copilot' || provider === 'codex' || provider === 'antigravity' || provider === 'minimax' || provider === 'gemini' || provider === 'openrouter';
  const deltaUsesPercent = usePercent && provider !== 'minimax';
  const isLoggingHistory = State.isLoggingHistory === true;

  // Build dynamic header
  let headerHtml;
  if (isLoggingHistory) {
    // Logging history: simpler header with # and Time
    headerHtml = `
      <tr>
        <th data-sort-key="id" role="button" tabindex="0"># <span class="sort-arrow"></span></th>
        <th data-sort-key="start" role="button" tabindex="0">Time <span class="sort-arrow"></span></th>`;
  } else {
    // Cycle-based: full header with Start, End, Duration, Total Δ
    headerHtml = `
      <tr>
        <th data-sort-key="id" role="button" tabindex="0">Cycle <span class="sort-arrow"></span></th>
        <th data-sort-key="start" role="button" tabindex="0">Start <span class="sort-arrow"></span></th>
        <th data-sort-key="end" role="button" tabindex="0">End <span class="sort-arrow"></span></th>
        <th data-sort-key="duration" role="button" tabindex="0">Duration <span class="sort-arrow"></span></th>
        <th data-sort-key="totalDelta" role="button" tabindex="0">Total Δ${deltaUsesPercent ? ' %' : ''} <span class="sort-arrow"></span></th>`;
  }

  quotaNames.forEach(qn => {
    const displayName = getQuotaDisplayName(qn, provider);
    const suffix = usePercent ? ' %' : '';
    headerHtml += `<th data-sort-key="cq_${qn}" role="button" tabindex="0">${displayName}${suffix} <span class="sort-arrow"></span></th>`;
  });
  headerHtml += '</tr>';
  thead.innerHTML = headerHtml;

  // Attach sort handlers to new headers and restore sort indicator
  thead.querySelectorAll('th[data-sort-key]').forEach(th => {
    th.addEventListener('click', () => handleTableSort('cycles', th));
    th.addEventListener('keydown', e => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); handleTableSort('cycles', th); } });
    if (State.cyclesSort.key === th.dataset.sortKey) {
      th.setAttribute('data-sort-dir', State.cyclesSort.dir);
    }
  });

  // Filter by time range
  const now = Date.now();
  const rangeMs = State.cyclesRange;
  const cutoff = now - rangeMs;

  let data = State.allCyclesData.filter(c =>
    new Date(c.cycleStart).getTime() >= cutoff || !c.cycleEnd
  );

  // Apply bucket aggregation (not for logging history - already point-in-time)
  const bucketMinutes = State.cyclesBucket || 1;
  if (!isLoggingHistory) {
    // Collapse duplicate active rows for cycle-based view
    const activeRows = data.filter(c => !c.cycleEnd);
    if (activeRows.length > 1) {
      const startTimes = activeRows.map(r => new Date(r.cycleStart).getTime()).filter(Number.isFinite);
      const earliestStart = startTimes.length ? Math.min(...startTimes) : Date.now();
      const collapsedByQuota = new Map();
      for (const row of activeRows) {
        for (const cq of (row.crossQuotas || [])) {
          const existing = collapsedByQuota.get(cq.name);
          if (!existing || (cq.percent ?? -1) > (existing.percent ?? -1)) {
            collapsedByQuota.set(cq.name, { ...cq });
          }
        }
      }
      const collapsedActive = {
        cycleId: 'active',
        cycleStart: new Date(earliestStart).toISOString(),
        cycleEnd: null,
        totalDelta: activeRows.reduce((sum, r) => sum + (typeof r.totalDelta === 'number' ? r.totalDelta : 0), 0),
        crossQuotas: [...collapsedByQuota.values()],
      };
      data = [collapsedActive, ...data.filter(c => c.cycleEnd)];
    }
    data = aggregateCyclesByBucket(data, bucketMinutes);
  } else if (bucketMinutes > 1) {
    // For logging history with bucket > 1, aggregate snapshots into time buckets
    data = aggregateCyclesByBucket(data, bucketMinutes);
  }

  // Sort
  if (State.cyclesSort.key) {
    const { key, dir } = State.cyclesSort;
    data.sort((a, b) => {
      let va, vb;
      if (key === 'id') { va = a.cycleId; vb = b.cycleId; }
      else if (key === 'start') { va = a.cycleStart; vb = b.cycleStart; }
      else if (key === 'end') { va = a.cycleEnd || ''; vb = b.cycleEnd || ''; }
      else if (key === 'duration') {
        va = a.cycleEnd ? new Date(a.cycleEnd) - new Date(a.cycleStart) : 0;
        vb = b.cycleEnd ? new Date(b.cycleEnd) - new Date(b.cycleStart) : 0;
      }
      else if (key === 'totalDelta') { va = a.totalDelta; vb = b.totalDelta; }
      else if (key.startsWith('cq_')) {
        const qn = key.slice(3);
        va = getCrossQuotaPercent(a, qn);
        vb = getCrossQuotaPercent(b, qn);
      }
      else { va = 0; vb = 0; }
      if (va < vb) return dir === 'asc' ? -1 : 1;
      if (va > vb) return dir === 'asc' ? 1 : -1;
      return 0;
    });
  }

  // Pagination
  const pageSize = State.cyclesPageSize || 10;
  const totalRows = data.length;
  const totalPages = pageSize > 0 ? Math.ceil(totalRows / pageSize) : 1;
  if (State.cyclesPage > totalPages) State.cyclesPage = totalPages || 1;
  const page = State.cyclesPage;
  const startIdx = pageSize > 0 ? (page - 1) * pageSize : 0;
  const pageData = pageSize > 0 ? data.slice(startIdx, startIdx + pageSize) : data;

  // Format value with rate: "45.2% [⚡5.2%/hr]"
  const fmtCyclesWithRate = (val, durationHrs, suffix) => {
    if (typeof val !== 'number') return '--';
    const valStr = val.toFixed(1) + suffix;
    if (durationHrs > 0) {
      const rate = val / durationHrs;
      return `${valStr} <span class="rate-indicator">[⚡${rate.toFixed(1)}${suffix}/hr]</span>`;
    }
    return valStr;
  };

  const colCount = isLoggingHistory ? (2 + quotaNames.length) : (5 + quotaNames.length);

  if (pageData.length === 0) {
    const emptyMsg = isLoggingHistory ? 'No logging data in this range.' : 'No polling data in this range.';
    tbody.innerHTML = `<tr><td colspan="${colCount}" class="empty-state">${emptyMsg}</td></tr>`;
  } else {
    tbody.innerHTML = pageData.map(row => {
      const start = row.cycleStart || null;
      const end = row.cycleEnd || null;
      const startDate = start ? new Date(start) : null;
      const endDate = end ? new Date(end) : null;
      const suffix = deltaUsesPercent ? '%' : '';

      let html;
      if (isLoggingHistory) {
        // Logging history: simpler row with # and Time
        html = `<tr>
          <td>${row.cycleId}</td>
          <td>${start ? formatDateTime(start) : '--'}</td>`;
      } else {
        // Cycle view: full row with Start, End, Duration, Total Δ
        // Calculate duration: for buckets > 1, use bucket window; otherwise use actual span
        let durationHrs, duration;
        if (bucketMinutes > 1) {
          durationHrs = bucketMinutes / 60;
          duration = bucketMinutes >= 60 ? `${bucketMinutes / 60}h` : `${bucketMinutes}m`;
        } else {
          const durationMs = (startDate && endDate) ? endDate - startDate : 0;
          durationHrs = durationMs / 3600000;
          duration = durationMs > 0 ? formatDuration(Math.floor(durationMs / 1000)) : '--';
        }

        // For active cycles (no end, or cycleId is -1 or 'active'), show "Active" badge
        const isActive = !end || row.cycleId === -1 || row.cycleId === 'active';
        let cycleLabel;
        if (bucketMinutes > 1) {
          cycleLabel = start ? formatDateTime(start) : '--';
        } else if (isActive) {
          cycleLabel = '<span class="badge">Active</span>';
        } else {
          cycleLabel = `${row.cycleId}`;
        }

        html = `<tr>
          <td>${cycleLabel}</td>
          <td>${start ? formatDateTime(start) : '--'}</td>
          <td>${end ? formatDateTime(end) : '<span class="badge">Active</span>'}</td>
          <td>${duration}</td>
          <td>${fmtCyclesWithRate(row.totalDelta, durationHrs, suffix)}</td>`;
      }

      quotaNames.forEach(qn => {
        const pct = getCrossQuotaPercent(row, qn);
        const delta = getCrossQuotaDelta(row, qn);
        const cls = getThresholdClass(pct);
        let cellVal = '--';
        if (pct >= 0) {
          if (provider === 'minimax') {
            const cq = getCrossQuotaValue(row, qn);
            const used = Number(cq?.value || 0);
            const limit = Number(cq?.limit || 0);
            const percentText = `${pct.toFixed(1)}%`;
            const deltaText = delta == null ? '' : ` <span class="delta">(${delta >= 0 ? '+' : ''}${delta.toFixed(1)}%)</span>`;
            cellVal = limit > 0
              ? `${formatNumber(used)} / ${formatNumber(limit)} <span class="delta">(${percentText})</span>${deltaText}`
              : `${formatNumber(used)} <span class="delta">(${percentText})</span>${deltaText}`;
          } else if (usePercent) {
            cellVal = fmtPctWithDelta(pct, delta);
          } else {
            const cq = getCrossQuotaValue(row, qn);
            cellVal = cq ? formatNumber(cq.value) : pct.toFixed(1) + '%';
          }
        }
        html += `<td class="${cls}">${cellVal}</td>`;
      });

      html += '</tr>';
      return html;
    }).join('');
  }

  // Info
  if (infoEl) {
    const showStart = totalRows > 0 ? startIdx + 1 : 0;
    const showEnd = pageSize > 0 ? Math.min(startIdx + pageSize, totalRows) : totalRows;
    infoEl.textContent = `Showing ${showStart}-${showEnd} of ${totalRows}`;
  }

  // Pagination buttons
  if (paginationEl) {
    paginationEl.innerHTML = renderPagination('cycles', page, totalPages);
  }
}

// ── Sessions Table (client-side search/sort/paginate + expandable rows) ──

async function fetchSessions() {
  if (!shouldShowHistoryTables()) return;
  const requestProvider = getCurrentProvider();
  const requestAccount = requestProvider === 'codex' ? State.codexAccount : null;
  const requestSeq = (State.sessionsRequestSeq || 0) + 1;
  State.sessionsRequestSeq = requestSeq;

  try {
    const res = await authFetch(`${API_BASE}/api/sessions?${providerParam()}`);
    if (!res.ok) throw new Error('Failed to fetch sessions');
    const data = await res.json();
    if (State.sessionsRequestSeq !== requestSeq) return;
    if (getCurrentProvider() !== requestProvider) return;
    if (requestProvider === 'codex' && State.codexAccount !== requestAccount) return;

    const provider = requestProvider;

    if (provider === 'both') {
      // "both" response: { synthetic: [...], zai: [...], anthropic: [...], codex: [...] }
      let merged = [];
      if (data.synthetic) merged = merged.concat(data.synthetic.map(s => ({ ...s, _provider: 'Syn' })));
      if (data.zai) merged = merged.concat(data.zai.map(s => ({ ...s, _provider: 'Z.ai' })));
      if (data.anthropic) merged = merged.concat(data.anthropic.map(s => ({ ...s, _provider: 'Anth' })));
      if (data.minimax) merged = merged.concat(data.minimax.map(s => ({ ...s, _provider: 'MiniMax' })));
      if (data.gemini) merged = merged.concat(data.gemini.map(s => ({ ...s, _provider: 'Gemini' })));
      if (data.codex) merged = merged.concat(data.codex.map(s => ({ ...s, _provider: 'Codex' })));
      merged.sort((a, b) => new Date(b.startedAt).getTime() - new Date(a.startedAt).getTime());
      State.allSessionsData = merged;
    } else {
      State.allSessionsData = data;
    }
    State.sessionsPage = 1;
    renderSessionsTable();
    // Update Anthropic session headers with actual quota names after render
    if (getCurrentProvider() === 'anthropic') {
      updateAnthropicSessionHeaders();
    } else if (getCurrentProvider() === 'codex') {
      updateCodexSessionHeaders();
    }
  } catch (err) {
    // sessions fetch error - table shows empty state
  }
}

function getSessionComputedFields(session) {
  const start = new Date(session.startedAt);
  const end = session.endedAt ? new Date(session.endedAt) : new Date();
  const durationMins = Math.round((end - start) / 60000);
  const totalConsumption = (session.maxSubRequests || 0) + (session.maxSearchRequests || 0) + (session.maxToolRequests || 0);
  const durationHours = durationMins / 60;
  const consumptionRate = durationHours > 0 ? totalConsumption / durationHours : 0;
  const snapshotsPerMin = durationMins > 0 ? (session.snapshotCount || 0) / durationMins : 0;
  return {
    start, end, durationMins,
    durationStr: formatDurationMins(durationMins),
    isActive: !session.endedAt,
    totalConsumption, consumptionRate, snapshotsPerMin, durationHours
  };
}

function renderSessionsTable() {
  const tbody = document.getElementById('sessions-tbody');
  const infoEl = document.getElementById('sessions-info');
  const paginationEl = document.getElementById('sessions-pagination');
  if (!tbody) return;

  const provider = getCurrentProvider();
  const isBoth = provider === 'both';
  const isZai = provider === 'zai';
  const isAnthropic = provider === 'anthropic';
  const isCodex = provider === 'codex';
  const isMiniMax = provider === 'minimax';
  const isAntigravity = provider === 'antigravity';
  const isGemini = provider === 'gemini';
  const colSpan = isBoth ? 6 : isZai ? 5 : isCodex ? 6 : isMiniMax ? 6 : isAntigravity ? 7 : isGemini ? 7 : 7;

  let data = State.allSessionsData.map((s, i) => ({ ...s, _computed: getSessionComputedFields(s), _index: i }));

  // Sort
  if (State.sessionsSort.key) {
    const dir = State.sessionsSort.dir === 'asc' ? 1 : -1;
    data.sort((a, b) => {
      let va, vb;
      switch (State.sessionsSort.key) {
        case 'id': va = a.id; vb = b.id; break;
        case 'start': va = a._computed.start.getTime(); vb = b._computed.start.getTime(); break;
        case 'end': va = a._computed.end.getTime(); vb = b._computed.end.getTime(); break;
        case 'duration': va = a._computed.durationMins; vb = b._computed.durationMins; break;
        case 'snapshots': va = a.snapshotCount || 0; vb = b.snapshotCount || 0; break;
        case 'sub': va = a.maxSubRequests; vb = b.maxSubRequests; break;
        case 'search': va = a.maxSearchRequests; vb = b.maxSearchRequests; break;
        case 'tool': va = a.maxToolRequests; vb = b.maxToolRequests; break;
        case 'provider': va = a._provider || ''; vb = b._provider || ''; break;
        default: va = 0; vb = 0;
      }
      return va > vb ? dir : va < vb ? -dir : 0;
    });
  }

  const total = data.length;
  const pageSize = State.sessionsPageSize;
  const totalPages = pageSize > 0 ? Math.max(1, Math.ceil(total / pageSize)) : 1;
  if (State.sessionsPage > totalPages) State.sessionsPage = totalPages;
  const page = State.sessionsPage;
  const startIdx = pageSize > 0 ? (page - 1) * pageSize : 0;
  const pageData = pageSize > 0 ? data.slice(startIdx, startIdx + pageSize) : data;

  if (infoEl) {
    if (total === 0) {
      infoEl.textContent = 'No results';
    } else {
      infoEl.textContent = `Showing ${startIdx + 1}-${Math.min(startIdx + pageData.length, total)} of ${total}`;
    }
  }

  if (total === 0) {
    tbody.innerHTML = `<tr><td colspan="${colSpan}" class="empty-state">No sessions recorded yet.</td></tr>`;
  } else if (isBoth) {
    // Both: show Provider, Session, Start, End, Duration, Snapshots
    tbody.innerHTML = pageData.map(session => {
      const c = session._computed;
      return `<tr class="session-row">
        <td><span class="badge">${session._provider || '-'}</span></td>
        <td>${session.id.slice(0, 8)}${c.isActive ? ' <span class="badge">Active</span>' : ''}</td>
        <td>${c.start.toLocaleString('en-US', { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })}</td>
        <td>${session.endedAt ? new Date(session.endedAt).toLocaleString('en-US', { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' }) : 'Active'}</td>
        <td>${c.durationStr}</td>
        <td>${session.snapshotCount || 0}</td>
      </tr>`;
    }).join('');
  } else if (isZai) {
    // Z.ai: show Session, Start, End, Duration, Snapshots
    tbody.innerHTML = pageData.map(session => {
      const c = session._computed;
      const isExpanded = State.expandedSessionId === session.id;
      const mainRow = `<tr class="session-row" role="button" tabindex="0" data-session-id="${session.id}">
        <td>${session.id.slice(0, 8)}${c.isActive ? ' <span class="badge">Active</span>' : ''}</td>
        <td>${c.start.toLocaleString('en-US', { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })}</td>
        <td>${session.endedAt ? new Date(session.endedAt).toLocaleString('en-US', { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' }) : 'Active'}</td>
        <td>${c.durationStr}</td>
        <td>${session.snapshotCount || 0}</td>
      </tr>`;
      const detailRow = `<tr class="session-detail-row ${isExpanded ? 'expanded' : ''}" data-detail-for="${session.id}">
        <td colspan="${colSpan}">
          <div class="session-detail-content">
            <div class="session-detail-grid">
              <div class="detail-item">
                <span class="detail-label">Poll Interval</span>
                <span class="detail-value">${session.pollInterval ? Math.round(session.pollInterval / 1000) : '-'}s</span>
              </div>
              <div class="detail-item">
                <span class="detail-label">Snapshots</span>
                <span class="detail-value">${session.snapshotCount || 0}</span>
              </div>
              <div class="detail-item">
                <span class="detail-label">Snapshots/min</span>
                <span class="detail-value">${c.snapshotsPerMin.toFixed(2)}</span>
              </div>
              <div class="detail-item">
                <span class="detail-label">Duration</span>
                <span class="detail-value">${c.durationStr}</span>
              </div>
            </div>
          </div>
        </td>
      </tr>`;
      return mainRow + detailRow;
    }).join('');
  } else if (isAnthropic) {
    // Anthropic: show Session, Start, End, Duration, + dynamic quota columns (max 3)
    // Labels come from State.anthropicSessionQuotas (populated on first current-data fetch)
    const lbl0 = getAnthropicSessionLabel(0);
    const lbl1 = getAnthropicSessionLabel(1);
    const lbl2 = getAnthropicSessionLabel(2);
    tbody.innerHTML = pageData.map(session => {
      const c = session._computed;
      const isExpanded = State.expandedSessionId === session.id;
      const fmtPct = (v) => v != null ? v.toFixed(1) + '%' : '-';
      const fmtDelta = (start, end) => {
        const d = (end || 0) - (start || 0);
        return d >= 0 ? `+${d.toFixed(1)}%` : `${d.toFixed(1)}%`;
      };
      // Format with delta inline: "45.2% (+12.3%)"
      const fmtWithDelta = (start, max) => {
        const pct = max != null ? max.toFixed(1) + '%' : '-';
        if (start == null || max == null) return pct;
        const d = max - start;
        const delta = d >= 0 ? `+${d.toFixed(1)}%` : `${d.toFixed(1)}%`;
        return `${pct} <span class="delta">(${delta})</span>`;
      };
      const mainRow = `<tr class="session-row" role="button" tabindex="0" data-session-id="${session.id}">
        <td>${session.id.slice(0, 8)}${c.isActive ? ' <span class="badge">Active</span>' : ''}</td>
        <td>${c.start.toLocaleString('en-US', { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })}</td>
        <td>${session.endedAt ? new Date(session.endedAt).toLocaleString('en-US', { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' }) : 'Active'}</td>
        <td>${c.durationStr}</td>
        <td>${fmtWithDelta(session.startSubRequests, session.maxSubRequests)}</td>
        <td>${fmtWithDelta(session.startSearchRequests, session.maxSearchRequests)}</td>
        <td>${fmtWithDelta(session.startToolRequests, session.maxToolRequests)}</td>
      </tr>`;
      const detailRow = `<tr class="session-detail-row ${isExpanded ? 'expanded' : ''}" data-detail-for="${session.id}">
        <td colspan="${colSpan}">
          <div class="session-detail-content">
            <div class="session-detail-grid">
              <div class="detail-item">
                <span class="detail-label">${lbl0}</span>
                <span class="detail-value">${fmtPct(session.startSubRequests)} &rarr; ${fmtPct(session.maxSubRequests)} (${fmtDelta(session.startSubRequests, session.maxSubRequests)})</span>
              </div>
              <div class="detail-item">
                <span class="detail-label">${lbl1}</span>
                <span class="detail-value">${fmtPct(session.startSearchRequests)} &rarr; ${fmtPct(session.maxSearchRequests)} (${fmtDelta(session.startSearchRequests, session.maxSearchRequests)})</span>
              </div>
              <div class="detail-item">
                <span class="detail-label">${lbl2}</span>
                <span class="detail-value">${fmtPct(session.startToolRequests)} &rarr; ${fmtPct(session.maxToolRequests)} (${fmtDelta(session.startToolRequests, session.maxToolRequests)})</span>
              </div>
              <div class="detail-item">
                <span class="detail-label">Snapshots</span>
                <span class="detail-value">${session.snapshotCount || 0}</span>
              </div>
              <div class="detail-item">
                <span class="detail-label">Snapshots/min</span>
                <span class="detail-value">${c.snapshotsPerMin.toFixed(2)}</span>
              </div>
              <div class="detail-item">
                <span class="detail-label">Duration</span>
                <span class="detail-value">${c.durationStr}</span>
              </div>
            </div>
          </div>
        </td>
      </tr>`;
      return mainRow + detailRow;
    }).join('');
  } else if (isCodex) {
    // Codex: show Session, Start, End, Duration, 2 dynamic quota columns
    const codexLabel0 = getCodexSessionLabel(0);
    const codexLabel1 = getCodexSessionLabel(1);
    tbody.innerHTML = pageData.map(session => {
      const c = session._computed;
      const isExpanded = State.expandedSessionId === session.id;
      const fmtPct = (v) => v != null ? v.toFixed(1) + '%' : '-';
      const fmtDelta = (start, end) => {
        const d = (end || 0) - (start || 0);
        return d >= 0 ? `+${d.toFixed(1)}%` : `${d.toFixed(1)}%`;
      };
      const fmtWithDelta = (start, max) => {
        const pct = max != null ? max.toFixed(1) + '%' : '-';
        if (start == null || max == null) return pct;
        const d = max - start;
        const delta = d >= 0 ? `+${d.toFixed(1)}%` : `${d.toFixed(1)}%`;
        return `${pct} <span class="delta">(${delta})</span>`;
      };
      const mainRow = `<tr class="session-row" role="button" tabindex="0" data-session-id="${session.id}">
        <td>${session.id.slice(0, 8)}${c.isActive ? ' <span class="badge">Active</span>' : ''}</td>
        <td>${c.start.toLocaleString('en-US', { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })}</td>
        <td>${session.endedAt ? new Date(session.endedAt).toLocaleString('en-US', { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' }) : 'Active'}</td>
        <td>${c.durationStr}</td>
        <td>${fmtWithDelta(session.startSubRequests, session.maxSubRequests)}</td>
        <td>${fmtWithDelta(session.startSearchRequests, session.maxSearchRequests)}</td>
      </tr>`;
      const detailRow = `<tr class="session-detail-row ${isExpanded ? 'expanded' : ''}" data-detail-for="${session.id}">
        <td colspan="${colSpan}">
          <div class="session-detail-content">
            <div class="session-detail-grid">
              <div class="detail-item">
                <span class="detail-label">${codexLabel0}</span>
                <span class="detail-value">${fmtPct(session.startSubRequests)} &rarr; ${fmtPct(session.maxSubRequests)} (${fmtDelta(session.startSubRequests, session.maxSubRequests)})</span>
              </div>
              <div class="detail-item">
                <span class="detail-label">${codexLabel1}</span>
                <span class="detail-value">${fmtPct(session.startSearchRequests)} &rarr; ${fmtPct(session.maxSearchRequests)} (${fmtDelta(session.startSearchRequests, session.maxSearchRequests)})</span>
              </div>
              <div class="detail-item">
                <span class="detail-label">Snapshots</span>
                <span class="detail-value">${session.snapshotCount || 0}</span>
              </div>
              <div class="detail-item">
                <span class="detail-label">Snapshots/min</span>
                <span class="detail-value">${c.snapshotsPerMin.toFixed(2)}</span>
              </div>
              <div class="detail-item">
                <span class="detail-label">Duration</span>
                <span class="detail-value">${c.durationStr}</span>
              </div>
            </div>
          </div>
        </td>
      </tr>`;
      return mainRow + detailRow;
    }).join('');
  } else if (isMiniMax) {
    // Helper to get current MiniMax plan quota total from loaded card data
    function _minimaxQuotaTotal() {
      for (const [key, q] of Object.entries(State.currentQuotas || {})) {
        if (q && q.total > 0 && String(key).startsWith('minimax')) return q.total;
      }
      return 0;
    }
    tbody.innerHTML = pageData.map(session => {
      const c = session._computed;
      const isExpanded = State.expandedSessionId === session.id;
      const startUsed = Number(session.startSubRequests || 0);
      const peakUsed = Number(session.maxSubRequests || 0);
      const sessionDelta = Math.max(0, peakUsed - startUsed);
      const hourlyRate = c.durationHours > 0 ? sessionDelta / c.durationHours : 0;

      const quotaTotal = _minimaxQuotaTotal();
      const peakPct = quotaTotal > 0 ? ((peakUsed / quotaTotal) * 100).toFixed(1) + '%' : '';
      const mainRow = `<tr class="session-row" role="button" tabindex="0" data-session-id="${session.id}">
        <td>${c.start.toLocaleString('en-US', { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })}</td>
        <td>${session.endedAt ? new Date(session.endedAt).toLocaleString('en-US', { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' }) : '<span class="badge badge-active">Active</span>'}</td>
        <td>${c.durationStr}</td>
        <td>${formatNumber(peakUsed)}${peakPct ? ' (' + peakPct + ')' : ''}</td>
        <td>${formatNumber(sessionDelta)}</td>
        <td>${session.snapshotCount || 0}</td>
      </tr>`;

      // Weekly quota data (repurposed fields: searchRequests=weekly, toolRequests=weeklyTotal)
      const weeklyPeak = Number(session.maxSearchRequests || 0);
      const weeklyStart = Number(session.startSearchRequests || 0);
      const weeklyTotal = Number(session.maxToolRequests || 0);
      const weeklyDelta = Math.max(0, weeklyPeak - weeklyStart);
      const weeklyPct = weeklyTotal > 0 ? ((weeklyPeak / weeklyTotal) * 100).toFixed(1) + '%' : '';
      const hasWeekly = weeklyTotal > 0;

      const detailRow = `<tr class="session-detail-row ${isExpanded ? 'expanded' : ''}" data-detail-for="${session.id}">
        <td colspan="${colSpan}">
          <div class="session-detail-content">
            <div class="session-detail-grid">
              <div class="detail-item">
                <span class="detail-label">Quota Pool</span>
                <span class="detail-value">MiniMax Coding Plan</span>
              </div>
              <div class="detail-item">
                <span class="detail-label">Start Used</span>
                <span class="detail-value">${formatNumber(startUsed)}</span>
              </div>
              <div class="detail-item">
                <span class="detail-label">Peak Used</span>
                <span class="detail-value">${formatNumber(peakUsed)}</span>
              </div>
              <div class="detail-item">
                <span class="detail-label">Session Delta</span>
                <span class="detail-value">${formatNumber(sessionDelta)}</span>
              </div>
              <div class="detail-item">
                <span class="detail-label">Burn Rate</span>
                <span class="detail-value">${hourlyRate > 0 ? `${formatNumber(hourlyRate)}/hr` : '0/hr'}</span>
              </div>
              <div class="detail-item">
                <span class="detail-label">Snapshots/min</span>
                <span class="detail-value">${c.snapshotsPerMin.toFixed(2)}</span>
              </div>
              ${hasWeekly ? `
              <div class="detail-item" style="grid-column: 1 / -1; border-top: 1px solid var(--border-default, #e0e0e0); padding-top: 8px; margin-top: 4px;">
                <span class="detail-label" style="font-weight: 600;">Weekly Quota</span>
                <span class="detail-value">${formatNumber(weeklyPeak)} / ${formatNumber(weeklyTotal)}${weeklyPct ? ' (' + weeklyPct + ')' : ''}</span>
              </div>
              <div class="detail-item">
                <span class="detail-label">Weekly Start</span>
                <span class="detail-value">${formatNumber(weeklyStart)}</span>
              </div>
              <div class="detail-item">
                <span class="detail-label">Weekly Delta</span>
                <span class="detail-value">${formatNumber(weeklyDelta)}</span>
              </div>` : ''}
            </div>
          </div>
        </td>
      </tr>`;

      return mainRow + detailRow;
    }).join('');
  } else if (provider === 'antigravity') {
    // Antigravity: show Session, Start, End, Duration, and grouped quota usage
    tbody.innerHTML = pageData.map(session => {
      const c = session._computed;
      const isExpanded = State.expandedSessionId === session.id;
      const fmtPct = (v) => v != null ? v.toFixed(1) + '%' : '-';
      const fmtDelta = (start, end) => {
        const d = (end || 0) - (start || 0);
        return d >= 0 ? `+${d.toFixed(1)}%` : `${d.toFixed(1)}%`;
      };
      const fmtWithDelta = (start, max) => {
        const pct = max != null ? max.toFixed(1) + '%' : '-';
        if (start == null || max == null) return pct;
        const d = max - start;
        const delta = d >= 0 ? `+${d.toFixed(1)}%` : `${d.toFixed(1)}%`;
        return `${pct} <span class="delta">(${delta})</span>`;
      };

      const mainRow = `<tr class="session-row" role="button" tabindex="0" data-session-id="${session.id}">
        <td>${session.id.slice(0, 8)}${c.isActive ? ' <span class="badge">Active</span>' : ''}</td>
        <td>${c.start.toLocaleString('en-US', { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })}</td>
        <td>${session.endedAt ? new Date(session.endedAt).toLocaleString('en-US', { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' }) : 'Active'}</td>
        <td>${c.durationStr}</td>
        <td>${fmtWithDelta(session.startSubRequests, session.maxSubRequests)}</td>
        <td>${fmtWithDelta(session.startSearchRequests, session.maxSearchRequests)}</td>
        <td>${fmtWithDelta(session.startToolRequests, session.maxToolRequests)}</td>
      </tr>`;

      const detailRow = `<tr class="session-detail-row ${isExpanded ? 'expanded' : ''}" data-detail-for="${session.id}">
        <td colspan="7">
          <div class="session-detail-content">
            <div class="session-detail-grid">
              <div class="detail-item">
                <span class="detail-label">Claude + GPT Quota</span>
                <span class="detail-value">${fmtPct(session.startSubRequests)} &rarr; ${fmtPct(session.maxSubRequests)} (${fmtDelta(session.startSubRequests, session.maxSubRequests)})</span>
              </div>
              <div class="detail-item">
                <span class="detail-label">Gemini Pro Quota</span>
                <span class="detail-value">${fmtPct(session.startSearchRequests)} &rarr; ${fmtPct(session.maxSearchRequests)} (${fmtDelta(session.startSearchRequests, session.maxSearchRequests)})</span>
              </div>
              <div class="detail-item">
                <span class="detail-label">Gemini Flash Quota</span>
                <span class="detail-value">${fmtPct(session.startToolRequests)} &rarr; ${fmtPct(session.maxToolRequests)} (${fmtDelta(session.startToolRequests, session.maxToolRequests)})</span>
              </div>
              <div class="detail-item">
                <span class="detail-label">Snapshots</span>
                <span class="detail-value">${session.snapshotCount || 0}</span>
              </div>
              <div class="detail-item">
                <span class="detail-label">Snapshots/min</span>
                <span class="detail-value">${c.snapshotsPerMin.toFixed(2)}</span>
              </div>
              <div class="detail-item">
                <span class="detail-label">Duration</span>
                <span class="detail-value">${c.durationStr}</span>
              </div>
            </div>
          </div>
        </td>
      </tr>`;
      return mainRow + detailRow;
    }).join('');
  } else if (isGemini) {
    // Gemini: show Session, Start, End, Duration, and per-model usage percentages
    tbody.innerHTML = pageData.map(session => {
      const c = session._computed;
      const isExpanded = State.expandedSessionId === session.id;
      const fmtPct = (v) => v != null ? v.toFixed(1) + '%' : '-';
      const fmtDelta = (start, end) => {
        const d = (end || 0) - (start || 0);
        return d >= 0 ? `+${d.toFixed(1)}%` : `${d.toFixed(1)}%`;
      };
      const fmtWithDelta = (start, max) => {
        const pct = max != null ? max.toFixed(1) + '%' : '-';
        if (start == null || max == null) return pct;
        const d = max - start;
        const delta = d >= 0 ? `+${d.toFixed(1)}%` : `${d.toFixed(1)}%`;
        return `${pct} <span class="delta">(${delta})</span>`;
      };

      const mainRow = `<tr class="session-row" role="button" tabindex="0" data-session-id="${session.id}">
        <td>${session.id.slice(0, 8)}${c.isActive ? ' <span class="badge">Active</span>' : ''}</td>
        <td>${c.start.toLocaleString('en-US', { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })}</td>
        <td>${session.endedAt ? new Date(session.endedAt).toLocaleString('en-US', { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' }) : 'Active'}</td>
        <td>${c.durationStr}</td>
        <td>${fmtWithDelta(session.startSubRequests, session.maxSubRequests)}</td>
        <td>${fmtWithDelta(session.startSearchRequests, session.maxSearchRequests)}</td>
        <td>${fmtWithDelta(session.startToolRequests, session.maxToolRequests)}</td>
      </tr>`;

      const detailRow = `<tr class="session-detail-row ${isExpanded ? 'expanded' : ''}" data-detail-for="${session.id}">
        <td colspan="7">
          <div class="session-detail-content">
            <div class="session-detail-grid">
              <div class="detail-item">
                <span class="detail-label">Model Quota 1</span>
                <span class="detail-value">${fmtPct(session.startSubRequests)} &rarr; ${fmtPct(session.maxSubRequests)} (${fmtDelta(session.startSubRequests, session.maxSubRequests)})</span>
              </div>
              <div class="detail-item">
                <span class="detail-label">Model Quota 2</span>
                <span class="detail-value">${fmtPct(session.startSearchRequests)} &rarr; ${fmtPct(session.maxSearchRequests)} (${fmtDelta(session.startSearchRequests, session.maxSearchRequests)})</span>
              </div>
              <div class="detail-item">
                <span class="detail-label">Model Quota 3</span>
                <span class="detail-value">${fmtPct(session.startToolRequests)} &rarr; ${fmtPct(session.maxToolRequests)} (${fmtDelta(session.startToolRequests, session.maxToolRequests)})</span>
              </div>
              <div class="detail-item">
                <span class="detail-label">Snapshots</span>
                <span class="detail-value">${session.snapshotCount || 0}</span>
              </div>
              <div class="detail-item">
                <span class="detail-label">Snapshots/min</span>
                <span class="detail-value">${c.snapshotsPerMin.toFixed(2)}</span>
              </div>
              <div class="detail-item">
                <span class="detail-label">Duration</span>
                <span class="detail-value">${c.durationStr}</span>
              </div>
            </div>
          </div>
        </td>
      </tr>`;
      return mainRow + detailRow;
    }).join('');
  } else {
    // Synthetic: show Session, Start, End, Duration, Sub, Search, Tool
    tbody.innerHTML = pageData.map(session => {
      const c = session._computed;
      const isExpanded = State.expandedSessionId === session.id;
      const mainRow = `<tr class="session-row" role="button" tabindex="0" data-session-id="${session.id}">
        <td>${session.id.slice(0, 8)}${c.isActive ? ' <span class="badge">Active</span>' : ''}</td>
        <td>${c.start.toLocaleString('en-US', { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })}</td>
        <td>${session.endedAt ? new Date(session.endedAt).toLocaleString('en-US', { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' }) : 'Active'}</td>
        <td>${c.durationStr}</td>
        <td>${formatNumber(session.maxSubRequests)}</td>
        <td>${formatNumber(session.maxSearchRequests)}</td>
        <td>${formatNumber(session.maxToolRequests)}</td>
      </tr>`;
      const detailRow = `<tr class="session-detail-row ${isExpanded ? 'expanded' : ''}" data-detail-for="${session.id}">
        <td colspan="${colSpan}">
          <div class="session-detail-content">
            <div class="session-detail-grid">
              <div class="detail-item">
                <span class="detail-label">Sub Max</span>
                <span class="detail-value">${formatNumber(session.maxSubRequests)}</span>
              </div>
              <div class="detail-item">
                <span class="detail-label">Search Max</span>
                <span class="detail-value">${formatNumber(session.maxSearchRequests)}</span>
              </div>
              <div class="detail-item">
                <span class="detail-label">Tool Max</span>
                <span class="detail-value">${formatNumber(session.maxToolRequests)}</span>
              </div>
              <div class="detail-item">
                <span class="detail-label">Total Consumption</span>
                <span class="detail-value">${formatNumber(c.totalConsumption)}</span>
              </div>
              <div class="detail-item">
                <span class="detail-label">Rate</span>
                <span class="detail-value">${formatNumber(c.consumptionRate)}/hr</span>
              </div>
              <div class="detail-item">
                <span class="detail-label">Snapshots/min</span>
                <span class="detail-value">${c.snapshotsPerMin.toFixed(2)}</span>
              </div>
              <div class="detail-item">
                <span class="detail-label">Poll Interval</span>
                <span class="detail-value">${session.pollInterval ? Math.round(session.pollInterval / 1000) : '-'}s</span>
              </div>
              <div class="detail-item">
                <span class="detail-label">Snapshots</span>
                <span class="detail-value">${session.snapshotCount || 0}</span>
              </div>
            </div>
          </div>
        </td>
      </tr>`;
      return mainRow + detailRow;
    }).join('');
  }

  // Pagination
  if (paginationEl) {
    paginationEl.innerHTML = (pageSize > 0 && totalPages > 1) ? renderPagination('sessions', page, totalPages) : '';
  }
}

// ── Session Row Expansion ──

function handleSessionRowClick(e) {
  const row = e.target.closest('.session-row');
  if (!row) return;
  const sessionId = row.dataset.sessionId;
  if (State.expandedSessionId === sessionId) {
    State.expandedSessionId = null;
  } else {
    State.expandedSessionId = sessionId;
  }
  // Toggle expansion without full re-render for smoothness
  document.querySelectorAll('.session-detail-row').forEach(dr => {
    dr.classList.toggle('expanded', dr.dataset.detailFor === State.expandedSessionId);
  });
}

// ── Table Sort ──

function handleTableSort(tableId, th) {
  const key = th.dataset.sortKey;
  if (!key) return;

  const table = th.closest('table');
  // Clear other sort indicators in this table
  table.querySelectorAll('th[data-sort-key]').forEach(h => {
    if (h !== th) h.removeAttribute('data-sort-dir');
  });

  const currentDir = th.getAttribute('data-sort-dir');
  const newDir = currentDir === 'asc' ? 'desc' : 'asc';
  th.setAttribute('data-sort-dir', newDir);

  if (tableId === 'cycles') {
    State.cyclesSort = { key, dir: newDir };
    State.cyclesPage = 1;
    renderCyclesTable();
  } else if (tableId === 'sessions') {
    State.sessionsSort = { key, dir: newDir };
    State.sessionsPage = 1;
    renderSessionsTable();
  } else if (tableId === 'overview') {
    State.overviewSort = { key, dir: newDir };
    State.overviewPage = 1;
    renderOverviewTable();
  }
}

// ── KPI Card Modal ──

function openModal(quotaType, providerOverride) {
  const modal = document.getElementById('detail-modal');
  const titleEl = document.getElementById('modal-title');
  const bodyEl = document.getElementById('modal-body');
  if (!modal || !bodyEl) return;

  // In "both" mode with a specific provider override, resolve the correct state key
  const currentProv = getCurrentProvider();
  const effectiveProvider = (currentProv === 'both' && providerOverride) ? providerOverride : currentProv;

  let quotaKey = quotaType;
  if (currentProv === 'both' && providerOverride === 'synthetic' && quotaType === 'toolCalls') {
    quotaKey = 'toolCalls_syn';
  } else if (currentProv === 'both' && providerOverride === 'zai' && quotaType === 'toolCalls') {
    quotaKey = 'toolCalls_zai';
  }

  const data = State.currentQuotas[quotaKey];
  if (!data) return;

  const zaiQuotaNames = { tokensLimit: 'Tokens Limit', timeLimit: 'Time Limit', toolCalls: 'Tool Calls' };
  const names = effectiveProvider === 'zai' ? zaiQuotaNames : quotaNames;
  titleEl.textContent = names[quotaType] || quotaType;

  const statusCfg = statusConfig[data.status] || statusConfig.healthy;
  const timeLeft = formatDuration(data.timeUntilResetSeconds);
  const pctUsed = data.percent.toFixed(1);
  const remaining = data.limit - data.usage;

  bodyEl.innerHTML = `
    <div class="modal-kpi-row">
      <div class="modal-kpi">
        <div class="modal-kpi-value">${pctUsed}%</div>
        <div class="modal-kpi-label">Usage</div>
      </div>
      <div class="modal-kpi">
        <div class="modal-kpi-value">${formatNumber(data.usage)}</div>
        <div class="modal-kpi-label">Used</div>
      </div>
      <div class="modal-kpi">
        <div class="modal-kpi-value">${formatNumber(remaining)}</div>
        <div class="modal-kpi-label">Remaining</div>
      </div>
      <div class="modal-kpi">
        <div class="modal-kpi-value">${timeLeft}</div>
        <div class="modal-kpi-label">Until Reset</div>
      </div>
    </div>
    <h3 class="modal-section-title">Usage History</h3>
    <div class="modal-chart-container">
      <canvas id="modal-chart"></canvas>
    </div>
    ${data.insight ? `<h3 class="modal-section-title">Insight</h3><div class="modal-insight">${data.insight}</div>` : ''}
    <h3 class="modal-section-title">Recent Cycles</h3>
    <div class="table-wrapper">
      <table class="data-table" id="modal-cycles-table">
        <thead><tr><th>Cycle</th><th>Duration</th><th>Peak</th><th>Total</th><th>Rate</th></tr></thead>
        <tbody id="modal-cycles-tbody"><tr><td colspan="5" class="empty-state">Loading...</td></tr></tbody>
      </table>
    </div>
  `;

  modal.hidden = false;
  // Trap focus: focus the close button
  document.getElementById('modal-close').focus();

  // Fetch modal-specific data (use effectiveProvider to avoid "both" API responses)
  loadModalChart(quotaType, effectiveProvider);
  loadModalCycles(quotaType, effectiveProvider);
}

async function loadModalChart(quotaType, effectiveProvider) {
  const ctx = document.getElementById('modal-chart');
  if (!ctx || typeof Chart === 'undefined') return;

  // Destroy previous modal chart
  if (State.modalChart) {
    State.modalChart.destroy();
    State.modalChart = null;
  }

  const range = State.currentRange || '6h';
  const rangeKey = range.toLowerCase();
  const timeUnit = ['7d', '30d', '15d'].includes(rangeKey) ? 'day' : 'hour';

  const provider = effectiveProvider || getCurrentProvider();
  try {
    const res = await authFetch(`${API_BASE}/api/history?range=${range}&provider=${provider}`);
    if (!res.ok) return;
    const data = await res.json();
    if (!Array.isArray(data) || data.length === 0) return;
    const historyRows = data;
    let datasetKey;
    if (provider === 'zai') {
      datasetKey = quotaType === 'tokensLimit' ? 'tokensPercent' : quotaType === 'toolCalls' ? 'toolCallsPercent' : 'timePercent';
    } else {
      datasetKey = quotaType === 'subscription' ? 'subscriptionPercent' : quotaType === 'search' ? 'searchPercent' : 'toolCallsPercent';
    }
    const style = getComputedStyle(document.documentElement);
    const colorMap = { subscription: style.getPropertyValue('--chart-subscription').trim() || '#0D9488', search: style.getPropertyValue('--chart-search').trim() || '#F59E0B', toolCalls: style.getPropertyValue('--chart-toolcalls').trim() || '#3B82F6', tokensLimit: style.getPropertyValue('--chart-subscription').trim() || '#0D9488', timeLimit: style.getPropertyValue('--chart-search').trim() || '#F59E0B' };
    const bgMap = { subscription: 'rgba(13,148,136,0.08)', search: 'rgba(245,158,11,0.08)', toolCalls: 'rgba(59,130,246,0.08)', tokensLimit: 'rgba(13,148,136,0.08)', timeLimit: 'rgba(245,158,11,0.08)' };

    const colors = getThemeColors();
    const rawData = historyRows.map(d => ({ x: new Date(d.capturedAt), y: d[datasetKey] }));
    const processed = processDataWithGaps(rawData, range);
    const maxVal = Math.max(...historyRows.map(d => d[datasetKey]), 0);

    // Dynamic Y-axis: if max is 0 or very low, show up to 10%
    // Otherwise add 20% padding, rounded to nearest 5
    let yMax;
    if (maxVal <= 0) {
      yMax = 10;
    } else if (maxVal < 5) {
      yMax = 10;
    } else {
      yMax = Math.min(Math.max(Math.ceil((maxVal * 1.2) / 5) * 5, 10), 100);
    }

    State.modalChart = new Chart(ctx, {
      type: 'line',
      data: {
        datasets: [{
          label: (provider === 'zai' ? { tokensLimit: 'Tokens Limit', timeLimit: 'Time Limit', toolCalls: 'Tool Calls' } : quotaNames)[quotaType] || quotaType,
          data: processed.data,
          borderColor: colorMap[quotaType] || '#3B82F6',
          backgroundColor: bgMap[quotaType] || 'rgba(59,130,246,0.08)',
          fill: true,
          tension: 0.3,
          borderWidth: 2.5,
          pointRadius: processed.pointRadii,
          pointHoverRadius: 5,
          spanGaps: true,
          segment: getSegmentStyle(processed.gapSegments, colorMap[quotaType] || '#3B82F6')
        }]
      },
      options: {
        responsive: true,
        maintainAspectRatio: false,
        plugins: {
          legend: { display: false },
          tooltip: {
            backgroundColor: colors.surfaceContainer,
            titleColor: colors.onSurface,
            bodyColor: colors.text,
            borderColor: colors.outline,
            borderWidth: 1,
            callbacks: { label: c => `${c.parsed.y.toFixed(1)}%` }
          }
        },
        scales: {
          x: { type: 'time', time: { unit: timeUnit, displayFormats: { minute: 'HH:mm', hour: ['7d', '30d', '15d', '24h', '3d'].includes(rangeKey) ? 'MMM d, HH:mm' : 'HH:mm', day: 'MMM d' } }, grid: { color: colors.grid, drawBorder: false }, ticks: { color: colors.text, maxTicksLimit: 6, source: 'auto' } },
          y: { grid: { color: colors.grid, drawBorder: false }, ticks: { color: colors.text, callback: v => v + '%' }, min: 0, max: yMax }
        }
      }
    });
  } catch (err) {
    // modal chart error - non-critical
  }
}

async function loadModalCycles(quotaType, effectiveProvider) {
  const provider = effectiveProvider || getCurrentProvider();
  let apiType;
  if (provider === 'zai') {
    apiType = quotaType === 'tokensLimit' ? 'tokens' : 'time';
  } else {
    apiType = quotaType === 'toolCalls' ? 'toolcall' : quotaType;
  }
  try {
    const accountParam = provider === 'codex' ? codexAccountParam() : provider === 'minimax' ? minimaxAccountParam() : '';
    const res = await authFetch(`${API_BASE}/api/cycles?type=${apiType}&provider=${provider}${accountParam}`);
    if (!res.ok) return;
    const cycles = await res.json();

    const tbody = document.getElementById('modal-cycles-tbody');
    if (!tbody) return;

    const recent = cycles.slice(0, 5);
    if (recent.length === 0) {
      tbody.innerHTML = '<tr><td colspan="5" class="empty-state">No cycles yet.</td></tr>';
      return;
    }

    tbody.innerHTML = recent.map(cycle => {
      const c = getCycleComputedFields(cycle);
      return `<tr>
        <td>#${cycle.id}${c.isActive ? ' <span class="badge">Active</span>' : ''}</td>
        <td>${c.durationStr}</td>
        <td>${formatNumber(cycle.peakRequests)}</td>
        <td>${formatNumber(cycle.totalDelta)}</td>
        <td>${c.durationMins > 0 ? formatNumber(c.rate) + '/hr' : '-'}</td>
      </tr>`;
    }).join('');
  } catch (err) {
    // modal cycles error - non-critical
  }
}

function closeModal() {
  const modal = document.getElementById('detail-modal');
  if (!modal) return;
  modal.hidden = true;
  // Destroy modal chart to free memory
  if (State.modalChart) {
    State.modalChart.destroy();
    State.modalChart = null;
  }
}

// ── Event Setup ──

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

function setupCycleFilters() {
  // Range pills
  const rangePills = document.getElementById('cycle-range-pills');
  if (rangePills) {
    rangePills.addEventListener('click', (e) => {
      const pill = e.target.closest('.filter-pill');
      if (!pill) return;
      rangePills.querySelectorAll('.filter-pill').forEach(p => p.classList.remove('active'));
      pill.classList.add('active');
      State.cyclesRange = parseInt(pill.dataset.range, 10);
      State.cyclesPage = 1;
      fetchCycles(); // Re-fetch with new range
    });
  }

  // Grouping window pills - dynamic based on poll interval
  const bucketPills = document.getElementById('cycles-bucket-pills');
  if (bucketPills) {
    const appEl = document.querySelector('.app[data-poll-interval]');
    const pollSec = parseInt(appEl?.dataset.pollInterval || '300', 10);
    const pollMin = Math.max(1, Math.round(pollSec / 60));
    const multipliers = [1, 3, 6, 12];
    const buckets = multipliers.map(m => m * pollMin);
    // Set default bucket to 1x poll interval
    State.cyclesBucket = buckets[0];
    bucketPills.innerHTML = buckets.map((mins, i) => {
      const label = mins >= 60 ? `${mins / 60}h` : `${mins}m`;
      return `<button class="filter-pill${i === 0 ? ' active' : ''}" data-bucket-minutes="${mins}">${label}</button>`;
    }).join('');

    bucketPills.addEventListener('click', (e) => {
      const pill = e.target.closest('.filter-pill');
      if (!pill) return;
      bucketPills.querySelectorAll('.filter-pill').forEach(p => p.classList.remove('active'));
      pill.classList.add('active');
      const mins = parseInt(pill.dataset.bucketMinutes, 10);
      State.cyclesBucket = Number.isFinite(mins) && mins > 0 ? mins : 1;
      State.cyclesPage = 1;
      renderCyclesTable();
    });
  }
}

function initCollapsibleSections() {
  const storageKey = 'onwatch-collapsed-sections';
  let stored = {};
  try {
    const raw = localStorage.getItem(storageKey);
    if (raw) stored = JSON.parse(raw);
  } catch (e) {
    stored = {};
  }

  const sections = document.querySelectorAll('.section-collapsible');
  sections.forEach((section) => {
    const sectionID = section.id;
    if (!sectionID) return;
    const toggle = section.querySelector(`.section-toggle[data-section-id="${sectionID}"]`);
    if (!toggle) return;

    const text = toggle.querySelector('.section-toggle-text');
    const applyCollapsedState = (collapsed) => {
      section.classList.toggle('section-collapsed', collapsed);
      toggle.setAttribute('aria-expanded', collapsed ? 'false' : 'true');
      if (text) text.textContent = collapsed ? 'Expand' : 'Collapse';
    };

    applyCollapsedState(Boolean(stored[sectionID]));

    toggle.addEventListener('click', () => {
      const collapsed = !section.classList.contains('section-collapsed');
      applyCollapsedState(collapsed);
      stored[sectionID] = collapsed;
      try {
        localStorage.setItem(storageKey, JSON.stringify(stored));
      } catch (e) {
        // silent
      }
    });
  });
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

// ── Cycle Overview ──

function getOverviewCategories() {
  const provider = getCurrentProvider();
  if (provider === 'both') {
    // Merge all categories
    return [
      ...(renewalCategories.anthropic || []),
      ...(renewalCategories.synthetic || []),
      ...(renewalCategories.zai || []),
      ...(renewalCategories.copilot || []),
      ...renewalCategories.codex || [],
      ...(renewalCategories.antigravity || []),
      ...(renewalCategories.minimax || []),
      ...(renewalCategories.openrouter || []),
      ...(renewalCategories.gemini || [])
    ];
  }
  if (provider === 'codex') {
    const codexCategories = renewalCategories.codex || [];
    const visible = new Set((State.codexQuotaNames || []).filter(Boolean));
    if (visible.size > 0) {
      return codexCategories.filter(cat => visible.has(cat.groupBy));
    }
    if (isCodexFreePlan(State.codexPlanType)) {
      return codexCategories.filter(cat => cat.groupBy !== 'five_hour');
    }
    return codexCategories;
  }
  return renewalCategories[provider] || [];
}

async function setupOverviewControls() {
  const pillsContainer = document.getElementById('overview-period-pills');
  if (!pillsContainer) return;

  const categories = getOverviewCategories();
  if (categories.length === 0) {
    pillsContainer.innerHTML = '<span class="filter-label">No categories available</span>';
    State.overviewGroupBy = null;
    return;
  }

  const hasCurrent = categories.some(cat => cat.groupBy === State.overviewGroupBy);
  if (!hasCurrent) {
    State.overviewGroupBy = categories[0].groupBy;
  }

  pillsContainer.innerHTML = categories.map((cat) =>
    `<button class="filter-pill ${cat.groupBy === State.overviewGroupBy ? 'active' : ''}" data-group-by="${cat.groupBy}">${cat.label}</button>`
  ).join('');

  // Click handler for pills
  pillsContainer.onclick = (e) => {
    const pill = e.target.closest('.filter-pill');
    if (!pill) return;
    pillsContainer.querySelectorAll('.filter-pill').forEach(p => p.classList.remove('active'));
    pill.classList.add('active');
    State.overviewGroupBy = pill.dataset.groupBy;
    State.overviewPage = 1;
    fetchCycleOverview();
  };

  // Page size
  const pageSizeEl = document.getElementById('overview-page-size');
  if (pageSizeEl) {
    pageSizeEl.onchange = () => {
      State.overviewPageSize = parseInt(pageSizeEl.value, 10);
      State.overviewPage = 1;
      renderOverviewTable();
    };
  }
}

function syncCodexOverviewControls() {
  if (getCurrentProvider() !== 'codex') return;
  setupOverviewControls().then(() => {
    const section = document.querySelector('.cycle-overview-section');
    if (section && !section.hidden) {
      fetchCycleOverview();
    }
  });
}

// Truncate label for pill buttons
function truncateLabel(str, maxLen) {
  if (!str || str.length <= maxLen) return str;
  return str.substring(0, maxLen - 1) + '…';
}

async function fetchCycleOverview() {
  if (!shouldShowHistoryTables()) return;
  const provider = getCurrentProvider();
  const requestProvider = provider;
  const requestAccount = requestProvider === 'codex' ? State.codexAccount : null;
  const categories = getOverviewCategories();
  if (categories.length === 0) return;
  if (!categories.some(cat => cat.groupBy === State.overviewGroupBy)) {
    State.overviewGroupBy = categories[0].groupBy;
  }
  if (!State.overviewGroupBy) return;
  const requestGroupBy = State.overviewGroupBy;
  const requestSeq = (State.overviewRequestSeq || 0) + 1;
  State.overviewRequestSeq = requestSeq;

  let url;
  if (provider === 'both') {
    // Determine which provider this groupBy belongs to
    let effectiveProvider = 'synthetic';
    for (const [prov, cats] of Object.entries(renewalCategories)) {
      if (cats.some(c => c.groupBy === requestGroupBy)) {
        effectiveProvider = prov;
        break;
      }
    }
    const accountParam = effectiveProvider === 'codex' ? codexAccountParam() : effectiveProvider === 'minimax' ? minimaxAccountParam() : '';
    url = `/api/cycle-overview?provider=${effectiveProvider}&groupBy=${requestGroupBy}&limit=50${accountParam}`;
  } else {
    url = `/api/cycle-overview?${providerParam()}&groupBy=${requestGroupBy}&limit=50`;
  }

  try {
    const res = await authFetch(url);
    if (!res.ok) return;
    const data = await res.json();
    if (State.overviewRequestSeq !== requestSeq) return;
    if (getCurrentProvider() !== requestProvider) return;
    if (requestProvider === 'codex' && State.codexAccount !== requestAccount) return;
    if (State.overviewGroupBy !== requestGroupBy) return;

    State.allOverviewData = data.cycles || [];
    State.overviewQuotaNames = data.quotaNames || [];
    renderOverviewTable();
  } catch (e) {
    // cycle overview fetch error - non-critical
  }
}

function renderOverviewTable() {
  const thead = document.getElementById('overview-thead');
  const tbody = document.getElementById('overview-tbody');
  const info = document.getElementById('overview-info');
  const pagination = document.getElementById('overview-pagination');
  if (!thead || !tbody) return;

  const quotaNames = State.overviewQuotaNames;
  const overviewProv = getOverviewProvider();
  const usePercent = overviewProv === 'anthropic' || overviewProv === 'codex' || overviewProv === 'antigravity' || overviewProv === 'minimax' || overviewProv === 'gemini' || overviewProv === 'openrouter';
  const deltaUsesPercent = usePercent && overviewProv !== 'minimax';

  // Build dynamic header
  let headerHtml = `
    <tr>
      <th data-sort-key="id" role="button" tabindex="0">Cycle <span class="sort-arrow"></span></th>
      <th data-sort-key="start" role="button" tabindex="0">Start <span class="sort-arrow"></span></th>
      <th data-sort-key="end" role="button" tabindex="0">End <span class="sort-arrow"></span></th>
      <th data-sort-key="duration" role="button" tabindex="0">Duration <span class="sort-arrow"></span></th>
      <th data-sort-key="totalDelta" role="button" tabindex="0">Total Delta${deltaUsesPercent ? ' %' : ''} <span class="sort-arrow"></span></th>`;

  quotaNames.forEach(qn => {
    const isPrimary = qn === State.overviewGroupBy;
    const displayName = getQuotaDisplayName(qn, overviewProv);
    const suffix = usePercent ? ' %' : '';
    const maxIndicator = isPrimary ? ' Max' : '';
    headerHtml += `<th data-sort-key="cq_${qn}" role="button" tabindex="0" ${isPrimary ? 'class="overview-primary-col"' : ''}>${displayName}${maxIndicator}${suffix} <span class="sort-arrow"></span></th>`;
  });
  headerHtml += '</tr>';
  thead.innerHTML = headerHtml;

  // Attach sort handlers to new headers and restore sort indicator
  thead.querySelectorAll('th[data-sort-key]').forEach(th => {
    th.addEventListener('click', () => handleTableSort('overview', th));
    th.addEventListener('keydown', e => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); handleTableSort('overview', th); } });
    // Restore sort indicator if this column is currently sorted
    if (State.overviewSort.key === th.dataset.sortKey) {
      th.setAttribute('data-sort-dir', State.overviewSort.dir);
    }
  });

  let data = [...State.allOverviewData];

  // Sort
  if (State.overviewSort.key) {
    const { key, dir } = State.overviewSort;
    data.sort((a, b) => {
      let va, vb;
      if (key === 'id') { va = a.cycleId; vb = b.cycleId; }
      else if (key === 'start') { va = a.cycleStart; vb = b.cycleStart; }
      else if (key === 'end') { va = a.cycleEnd || ''; vb = b.cycleEnd || ''; }
      else if (key === 'duration') {
        va = a.cycleEnd ? new Date(a.cycleEnd) - new Date(a.cycleStart) : 0;
        vb = b.cycleEnd ? new Date(b.cycleEnd) - new Date(b.cycleStart) : 0;
      }
      else if (key === 'totalDelta') { va = a.totalDelta; vb = b.totalDelta; }
      else if (key.startsWith('cq_')) {
        const qn = key.slice(3);
        va = getCrossQuotaPercent(a, qn);
        vb = getCrossQuotaPercent(b, qn);
      }
      else { va = 0; vb = 0; }
      if (va < vb) return dir === 'asc' ? -1 : 1;
      if (va > vb) return dir === 'asc' ? 1 : -1;
      return 0;
    });
  }

  // Pagination
  const pageSize = State.overviewPageSize || 10;
  const totalRows = data.length;
  const totalPages = pageSize > 0 ? Math.ceil(totalRows / pageSize) : 1;
  if (State.overviewPage > totalPages) State.overviewPage = totalPages || 1;
  const page = State.overviewPage;
  const startIdx = pageSize > 0 ? (page - 1) * pageSize : 0;
  const pageData = pageSize > 0 ? data.slice(startIdx, startIdx + pageSize) : data;

  // Format value with rate: "45.2% [⚡5.2%/hr]"
  const fmtOverviewWithRate = (val, durationHrs, suffix) => {
    if (typeof val !== 'number') return '--';
    const valStr = val.toFixed(1) + suffix;
    if (durationHrs > 0) {
      const rate = val / durationHrs;
      return `${valStr} <span class="rate-indicator">[⚡${rate.toFixed(1)}${suffix}/hr]</span>`;
    }
    return valStr;
  };

  if (pageData.length === 0) {
    const colCount = 5 + quotaNames.length;
    tbody.innerHTML = `<tr><td colspan="${colCount}" class="empty-state">No completed cycles found for this period.</td></tr>`;
  } else {
    tbody.innerHTML = pageData.map(row => {
      const start = row.cycleStart || null;
      const end = row.cycleEnd || null;
      const startDate = start ? new Date(start) : null;
      const endDate = end ? new Date(end) : null;
      const durationMs = (startDate && endDate) ? endDate - startDate : 0;
      const durationHrs = durationMs / 3600000;
      const duration = durationMs > 0 ? formatDuration(Math.floor(durationMs / 1000)) : '--';
      const suffix = deltaUsesPercent ? '%' : '';

      // For active cycles (no end, or cycleId is -1 or 'active'), show "Active" badge
      const isActive = !end || row.cycleId === -1 || row.cycleId === 'active';
      const cycleLabel = isActive ? '<span class="badge">Active</span>' : `${row.cycleId}`;

      let html = `<tr>
        <td>${cycleLabel}</td>
        <td>${start ? formatDateTime(start) : '--'}</td>
        <td>${end ? formatDateTime(end) : '<span class="badge">Active</span>'}</td>
        <td>${duration}</td>
        <td>${fmtOverviewWithRate(row.totalDelta, durationHrs, suffix)}</td>`;

      quotaNames.forEach(qn => {
        const pct = getCrossQuotaPercent(row, qn);
        const delta = getCrossQuotaDelta(row, qn);
        const isPrimary = qn === State.overviewGroupBy;
        const cls = getThresholdClass(pct);
        let cellVal = '--';
        if (pct >= 0) {
          if (overviewProv === 'minimax' || overviewProv === 'gemini') {
            const cq = getCrossQuotaValue(row, qn);
            const used = Number(cq?.value || 0);
            const limit = Number(cq?.limit || 0);
            const percentText = `${pct.toFixed(1)}%`;
            const deltaText = delta == null ? '' : ` <span class="delta">(${delta >= 0 ? '+' : ''}${delta.toFixed(1)}%)</span>`;
            cellVal = limit > 0
              ? `${formatNumber(used)} / ${formatNumber(limit)} <span class="delta">(${percentText})</span>${deltaText}`
              : `${formatNumber(used)} <span class="delta">(${percentText})</span>${deltaText}`;
          } else if (usePercent) {
            cellVal = fmtPctWithDelta(pct, delta);
          } else {
            const cq = getCrossQuotaValue(row, qn);
            cellVal = cq ? formatNumber(cq.value) : pct.toFixed(1) + '%';
          }
        }
        html += `<td class="${cls}${isPrimary ? ' overview-primary-val' : ''}">${cellVal}</td>`;
      });

      html += '</tr>';
      return html;
    }).join('');
  }

  // Info
  if (info) {
    const showStart = totalRows > 0 ? startIdx + 1 : 0;
    const showEnd = pageSize > 0 ? Math.min(startIdx + pageSize, totalRows) : totalRows;
    info.textContent = `Showing ${showStart}-${showEnd} of ${totalRows}`;
  }

  // Pagination buttons
  if (pagination) {
    pagination.innerHTML = renderPagination('overview', page, totalPages);
  }
}

function getCrossQuotaPercent(row, quotaName) {
  if (!row.crossQuotas || row.crossQuotas.length === 0) return -1;
  const entry = row.crossQuotas.find(cq => cq.name === quotaName);
  return entry ? entry.percent : -1;
}

function getCrossQuotaDelta(row, quotaName) {
  if (!row.crossQuotas || row.crossQuotas.length === 0) return null;
  const entry = row.crossQuotas.find(cq => cq.name === quotaName);
  return entry ? entry.delta : null;
}

function getCrossQuotaValue(row, quotaName) {
  if (!row.crossQuotas || row.crossQuotas.length === 0) return null;
  return row.crossQuotas.find(cq => cq.name === quotaName) || null;
}

// Format value with delta inline: "24.0% (+12.3%)"
function fmtPctWithDelta(pct, delta) {
  if (pct == null || pct < 0) return '--';
  const pctStr = pct.toFixed(1) + '%';
  if (delta == null) return pctStr;
  const deltaStr = delta >= 0 ? `+${delta.toFixed(1)}%` : `${delta.toFixed(1)}%`;
  return `${pctStr} <span class="delta">(${deltaStr})</span>`;
}

function getOverviewProvider() {
  const gb = State.overviewGroupBy;
  if (!gb) return getCurrentProvider();
  for (const [prov, cats] of Object.entries(renewalCategories)) {
    if (cats.some(c => c.groupBy === gb)) return prov;
  }
  return getCurrentProvider();
}

function getThresholdClass(pct) {
  if (pct < 0) return '';
  if (pct >= 95) return 'threshold-critical';
  if (pct >= 80) return 'threshold-danger';
  if (pct >= 50) return 'threshold-warning';
  return 'threshold-healthy';
}

function setupTableControls() {
  // Cycles page size
  const cyclesPageSizeEl = document.getElementById('cycles-page-size');
  if (cyclesPageSizeEl) {
    cyclesPageSizeEl.addEventListener('change', () => {
      State.cyclesPageSize = parseInt(cyclesPageSizeEl.value, 10);
      State.cyclesPage = 1;
      renderCyclesTable();
    });
  }

  // Sessions page size
  const sessionsPageSizeEl = document.getElementById('sessions-page-size');
  if (sessionsPageSizeEl) {
    sessionsPageSizeEl.addEventListener('change', () => {
      State.sessionsPageSize = parseInt(sessionsPageSizeEl.value, 10);
      State.sessionsPage = 1;
      renderSessionsTable();
    });
  }

  // Sort headers (cycles): attached dynamically in renderCyclesTable() since headers are dynamic

  // Sort headers (sessions)
  document.querySelectorAll('#sessions-table th[data-sort-key]').forEach(th => {
    th.addEventListener('click', () => handleTableSort('sessions', th));
    th.addEventListener('keydown', e => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); handleTableSort('sessions', th); } });
  });

  // Pagination (delegated)
  document.addEventListener('click', (e) => {
    const btn = e.target.closest('.page-btn');
    if (!btn || btn.disabled) return;
    const table = btn.dataset.table;
    const page = parseInt(btn.dataset.page, 10);
    if (table === 'cycles') {
      State.cyclesPage = page;
      renderCyclesTable();
    } else if (table === 'sessions') {
      State.sessionsPage = page;
      renderSessionsTable();
    } else if (table === 'overview') {
      State.overviewPage = page;
      renderOverviewTable();
    }
  });

  // Session row expansion (delegated)
  const sessionsTbody = document.getElementById('sessions-tbody');
  if (sessionsTbody) {
    sessionsTbody.addEventListener('click', handleSessionRowClick);
    sessionsTbody.addEventListener('keydown', (e) => {
      if (e.key === 'Enter' || e.key === ' ') {
        const row = e.target.closest('.session-row');
        if (row) { e.preventDefault(); handleSessionRowClick(e); }
      }
    });
  }
}

function setupProviderSelector() {
  const tabs = document.getElementById('provider-tabs');
  if (!tabs) return;
  tabs.querySelectorAll('.provider-tab').forEach(tab => {
    tab.addEventListener('click', () => {
      const provider = tab.dataset.provider;
      saveDefaultProvider(provider);
      window.location.href = `${BASE_PATH}/?provider=${provider}`;
    });
  });
}

function setupHeaderActions() {
  // Scroll to top
  const scrollBtn = document.getElementById('scroll-top');
  if (scrollBtn) {
    scrollBtn.addEventListener('click', (e) => {
      e.preventDefault();
      window.scrollTo({ top: 0, behavior: 'smooth' });
    });
  }

  // Manual refresh
  const refreshBtn = document.getElementById('refresh-btn');
  if (refreshBtn) {
    refreshBtn.addEventListener('click', () => {
      refreshBtn.classList.add('spinning');
      const tasks = [fetchCurrent(), fetchDeepInsights(), fetchHistory()];
      if (shouldShowHistoryTables()) {
        tasks.push(fetchCycles(), fetchSessions(), fetchCycleOverview());
      }
      Promise.all(tasks).finally(() => {
        setTimeout(() => refreshBtn.classList.remove('spinning'), 600);
      });
    });
  }
}

function setupCardModals() {
  document.querySelectorAll('.quota-card[role="button"]').forEach(card => {
    const handler = () => {
      // In "both" mode, detect which provider column the card belongs to
      const providerCol = card.closest('.provider-column');
      const providerOverride = providerCol ? providerCol.dataset.provider : null;
      openModal(card.dataset.quota, providerOverride);
    };
    card.addEventListener('click', handler);
    card.addEventListener('keydown', e => {
      if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); handler(); }
    });
  });

  // Modal close
  const closeBtn = document.getElementById('modal-close');
  if (closeBtn) closeBtn.addEventListener('click', closeModal);

  const overlay = document.getElementById('detail-modal');
  if (overlay) {
    overlay.addEventListener('click', (e) => {
      if (e.target === overlay) closeModal();
    });
  }

  // ESC to close
  document.addEventListener('keydown', (e) => {
    if (e.key === 'Escape') closeModal();
  });
}

function startAutoRefresh() {
  if (State.refreshInterval) clearInterval(State.refreshInterval);
  State.refreshInterval = setInterval(() => {
    // Always refresh above-fold data
    fetchCurrent(); fetchDeepInsights(); fetchHistory();
    // Only refresh below-fold sections that have been loaded
    if (shouldShowHistoryTables()) {
      if (_lazyLoaded.has('.cycles-section')) fetchCycles();
      if (_lazyLoaded.has('.cycle-overview-section')) fetchCycleOverview();
      if (_lazyLoaded.has('.sessions-section')) fetchSessions();
    }
  }, REFRESH_INTERVAL);
}

// ── Pagination Helper ──

function renderPagination(table, page, totalPages) {
  if (totalPages <= 1) return '';
  const maxVisible = 7; // max page buttons (excluding prev/next)
  let pages = [];

  if (totalPages <= maxVisible) {
    for (let p = 1; p <= totalPages; p++) pages.push(p);
  } else {
    // Always show first, last, and a window around current
    const wing = 2;
    let start = Math.max(2, page - wing);
    let end = Math.min(totalPages - 1, page + wing);
    // Shift window if near edges
    if (start <= 2) end = Math.min(totalPages - 1, maxVisible - 2);
    if (end >= totalPages - 1) start = Math.max(2, totalPages - maxVisible + 3);

    pages.push(1);
    if (start > 2) pages.push('...');
    for (let p = start; p <= end; p++) pages.push(p);
    if (end < totalPages - 1) pages.push('...');
    pages.push(totalPages);
  }

  let html = `<button class="page-btn" ${page <= 1 ? 'disabled' : ''} data-table="${table}" data-page="${page - 1}">&laquo;</button>`;
  for (const p of pages) {
    if (p === '...') {
      html += `<span class="page-ellipsis">&hellip;</span>`;
    } else {
      html += `<button class="page-btn ${p === page ? 'active' : ''}" data-table="${table}" data-page="${p}">${p}</button>`;
    }
  }
  html += `<button class="page-btn" ${page >= totalPages ? 'disabled' : ''} data-table="${table}" data-page="${page + 1}">&raquo;</button>`;
  return html;
}

// ── Self-Update ──

async function checkForUpdate() {
  try {
    const res = await authFetch('/api/update/check');
    const data = await res.json();
    const badge = document.getElementById('update-badge');
    if (data.available) {
      const versionSpan = document.getElementById('update-version');
      if (badge && versionSpan) {
        versionSpan.textContent = data.latest_version;
        badge.hidden = false;
      }
    } else if (badge) {
      badge.hidden = true;
    }
  } catch (e) {
    // Silent fail - update check is best-effort
  }
}

async function applyUpdate() {
  const btn = document.getElementById('update-btn');
  if (!btn) return;

  const origText = btn.textContent;
  btn.textContent = 'Updating...';
  btn.disabled = true;

  try {
    const res = await authFetch('/api/update/apply', { method: 'POST' });
    if (!res.ok) {
      const data = await res.json();
      btn.textContent = 'Update failed';
      btn.disabled = false;
      // update failed - error shown in UI
      setTimeout(() => { btn.textContent = origText; }, 3000);
      return;
    }
    btn.textContent = 'Restarting...';
    // Poll until server comes back with new version
    setTimeout(() => pollForRestart(), 3000);
  } catch (e) {
    btn.textContent = 'Update failed';
    btn.disabled = false;
    setTimeout(() => { btn.textContent = origText; }, 3000);
  }
}

function pollForRestart() {
  let serverWentDown = false;
  const interval = setInterval(async () => {
    try {
      await fetch('/api/update/check');
      if (serverWentDown) {
        // Server is back up after going down - force fresh page load (no cache)
        clearInterval(interval);
        window.location.href = window.location.pathname + '?_=' + Date.now();
      }
      // Server still responding (hasn't died yet) - keep waiting
    } catch (e) {
      // Network error = server went down
      serverWentDown = true;
    }
  }, 1000);
  // Force reload after 30s even if we didn't detect restart
  setTimeout(() => {
    clearInterval(interval);
    window.location.href = window.location.pathname + '?_=' + Date.now();
  }, 30000);
}

// ═══════════════════════════════════════════
// SETTINGS PAGE
// ═══════════════════════════════════════════

function isSettingsPage() {
  return window.location.pathname === '/settings';
}

async function initSettingsPage() {
  setupSettingsTabs();
  await setupMenubarSettings();
  await loadSettings();
  setupSettingsSave();
  setupProviderReload();
  setupProviderSettingsModal();
  setupSMTPTest();
  setupPushNotifications();
  setupSettingsPassword();
  setupThresholdSliders();
  setupOverrides();
  populateTimezoneSelect();
}

function activateSettingsTab(tabName) {
  const nextTab = document.querySelector(`.settings-tab[data-tab="${tabName}"]`);
  if (!nextTab || nextTab.hidden) return;
  nextTab.click();
}

function setupSettingsTabs() {
  const tabs = document.querySelectorAll('.settings-tab');
  const panels = document.querySelectorAll('.settings-panel');
  tabs.forEach(tab => {
    tab.addEventListener('click', () => {
      const target = tab.dataset.tab;
      tabs.forEach(t => { t.classList.remove('active'); t.setAttribute('aria-selected', 'false'); });
      panels.forEach(p => { p.classList.remove('active'); p.hidden = true; });
      tab.classList.add('active');
      tab.setAttribute('aria-selected', 'true');
      const panel = document.getElementById('panel-' + target);
      if (panel) { panel.classList.add('active'); panel.hidden = false; }
    });
  });
}

function setupThresholdSliders() {
  // Warning threshold
  const wSlider = document.getElementById('threshold-warning-slider');
  const wInput = document.getElementById('threshold-warning');
  if (wSlider && wInput) {
    wSlider.addEventListener('input', () => { wInput.value = wSlider.value; });
    wInput.addEventListener('input', () => { wSlider.value = wInput.value; });
  }
  // Critical threshold
  const cSlider = document.getElementById('threshold-critical-slider');
  const cInput = document.getElementById('threshold-critical');
  if (cSlider && cInput) {
    cSlider.addEventListener('input', () => { cInput.value = cSlider.value; });
    cInput.addEventListener('input', () => { cSlider.value = cInput.value; });
  }
}

async function loadCapabilities() {
  try {
    const resp = await authFetch('/api/capabilities');
    if (!resp.ok) return null;
    return await resp.json();
  } catch (e) {
    return null;
  }
}

async function setupMenubarSettings() {
  const tab = document.querySelector('.settings-tab[data-tab="menubar"]');
  const panel = document.getElementById('panel-menubar');
  const settingsShell = document.getElementById('menubar-settings-shell');
  const orderShell = document.getElementById('menubar-order-shell');
  const divider = document.getElementById('menubar-order-divider');
  if (!tab || !panel) return;

  const caps = await loadCapabilities();
  State.menubarCapabilities = caps;

  const isMac = caps && caps.platform === 'darwin';
  if (!isMac) {
    tab.hidden = true;
    panel.hidden = true;
    if (tab.classList.contains('active')) activateSettingsTab('general');
    return;
  }

  tab.hidden = false;
  const supported = !!caps.menubar_supported;
  if (settingsShell) settingsShell.hidden = !supported;
  if (orderShell) orderShell.hidden = !supported;
  if (divider) divider.hidden = !supported;
}

async function loadSettings() {
  try {
    const resp = await authFetch('/api/settings');
    if (!resp.ok) return;
    const data = await resp.json();

    // Timezone
    const tzSelect = document.getElementById('settings-timezone');
    if (tzSelect && data.timezone) { tzSelect.value = data.timezone; }

    // SMTP
    if (data.smtp) {
      const s = data.smtp;
      setVal('smtp-host', s.host);
      setVal('smtp-port', s.port);
      if (s.protocol) {
        setVal('smtp-protocol', s.protocol);
      }
      setVal('smtp-username', s.username);
      setVal('smtp-from-address', s.from_address);
      setVal('smtp-from-name', s.from_name);
      setVal('smtp-to', s.to);
      if (s.password_set) {
        const pwdInput = document.getElementById('smtp-password');
        if (pwdInput) pwdInput.placeholder = '********** (saved)';
      }
    }

    // Notifications
    if (data.notifications) {
      const n = data.notifications;
      setVal('threshold-warning', n.warning_threshold);
      setVal('threshold-warning-slider', n.warning_threshold);
      setVal('threshold-critical', n.critical_threshold);
      setVal('threshold-critical-slider', n.critical_threshold);
      const warnCheck = document.getElementById('notify-warning');
      const critCheck = document.getElementById('notify-critical');
      const resetCheck = document.getElementById('notify-reset');
      if (warnCheck) warnCheck.checked = n.notify_warning !== false;
      if (critCheck) critCheck.checked = n.notify_critical !== false;
      if (resetCheck) resetCheck.checked = n.notify_reset !== false;
      const authErrorCheck = document.getElementById('notify-auth-error');
      if (authErrorCheck) authErrorCheck.checked = !!n.notify_auth_error;
      setVal('notify-cooldown', n.cooldown_minutes || 30);
      // Load channel preferences
      if (n.channels) {
        const emailToggle = document.getElementById('channel-email');
        const pushToggle = document.getElementById('channel-push');
        if (emailToggle) emailToggle.checked = n.channels.email !== false;
        if (pushToggle) pushToggle.checked = n.channels.push !== false;
      }
      // Load overrides
      if (n.overrides && n.overrides.length > 0) {
        n.overrides.forEach(o => addOverrideRow(o.quota_key, o.provider, o.warning, o.critical, o.is_absolute, o.disable_reset, o.disable_warning, o.disable_critical));
      }
    }

    // Provider settings - store in State for modal use
    State.providerSettings = data.provider_settings || {};

    // Provider visibility + dynamic provider status
    await populateProviderToggles(data.provider_visibility || {});
    await populateMenubarSettings(data.menubar || {});
  } catch (e) {
    // Settings load failed silently
  }
}

async function populateMenubarSettings(data) {
  const caps = State.menubarCapabilities || await loadCapabilities();
  State.menubarCapabilities = caps;
  if (!caps || caps.platform !== 'darwin') return;

  const settings = data || {};
  const shell = document.getElementById('menubar-settings-shell');
  if (shell && shell.hidden) return;

  const enabled = document.getElementById('menubar-enabled');
  const defaultView = document.getElementById('menubar-default-view');
  const refresh = document.getElementById('menubar-refresh');
  const warning = document.getElementById('menubar-warning');
  const critical = document.getElementById('menubar-critical');

  if (enabled) enabled.checked = settings.enabled !== false;
  if (defaultView && settings.default_view) {
    defaultView.value = settings.default_view === 'detailed' ? 'detailed' : 'standard';
  }
  if (refresh && settings.refresh_seconds) refresh.value = String(settings.refresh_seconds);
  if (warning && settings.warning_percent != null) warning.value = settings.warning_percent;
  if (critical && settings.critical_percent != null) critical.value = settings.critical_percent;

  State.menubarProviderOrder = Array.isArray(settings.providers_order) ? settings.providers_order.slice() : [];
  State.menubarVisibleProviders = Array.isArray(settings.visible_providers) ? settings.visible_providers.slice() : [];
  State.menubarStatusDisplay = settings.status_display && typeof settings.status_display === 'object'
    ? JSON.parse(JSON.stringify(settings.status_display))
    : { mode: 'multi_provider', selected_quotas: [] };
  await populateMenubarProviderOrder();
}

function setVal(id, val) {
  const el = document.getElementById(id);
  if (el && val !== undefined && val !== null) el.value = val;
}

function populateTimezoneSelect() {
  const select = document.getElementById('settings-timezone');
  if (!select) return;
  const zones = [
    'UTC', 'America/New_York', 'America/Chicago', 'America/Denver', 'America/Los_Angeles',
    'America/Sao_Paulo', 'Europe/London', 'Europe/Paris', 'Europe/Berlin', 'Europe/Moscow',
    'Asia/Dubai', 'Asia/Kolkata', 'Asia/Shanghai', 'Asia/Tokyo', 'Asia/Seoul',
    'Australia/Sydney', 'Pacific/Auckland'
  ];
  zones.forEach(tz => {
    const opt = document.createElement('option');
    opt.value = tz;
    opt.textContent = tz.replace(/_/g, ' ');
    select.appendChild(opt);
  });
}

// createCodexProviderSection creates a consolidated Codex card with sub-profiles.
function createCodexProviderSection(profiles, codexStatus, baseVisibility) {
  // Single account - render as a regular provider row (same as other providers)
  if (profiles.length <= 1) {
    const vis = baseVisibility.codex || {
      polling: codexStatus ? codexStatus.pollingEnabled !== false : true,
      dashboard: codexStatus ? codexStatus.dashboardVisible !== false : true
    };
    return createProviderToggleRow({
      key: 'codex',
      name: 'Codex',
      desc: codexStatus?.description || 'OpenAI Codex usage tracking',
      vis,
      configured: codexStatus ? codexStatus.configured !== false : true,
      autoDetectable: codexStatus ? !!codexStatus.autoDetectable : true,
      isPolling: codexStatus ? !!codexStatus.isPolling : profiles.length === 1,
    });
  }

  // Multiple accounts - consolidated card with nested sub-profiles
  const wrapper = document.createElement('div');
  wrapper.className = 'codex-provider-section';

  const anyPolling = profiles.some(p => !p.deletedAt);

  const headerVis = baseVisibility.codex || {
    polling: codexStatus ? codexStatus.pollingEnabled !== false : true,
    dashboard: codexStatus ? codexStatus.dashboardVisible !== false : true
  };

  // Header row with gear icon (shared settings for all accounts)
  const headerRow = createProviderToggleRow({
    key: 'codex',
    name: 'Codex',
    desc: `${profiles.length} accounts configured`,
    vis: headerVis,
    configured: codexStatus ? codexStatus.configured !== false : true,
    autoDetectable: codexStatus ? !!codexStatus.autoDetectable : true,
    isPolling: anyPolling,
  });
  wrapper.appendChild(headerRow);

  // Sub-profiles - only telemetry & dashboard toggles, no gear
  const subProfilesDiv = document.createElement('div');
  subProfilesDiv.className = 'codex-subprofiles';

  profiles.forEach(profile => {
    const isDeleted = !!profile.deletedAt;
    const key = `codex:${profile.id}`;
    const vis = isDeleted
      ? { polling: false, dashboard: false }
      : (baseVisibility[key] || baseVisibility.codex || headerVis);

    const subRow = createProviderToggleRow({
      key,
      name: escapeHtml(profile.name),
      desc: isDeleted
        ? 'Profile deleted - credentials removed'
        : `ChatGPT account: ${escapeHtml(profile.name)}`,
      vis,
      configured: !isDeleted,
      autoDetectable: true,
      isPolling: false,
      isDeleted,
    });
    subRow.classList.add('codex-subprofile-row');
    subProfilesDiv.appendChild(subRow);
  });

  wrapper.appendChild(subProfilesDiv);

  return wrapper;
}

// createMiniMaxProviderSection creates a consolidated MiniMax card with sub-accounts.
// Mirrors createCodexProviderSection pattern.
function createMiniMaxProviderSection(accounts, minimaxStatus, baseVisibility) {
  if (accounts.length <= 1) {
    const vis = baseVisibility.minimax || {
      polling: minimaxStatus ? minimaxStatus.pollingEnabled !== false : true,
      dashboard: minimaxStatus ? minimaxStatus.dashboardVisible !== false : true
    };
    return createProviderToggleRow({
      key: 'minimax',
      name: 'MiniMax',
      desc: minimaxStatus?.description || 'MiniMax Coding Plan usage tracking',
      vis,
      configured: minimaxStatus ? minimaxStatus.configured !== false : true,
      autoDetectable: false,
      isPolling: minimaxStatus ? !!minimaxStatus.isPolling : accounts.length === 1,
    });
  }

  const wrapper = document.createElement('div');
  wrapper.className = 'minimax-provider-section';

  const headerVis = baseVisibility.minimax || {
    polling: minimaxStatus ? minimaxStatus.pollingEnabled !== false : true,
    dashboard: minimaxStatus ? minimaxStatus.dashboardVisible !== false : true
  };

  const headerRow = createProviderToggleRow({
    key: 'minimax',
    name: 'MiniMax',
    desc: `${accounts.length} accounts configured`,
    vis: headerVis,
    configured: minimaxStatus ? minimaxStatus.configured !== false : true,
    autoDetectable: false,
    isPolling: accounts.some(a => !a.deletedAt),
  });
  wrapper.appendChild(headerRow);

  const subProfilesDiv = document.createElement('div');
  subProfilesDiv.className = 'minimax-subprofiles';

  accounts.forEach(account => {
    const isDeleted = !!account.deletedAt;
    const key = `minimax:${account.id}`;
    const vis = isDeleted
      ? { polling: false, dashboard: false }
      : (baseVisibility[key] || baseVisibility.minimax || headerVis);

    const subRow = createProviderToggleRow({
      key,
      name: escapeHtml(account.name),
      desc: isDeleted
        ? 'Account deleted'
        : `MiniMax account: ${escapeHtml(account.name)}${account.region ? ' (' + account.region + ')' : ''}`,
      vis,
      configured: !isDeleted && account.hasKey,
      autoDetectable: false,
      isPolling: false,
      isDeleted,
    });
    subRow.classList.add('minimax-subprofile-row');
    subProfilesDiv.appendChild(subRow);
  });

  wrapper.appendChild(subProfilesDiv);
  return wrapper;
}

async function populateProviderToggles(visibility) {
  const container = document.getElementById('provider-toggles');
  if (!container) return;
  const baseVisibility = visibility && typeof visibility === 'object' ? visibility : {};
  if (!State.providerVisibility || typeof State.providerVisibility !== 'object') {
    State.providerVisibility = {};
  }
  Object.assign(State.providerVisibility, baseVisibility);

  container.innerHTML = '';

  let providers = [];
  try {
    const res = await authFetch(`${API_BASE}/api/providers/status`);
    if (res.ok) {
      const data = await res.json();
      providers = Array.isArray(data.providers) ? data.providers : [];
    }
  } catch (e) {
    // Keep fallback list below.
  }

  if (providers.length === 0) {
    providers = [
      { key: 'anthropic', name: 'Anthropic', description: 'Claude Code usage tracking', configured: false, autoDetectable: true, pollingEnabled: true, dashboardVisible: true, isPolling: false },
      { key: 'synthetic', name: 'Synthetic', description: 'Synthetic API quota monitoring', configured: false, autoDetectable: false, pollingEnabled: true, dashboardVisible: true, isPolling: false },
      { key: 'zai', name: 'Z.ai', description: 'Z.ai API usage tracking', configured: false, autoDetectable: false, pollingEnabled: true, dashboardVisible: true, isPolling: false },
      { key: 'copilot', name: 'Copilot', description: 'GitHub Copilot premium request tracking', configured: false, autoDetectable: false, pollingEnabled: true, dashboardVisible: true, isPolling: false },
      { key: 'codex', name: 'Codex', description: 'OpenAI Codex usage tracking', configured: false, autoDetectable: true, pollingEnabled: true, dashboardVisible: true, isPolling: false },
      { key: 'antigravity', name: 'Antigravity', description: 'Antigravity model usage tracking', configured: false, autoDetectable: true, pollingEnabled: true, dashboardVisible: true, isPolling: false },
      { key: 'minimax', name: 'MiniMax', description: 'MiniMax Coding Plan usage tracking', configured: false, autoDetectable: false, pollingEnabled: true, dashboardVisible: true, isPolling: false },
      { key: 'gemini', name: 'Gemini', description: 'Google Gemini CLI quota tracking', configured: false, autoDetectable: true, pollingEnabled: true, dashboardVisible: true, isPolling: false },
    ];
  }

  const providerByKey = new Map(providers.map(p => [p.key, p]));
  const codexStatus = providerByKey.get('codex') || null;
  const minimaxStatus = providerByKey.get('minimax') || null;

  providers
    .filter(p => p.key !== 'codex' && p.key !== 'minimax')
    .forEach((p) => {
      const vis = baseVisibility[p.key] || {
        polling: p.pollingEnabled !== false,
        dashboard: p.dashboardVisible !== false
      };
      container.appendChild(createProviderToggleRow({
        key: p.key,
        name: p.name,
        desc: p.description,
        vis,
        configured: p.configured !== false,
        autoDetectable: !!p.autoDetectable,
        isPolling: !!p.isPolling
      }));
    });

  // Codex: always ONE card with sub-profiles listed inside
  let codexSection = null;
  try {
    const res = await authFetch(`${API_BASE}/api/codex/profiles`);
    if (res.ok) {
      const data = await res.json();
      const profiles = Array.isArray(data.profiles) ? data.profiles : [];
      codexSection = createCodexProviderSection(profiles, codexStatus, baseVisibility);
    }
  } catch (e) {
    // fall back to single row below
  }

  if (codexSection) {
    container.appendChild(codexSection);
  } else {
    // fallback single row
    const fallbackCodex = codexStatus || {
      key: 'codex',
      name: 'Codex',
      description: 'OpenAI Codex usage tracking',
      configured: false,
      autoDetectable: true,
      pollingEnabled: true,
      dashboardVisible: true,
      isPolling: false
    };
    const vis = baseVisibility.codex || {
      polling: fallbackCodex.pollingEnabled !== false,
      dashboard: fallbackCodex.dashboardVisible !== false
    };
    container.appendChild(createProviderToggleRow({
      key: 'codex',
      name: fallbackCodex.name || 'Codex',
      desc: fallbackCodex.description || 'OpenAI Codex usage tracking',
      vis,
      configured: fallbackCodex.configured !== false,
      autoDetectable: !!fallbackCodex.autoDetectable,
      isPolling: !!fallbackCodex.isPolling
    }));
  }

  // MiniMax: similar to Codex - ONE card with sub-accounts listed inside
  let minimaxSection = null;
  try {
    const res = await authFetch(`${API_BASE}/api/minimax/accounts`);
    if (res.ok) {
      const data = await res.json();
      const accounts = Array.isArray(data.accounts) ? data.accounts : [];
      minimaxSection = createMiniMaxProviderSection(accounts, minimaxStatus, baseVisibility);
    }
  } catch (e) {
    // fall back to single row below
  }

  if (minimaxSection) {
    container.appendChild(minimaxSection);
  } else {
    const fallbackMinimax = minimaxStatus || {
      key: 'minimax',
      name: 'MiniMax',
      description: 'MiniMax Coding Plan usage tracking',
      configured: false,
      autoDetectable: false,
      pollingEnabled: true,
      dashboardVisible: true,
      isPolling: false
    };
    const vis = baseVisibility.minimax || {
      polling: fallbackMinimax.pollingEnabled !== false,
      dashboard: fallbackMinimax.dashboardVisible !== false
    };
    container.appendChild(createProviderToggleRow({
      key: 'minimax',
      name: fallbackMinimax.name || 'MiniMax',
      desc: fallbackMinimax.description || 'MiniMax Coding Plan usage tracking',
      vis,
      configured: fallbackMinimax.configured !== false,
      autoDetectable: false,
      isPolling: !!fallbackMinimax.isPolling
    }));
  }
}

async function fetchMenubarProviders() {
  let providers = [];
  try {
    const res = await authFetch(`${API_BASE}/api/providers/status`);
    if (res.ok) {
      const data = await res.json();
      providers = Array.isArray(data.providers) ? data.providers : [];
    }
  } catch (e) {
    providers = [];
  }

  if (providers.length === 0) {
    return [];
  }

  const providerByKey = new Map(providers.map(p => [p.key, p]));
  const codexStatus = providerByKey.get('codex') || null;
  const items = providers
    .filter(p => p.key !== 'codex')
    .map(p => ({
      key: p.key,
      name: p.name,
      meta: `${p.pollingEnabled === false ? 'Telemetry Off' : 'Telemetry On'} · ${p.dashboardVisible === false ? 'Hidden from dashboard' : 'Visible in dashboard'}`,
      dashboardVisible: p.dashboardVisible !== false,
    }));

  try {
    const res = await authFetch(`${API_BASE}/api/codex/profiles`);
    if (res.ok) {
      const data = await res.json();
      const profiles = Array.isArray(data.profiles) ? data.profiles : [];
      if (profiles.length > 1) {
        profiles.forEach(profile => {
          const key = `codex:${profile.id}`;
          items.push({
            key,
            name: `Codex - ${escapeHtml(profile.name)}`,
            meta: 'Per-account Codex usage',
            dashboardVisible: true,
          });
        });
        return items;
      }
    }
  } catch (e) {
    // fall back to single Codex item below
  }

  if (codexStatus) {
    items.push({
      key: 'codex',
      name: codexStatus.name || 'Codex',
      meta: `${codexStatus.pollingEnabled === false ? 'Telemetry Off' : 'Telemetry On'} · ${codexStatus.dashboardVisible === false ? 'Hidden from dashboard' : 'Visible in dashboard'}`,
      dashboardVisible: codexStatus.dashboardVisible !== false,
    });
  }

  return items;
}

async function populateMenubarProviderOrder() {
  const list = document.getElementById('menubar-provider-order');
  if (!list) return;

  const providers = await fetchMenubarProviders();
  State.menubarProviders = providers.slice();
  if (providers.length === 0) {
    list.innerHTML = '<li class="menubar-order-item"><div class="menubar-order-copy"><span class="menubar-order-name">No providers available</span><span class="menubar-order-meta">Configure providers first to control menubar ordering.</span></div></li>';
    return;
  }

  const order = Array.isArray(State.menubarProviderOrder) ? State.menubarProviderOrder : [];
  const indexByKey = new Map(order.map((key, index) => [key, index]));
  providers.sort((a, b) => {
    const left = indexByKey.has(a.key) ? indexByKey.get(a.key) : Number.MAX_SAFE_INTEGER;
    const right = indexByKey.has(b.key) ? indexByKey.get(b.key) : Number.MAX_SAFE_INTEGER;
    if (left !== right) return left - right;
    return a.name.localeCompare(b.name);
  });
  State.menubarProviderOrder = providers.map(provider => provider.key);

  const knownKeys = new Set(providers.map(provider => provider.key));
  const explicitVisible = Array.isArray(State.menubarVisibleProviders)
    ? State.menubarVisibleProviders.filter((providerKey) => knownKeys.has(providerKey))
    : [];
  const visibleSet = new Set(explicitVisible);
  const showAll = visibleSet.size === 0;

  list.innerHTML = providers.map(provider => {
    const visible = showAll || visibleSet.has(provider.key);
    return `
    <li class="menubar-order-item ${provider.dashboardVisible ? '' : 'is-disabled'} ${visible ? '' : 'is-hidden'}" draggable="true" tabindex="0" data-provider="${provider.key}">
      <div class="menubar-order-handle" aria-hidden="true"><span></span><span></span><span></span></div>
      <div class="menubar-order-copy">
        <span class="menubar-order-name">${provider.name}</span>
        <span class="menubar-order-meta">${provider.meta}</span>
      </div>
      <div class="menubar-order-controls">
        <label class="menubar-order-toggle">
          <input type="checkbox" data-role="menubar-visible" data-provider="${provider.key}" ${visible ? 'checked' : ''}>
          <span>${visible ? 'Show' : 'Hide'}</span>
        </label>
      </div>
    </li>
  `;
  }).join('');

  let dragged = null;
  list.querySelectorAll('.menubar-order-item').forEach(item => {
    item.addEventListener('dragstart', () => {
      dragged = item;
      item.classList.add('dragging');
    });
    item.addEventListener('dragend', () => {
      item.classList.remove('dragging');
      syncMenubarProviderOrder();
    });
  });

  list.querySelectorAll('input[data-role="menubar-visible"]').forEach((input) => {
    input.addEventListener('change', () => {
      const toggles = [...list.querySelectorAll('input[data-role="menubar-visible"]')]
        .filter((toggle) => toggle instanceof HTMLInputElement);

      let visibleProviders = toggles
        .filter((toggle) => toggle.checked)
        .map((toggle) => toggle.dataset.provider)
        .filter(Boolean);

      if (visibleProviders.length === 0 && input instanceof HTMLInputElement) {
        input.checked = true;
        visibleProviders = [input.dataset.provider].filter(Boolean);
      }

      const visibleSet = new Set(visibleProviders);
      list.querySelectorAll('.menubar-order-item[data-provider]').forEach((row) => {
        const rowProvider = row.dataset.provider;
        const rowToggle = row.querySelector('input[data-role="menubar-visible"]');
        if (!rowProvider || !(rowToggle instanceof HTMLInputElement)) return;
        const isVisible = visibleSet.has(rowProvider);
        row.classList.toggle('is-hidden', !isVisible);
        rowToggle.checked = isVisible;
        const label = rowToggle.nextElementSibling;
        if (label) {
          label.textContent = isVisible ? 'Show' : 'Hide';
        }
      });

      if (visibleSet.size === State.menubarProviderOrder.length) {
        State.menubarVisibleProviders = [];
      } else {
        State.menubarVisibleProviders = State.menubarProviderOrder.filter((provider) => visibleSet.has(provider));
      }
    });
  });

  list.addEventListener('dragover', (event) => {
    event.preventDefault();
    const dragging = list.querySelector('.menubar-order-item.dragging');
    if (!dragging) return;
    const afterElement = getMenubarDragAfterElement(list, event.clientY);
    if (!afterElement) {
      list.appendChild(dragging);
    } else if (afterElement !== dragging) {
      list.insertBefore(dragging, afterElement);
    }
  }, { passive: false });

  syncMenubarProviderOrder();
}

function getMenubarDragAfterElement(container, y) {
  const items = [...container.querySelectorAll('.menubar-order-item:not(.dragging)')];
  return items.reduce((closest, child) => {
    const box = child.getBoundingClientRect();
    const offset = y - box.top - box.height / 2;
    if (offset < 0 && offset > closest.offset) {
      return { offset, element: child };
    }
    return closest;
  }, { offset: Number.NEGATIVE_INFINITY, element: null }).element;
}

function syncMenubarProviderOrder() {
  const list = document.getElementById('menubar-provider-order');
  if (!list) return;
  State.menubarProviderOrder = [...list.querySelectorAll('.menubar-order-item[data-provider]')]
    .map(item => item.dataset.provider)
    .filter(Boolean);

  const visibleSet = new Set(
    [...list.querySelectorAll('input[data-role="menubar-visible"]')]
      .filter((input) => input instanceof HTMLInputElement && input.checked)
      .map((input) => input.dataset.provider)
      .filter(Boolean)
  );

  if (visibleSet.size === State.menubarProviderOrder.length) {
    State.menubarVisibleProviders = [];
  } else {
    State.menubarVisibleProviders = State.menubarProviderOrder.filter((provider) => visibleSet.has(provider));
  }
}

function providerStatusBadge(configured, autoDetectable, isPolling) {
  if (!configured) {
    return autoDetectable
      ? '<span class="badge">Auto-detect</span>'
      : '<span class="badge">Not configured</span>';
  }
  if (isPolling) {
    return '<span class="badge">Polling</span>';
  }
  return '<span class="badge">Idle</span>';
}

function updateProviderVisibilityState(provider, role, enabled) {
  if (!State.providerVisibility || typeof State.providerVisibility !== 'object') {
    State.providerVisibility = {};
  }
  if (!State.providerVisibility[provider] || typeof State.providerVisibility[provider] !== 'object') {
    State.providerVisibility[provider] = {};
  }
  State.providerVisibility[provider][role] = enabled;
}

function createProviderToggleRow({ key, name, desc, vis, configured, autoDetectable, isPolling, isDeleted }) {
  const row = document.createElement('div');
  row.className = 'settings-toggle-row settings-toggle-row-dual';
  const badge = isDeleted
    ? '<span class="status-badge deleted" style="background:var(--md-error,#b3261e);color:#fff;padding:2px 8px;border-radius:12px;font-size:0.7rem;margin-left:8px">Deleted</span>'
    : providerStatusBadge(configured, autoDetectable, isPolling);
  const telemetryDisabled = isDeleted ? 'disabled title="Telemetry unavailable - profile deleted"' : '';
  // Determine base provider key for settings (codex:123 → codex)
  // Gear icon only on top-level providers, not sub-profile rows (codex:xxx)
  const isSubProfile = key.includes(':');
  const baseKey = isSubProfile ? key.split(':')[0] : key;
  const hasSettings = !isSubProfile && providerSettingsConfig[baseKey] != null;
  const gearHTML = hasSettings ? `
      <button class="provider-settings-btn" data-provider-key="${baseKey}" title="Configure ${name}" aria-label="Configure ${name}">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <circle cx="12" cy="12" r="3"/>
          <path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83-2.83l.06-.06A1.65 1.65 0 0 0 4.68 15a1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 2.83-2.83l.06.06A1.65 1.65 0 0 0 9 4.68a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 2.83l-.06.06A1.65 1.65 0 0 0 19.4 9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z"/>
        </svg>
      </button>` : '';
  row.innerHTML = `
    <div class="settings-toggle-info">
      <div class="settings-toggle-label">${name} ${badge}</div>
      <div class="settings-toggle-sublabel">${desc}</div>
    </div>
    <div class="settings-toggle-group">
      <div class="settings-toggle-item">
        <div class="settings-toggle-item-label">Telemetry</div>
        <div class="settings-toggle-item-hint">${isDeleted ? 'Unavailable - profile deleted' : 'Track usage data in background'}</div>
        <label class="settings-toggle" title="${isDeleted ? 'Telemetry unavailable - profile deleted' : 'Telemetry'}">
          <input type="checkbox" data-provider="${key}" data-role="polling" ${vis.polling !== false && !isDeleted ? 'checked' : ''} ${telemetryDisabled}>
          <span class="settings-toggle-track"></span>
        </label>
      </div>
      <div class="settings-toggle-item">
        <div class="settings-toggle-item-label">Dashboard</div>
        <div class="settings-toggle-item-hint">${isDeleted ? 'Show historical data' : 'Show as individual tab'}</div>
        <label class="settings-toggle" title="Dashboard">
          <input type="checkbox" data-provider="${key}" data-role="dashboard" ${vis.dashboard !== false ? 'checked' : ''}>
          <span class="settings-toggle-track"></span>
        </label>
      </div>
      ${gearHTML}
    </div>
  `;

  row.querySelectorAll('input[type="checkbox"]').forEach((cb) => {
    cb.addEventListener('change', async (event) => {
      const input = event.target;
      const provider = input.dataset.provider;
      const role = input.dataset.role;
      const enabled = input.checked;
      const feedback = document.getElementById('settings-feedback');

      input.disabled = true;
      try {
        const payload = { provider };
        payload[role] = enabled;
        const res = await authFetch(`${API_BASE}/api/providers/toggle`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(payload),
        });
        const data = await res.json();
        if (!res.ok || data.success === false) {
          input.checked = !enabled;
          const msg = data && data.message ? data.message : 'Failed to update provider.';
          showSettingsFeedback(feedback, `${name}: ${msg}`, 'error');
          return;
        }

        updateProviderVisibilityState(provider, role, enabled);
        if (provider.startsWith('codex:')) {
          // Keep global codex visibility in sync when account toggles are used.
          updateProviderVisibilityState('codex', role, enabled);
        }
        showSettingsFeedback(feedback, `${name} ${role} ${enabled ? 'enabled' : 'disabled'}.`, 'success');

        if (getCurrentProvider() === 'both' && role === 'polling') {
          renderAllProvidersView();
        }
      } catch (e) {
        input.checked = !enabled;
        showSettingsFeedback(document.getElementById('settings-feedback'), `${name}: Network error.`, 'error');
      } finally {
        input.disabled = false;
      }
    });
  });

  // Gear icon click handler - open provider settings modal
  const gearBtn = row.querySelector('.provider-settings-btn');
  if (gearBtn) {
    gearBtn.addEventListener('click', async (e) => {
      e.stopPropagation();
      await openProviderSettingsModal(gearBtn.dataset.providerKey);
    });
  }

  return row;
}

// ── Provider Settings Modal ──

// Configuration for each provider's settings fields.
// Each entry defines the form fields shown in the modal.
const providerSettingsConfig = {
  anthropic: {
    title: 'Anthropic',
    desc: 'Configure how onWatch collects Anthropic usage data. Changes take effect after daemon restart.',
    fields: [
      { id: 'source', label: 'Data Source', type: 'select', options: [
        { value: 'auto', text: 'Auto (statusline + API fallback)' },
        { value: 'statusline', text: 'Statusline Only (zero API calls)' },
        { value: 'api', text: 'API Only (no statusline)' },
      ], default: 'auto', hint: 'Auto uses Claude Code\'s statusline for live data and polls the API every N cycles for supplementary quotas.' },
      { id: 'api_poll_cycle_interval', label: 'API Poll Cycle Interval', type: 'number', min: 1, max: 100, default: 10, hint: 'Full API poll every N cycles (e.g., 10 = every 10th poll). Only applies in Auto mode.' },
      { id: 'staleness_minutes', label: 'Statusline Staleness (minutes)', type: 'number', min: 1, max: 60, default: 5, hint: 'How old the statusline data can be before falling back to API polling.' },
      { id: 'cc_detection', label: 'Claude Code Detection', type: 'select', options: [
        { value: 'on', text: 'On (skip OAuth refresh when CC is running)' },
        { value: 'off', text: 'Off (always attempt OAuth refresh)' },
      ], default: 'on', hint: 'When enabled, onWatch skips OAuth token refresh while Claude Code is running to prevent login disruption.' },
    ],
  },
  codex: {
    title: 'Codex',
    desc: 'Configure Codex profile discovery and display. Display changes take effect immediately; directory changes require a daemon restart.',
    fields: [
      { id: 'profiles_dir', label: 'Profiles Directory', type: 'text', placeholder: 'Auto-detected (default)', hint: 'Override the auto-detected Codex profiles directory. Leave blank to use the default.' },
      { id: 'display_mode', label: 'Quota Display', type: 'select', options: [
        { value: 'usage', text: 'Usage (show utilization %)' },
        { value: 'available', text: 'Available (show remaining %)' },
      ], default: 'usage', hint: 'Choose how to display five_hour and seven_day quota usage.' },
    ],
  },
  copilot: {
    title: 'Copilot',
    desc: 'Configure GitHub Copilot quota tracking. Changes take effect after daemon restart.',
    fields: [
      { id: 'token', label: 'GitHub PAT', type: 'password', placeholder: 'Not configured', hint: 'GitHub Personal Access Token with the copilot scope. Overrides COPILOT_TOKEN from .env.', sensitive: true },
    ],
  },
  zai: {
    title: 'Z.ai',
    desc: 'Configure Z.ai (ZhipuAI) quota tracking. Changes take effect after daemon restart.',
    fields: [
      { id: 'api_key', label: 'API Key', type: 'password', placeholder: 'Not configured', hint: 'Z.ai API key. Overrides ZAI_API_KEY from .env.', sensitive: true },
      { id: 'region', label: 'Region', type: 'select', options: [
        { value: 'global', text: 'Global (api.z.ai)' },
        { value: 'cn', text: 'China (open.bigmodel.cn)' },
      ], default: 'global', hint: 'Selects the API endpoint. Overrides ZAI_REGION from .env.' },
    ],
  },
  minimax: {
    title: 'MiniMax',
    desc: 'Manage MiniMax accounts and API keys. Add multiple accounts to track separate subscriptions.',
    fields: [],
    hasAccountManagement: true,
  },
  openrouter: {
    title: 'OpenRouter',
    desc: 'Configure OpenRouter usage tracking. Changes take effect after daemon restart.',
    fields: [
      { id: 'api_key', label: 'API Key', type: 'password', placeholder: 'Not configured', hint: 'OpenRouter API key. Overrides OPENROUTER_API_KEY from .env.', sensitive: true },
    ],
  },
  synthetic: {
    title: 'Synthetic',
    desc: 'Configure Synthetic quota tracking. Changes take effect after daemon restart.',
    fields: [
      { id: 'api_key', label: 'API Key', type: 'password', placeholder: 'Not configured', hint: 'Synthetic API key (must start with syn_). Overrides SYNTHETIC_API_KEY from .env.', sensitive: true },
    ],
  },
  antigravity: {
    title: 'Antigravity',
    desc: 'Override auto-detection for Docker or remote environments. Leave blank to use auto-detection.',
    fields: [
      { id: 'base_url', label: 'Base URL', type: 'text', placeholder: 'Auto-detected', hint: 'Override the auto-detected Antigravity server URL (e.g. for Docker). Equivalent to ANTIGRAVITY_BASE_URL.' },
      { id: 'csrf_token', label: 'CSRF Token', type: 'password', placeholder: 'Auto-detected', hint: 'Override the CSRF token for the Antigravity server. Equivalent to ANTIGRAVITY_CSRF_TOKEN.', sensitive: true },
    ],
  },
  gemini: {
    title: 'Gemini',
    desc: 'Gemini is auto-detected from your local credentials. Use the telemetry toggle to enable or disable tracking.',
    fields: [],
  },
};

async function openProviderSettingsModal(providerKey) {
  const config = providerSettingsConfig[providerKey];
  if (!config) return;

  const modal = document.getElementById('provider-settings-modal');
  const titleEl = document.getElementById('provider-settings-title');
  const bodyEl = document.getElementById('provider-settings-body');
  const feedbackEl = document.getElementById('provider-settings-feedback');
  if (!modal || !bodyEl) return;

  titleEl.textContent = config.title + ' Settings';
  if (feedbackEl) { feedbackEl.hidden = true; feedbackEl.textContent = ''; }

  const saved = (State.providerSettings && State.providerSettings[providerKey]) || {};

  // Build fields HTML (shared for non-Codex providers)
  let buildFieldsHTML = () => {
    if (config.fields.length === 0) {
      return `<p style="color:var(--text-secondary);font-size:14px;margin:0">${config.desc}</p>`;
    }
    let html = `<p style="color:var(--text-secondary);font-size:13px;margin:0 0 20px">${config.desc}</p>`;
    html += '<div class="settings-fields">';
    config.fields.forEach(f => {
      html += '<div class="settings-field">';
      html += `<label for="ps-${providerKey}-${f.id}">${f.label}</label>`;

      if (f.type === 'select') {
        html += `<select id="ps-${providerKey}-${f.id}" class="settings-input">`;
        f.options.forEach(o => {
          const sel = (saved[f.id] || f.default) === o.value ? ' selected' : '';
          html += `<option value="${o.value}"${sel}>${o.text}</option>`;
        });
        html += '</select>';
      } else if (f.type === 'number') {
        const val = saved[f.id] != null ? saved[f.id] : f.default;
        html += `<input type="number" id="ps-${providerKey}-${f.id}" class="settings-input" min="${f.min || ''}" max="${f.max || ''}" value="${val}" />`;
      } else if (f.type === 'password') {
        const isSet = saved[f.id + '_set'] === true;
        const placeholder = isSet ? 'Key is configured - leave blank to keep' : (f.placeholder || '');
        html += `<input type="password" id="ps-${providerKey}-${f.id}" class="settings-input" placeholder="${placeholder}" autocomplete="off" />`;
      } else {
        const val = saved[f.id] || '';
        html += `<input type="text" id="ps-${providerKey}-${f.id}" class="settings-input" value="${escapeHtml(val)}" placeholder="${f.placeholder || ''}" />`;
      }

      if (f.hint) {
        html += `<span class="settings-field-hint">${f.hint}</span>`;
      }
      html += '</div>';
    });
    html += '</div>';
    return html;
  };

  // For Codex, include profile management section
  if (providerKey === 'codex') {
    let profilesHTML = '<div class="codex-modal-profiles"><h4 style="margin:0 0 12px;font-size:13px;color:var(--text-secondary)">Saved Profiles</h4>';
    profilesHTML += '<div id="codex-profiles-list">Loading...</div>';
    profilesHTML += '</div>';

    // Build the full body with profiles section first, then settings fields
    bodyEl.innerHTML = profilesHTML + buildFieldsHTML();

    // Fetch and render profiles
    const profilesList = document.getElementById('codex-profiles-list');
    try {
      const res = await authFetch(`${API_BASE}/api/codex/profiles`);
      if (res.ok) {
        const data = await res.json();
        const profiles = Array.isArray(data.profiles) ? data.profiles : [];
        if (profiles.length === 0) {
          profilesList.innerHTML = '<p style="color:var(--text-secondary);font-size:13px;margin:0">No profiles saved. Save your first profile using the CLI: <code>onwatch codex profile save &lt;name&gt;</code></p>';
        } else {
          let html = '';
          profiles.forEach(profile => {
            const isDeleted = !!profile.deletedAt;
            html += `<div class="codex-profile-item" style="display:flex;align-items:center;justify-content:space-between;padding:8px 0;border-bottom:1px solid var(--border-light)">`;
            html += `<div>`;
            html += `<div style="font-weight:500">${escapeHtml(profile.name)}</div>`;
            html += `<div style="font-size:12px;color:var(--text-secondary)">${isDeleted ? 'Deleted' : 'Active'}</div>`;
            html += `</div>`;
            html += `<div style="display:flex;gap:8px">`;
            if (!isDeleted) {
              html += `<button class="codex-profile-action-btn" data-action="refresh" data-name="${escapeHtml(profile.name)}" title="Refresh from current auth.json" style="padding:4px 8px;font-size:12px;background:var(--surface-inset);border:1px solid var(--border);border-radius:4px;cursor:pointer">Refresh</button>`;
            }
            html += `<button class="codex-profile-action-btn" data-action="delete" data-name="${escapeHtml(profile.name)}" title="Delete profile" style="padding:4px 8px;font-size:12px;background:var(--surface-inset);border:1px solid var(--border);border-radius:4px;cursor:pointer;color:${isDeleted ? 'var(--text-disabled)' : 'var(--md-error,#b3261e)'}">Delete</button>`;
            html += `</div></div>`;
          });
          profilesList.innerHTML = html;

          // Attach event handlers to profile action buttons
          profilesList.querySelectorAll('.codex-profile-action-btn').forEach(btn => {
            btn.addEventListener('click', async (e) => {
              const action = btn.dataset.action;
              const name = btn.dataset.name;
              btn.disabled = true;
              btn.textContent = '...';
              try {
                const url = action === 'refresh'
                  ? `${API_BASE}/api/codex/profiles?refresh=${encodeURIComponent(name)}`
                  : `${API_BASE}/api/codex/profiles?name=${encodeURIComponent(name)}`;
                const res = await authFetch(url, {
                  method: action === 'refresh' ? 'POST' : 'DELETE',
                });
                if (res.ok) {
                  // Refresh the profiles list
                  await openProviderSettingsModal('codex');
                } else {
                  const errData = await res.json().catch(() => ({}));
                  alert(`${action === 'refresh' ? 'Refresh' : 'Delete'} failed: ${errData.error || res.statusText}`);
                  btn.disabled = false;
                  btn.textContent = action === 'refresh' ? 'Refresh' : 'Delete';
                }
              } catch (err) {
                alert(`${action === 'refresh' ? 'Refresh' : 'Delete'} failed: ${err.message}`);
                btn.disabled = false;
                btn.textContent = action === 'refresh' ? 'Refresh' : 'Delete';
              }
            });
          });
        }
      } else {
        profilesList.innerHTML = '<p style="color:var(--text-secondary);font-size:13px">Failed to load profiles</p>';
      }
    } catch (e) {
      profilesList.innerHTML = '<p style="color:var(--text-secondary);font-size:13px">Failed to load profiles</p>';
    }
  } else if (providerKey === 'minimax') {
    // MiniMax: account management UI (fully UI-driven, unlike Codex file-based profiles)
    let accountsHTML = '<div class="minimax-modal-accounts">';
    accountsHTML += '<p style="color:var(--text-secondary);font-size:13px;margin:0 0 16px">' + config.desc + '</p>';
    accountsHTML += '<div style="display:flex;align-items:center;justify-content:space-between;margin-bottom:12px">';
    accountsHTML += '<h4 style="margin:0;font-size:13px;color:var(--text-secondary)">Accounts</h4>';
    accountsHTML += '<button id="minimax-add-account-btn" style="padding:4px 12px;font-size:12px;background:var(--md-primary,#6750a4);color:#fff;border:none;border-radius:4px;cursor:pointer">+ Add Account</button>';
    accountsHTML += '</div>';
    accountsHTML += '<div id="minimax-accounts-list">Loading...</div>';
    accountsHTML += '<div id="minimax-add-form" hidden style="margin-top:12px;padding:12px;border:1px solid var(--border);border-radius:8px;background:var(--surface-inset)">';
    accountsHTML += '<div class="settings-fields">';
    accountsHTML += '<div class="settings-field"><label for="minimax-new-name">Account Name</label><input type="text" id="minimax-new-name" class="settings-input" placeholder="e.g. work, personal" /></div>';
    accountsHTML += '<div class="settings-field"><label for="minimax-new-key">API Key</label><input type="password" id="minimax-new-key" class="settings-input" placeholder="MiniMax API key" autocomplete="off" /></div>';
    accountsHTML += '<div class="settings-field"><label for="minimax-new-region">Region</label><select id="minimax-new-region" class="settings-input"><option value="global">Global</option><option value="cn">China</option></select></div>';
    accountsHTML += '</div>';
    accountsHTML += '<div style="display:flex;gap:8px;margin-top:8px">';
    accountsHTML += '<button id="minimax-save-new-btn" style="padding:4px 12px;font-size:12px;background:var(--md-primary,#6750a4);color:#fff;border:none;border-radius:4px;cursor:pointer">Save</button>';
    accountsHTML += '<button id="minimax-cancel-new-btn" style="padding:4px 12px;font-size:12px;background:var(--surface-inset);border:1px solid var(--border);border-radius:4px;cursor:pointer">Cancel</button>';
    accountsHTML += '</div></div>';
    accountsHTML += '</div>';

    bodyEl.innerHTML = accountsHTML;

    // Wire add button
    const addBtn = document.getElementById('minimax-add-account-btn');
    const addForm = document.getElementById('minimax-add-form');
    if (addBtn && addForm) {
      addBtn.addEventListener('click', () => { addForm.hidden = false; addBtn.hidden = true; });
      document.getElementById('minimax-cancel-new-btn')?.addEventListener('click', () => { addForm.hidden = true; addBtn.hidden = false; });
      document.getElementById('minimax-save-new-btn')?.addEventListener('click', async () => {
        const name = document.getElementById('minimax-new-name')?.value?.trim();
        const apiKey = document.getElementById('minimax-new-key')?.value?.trim();
        const region = document.getElementById('minimax-new-region')?.value || 'global';
        if (!name) { alert('Account name is required'); return; }
        try {
          const res = await authFetch(`${API_BASE}/api/minimax/accounts`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ name, api_key: apiKey, region }),
          });
          if (res.ok) {
            await openProviderSettingsModal('minimax');
          } else {
            const err = await res.json().catch(() => ({}));
            alert('Failed to create account: ' + (err.error || res.statusText));
          }
        } catch (e) { alert('Failed to create account: ' + e.message); }
      });
    }

    // Fetch and render accounts
    const accountsList = document.getElementById('minimax-accounts-list');
    try {
      const res = await authFetch(`${API_BASE}/api/minimax/accounts`);
      if (res.ok) {
        const data = await res.json();
        const accounts = Array.isArray(data.accounts) ? data.accounts : [];
        if (accounts.length === 0) {
          accountsList.innerHTML = '<p style="color:var(--text-secondary);font-size:13px;margin:0">No accounts configured. Add your first MiniMax account above.</p>';
        } else {
          let html = '';
          accounts.forEach(account => {
            const isDeleted = !!account.deletedAt;
            const regionLabel = account.region === 'cn' ? 'China' : 'Global';
            html += `<div class="minimax-account-item" style="display:flex;align-items:center;justify-content:space-between;padding:10px 0;border-bottom:1px solid var(--border-light)">`;
            html += `<div style="flex:1">`;
            html += `<div style="font-weight:500">${escapeHtml(account.name)}</div>`;
            html += `<div style="font-size:12px;color:var(--text-secondary)">${isDeleted ? 'Deleted' : regionLabel + ' - ' + (account.hasKey ? 'Key configured' : 'No key set')}</div>`;
            html += `</div>`;
            html += `<div style="display:flex;gap:8px;align-items:center">`;
            if (!isDeleted) {
              html += `<button class="minimax-acct-btn" data-action="edit" data-id="${account.id}" data-name="${escapeHtml(account.name)}" data-region="${account.region || 'global'}" data-has-key="${account.hasKey}" title="Edit account" style="padding:4px 8px;font-size:12px;background:var(--surface-inset);border:1px solid var(--border);border-radius:4px;cursor:pointer">Edit</button>`;
              html += `<button class="minimax-acct-btn" data-action="delete" data-id="${account.id}" data-name="${escapeHtml(account.name)}" title="Delete account" style="padding:4px 8px;font-size:12px;background:var(--surface-inset);border:1px solid var(--border);border-radius:4px;cursor:pointer;color:var(--md-error,#b3261e)">Delete</button>`;
            }
            html += `</div></div>`;
          });
          accountsList.innerHTML = html;

          // Wire action buttons
          accountsList.querySelectorAll('.minimax-acct-btn').forEach(btn => {
            btn.addEventListener('click', async () => {
              const action = btn.dataset.action;
              const id = btn.dataset.id;
              if (action === 'delete') {
                if (!confirm(`Delete account "${btn.dataset.name}"? Historical data will be preserved.`)) return;
                btn.disabled = true; btn.textContent = '...';
                try {
                  const res = await authFetch(`${API_BASE}/api/minimax/accounts?id=${id}`, { method: 'DELETE' });
                  if (res.ok) { await openProviderSettingsModal('minimax'); }
                  else { const e = await res.json().catch(() => ({})); alert('Delete failed: ' + (e.error || res.statusText)); btn.disabled = false; btn.textContent = 'Delete'; }
                } catch (e) { alert('Delete failed: ' + e.message); btn.disabled = false; btn.textContent = 'Delete'; }
              } else if (action === 'edit') {
                // Show inline edit form
                const item = btn.closest('.minimax-account-item');
                if (!item) return;
                const currentName = btn.dataset.name;
                const currentRegion = btn.dataset.region;
                const hasKey = btn.dataset.hasKey === 'true';
                item.innerHTML = `<div style="width:100%">
                  <div class="settings-fields" style="gap:8px">
                    <div class="settings-field" style="margin-bottom:4px"><label style="font-size:12px">Name</label><input type="text" class="settings-input minimax-edit-name" value="${escapeHtml(currentName)}" /></div>
                    <div class="settings-field" style="margin-bottom:4px"><label style="font-size:12px">API Key</label><input type="password" class="settings-input minimax-edit-key" placeholder="${hasKey ? 'Key configured - leave blank to keep' : 'Enter API key'}" autocomplete="off" /></div>
                    <div class="settings-field" style="margin-bottom:4px"><label style="font-size:12px">Region</label><select class="settings-input minimax-edit-region"><option value="global"${currentRegion !== 'cn' ? ' selected' : ''}>Global</option><option value="cn"${currentRegion === 'cn' ? ' selected' : ''}>China</option></select></div>
                  </div>
                  <div style="display:flex;gap:8px;margin-top:8px">
                    <button class="minimax-edit-save" data-id="${id}" style="padding:4px 12px;font-size:12px;background:var(--md-primary,#6750a4);color:#fff;border:none;border-radius:4px;cursor:pointer">Save</button>
                    <button class="minimax-edit-cancel" style="padding:4px 12px;font-size:12px;background:var(--surface-inset);border:1px solid var(--border);border-radius:4px;cursor:pointer">Cancel</button>
                  </div>
                </div>`;
                item.querySelector('.minimax-edit-cancel')?.addEventListener('click', () => openProviderSettingsModal('minimax'));
                item.querySelector('.minimax-edit-save')?.addEventListener('click', async (e) => {
                  const saveBtn = e.target;
                  const newName = item.querySelector('.minimax-edit-name')?.value?.trim();
                  const newKey = item.querySelector('.minimax-edit-key')?.value?.trim();
                  const newRegion = item.querySelector('.minimax-edit-region')?.value;
                  const body = {};
                  if (newName && newName !== currentName) body.name = newName;
                  if (newKey) body.api_key = newKey;
                  if (newRegion && newRegion !== currentRegion) body.region = newRegion;
                  if (Object.keys(body).length === 0) { await openProviderSettingsModal('minimax'); return; }
                  saveBtn.disabled = true; saveBtn.textContent = '...';
                  try {
                    const res = await authFetch(`${API_BASE}/api/minimax/accounts?id=${id}`, {
                      method: 'PUT',
                      headers: { 'Content-Type': 'application/json' },
                      body: JSON.stringify(body),
                    });
                    if (res.ok) { await openProviderSettingsModal('minimax'); }
                    else { const err = await res.json().catch(() => ({})); alert('Update failed: ' + (err.error || res.statusText)); saveBtn.disabled = false; saveBtn.textContent = 'Save'; }
                  } catch (e) { alert('Update failed: ' + e.message); saveBtn.disabled = false; saveBtn.textContent = 'Save'; }
                });
              }
            });
          });
        }
      } else {
        accountsList.innerHTML = '<p style="color:var(--text-secondary);font-size:13px">Failed to load accounts</p>';
      }
    } catch (e) {
      accountsList.innerHTML = '<p style="color:var(--text-secondary);font-size:13px">Failed to load accounts</p>';
    }
  } else {
    bodyEl.innerHTML = buildFieldsHTML();
  }

  // Store which provider is being edited
  modal.dataset.providerKey = providerKey;
  modal.hidden = false;
}

function closeProviderSettingsModal() {
  const modal = document.getElementById('provider-settings-modal');
  if (modal) modal.hidden = true;
}

async function saveProviderSettings() {
  const modal = document.getElementById('provider-settings-modal');
  if (!modal || modal.hidden) return;

  const providerKey = modal.dataset.providerKey;
  const config = providerSettingsConfig[providerKey];
  if (!config || config.fields.length === 0) { closeProviderSettingsModal(); return; }

  const feedbackEl = document.getElementById('provider-settings-feedback');
  const saveBtn = document.getElementById('provider-settings-save');
  if (saveBtn) { saveBtn.disabled = true; saveBtn.textContent = 'Saving...'; }

  // Gather values from the modal form
  const provData = {};
  config.fields.forEach(f => {
    const el = document.getElementById(`ps-${providerKey}-${f.id}`);
    if (!el) return;

    if (f.type === 'password' && f.sensitive) {
      // Only include if user typed a new value
      if (el.value) provData[f.id] = el.value;
    } else if (f.type === 'number') {
      provData[f.id] = parseInt(el.value, 10) || f.default || 0;
    } else {
      provData[f.id] = el.value.trim();
    }
  });

  try {
    const payload = { provider_settings: { [providerKey]: provData } };
    const res = await authFetch(`${API_BASE}/api/settings`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
    });
    if (!res.ok) {
      const errData = await res.json().catch(() => ({}));
      throw new Error(errData.error || 'Save failed');
    }
    const data = await res.json();
    // Update local state with returned settings
    if (data.provider_settings) {
      State.providerSettings = data.provider_settings;
    } else {
      // Merge locally
      if (!State.providerSettings) State.providerSettings = {};
      State.providerSettings[providerKey] = provData;
    }
    showSettingsFeedback(feedbackEl, 'Settings saved. Restart daemon to apply changes.', 'success');
    setTimeout(closeProviderSettingsModal, 1200);
  } catch (e) {
    showSettingsFeedback(feedbackEl, e.message || 'Failed to save settings.', 'error');
  } finally {
    if (saveBtn) { saveBtn.disabled = false; saveBtn.innerHTML = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M19 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h11l5 5v11a2 2 0 0 1-2 2z"/><polyline points="17 21 17 13 7 13 7 21"/><polyline points="7 3 7 8 15 8"/></svg> Save'; }
  }
}

function setupProviderSettingsModal() {
  const modal = document.getElementById('provider-settings-modal');
  if (!modal) return;

  const closeBtn = document.getElementById('provider-settings-close');
  const cancelBtn = document.getElementById('provider-settings-cancel');
  const saveBtn = document.getElementById('provider-settings-save');

  if (closeBtn) closeBtn.addEventListener('click', closeProviderSettingsModal);
  if (cancelBtn) cancelBtn.addEventListener('click', closeProviderSettingsModal);
  if (saveBtn) saveBtn.addEventListener('click', saveProviderSettings);

  // Close on overlay click
  modal.addEventListener('click', (e) => {
    if (e.target === modal) closeProviderSettingsModal();
  });

  // Close on Escape
  document.addEventListener('keydown', (e) => {
    if (e.key === 'Escape' && !modal.hidden) closeProviderSettingsModal();
  });
}

function setupProviderReload() {
  const section = document.getElementById('panel-providers');
  const fields = document.getElementById('provider-toggles');
  if (!section || !fields) return;
  if (document.getElementById('providers-reload-btn')) return;

  const wrap = document.createElement('div');
  wrap.className = 'settings-actions';
  wrap.innerHTML = `
    <button class="settings-test-btn" id="providers-reload-btn" type="button">
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M23 4v6h-6"/><path d="M1 20v-6h6"/><path d="M3.51 9a9 9 0 0 1 14.13-3.36L23 10"/><path d="M20.49 15a9 9 0 0 1-14.13 3.36L1 14"/></svg>
      Reload Providers From .env
    </button>
    <span class="settings-test-result" id="providers-reload-result"></span>
  `;
  section.querySelector('.settings-section')?.appendChild(wrap);

  const btn = document.getElementById('providers-reload-btn');
  const result = document.getElementById('providers-reload-result');
  if (!btn) return;

  btn.addEventListener('click', async () => {
    btn.disabled = true;
    btn.textContent = 'Reloading...';
    if (result) {
      result.textContent = '';
      result.className = 'settings-test-result';
    }
    try {
      const res = await authFetch(`${API_BASE}/api/providers/reload`, { method: 'POST' });
      const data = await res.json();
      if (!res.ok || !data.success) {
        if (result) {
          result.textContent = (data && data.error) || 'Reload failed.';
          result.className = 'settings-test-result error';
        }
      } else {
        await populateProviderToggles(State.providerVisibility || {});
        if (result) {
          result.textContent = 'Provider configuration reloaded.';
          result.className = 'settings-test-result success';
        }
      }
    } catch (e) {
      if (result) {
        result.textContent = 'Network error.';
        result.className = 'settings-test-result error';
      }
    } finally {
      btn.disabled = false;
      btn.innerHTML = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M23 4v6h-6"/><path d="M1 20v-6h6"/><path d="M3.51 9a9 9 0 0 1 14.13-3.36L23 10"/><path d="M20.49 15a9 9 0 0 1-14.13 3.36L1 14"/></svg> Reload Providers From .env';
    }
  });
}

function gatherSettings() {
  const settings = {};

  // SMTP
  const smtpHost = document.getElementById('smtp-host');
  if (smtpHost) {
    settings.smtp = {
      host: smtpHost.value.trim(),
      port: parseInt(document.getElementById('smtp-port')?.value) || 587,
      protocol: document.getElementById('smtp-protocol')?.value || 'auto',
      username: document.getElementById('smtp-username')?.value.trim() || '',
      password: document.getElementById('smtp-password')?.value || '',
      from_address: document.getElementById('smtp-from-address')?.value.trim() || '',
      from_name: document.getElementById('smtp-from-name')?.value.trim() || '',
      to: document.getElementById('smtp-to')?.value.trim() || '',
    };
  }

  // Notifications
  const warningInput = document.getElementById('threshold-warning');
  if (warningInput) {
    const overrides = [];
    document.querySelectorAll('.settings-override-row').forEach(row => {
      const quota = row.querySelector('.override-quota')?.value;
      const provider = row.querySelector('.override-provider')?.value;
      const w = parseFloat(row.querySelector('.override-warning')?.value);
      const c = parseFloat(row.querySelector('.override-critical')?.value);
      const isAbs = row.querySelector('.override-is-absolute')?.value === 'true';
      const disableWarning = !(row.querySelector('.override-enable-warning')?.checked ?? true);
      const disableCrit = !(row.querySelector('.override-enable-critical')?.checked ?? true);
      const disableReset = !(row.querySelector('.override-enable-reset')?.checked ?? true);
      if (quota && !isNaN(w) && !isNaN(c)) {
        overrides.push({ quota_key: quota, provider: provider || '', warning: w, critical: c, is_absolute: isAbs, disable_reset: disableReset, disable_warning: disableWarning, disable_critical: disableCrit });
      }
    });

    settings.notifications = {
      warning_threshold: parseFloat(warningInput.value) || 80,
      critical_threshold: parseFloat(document.getElementById('threshold-critical')?.value) || 95,
      notify_warning: document.getElementById('notify-warning')?.checked ?? true,
      notify_critical: document.getElementById('notify-critical')?.checked ?? true,
      notify_reset: document.getElementById('notify-reset')?.checked ?? true,
      notify_auth_error: document.getElementById('notify-auth-error')?.checked ?? false,
      cooldown_minutes: parseInt(document.getElementById('notify-cooldown')?.value) || 30,
      channels: {
        email: document.getElementById('channel-email')?.checked ?? true,
        push: document.getElementById('channel-push')?.checked ?? true,
      },
      overrides: overrides,
    };
  }

  // Provider visibility
  const toggles = document.querySelectorAll('#provider-toggles input[type="checkbox"]');
  if (toggles.length > 0) {
    const vis = {};
    toggles.forEach(t => {
      const prov = t.dataset.provider;
      const role = t.dataset.role;
      if (!vis[prov]) vis[prov] = {};
      vis[prov][role] = t.checked;
    });
    settings.provider_visibility = vis;
  }

  // Timezone
  const tzSelect = document.getElementById('settings-timezone');
  if (tzSelect) {
    settings.timezone = tzSelect.value;
  }

  // Provider settings are saved via the provider settings modal (saveProviderSettings),
  // NOT through the general settings save. Including them here would overwrite
  // sensitive keys (API keys/tokens) with empty strings since the server strips
  // them from GET responses for security.

  const menubarShell = document.getElementById('menubar-settings-shell');
  if (menubarShell && !menubarShell.hidden) {
    settings.menubar = {
      enabled: document.getElementById('menubar-enabled')?.checked ?? true,
      default_view: document.getElementById('menubar-default-view')?.value || 'standard',
      refresh_seconds: parseInt(document.getElementById('menubar-refresh')?.value, 10) || 60,
      warning_percent: parseInt(document.getElementById('menubar-warning')?.value, 10) || 70,
      critical_percent: parseInt(document.getElementById('menubar-critical')?.value, 10) || 90,
      providers_order: [...State.menubarProviderOrder],
      visible_providers: [...State.menubarVisibleProviders],
      status_display: State.menubarStatusDisplay ? JSON.parse(JSON.stringify(State.menubarStatusDisplay)) : { mode: 'multi_provider', selected_quotas: [] },
    };
  }

  return settings;
}

function setupSettingsSave() {
  const saveBtn = document.getElementById('settings-save-btn');
  const feedback = document.getElementById('settings-feedback');
  if (!saveBtn) return;

  saveBtn.addEventListener('click', async () => {
    saveBtn.disabled = true;
    saveBtn.textContent = 'Saving...';
    if (feedback) { feedback.hidden = true; }

    const settings = gatherSettings();

    // Client-side validation
    if (settings.notifications) {
      if (settings.notifications.warning_threshold >= settings.notifications.critical_threshold) {
        showSettingsFeedback(feedback, 'Warning threshold must be less than critical threshold.', 'error');
        saveBtn.disabled = false;
        saveBtn.innerHTML = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M19 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h11l5 5v11a2 2 0 0 1-2 2z"/><polyline points="17 21 17 13 7 13 7 21"/><polyline points="7 3 7 8 15 8"/></svg> Save Settings';
        return;
      }
    }
    if (settings.menubar) {
      if (settings.menubar.warning_percent >= settings.menubar.critical_percent) {
        showSettingsFeedback(feedback, 'Menubar warning threshold must be less than critical threshold.', 'error');
        saveBtn.disabled = false;
        saveBtn.innerHTML = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M19 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h11l5 5v11a2 2 0 0 1-2 2z"/><polyline points="17 21 17 13 7 13 7 21"/><polyline points="7 3 7 8 15 8"/></svg> Save Settings';
        return;
      }
    }

    try {
      const resp = await authFetch('/api/settings', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(settings),
      });
      const data = await resp.json();
      if (!resp.ok) {
        showSettingsFeedback(feedback, data.error || 'Failed to save settings.', 'error');
      } else {
        showSettingsFeedback(feedback, 'Settings saved successfully.', 'success');
      }
    } catch (e) {
      showSettingsFeedback(feedback, 'Network error. Please try again.', 'error');
    } finally {
      saveBtn.disabled = false;
      saveBtn.innerHTML = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M19 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h11l5 5v11a2 2 0 0 1-2 2z"/><polyline points="17 21 17 13 7 13 7 21"/><polyline points="7 3 7 8 15 8"/></svg> Save Settings';
    }
  });
}

function showSettingsFeedback(el, msg, type) {
  if (!el) return;
  el.textContent = msg;
  el.className = 'settings-feedback ' + type;
  el.hidden = false;
  setTimeout(() => { el.hidden = true; }, 5000);
}

function setupSMTPTest() {
  const testBtn = document.getElementById('smtp-test-btn');
  const result = document.getElementById('smtp-test-result');
  if (!testBtn) return;

  testBtn.addEventListener('click', async () => {
    testBtn.disabled = true;
    testBtn.textContent = 'Sending...';
    if (result) { result.textContent = ''; result.className = 'settings-test-result'; }

    try {
      const resp = await authFetch('/api/settings/smtp/test', { method: 'POST' });
      const data = await resp.json();
      if (result) {
        result.textContent = data.message || (data.success ? 'Test email sent.' : 'Test failed.');
        result.className = 'settings-test-result ' + (data.success ? 'success' : 'error');
      }
      // Show diagnostics if available
      const diagEl = document.getElementById('smtp-diagnostics');
      if (diagEl && data.diagnostics) {
        diagEl.textContent = data.diagnostics;
        diagEl.parentElement.hidden = false;
      }
    } catch (e) {
      if (result) {
        result.textContent = 'Network error.';
        result.className = 'settings-test-result error';
      }
    } finally {
      testBtn.disabled = false;
      testBtn.innerHTML = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M22 2L11 13M22 2l-7 20-4-9-9-4 20-7z"/></svg> Send Test Email';
    }
  });
}

function setupPushNotifications() {
  var statusLabel = document.getElementById('push-status-label');
  var subscribeBtn = document.getElementById('push-subscribe-btn');
  var channelToggle = document.getElementById('channel-push');
  var testActions = document.getElementById('push-test-actions');
  var testBtn = document.getElementById('push-test-btn');
  var testResult = document.getElementById('push-test-result');

  if (!statusLabel) return;

  // Always collect diagnostics (most useful when push is NOT working)
  collectPushDiagnostics();

  // Check for HTTPS - required for Push API on mobile devices
  // Note: window.isSecureContext is true for localhost over HTTP, but Android Chrome
  // still requires actual HTTPS for push notifications to work reliably
  var isHttps = location.protocol === 'https:';
  if (!isHttps) {
    statusLabel.textContent = 'Push notifications require HTTPS';
    if (channelToggle) { channelToggle.disabled = true; }
    if (subscribeBtn) { subscribeBtn.disabled = true; subscribeBtn.hidden = true; }
    // Add warning message below status
    var warning = document.createElement('div');
    warning.className = 'push-http-warning';
    warning.textContent = 'Requires HTTPS connection. Push notifications are unavailable over HTTP.';
    warning.style.cssText = 'color: #dc2626; font-size: 11px; margin-top: 4px;';
    statusLabel.parentNode.appendChild(warning);
    return;
  }

  if (!('serviceWorker' in navigator) || !('PushManager' in window)) {
    statusLabel.textContent = 'Push notifications not supported in this browser';
    if (channelToggle) { channelToggle.disabled = true; }
    return;
  }

  // Register service worker
  navigator.serviceWorker.register(BASE_PATH + '/sw.js').then(function(reg) {
    return reg.pushManager.getSubscription();
  }).then(function(sub) {
    if (sub) {
      statusLabel.textContent = 'Subscribed - push notifications active';
      if (subscribeBtn) { subscribeBtn.hidden = true; }
      if (testActions) { testActions.hidden = false; }
    } else {
      statusLabel.textContent = 'Not subscribed - click Enable to subscribe';
      if (subscribeBtn) { subscribeBtn.hidden = false; }
    }
  }).catch(function() {
    statusLabel.textContent = 'Service worker registration failed';
  });

  // Subscribe button
  if (subscribeBtn) {
    subscribeBtn.addEventListener('click', async function() {
      subscribeBtn.disabled = true;
      subscribeBtn.textContent = 'Enabling...';
      try {
        var vapidResp = await authFetch('/api/push/vapid');
        if (!vapidResp.ok) throw new Error('Failed to get VAPID key');
        var vapidData = await vapidResp.json();
        var applicationServerKey = urlBase64ToUint8Array(vapidData.public_key);

        var reg = await navigator.serviceWorker.ready;
        var sub = await reg.pushManager.subscribe({
          userVisibleOnly: true,
          applicationServerKey: applicationServerKey
        });

        var subJSON = sub.toJSON();
        var saveResp = await authFetch('/api/push/subscribe', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            endpoint: subJSON.endpoint,
            keys: { p256dh: subJSON.keys.p256dh, auth: subJSON.keys.auth }
          })
        });
        if (!saveResp.ok) throw new Error('Failed to save subscription');

        statusLabel.textContent = 'Subscribed - push notifications active';
        subscribeBtn.hidden = true;
        if (testActions) testActions.hidden = false;
        if (channelToggle) channelToggle.checked = true;
      } catch (e) {
        statusLabel.textContent = 'Failed: ' + (e.message || 'unknown error');
      } finally {
        subscribeBtn.disabled = false;
        subscribeBtn.innerHTML = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M18 8A6 6 0 0 0 6 8c0 7-3 9-3 9h18s-3-2-3-9M13.73 21a2 2 0 0 1-3.46 0"/></svg> Enable';
      }
    });
  }

  // Unsubscribe when push toggle is turned off
  if (channelToggle) {
    channelToggle.addEventListener('change', async function() {
      if (!channelToggle.checked) {
        try {
          var reg = await navigator.serviceWorker.ready;
          var sub = await reg.pushManager.getSubscription();
          if (sub) {
            var endpoint = sub.endpoint;
            await sub.unsubscribe();
            await authFetch('/api/push/subscribe', {
              method: 'DELETE',
              headers: { 'Content-Type': 'application/json' },
              body: JSON.stringify({ endpoint: endpoint })
            });
          }
          statusLabel.textContent = 'Not subscribed - click Enable to subscribe';
          if (subscribeBtn) subscribeBtn.hidden = false;
          if (testActions) testActions.hidden = true;
        } catch (e) {
          statusLabel.textContent = 'Failed to unsubscribe';
          channelToggle.checked = true;
        }
      }
    });
  }

  // Test push button
  if (testBtn) {
    testBtn.addEventListener('click', async function() {
      testBtn.disabled = true;
      testBtn.textContent = 'Sending...';
      if (testResult) { testResult.textContent = ''; testResult.className = 'settings-test-result'; }

      try {
        var resp = await authFetch('/api/push/test', { method: 'POST' });
        var data = await resp.json();
        if (testResult) {
          testResult.textContent = data.message || (data.success ? 'Test push sent.' : 'Test failed.');
          testResult.className = 'settings-test-result ' + (data.success ? 'success' : 'error');
        }
      } catch (e) {
        if (testResult) {
          testResult.textContent = 'Network error.';
          testResult.className = 'settings-test-result error';
        }
      } finally {
        testBtn.disabled = false;
        testBtn.innerHTML = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M18 8A6 6 0 0 0 6 8c0 7-3 9-3 9h18s-3-2-3-9M13.73 21a2 2 0 0 1-3.46 0"/></svg> Send Test Push';
      }
    });
  }

}

async function collectPushDiagnostics() {
  var diagEl = document.getElementById('push-diagnostics');
  if (!diagEl) return;

  var lines = [];
  lines.push('Protocol: ' + location.protocol);
  lines.push('Secure context: ' + (window.isSecureContext ? 'Yes' : 'No'));
  lines.push('Service Worker support: ' + ('serviceWorker' in navigator ? 'Yes' : 'No'));
  lines.push('PushManager support: ' + ('PushManager' in window ? 'Yes' : 'No'));
  lines.push('Notification support: ' + ('Notification' in window ? 'Yes' : 'No'));

  if ('Notification' in window) {
    lines.push('Notification permission: ' + Notification.permission);
  }

  lines.push('User agent: ' + navigator.userAgent);

  // Check Safari
  var isSafari = /^((?!chrome|android).)*safari/i.test(navigator.userAgent);
  if (isSafari) {
    var match = navigator.userAgent.match(/Version\/(\d+\.\d+)/);
    lines.push('Safari version: ' + (match ? match[1] : 'unknown'));
  }

  // Check VAPID key availability
  try {
    var resp = await authFetch('/api/push/vapid');
    if (resp.ok) {
      var data = await resp.json();
      lines.push('VAPID key: ' + (data.public_key ? 'Available (' + data.public_key.substring(0, 12) + '...)' : 'Missing'));
    } else {
      lines.push('VAPID key: Unavailable (HTTP ' + resp.status + ')');
    }
  } catch (e) {
    lines.push('VAPID key: Error fetching');
  }

  // Check service worker registration & subscription
  if ('serviceWorker' in navigator) {
    try {
      var reg = await navigator.serviceWorker.getRegistration(BASE_PATH + '/sw.js');
      if (reg) {
        lines.push('SW registered: Yes (scope: ' + reg.scope + ')');
        lines.push('SW state: ' + (reg.active ? 'active' : reg.installing ? 'installing' : reg.waiting ? 'waiting' : 'none'));
        var sub = await reg.pushManager.getSubscription();
        lines.push('Push subscription: ' + (sub ? 'Active' : 'None'));
      } else {
        lines.push('SW registered: No');
      }
    } catch (e) {
      lines.push('SW check error: ' + e.message);
    }
  }

  diagEl.textContent = lines.join('\n');
  const section = document.getElementById('push-diagnostics-section');
  if (section) section.hidden = false;
}

function urlBase64ToUint8Array(base64String) {
  var padding = '='.repeat((4 - base64String.length % 4) % 4);
  var base64 = (base64String + padding).replace(/-/g, '+').replace(/_/g, '/');
  var rawData = atob(base64);
  var outputArray = new Uint8Array(rawData.length);
  for (var i = 0; i < rawData.length; ++i) {
    outputArray[i] = rawData.charCodeAt(i);
  }
  return outputArray;
}

function setupSettingsPassword() {
  const saveBtn = document.getElementById('password-save-btn');
  const feedback = document.getElementById('settings-password-feedback');
  if (!saveBtn) return;

  saveBtn.addEventListener('click', async () => {
    if (feedback) { feedback.hidden = true; }
    const currentPass = document.getElementById('settings-current-password')?.value;
    const newPass = document.getElementById('settings-new-password')?.value;
    const confirmPass = document.getElementById('settings-confirm-password')?.value;

    if (!currentPass || !newPass) {
      showSettingsFeedback(feedback, 'Please fill in all password fields.', 'error');
      return;
    }
    if (newPass !== confirmPass) {
      showSettingsFeedback(feedback, 'New passwords do not match.', 'error');
      return;
    }
    if (newPass.length < 6) {
      showSettingsFeedback(feedback, 'New password must be at least 6 characters.', 'error');
      return;
    }

    saveBtn.disabled = true;
    saveBtn.textContent = 'Updating...';

    try {
      const resp = await authFetch('/api/password', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ current_password: currentPass, new_password: newPass }),
      });
      const data = await resp.json();
      if (!resp.ok) {
        showSettingsFeedback(feedback, data.error || 'Failed to update password.', 'error');
      } else {
        showSettingsFeedback(feedback, 'Password updated! Redirecting to login...', 'success');
        setTimeout(() => { window.location.href = BASE_PATH + '/login'; }, 1500);
      }
    } catch (e) {
      showSettingsFeedback(feedback, 'Network error.', 'error');
    } finally {
      saveBtn.disabled = false;
      saveBtn.textContent = 'Update Password';
    }
  });
}

function setupOverrides() {
  const addBtn = document.getElementById('add-override-btn');
  if (addBtn) {
    addBtn.addEventListener('click', () => addOverrideRow('', 'anthropic', 80, 95, false, false, false, false));
  }
}

const _overrideQuotasByProvider = {
  anthropic: [
    { key: 'five_hour', label: '5-Hour Limit' },
    { key: 'seven_day', label: 'Weekly All-Model' },
    { key: 'seven_day_sonnet', label: 'Weekly Sonnet' },
    { key: 'monthly_limit', label: 'Monthly Limit' },
    { key: 'extra_usage', label: 'Extra Usage' },
  ],
  codex: [
    { key: 'five_hour', label: '5-Hour Limit' },
    { key: 'seven_day', label: 'Weekly All-Model' },
    { key: 'code_review', label: 'Review Requests' },
  ],
  copilot: [
    { key: 'premium_interactions', label: 'Premium Requests' },
    { key: 'chat', label: 'Chat' },
    { key: 'completions', label: 'Completions' },
  ],
  minimax: [
    { key: 'coding_plan', label: 'Coding Plan (Shared Pool)' },
  ],
  antigravity: [
    { key: 'antigravity_claude_gpt', label: 'Claude + GPT Quota' },
    { key: 'antigravity_gemini_pro', label: 'Gemini Pro Quota' },
    { key: 'antigravity_gemini_flash', label: 'Gemini Flash Quota' },
  ],
  gemini: [
    { key: 'gemini-3-pro-preview', label: 'Gemini 3 Pro' },
    { key: 'gemini-2.5-pro', label: 'Gemini 2.5 Pro' },
    { key: 'gemini-3-flash-preview', label: 'Gemini 3 Flash' },
    { key: 'gemini-2.5-flash', label: 'Gemini 2.5 Flash' },
    { key: 'gemini-3.1-flash-lite-preview', label: 'Gemini 3.1 Flash Lite' },
    { key: 'gemini-2.5-flash-lite', label: 'Gemini 2.5 Flash Lite' },
  ],
  synthetic: [
    { key: 'subscription', label: 'Subscription' },
    { key: 'search', label: 'Search Queries' },
    { key: 'toolcall', label: 'Tool Calls' },
  ],
  zai: [
    { key: 'tokens', label: 'Tokens Limit' },
    { key: 'time', label: 'Time Limit' },
  ],
};

function _isAbsoluteProvider(provider) {
  return provider === 'synthetic' || provider === 'zai';
}

function _updateOverrideQuotas(row) {
  const provSelect = row.querySelector('.override-provider-select');
  const quotaSelect = row.querySelector('.override-quota');
  const warnInput = row.querySelector('.override-warning');
  const critInput = row.querySelector('.override-critical');
  const absInput = row.querySelector('.override-is-absolute');
  const provider = provSelect.value;
  const quotas = _overrideQuotasByProvider[provider] || [];
  const prevQuota = quotaSelect.value;

  quotaSelect.innerHTML = '<option value="">Select quota...</option>';
  quotas.forEach(q => {
    const opt = document.createElement('option');
    opt.value = q.key;
    opt.textContent = q.label;
    if (q.key === prevQuota) opt.selected = true;
    quotaSelect.appendChild(opt);
  });

  const isAbs = _isAbsoluteProvider(provider);
  absInput.value = isAbs ? 'true' : 'false';
  if (isAbs) {
    warnInput.removeAttribute('max');
    warnInput.placeholder = 'Warn';
    warnInput.title = 'Warning threshold (absolute value)';
    critInput.removeAttribute('max');
    critInput.placeholder = 'Crit';
    critInput.title = 'Critical threshold (absolute value)';
  } else {
    warnInput.setAttribute('max', '100');
    warnInput.placeholder = 'Warn%';
    warnInput.title = 'Warning threshold (%)';
    critInput.setAttribute('max', '100');
    critInput.placeholder = 'Crit%';
    critInput.title = 'Critical threshold (%)';
  }
}

function addOverrideRow(quotaKey, provider, warning, critical, isAbsolute, disableReset, disableWarning, disableCrit) {
  const list = document.getElementById('override-list');
  if (!list) return;

  // Determine provider from quota key if not provided
  if (!provider && quotaKey) {
    for (const [prov, quotas] of Object.entries(_overrideQuotasByProvider)) {
      if (quotas.some(q => q.key === quotaKey)) { provider = prov; break; }
    }
  }

  const row = document.createElement('div');
  row.className = 'settings-override-row';
  row.innerHTML = `
    <select class="settings-input override-provider-select" style="flex:1">
      <option value="anthropic" ${provider === 'anthropic' ? 'selected' : ''}>Anthropic</option>
      <option value="codex" ${provider === 'codex' ? 'selected' : ''}>Codex</option>
      <option value="copilot" ${provider === 'copilot' ? 'selected' : ''}>Copilot</option>
      <option value="minimax" ${provider === 'minimax' ? 'selected' : ''}>MiniMax</option>
      <option value="antigravity" ${provider === 'antigravity' ? 'selected' : ''}>Antigravity</option>
      <option value="gemini" ${provider === 'gemini' ? 'selected' : ''}>Gemini</option>
      <option value="openrouter" ${provider === 'openrouter' ? 'selected' : ''}>OpenRouter</option>
      <option value="synthetic" ${provider === 'synthetic' ? 'selected' : ''}>Synthetic</option>
      <option value="zai" ${provider === 'zai' ? 'selected' : ''}>Z.ai</option>
    </select>
    <select class="settings-input override-quota" style="flex:2">
      <option value="">Select quota...</option>
    </select>
    <input type="number" class="settings-input settings-input-sm override-warning" value="${warning}" min="0" placeholder="Warn%">
    <input type="number" class="settings-input settings-input-sm override-critical" value="${critical}" min="0" placeholder="Crit%">
    <label class="override-toggle" title="Send warning notifications"><input type="checkbox" class="override-enable-warning" ${!disableWarning ? 'checked' : ''}> Warn</label>
    <label class="override-toggle" title="Send critical notifications"><input type="checkbox" class="override-enable-critical" ${!disableCrit ? 'checked' : ''}> Crit</label>
    <label class="override-toggle" title="Send reset notifications"><input type="checkbox" class="override-enable-reset" ${!disableReset ? 'checked' : ''}> Reset</label>
    <input type="hidden" class="override-provider" value="${provider || 'anthropic'}">
    <input type="hidden" class="override-is-absolute" value="${isAbsolute ? 'true' : 'false'}">
    <button class="override-remove" title="Remove override" type="button">
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M18 6L6 18M6 6l12 12"/></svg>
    </button>
  `;

  const provSelect = row.querySelector('.override-provider-select');
  const hiddenProv = row.querySelector('.override-provider');
  provSelect.addEventListener('change', () => {
    hiddenProv.value = provSelect.value;
    _updateOverrideQuotas(row);
  });

  row.querySelector('.override-remove').addEventListener('click', () => row.remove());
  list.appendChild(row);

  // Populate quota options for the selected provider
  _updateOverrideQuotas(row);

  // Re-select the quota key if restoring
  if (quotaKey) {
    const quotaSelect = row.querySelector('.override-quota');
    quotaSelect.value = quotaKey;
  }
}

// ═══════════════════════════════════════════
// NOTIFICATION CENTER
// ═══════════════════════════════════════════

let _notificationAlerts = [];

async function fetchSystemAlerts() {
  try {
    const res = await authFetch('/api/alerts');
    if (!res.ok) return [];
    const data = await res.json();
    return data.alerts || [];
  } catch (err) {
    console.error('Failed to fetch system alerts:', err);
    return [];
  }
}

function formatRelativeTime(dateStr) {
  const date = new Date(dateStr);
  const now = new Date();
  const diffMs = now - date;
  const diffMin = Math.floor(diffMs / 60000);
  const diffHr = Math.floor(diffMin / 60);
  const diffDay = Math.floor(diffHr / 24);

  if (diffMin < 1) return 'Just now';
  if (diffMin < 60) return `${diffMin}m ago`;
  if (diffHr < 24) return `${diffHr}h ago`;
  if (diffDay < 7) return `${diffDay}d ago`;
  return date.toLocaleDateString();
}

function renderNotificationItem(alert) {
  const iconSvg = alert.severity === 'error'
    ? '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="10"/><path d="M12 8v4M12 16h.01"/></svg>'
    : alert.severity === 'warning'
    ? '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M10.29 3.86L1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z"/><path d="M12 9v4M12 17h.01"/></svg>'
    : '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="10"/><path d="M12 16v-4M12 8h.01"/></svg>';

  return `
    <div class="notification-item" data-alert-id="${alert.id}">
      <div class="notification-icon ${alert.severity || 'info'}">
        ${iconSvg}
      </div>
      <div class="notification-content">
        <div class="notification-item-title">${escapeHtml(alert.title)}</div>
        <div class="notification-item-message">${escapeHtml(alert.message)}</div>
        <div class="notification-meta">
          <span class="notification-provider">${escapeHtml(alert.provider || 'system')}</span>
          <span class="notification-time">${formatRelativeTime(alert.createdAt)}</span>
        </div>
      </div>
      <button class="notification-dismiss" data-dismiss-id="${alert.id}" title="Dismiss" aria-label="Dismiss notification">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
          <path d="M18 6L6 18M6 6l12 12"/>
        </svg>
      </button>
    </div>
  `;
}

function escapeHtml(str) {
  if (!str) return '';
  const div = document.createElement('div');
  div.textContent = str;
  return div.innerHTML;
}

async function updateNotificationCenter() {
  const alerts = await fetchSystemAlerts();
  _notificationAlerts = alerts;

  const badge = document.getElementById('notification-badge');
  const list = document.getElementById('notification-list');

  if (!badge || !list) return;

  // Update badge
  if (alerts.length > 0) {
    badge.textContent = alerts.length > 99 ? '99+' : alerts.length;
    badge.style.display = 'flex';
  } else {
    badge.style.display = 'none';
  }

  // Update list
  if (alerts.length === 0) {
    list.innerHTML = '<div class="notification-empty">No notifications</div>';
  } else {
    list.innerHTML = alerts.map(renderNotificationItem).join('');

    // Add dismiss handlers
    list.querySelectorAll('.notification-dismiss').forEach(btn => {
      btn.addEventListener('click', async (e) => {
        e.stopPropagation();
        const id = parseInt(btn.dataset.dismissId, 10);
        await dismissAlert(id);
      });
    });
  }
}

async function dismissAlert(id) {
  try {
    const res = await authFetch('/api/alerts/dismiss', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ id })
    });
    if (res.ok) {
      await updateNotificationCenter();
    }
  } catch (err) {
    console.error('Failed to dismiss alert:', err);
  }
}

async function dismissAllAlerts() {
  try {
    const res = await authFetch('/api/alerts/dismiss-all', {
      method: 'POST'
    });
    if (res.ok) {
      await updateNotificationCenter();
    }
  } catch (err) {
    console.error('Failed to dismiss all alerts:', err);
  }
}

function initNotificationCenter() {
  const bell = document.getElementById('notification-bell');
  const dropdown = document.getElementById('notification-dropdown');
  const dismissAllBtn = document.getElementById('dismiss-all-alerts');

  if (!bell || !dropdown) return;

  // Toggle dropdown on bell click
  bell.addEventListener('click', (e) => {
    e.stopPropagation();
    const isVisible = dropdown.classList.contains('visible');
    dropdown.classList.toggle('visible', !isVisible);
    bell.setAttribute('aria-expanded', !isVisible);
  });

  // Dismiss all button
  if (dismissAllBtn) {
    dismissAllBtn.addEventListener('click', async (e) => {
      e.stopPropagation();
      await dismissAllAlerts();
    });
  }

  // Close dropdown when clicking outside
  document.addEventListener('click', (e) => {
    if (!bell.contains(e.target) && !dropdown.contains(e.target)) {
      dropdown.classList.remove('visible');
      bell.setAttribute('aria-expanded', 'false');
    }
  });

  // Close dropdown on Escape
  document.addEventListener('keydown', (e) => {
    if (e.key === 'Escape' && dropdown.classList.contains('visible')) {
      dropdown.classList.remove('visible');
      bell.setAttribute('aria-expanded', 'false');
      bell.focus(); // Return focus to bell for keyboard users
    }
  });

  // Initial fetch
  updateNotificationCenter();

  // Refresh notifications periodically (every 60 seconds)
  setInterval(updateNotificationCenter, 60000);
}

// ── Init ──

document.addEventListener('DOMContentLoaded', async () => {
  // Register service worker for PWA + push (all pages)
  if ('serviceWorker' in navigator) {
    navigator.serviceWorker.register(BASE_PATH + '/sw.js').catch(function() {});
  }

  // Settings page has its own initialization
  if (isSettingsPage()) {
    initTheme();
    initLayoutToggle();
    await initSettingsPage();
    return;
  }

  // Redirect to saved default provider if no explicit provider in URL
  // Only when multiple providers are available (tabs exist)
  const urlParams = new URLSearchParams(window.location.search);
  const providerTabs = document.getElementById('provider-tabs');
  if (!urlParams.has('provider') && providerTabs) {
    const savedProvider = loadDefaultProvider();
    if (savedProvider) {
      const availableProviders = [...providerTabs.querySelectorAll('.provider-tab')].map(t => t.dataset.provider);
      // Only redirect if saved provider is available and different from server default
      if (availableProviders.includes(savedProvider) && savedProvider !== availableProviders[0]) {
        window.location.href = `${BASE_PATH}/?provider=${savedProvider}`;
        return;
      }
    }
  }

  // Load persisted state (localStorage only - no API calls before auth check)
  loadHiddenQuotas();
  loadCodexAccount();
  if (getCurrentProvider() === 'codex') {
    await loadCodexProfiles();
  } else {
    updateCodexProfileTabsVisibility();
  }
  initCodexProfileTabs();

  loadMiniMaxAccount();
  if (getCurrentProvider() === 'minimax') {
    await loadMiniMaxAccounts();
  } else {
    updateMiniMaxAccountTabsVisibility();
  }
  initMiniMaxAccountTabs();

  initTheme();
  initLayoutToggle();
  initTimezoneBadge();
  setupProviderSelector();
  setupRangeSelector();
  setupCycleFilters();
  setupPasswordToggle();
  setupTableControls();
  initCollapsibleSections();
  await setupOverviewControls();
  setupHeaderActions();
  setupCardModals();
  initNotificationCenter();

  if (document.getElementById('usage-chart') || document.getElementById('both-view') || document.getElementById('all-providers-container')) {
    initChart();

    // Critical path: fetch above-fold data in parallel
    Promise.all([
      loadHiddenInsights(),
      fetchCurrent(),
      fetchDeepInsights(),
      fetchHistory('6h'),
    ]);

    // Preload providers whose history tables should appear immediately.
    const activeProvider = getCurrentProvider();
    const eagerHistoryProviders = new Set(['antigravity', 'minimax', 'gemini']);
    if (eagerHistoryProviders.has(activeProvider) && shouldShowHistoryTables(activeProvider)) {
      _lazyLoaded.add('.cycles-section');
      _lazyLoaded.add('.sessions-section');
      fetchCycles();
      fetchSessions();
      if (activeProvider !== 'gemini') {
        _lazyLoaded.add('.cycle-overview-section');
        fetchCycleOverview();
      }
    }

    // Hide cycle overview section entirely for Gemini
    const overviewSection = document.querySelector('.cycle-overview-section');
    if (overviewSection) {
      overviewSection.style.display = activeProvider === 'gemini' ? 'none' : '';
    }

    // Below-fold: lazy-load when sections scroll into view
    if (shouldShowHistoryTables(activeProvider)) {
      lazyLoadOnVisible('.cycles-section', () => fetchCycles());
      if (activeProvider !== 'gemini') {
        lazyLoadOnVisible('.cycle-overview-section', () => fetchCycleOverview());
      }
      lazyLoadOnVisible('.sessions-section', () => fetchSessions());
    }

    startCountdowns();
    startAutoRefresh();

    // Check for updates on load and every 60 minutes
    checkForUpdate();
    setInterval(checkForUpdate, 3600000);

    // Update button click handler
    const updateBtn = document.getElementById('update-btn');
    if (updateBtn) {
      updateBtn.addEventListener('click', applyUpdate);
    }

    // Update sessions table header for "both" mode
    const provider = getCurrentProvider();
    if (provider === 'both') {
      const sessionsHead = document.querySelector('#sessions-table thead tr');
      if (sessionsHead) {
        sessionsHead.innerHTML = `
          <th data-sort-key="provider" role="button" tabindex="0">Provider <span class="sort-arrow"></span></th>
          <th data-sort-key="id" role="button" tabindex="0">Session <span class="sort-arrow"></span></th>
          <th data-sort-key="start" role="button" tabindex="0">Started <span class="sort-arrow"></span></th>
          <th data-sort-key="end" role="button" tabindex="0">Ended <span class="sort-arrow"></span></th>
          <th data-sort-key="duration" role="button" tabindex="0">Duration <span class="sort-arrow"></span></th>
          <th data-sort-key="snapshots" role="button" tabindex="0">Snapshots <span class="sort-arrow"></span></th>
        `;
      }
      // Update cycles table for "both" - add provider column
      const cyclesHead = document.querySelector('#cycles-table thead tr');
      if (cyclesHead) {
        cyclesHead.innerHTML = `
          <th data-sort-key="provider" role="button" tabindex="0">Provider <span class="sort-arrow"></span></th>
          <th data-sort-key="id" role="button" tabindex="0">Cycle <span class="sort-arrow"></span></th>
          <th data-sort-key="start" role="button" tabindex="0">Start <span class="sort-arrow"></span></th>
          <th data-sort-key="end" role="button" tabindex="0">End <span class="sort-arrow"></span></th>
          <th data-sort-key="duration" role="button" tabindex="0">Duration <span class="sort-arrow"></span></th>
          <th data-sort-key="peak" role="button" tabindex="0">Peak <span class="sort-arrow"></span></th>
          <th data-sort-key="total" role="button" tabindex="0">Total <span class="sort-arrow"></span></th>
          <th data-sort-key="rate" role="button" tabindex="0">Rate <span class="sort-arrow"></span></th>
        `;
      }
      // Re-attach sort event listeners (headers were replaced, losing original listeners)
      document.querySelectorAll('#cycles-table th[data-sort-key]').forEach(th => {
        th.addEventListener('click', () => handleTableSort('cycles', th));
        th.addEventListener('keydown', e => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); handleTableSort('cycles', th); } });
      });
      document.querySelectorAll('#sessions-table th[data-sort-key]').forEach(th => {
        th.addEventListener('click', () => handleTableSort('sessions', th));
        th.addEventListener('keydown', e => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); handleTableSort('sessions', th); } });
      });
    }
  }

});
