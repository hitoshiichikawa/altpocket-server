const CLIENT_ID = 'YOUR_EXTENSION_CLIENT_ID';
const API_BASE = 'https://YOUR_API_BASE_URL';

const loginScreenEl = document.getElementById('loginScreen');
const readerScreenEl = document.getElementById('readerScreen');
const loginBtn = document.getElementById('login');
const signOutBtn = document.getElementById('signOut');
const openWebUIBtn = document.getElementById('openWebUI');
const utilityStatusEl = document.getElementById('utilityStatus');

const saveBtn = document.getElementById('save');
const tagInputEl = document.getElementById('tagInput');
const suggestionsEl = document.getElementById('suggestions');
const tagsEl = document.getElementById('tags');

const searchInputEl = document.getElementById('searchInput');
const resultMetaEl = document.getElementById('resultMeta');
const itemListEl = document.getElementById('itemList');

let token = '';
let tags = [];
let searchTimer = null;
let lastFetchID = 0;
let lastSuggestionFetchID = 0;

const CONTENT_CAPTURE_LIMIT = 200_000;

function setUtilityStatus(message = '', level = 'default') {
  if (!utilityStatusEl) return;
  utilityStatusEl.textContent = message;
  utilityStatusEl.classList.remove('is-success', 'is-error');
  if (level === 'success') utilityStatusEl.classList.add('is-success');
  if (level === 'error') utilityStatusEl.classList.add('is-error');
}

function setScreenMode(mode) {
  const readerMode = mode === 'reader';
  if (document.body) {
    document.body.dataset.screen = readerMode ? 'reader' : 'login';
  }
  if (loginScreenEl) {
    loginScreenEl.hidden = readerMode;
    loginScreenEl.setAttribute('aria-hidden', readerMode ? 'true' : 'false');
  }
  if (readerScreenEl) {
    readerScreenEl.hidden = !readerMode;
    readerScreenEl.setAttribute('aria-hidden', readerMode ? 'false' : 'true');
  }
}

function showLoginScreen() {
  token = '';
  tags = [];
  renderTags();
  if (tagInputEl) tagInputEl.value = '';
  if (suggestionsEl) suggestionsEl.innerHTML = '';
  if (searchInputEl) searchInputEl.value = '';
  if (itemListEl) itemListEl.innerHTML = '';
  if (resultMetaEl) resultMetaEl.textContent = '0件';
  setScreenMode('login');
  setUtilityStatus('');
}

function showReaderScreen() {
  setScreenMode('reader');
}

function getConfiguredAPIBase() {
  const configured = API_BASE.trim().replace(/\/+$/, '');
  if (!configured || configured.includes('YOUR_API_BASE_URL')) {
    return '';
  }
  try {
    const parsed = new URL(configured);
    if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') {
      return '';
    }
    return parsed.origin;
  } catch {
    return '';
  }
}

function apiOriginPattern(apiBase) {
  try {
    const parsed = new URL(apiBase);
    if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') {
      return '';
    }
    return `${parsed.origin}/*`;
  } catch {
    return '';
  }
}

async function ensureAPIAccessPermission(apiBase, options = {}) {
  const { interactive = false, silent = false } = options;
  const originPattern = apiOriginPattern(apiBase);
  if (!originPattern) {
    if (!silent) setUtilityStatus('API base is not configured', 'error');
    return false;
  }
  if (!chrome.permissions || typeof chrome.permissions.contains !== 'function') {
    return true;
  }

  const request = { origins: [originPattern] };
  const hasPermission = await chrome.permissions.contains(request);
  if (hasPermission) return true;

  if (!interactive || typeof chrome.permissions.request !== 'function') {
    if (!silent) setUtilityStatus('Allow site access to API base', 'error');
    return false;
  }
  const granted = await chrome.permissions.request(request);
  if (!granted && !silent) {
    setUtilityStatus('Site access permission is required', 'error');
  }
  return granted;
}

function parseJSONSafe(text) {
  if (!text) return null;
  try {
    return JSON.parse(text);
  } catch {
    return null;
  }
}

