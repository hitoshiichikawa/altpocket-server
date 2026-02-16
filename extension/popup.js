const CLIENT_ID = 'YOUR_EXTENSION_CLIENT_ID';
const API_BASE = (
  typeof globalThis.ALTPocketAPIBase === 'string' && globalThis.ALTPocketAPIBase !== ''
    ? globalThis.ALTPocketAPIBase
    : 'https://YOUR_API_BASE_URL'
).trim().replace(/\/+$/, '');

const authControlsEl = document.getElementById('authControls');
const loginBtn = document.getElementById('login');
const saveBtn = document.getElementById('save');
const openWebUIBtn = document.getElementById('openWebUI');
const statusEl = document.getElementById('status');
const tagInput = document.getElementById('tagInput');
const tagsEl = document.getElementById('tags');
const suggestionsEl = document.getElementById('suggestions');

let tags = [];
let token = null;

const CONTENT_CAPTURE_LIMIT = 200_000;

function setAuthControlsVisible(visible) {
  if (!authControlsEl) return;
  authControlsEl.hidden = !visible;
}

function setLoginButtonAuthenticated(authenticated) {
  if (!loginBtn) return;
  loginBtn.textContent = authenticated ? 'Sign out' : 'Sign in with Google';
}

function setStatus(msg, level = 'info') {
  const classes = {
    info: 'status status-info',
    success: 'status status-success',
    error: 'status status-error',
  };
  statusEl.textContent = msg;
  statusEl.className = classes[level] || classes.info;
}

function setError(msg) {
  setStatus(msg, 'error');
}

function setSuccess(msg) {
  setStatus(msg, 'success');
}

async function clearStoredToken() {
  token = null;
  try {
    if (chrome.storage && chrome.storage.local) {
      if (typeof chrome.storage.local.remove === 'function') {
        await chrome.storage.local.remove('token');
      } else if (typeof chrome.storage.local.set === 'function') {
        await chrome.storage.local.set({ token: '' });
      }
    }
  } catch {
    // Ignore storage cleanup failures and keep UI in unauthenticated mode.
  }
}

function showUnauthenticatedUI(message = 'Not logged in', level = 'info') {
  token = null;
  tags = [];
  renderTags();
  if (tagInput) tagInput.value = '';
  if (suggestionsEl) suggestionsEl.innerHTML = '';
  setAuthControlsVisible(false);
  setLoginButtonAuthenticated(false);
  setStatus(message, level);
}

async function moveToUnauthenticated(message = 'Not logged in', level = 'info') {
  await clearStoredToken();
  showUnauthenticatedUI(message, level);
}

function moveToAuthenticated(message = 'Ready') {
  setAuthControlsVisible(true);
  setLoginButtonAuthenticated(true);
  setSuccess(message);
}

function getConfiguredAPIBase() {
  if (!API_BASE || API_BASE.includes('YOUR_API_BASE_URL')) {
    return '';
  }
  try {
    const parsed = new URL(API_BASE);
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
    if (!silent) setError('Extension API base is not configured');
    return false;
  }
  if (!chrome.permissions || typeof chrome.permissions.contains !== 'function') {
    return true;
  }

  const request = { origins: [originPattern] };
  const hasPermission = await chrome.permissions.contains(request);
  if (hasPermission) {
    return true;
  }
  if (!interactive || typeof chrome.permissions.request !== 'function') {
    if (!silent) setError('Allow site access to this API Base URL');
    return false;
  }

  const granted = await chrome.permissions.request(request);
  if (!granted && !silent) {
    setError('Site access permission is required');
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

async function extractPageCapture(tabId) {
  if (!chrome.scripting || typeof chrome.scripting.executeScript !== 'function') {
    return null;
  }
  if (typeof tabId !== 'number') {
    return null;
  }

  try {
    const results = await chrome.scripting.executeScript({
      target: { tabId },
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
        if (!source) {
          return null;
        }

        const clone = source.cloneNode(true);
        for (const selector of selectorsToDrop) {
          clone.querySelectorAll(selector).forEach((node) => node.remove());
        }

        const rawText = clone.innerText || clone.textContent || '';
        const contentFull = truncate(normalize(rawText), limit);
        if (!contentFull) {
          return null;
        }

        return {
          title: normalize(document.title || ''),
          content_full: contentFull,
        };
      },
      args: [CONTENT_CAPTURE_LIMIT],
    });
    if (!Array.isArray(results) || results.length === 0) {
      return null;
    }
    const value = results[0]?.result;
    if (!value || typeof value.content_full !== 'string' || value.content_full === '') {
      return null;
    }
    return {
      title: typeof value.title === 'string' ? value.title : '',
      content_full: value.content_full,
    };
  } catch {
    return null;
  }
}

async function sendCapturedContent(apiBase, itemID, capture) {
  if (!capture || !capture.content_full) {
    return;
  }
  if (!token) {
    return;
  }

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
    await moveToUnauthenticated('Session expired. Please sign in again.', 'error');
  }
}