async function readResponseBody(res) {
  const text = await res.text().catch(() => '');
  const data = parseJSONSafe(text);
  return { text, data };
}

function errorMessage(err, fallback) {
  if (!err) return fallback;
  if (typeof err.message === 'string' && err.message) return err.message;
  return fallback;
}

function apiErrorMessage(status, data, fallback) {
  if (data && typeof data.error === 'string' && data.error) {
    if (data.error === 'user_not_registered') {
      return 'Account is not registered on this server';
    }
    return data.error;
  }
  return `${fallback} (${status})`;
}

function isAuthFailureStatus(status) {
  return status === 401 || status === 403;
}

function parseFragment(fragment) {
  if (!fragment) return '';
  const params = new URLSearchParams(fragment.replace(/^#/, ''));
  return params.get('id_token') || '';
}

async function clearStoredToken() {
  token = '';
  try {
    if (!chrome.storage || !chrome.storage.local) return;
    const tokenKeys = new Set(['token']);
    if (typeof chrome.storage.local.get === 'function') {
      const stored = await chrome.storage.local.get(null);
      if (stored && typeof stored === 'object') {
        for (const key of Object.keys(stored)) {
          if (/token/i.test(key)) tokenKeys.add(key);
        }
      }
    }
    if (typeof chrome.storage.local.remove === 'function') {
      await chrome.storage.local.remove(Array.from(tokenKeys));
      return;
    }
    if (typeof chrome.storage.local.set === 'function') {
      const patch = {};
      for (const key of tokenKeys) {
        patch[key] = '';
      }
      await chrome.storage.local.set(patch);
    }
  } catch {
    // Keep UI transition even if storage cleanup fails.
  }
}

function escapeHTML(text) {
  return String(text)
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;')
    .replaceAll("'", '&#39;');
}

function normalizeTag(value) {
  return value.trim();
}

function renderTags() {
  if (!tagsEl) return;
  tagsEl.innerHTML = '';
  for (const [idx, tag] of tags.entries()) {
    const chip = document.createElement('span');
    chip.className = 'chip';
    chip.textContent = tag;
    const removeBtn = document.createElement('button');
    removeBtn.type = 'button';
    removeBtn.textContent = '×';
    removeBtn.addEventListener('click', () => {
      tags.splice(idx, 1);
      renderTags();
    });
    chip.appendChild(removeBtn);
    tagsEl.appendChild(chip);
  }
}

function addTag(value) {
  const tag = normalizeTag(value);
  if (!tag) return;
  if (tags.includes(tag)) return;
  tags.push(tag);
  renderTags();
}

function renderSuggestions(list) {
  if (!suggestionsEl) return;
  suggestionsEl.innerHTML = '';
  if (!Array.isArray(list) || list.length === 0) return;
  for (const item of list) {
    const name = typeof item?.name === 'string' ? item.name : '';
    if (!name) continue;
    const btn = document.createElement('button');
    btn.type = 'button';
    btn.textContent = name;
    btn.addEventListener('click', () => {
      addTag(name);
      suggestionsEl.innerHTML = '';
      if (tagInputEl) tagInputEl.value = '';
    });
    suggestionsEl.appendChild(btn);
  }
}

async function fetchTagSuggestions() {
  if (!tagInputEl || !token) return;
  const query = tagInputEl.value.trim();
  if (query.length < 1) {
    renderSuggestions([]);
    return;
  }

  const apiBase = getConfiguredAPIBase();
  if (!apiBase) return;
  const granted = await ensureAPIAccessPermission(apiBase, { silent: true });
  if (!granted) {
    renderSuggestions([]);
    return;
  }

  const fetchID = ++lastSuggestionFetchID;
  let res;
  try {
    res = await fetch(`${apiBase}/v1/tags?q=${encodeURIComponent(query)}`, {
      headers: { 'Authorization': `Bearer ${token}` },
    });
  } catch {
    if (fetchID !== lastSuggestionFetchID) return;
    renderSuggestions([]);
    return;
  }

  if (fetchID !== lastSuggestionFetchID) return;
  if (!res.ok) {
    if (isAuthFailureStatus(res.status)) {
      await logout({ silent: true });
      showLoginScreen();
    }
    renderSuggestions([]);
    return;
  }

  const { data } = await readResponseBody(res);
  renderSuggestions(Array.isArray(data) ? data : []);
}

function renderTagList(tagsValue) {
  if (!Array.isArray(tagsValue) || tagsValue.length === 0) {
    return '<span class="tag">(no tags)</span>';
  }

  return tagsValue
    .map((tag) => {
      if (typeof tag === 'string') return tag;
      if (tag && typeof tag.name === 'string') return tag.name;
      if (tag && typeof tag.normalized_name === 'string') return tag.normalized_name;
      return '';
    })
    .filter(Boolean)
    .map((name) => `<span class="tag">${escapeHTML(name)}</span>`)
    .join('');
}

function renderItems(items) {
  if (!itemListEl) return;
  if (!Array.isArray(items) || items.length === 0) {
    itemListEl.innerHTML = '<li><p class="empty">条件に一致する記事がありません。</p></li>';
    if (resultMetaEl) resultMetaEl.textContent = '0件';
    return;
  }

  if (resultMetaEl) resultMetaEl.textContent = `${items.length}件`;
  itemListEl.innerHTML = '';

  for (const item of items) {
    const title = typeof item?.title === 'string' && item.title.trim() !== '' ? item.title : '(untitled)';
    const url = typeof item?.url === 'string' ? item.url : '#';
    const li = document.createElement('li');
    li.innerHTML = `
      <article class="item-card">
        <h3 class="item-title">${escapeHTML(title)}</h3>
        <div class="item-tags">${renderTagList(item?.tags)}</div>
        <div class="item-actions">
          <a class="show-original" href="${escapeHTML(url)}" target="_blank" rel="noopener noreferrer">Show original</a>
        </div>
      </article>
    `;
    itemListEl.appendChild(li);
  }
}

async function fetchItems(query) {
  if (!token) return;

  const apiBase = getConfiguredAPIBase();
  if (!apiBase) {
    setUtilityStatus('API base is not configured', 'error');
    return;
  }

  const granted = await ensureAPIAccessPermission(apiBase, { interactive: false });
  if (!granted) return;

  const fetchID = ++lastFetchID;
  setUtilityStatus('Loading...');

  const params = new URLSearchParams();
  params.set('page', '1');
  params.set('per_page', '50');
  if (query && query.trim() !== '') {
    params.set('q', query.trim());
    params.set('sort', 'relevance');
  } else {
    params.set('sort', 'newest');
  }

  let res;
  try {
    res = await fetch(`${apiBase}/v1/items?${params.toString()}`, {
      headers: { 'Authorization': `Bearer ${token}` },
    });
  } catch {
    if (fetchID !== lastFetchID) return;
    setUtilityStatus('Network error', 'error');
    return;
  }

  if (fetchID !== lastFetchID) return;

  const { data } = await readResponseBody(res);
  if (!res.ok) {
    if (isAuthFailureStatus(res.status)) {
      await logout({ silent: true });
      showLoginScreen();
      return;
    }
    setUtilityStatus(apiErrorMessage(res.status, data, 'Failed to load items'), 'error');
    return;
  }

  renderItems(Array.isArray(data?.items) ? data.items : []);
  setUtilityStatus('');
}

function requestItems(query) {
  if (searchTimer) clearTimeout(searchTimer);
  searchTimer = setTimeout(() => {
    void fetchItems(query);
  }, 180);
}

async function extractPageCapture(tabID) {
  if (!chrome.scripting || typeof chrome.scripting.executeScript !== 'function') {
    return null;
  }
  if (typeof tabID !== 'number') {
    return null;
  }

  try {
    const results = await chrome.scripting.executeScript({
      target: { tabId: tabID },
      func: (limit) => {
        const normalize = (v) => v.trim().replace(/\s+/g, ' ');
        const truncate = (v, max) => (v.length > max ? v.slice(0, max) : v);
        const selectorsToDrop = [
          'script',
          'style',
          'noscript',
          'template',
          'svg',
          'canvas',
          'iframe',
          'nav',
          'aside',
          'footer',
          'form',
          '[hidden]',
          '[aria-hidden="true"]',
        ];

        const source = document.querySelector('article, main, [role="main"]') || document.body;
        if (!source) return null;

        const clone = source.cloneNode(true);
        for (const selector of selectorsToDrop) {
          clone.querySelectorAll(selector).forEach((node) => node.remove());
        }

        const rawText = clone.innerText || clone.textContent || '';
        const contentFull = truncate(normalize(rawText), limit);
        if (!contentFull) return null;

        return {
          title: normalize(document.title || ''),
          content_full: contentFull,
        };
      },
      args: [CONTENT_CAPTURE_LIMIT],
    });

    if (!Array.isArray(results) || results.length === 0) return null;
    const value = results[0]?.result;
    if (!value || typeof value.content_full !== 'string' || value.content_full === '') return null;
    return {
      title: typeof value.title === 'string' ? value.title : '',
      content_full: value.content_full,
    };
  } catch {
    return null;
  }
}

async function sendCapturedContent(apiBase, itemID, capture) {
  if (!capture || !capture.content_full || !token) return;

  let res;
  try {
    res = await fetch(`${apiBase}/v1/items/${encodeURIComponent(itemID)}/capture`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'Authorization': `Bearer ${token}`,
      },
      body: JSON.stringify(capture),
    });
  } catch {
    return;
  }

  if (isAuthFailureStatus(res.status)) {
    await logout({ silent: true });
    showLoginScreen();
  }
}