function renderTags() {
  tagsEl.innerHTML = '';
  tags.forEach((tag, idx) => {
    const chip = document.createElement('span');
    chip.className = 'chip';
    chip.textContent = tag;
    const remove = document.createElement('button');
    remove.textContent = '×';
    remove.addEventListener('click', () => {
      tags.splice(idx, 1);
      renderTags();
    });
    chip.appendChild(remove);
    tagsEl.appendChild(chip);
  });
}

function renderSuggestions(list) {
  suggestionsEl.innerHTML = '';
  if (!list || list.length === 0) return;
  list.forEach((t) => {
    const btn = document.createElement('button');
    btn.textContent = t.name;
    btn.addEventListener('click', () => {
      addTag(t.name);
      suggestionsEl.innerHTML = '';
      tagInput.value = '';
    });
    suggestionsEl.appendChild(btn);
  });
}

function addTag(value) {
  const t = value.trim();
  if (!t) return;
  if (tags.includes(t)) return;
  tags.push(t);
  renderTags();
}

function parseFragment(fragment) {
  if (!fragment) return '';
  const params = new URLSearchParams(fragment.replace(/^#/, ''));
  return params.get('id_token');
}

async function login() {
  const apiBase = getConfiguredAPIBase();
  if (!apiBase) {
    setError('Extension API base is not configured');
    return;
  }

  try {
    const hasAccess = await ensureAPIAccessPermission(apiBase, { interactive: true });
    if (!hasAccess) {
      return;
    }

    setStatus('Signing in...');

    const redirectUrl = chrome.identity.getRedirectURL();
    const nonce = crypto.getRandomValues(new Uint8Array(16)).join('');
    const authUrl = new URL('https://accounts.google.com/o/oauth2/v2/auth');
    authUrl.searchParams.set('client_id', CLIENT_ID);
    authUrl.searchParams.set('response_type', 'id_token');
    authUrl.searchParams.set('redirect_uri', redirectUrl);
    authUrl.searchParams.set('scope', 'openid email profile');
    authUrl.searchParams.set('nonce', nonce);

    const resultUrl = await chrome.identity.launchWebAuthFlow({
      url: authUrl.toString(),
      interactive: true,
    });
    if (!resultUrl) {
      setError('Login canceled');
      return;
    }

    const parsedResultURL = new URL(resultUrl);
    const idToken = parseFragment(parsedResultURL.hash);
    if (!idToken) {
      setError('Login failed');
      return;
    }

    let res;
    try {
      res = await fetch(`${apiBase}/v1/auth/extension/exchange`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ id_token: idToken }),
      });
    } catch (err) {
      setError(`Exchange request failed: ${errorMessage(err, 'network error')}`);
      return;
    }

    const { data } = await readResponseBody(res);
    if (!res.ok) {
      setError(apiErrorMessage(res.status, data, 'Exchange failed'));
      return;
    }
    if (!data || typeof data.token !== 'string' || data.token === '') {
      setError('Exchange failed: token missing');
      return;
    }

    token = data.token;
    await chrome.storage.local.set({ token });
    moveToAuthenticated('Logged in');
  } catch (err) {
    setError(`Login error: ${errorMessage(err, 'unexpected error')}`);
  }
}

async function signOut() {
  await moveToUnauthenticated('Signed out');
}

async function saveCurrentTab() {
  const apiBase = getConfiguredAPIBase();
  if (!apiBase) {
    setError('Extension API base is not configured');
    return;
  }
  if (!token) {
    await moveToUnauthenticated('Login required', 'error');
    return;
  }

  try {
    const hasAccess = await ensureAPIAccessPermission(apiBase, { interactive: true });
    if (!hasAccess) {
      return;
    }

    const [tab] = await chrome.tabs.query({ active: true, currentWindow: true });
    if (!tab || !tab.url) {
      setError('No tab URL');
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
      setError(`Save request failed: ${errorMessage(err, 'network error')}`);
      return;
    }

    if (!res.ok) {
      const { data } = await readResponseBody(res);
      if (isAuthFailureStatus(res.status)) {
        await moveToUnauthenticated('Session expired. Please sign in again.', 'error');
        return;
      }
      setError(apiErrorMessage(res.status, data, 'Save failed'));
      return;
    }

    const { data } = await readResponseBody(res);
    const itemID = typeof data?.item_id === 'string' ? data.item_id : '';
    const created = data?.created === true;
    setSuccess('Saved');

    if (!created || itemID === '') {
      return;
    }

    void (async () => {
      const capture = await extractPageCapture(tab.id);
      await sendCapturedContent(apiBase, itemID, capture);
    })();
  } catch (err) {
    setError(`Save error: ${errorMessage(err, 'unexpected error')}`);
  }
}

async function openWebUI() {
  const apiBase = getConfiguredAPIBase();
  if (!apiBase) {
    setError('Extension API base is not configured');
    return;
  }
  const webURL = `${apiBase}/ui/items`;
  try {
    if (chrome.tabs && typeof chrome.tabs.create === 'function') {
      await chrome.tabs.create({ url: webURL });
      return;
    }
    window.open(webURL, '_blank', 'noopener,noreferrer');
  } catch (err) {
    setError(`Failed to open Web App: ${errorMessage(err, 'unexpected error')}`);
  }
}

loginBtn.addEventListener('click', async () => {
  if (token) {
    await signOut();
    return;
  }
  await login();
});

saveBtn.addEventListener('click', () => saveCurrentTab());
if (openWebUIBtn) {
  openWebUIBtn.addEventListener('click', () => openWebUI());
}

tagInput.addEventListener('keydown', (e) => {
  if (e.key === 'Enter' || e.key === ',') {
    e.preventDefault();
    addTag(tagInput.value.replace(',', ''));
    tagInput.value = '';
  }
});

tagInput.addEventListener('input', async () => {
  const apiBase = getConfiguredAPIBase();
  const q = tagInput.value.trim();
  if (!apiBase || !token || q.length < 1) {
    suggestionsEl.innerHTML = '';
    return;
  }
  try {
    const hasAccess = await ensureAPIAccessPermission(apiBase, { silent: true });
    if (!hasAccess) {
      suggestionsEl.innerHTML = '';
      return;
    }

    const res = await fetch(`${apiBase}/v1/tags?q=${encodeURIComponent(q)}`, {
      headers: { 'Authorization': `Bearer ${token}` },
    });
    if (!res.ok) {
      if (isAuthFailureStatus(res.status)) {
        await moveToUnauthenticated('Session expired. Please sign in again.', 'error');
      }
      suggestionsEl.innerHTML = '';
      return;
    }
    const { data } = await readResponseBody(res);
    renderSuggestions(Array.isArray(data) ? data : []);
  } catch {
    suggestionsEl.innerHTML = '';
  }
});

tagInput.addEventListener('blur', () => {
  if (tagInput.value.trim()) {
    addTag(tagInput.value);
    tagInput.value = '';
  }
});

(async () => {
  try {
    setAuthControlsVisible(false);
    setLoginButtonAuthenticated(false);
    const data = await chrome.storage.local.get(['token']);
    if (typeof data.token === 'string' && data.token.trim() !== '') {
      token = data.token;
      moveToAuthenticated('Ready');
      return;
    }
    showUnauthenticatedUI('Not logged in');
  } catch (err) {
    setError(`Init error: ${errorMessage(err, 'storage unavailable')}`);
  }
})();

if (typeof globalThis.addEventListener === 'function') {
  globalThis.addEventListener('unhandledrejection', (event) => {
    setError(`Unhandled rejection: ${errorMessage(event.reason, 'unknown error')}`);
  });
  globalThis.addEventListener('error', (event) => {
    const fallback = typeof event.message === 'string' && event.message ? event.message : 'unknown error';
    setError(`Unexpected error: ${fallback}`);
  });
}