async function saveCurrentTab() {
  const apiBase = getConfiguredAPIBase();
  if (!apiBase) {
    setUtilityStatus('API base is not configured', 'error');
    return;
  }
  if (!token) {
    showLoginScreen();
    return;
  }

  if (saveBtn) saveBtn.disabled = true;

  try {
    const granted = await ensureAPIAccessPermission(apiBase, { interactive: true });
    if (!granted) return;

    const [tab] = await chrome.tabs.query({ active: true, currentWindow: true });
    if (!tab || !tab.url) {
      setUtilityStatus('No tab URL', 'error');
      return;
    }

    let res;
    try {
      res = await fetch(`${apiBase}/v1/items`, {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          'Authorization': `Bearer ${token}`,
        },
        body: JSON.stringify({ url: tab.url, tags }),
      });
    } catch (err) {
      setUtilityStatus(`Save request failed: ${errorMessage(err, 'network error')}`, 'error');
      return;
    }

    const { data } = await readResponseBody(res);
    if (!res.ok) {
      if (isAuthFailureStatus(res.status)) {
        await logout({ silent: true });
        showLoginScreen();
        return;
      }
      setUtilityStatus(apiErrorMessage(res.status, data, 'Save failed'), 'error');
      return;
    }

    const itemID = typeof data?.item_id === 'string' ? data.item_id : '';
    const created = data?.created === true;
    setUtilityStatus('Saved', 'success');
    requestItems(searchInputEl?.value || '');

    if (!created || itemID === '') return;
    void (async () => {
      const capture = await extractPageCapture(tab.id);
      await sendCapturedContent(apiBase, itemID, capture);
    })();
  } finally {
    if (saveBtn) saveBtn.disabled = false;
  }
}

async function openWebUI() {
  const apiBase = getConfiguredAPIBase();
  if (!apiBase) {
    setUtilityStatus('API base is not configured', 'error');
    return;
  }
  const webURL = `${apiBase}/ui/items`;
  try {
    if (chrome.tabs && typeof chrome.tabs.create === 'function') {
      await chrome.tabs.create({ url: webURL });
      return;
    }
    window.open(webURL, '_blank', 'noopener,noreferrer');
  } catch {
    setUtilityStatus('Failed to open website', 'error');
  }
}

async function login() {
  const apiBase = getConfiguredAPIBase();
  if (!apiBase) {
    alert('Extension API base is not configured');
    return;
  }

  if (loginBtn) {
    loginBtn.disabled = true;
    loginBtn.textContent = 'Signing in...';
  }

  try {
    const granted = await ensureAPIAccessPermission(apiBase, { interactive: true });
    if (!granted) return;

    const redirectURL = chrome.identity.getRedirectURL();
    const nonce = crypto.getRandomValues(new Uint8Array(16)).join('');
    const authURL = new URL('https://accounts.google.com/o/oauth2/v2/auth');
    authURL.searchParams.set('client_id', CLIENT_ID);
    authURL.searchParams.set('response_type', 'id_token');
    authURL.searchParams.set('redirect_uri', redirectURL);
    authURL.searchParams.set('scope', 'openid email profile');
    authURL.searchParams.set('nonce', nonce);

    const resultURL = await chrome.identity.launchWebAuthFlow({
      url: authURL.toString(),
      interactive: true,
    });
    if (!resultURL) return;

    const idToken = parseFragment(new URL(resultURL).hash);
    if (!idToken) {
      alert('Login failed: id token missing');
      return;
    }

    const res = await fetch(`${apiBase}/v1/auth/extension/exchange`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ id_token: idToken }),
    });
    const { data } = await readResponseBody(res);
    if (!res.ok) {
      alert(apiErrorMessage(res.status, data, 'Exchange failed'));
      return;
    }
    if (!data || typeof data.token !== 'string' || data.token === '') {
      alert('Exchange failed: token missing');
      return;
    }

    token = data.token;
    await chrome.storage.local.set({ token });
    showReaderScreen();
    await fetchItems('');
  } catch {
    alert('Login error');
  } finally {
    if (loginBtn) {
      loginBtn.disabled = false;
      loginBtn.textContent = 'Sign in with Google';
    }
  }
}

async function logout(options = {}) {
  const { silent = false } = options;
  if (!silent) setUtilityStatus('Signing out...');
  if (signOutBtn) signOutBtn.disabled = true;

  try {
    await clearStoredToken();
    if (chrome.identity && typeof chrome.identity.clearAllCachedAuthTokens === 'function') {
      await chrome.identity.clearAllCachedAuthTokens();
    }
    if (!silent) setUtilityStatus('Signed out', 'success');
  } catch {
    if (!silent) setUtilityStatus('Sign out failed', 'error');
  } finally {
    if (signOutBtn) signOutBtn.disabled = false;
  }
}

if (loginBtn) {
  loginBtn.addEventListener('click', () => {
    void login();
  });
}

if (signOutBtn) {
  signOutBtn.addEventListener('click', async () => {
    await logout();
    showLoginScreen();
  });
}

if (openWebUIBtn) {
  openWebUIBtn.addEventListener('click', () => {
    void openWebUI();
  });
}

if (saveBtn) {
  saveBtn.addEventListener('click', () => {
    void saveCurrentTab();
  });
}

if (tagInputEl) {
  tagInputEl.addEventListener('keydown', (event) => {
    if (event.key !== 'Enter' && event.key !== ',') return;
    event.preventDefault();
    addTag(tagInputEl.value.replace(',', ''));
    tagInputEl.value = '';
    renderSuggestions([]);
  });

  tagInputEl.addEventListener('input', () => {
    void fetchTagSuggestions();
  });

  tagInputEl.addEventListener('blur', () => {
    renderSuggestions([]);
  });
}

if (searchInputEl) {
  searchInputEl.addEventListener('input', (event) => {
    requestItems(event.target.value || '');
  });

  searchInputEl.addEventListener('keydown', (event) => {
    if (event.key !== 'Escape') return;
    if (searchInputEl.value === '') return;
    searchInputEl.value = '';
    requestItems('');
  });
}

(async () => {
  try {
    const data = await chrome.storage.local.get(['token']);
    if (typeof data?.token === 'string' && data.token.trim() !== '') {
      token = data.token;
      showReaderScreen();
      await fetchItems('');
      return;
    }
  } catch {
    // Fall back to login screen.
  }
  showLoginScreen();
})();
