const CLIENT_ID = 'YOUR_EXTENSION_CLIENT_ID';

const apiBaseInput = document.getElementById('apiBase');
const loginBtn = document.getElementById('login');
const saveBtn = document.getElementById('save');
const statusEl = document.getElementById('status');
const tagInput = document.getElementById('tagInput');
const tagsEl = document.getElementById('tags');
const suggestionsEl = document.getElementById('suggestions');

let tags = [];
let token = null;

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

function normalizeAPIBase(value) {
  return value.trim().replace(/\/+$/, '');
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
  const apiBase = normalizeAPIBase(apiBaseInput.value);
  if (!apiBase) {
    setError('Set API Base URL');
    return;
  }
  apiBaseInput.value = apiBase;
  setStatus('Signing in...');

  try {
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
    await chrome.storage.local.set({ apiBase, token });
    setSuccess('Logged in');
  } catch (err) {
    setError(`Login error: ${errorMessage(err, 'unexpected error')}`);
  }
}

async function saveCurrentTab() {
  const apiBase = normalizeAPIBase(apiBaseInput.value);
  if (!apiBase || !token) {
    setError('Login required');
    return;
  }
  apiBaseInput.value = apiBase;

  try {
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
      setError(apiErrorMessage(res.status, data, 'Save failed'));
      return;
    }
    setSuccess('Saved');
  } catch (err) {
    setError(`Save error: ${errorMessage(err, 'unexpected error')}`);
  }
}

loginBtn.addEventListener('click', () => login());

saveBtn.addEventListener('click', () => saveCurrentTab());

tagInput.addEventListener('keydown', (e) => {
  if (e.key === 'Enter' || e.key === ',') {
    e.preventDefault();
    addTag(tagInput.value.replace(',', ''));
    tagInput.value = '';
  }
});

tagInput.addEventListener('input', async () => {
  const apiBase = normalizeAPIBase(apiBaseInput.value);
  const q = tagInput.value.trim();
  if (!apiBase || !token || q.length < 1) {
    suggestionsEl.innerHTML = '';
    return;
  }
  try {
    const res = await fetch(`${apiBase}/v1/tags?q=${encodeURIComponent(q)}`, {
      headers: { 'Authorization': `Bearer ${token}` },
    });
    if (!res.ok) {
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
    const data = await chrome.storage.local.get(['apiBase', 'token']);
    if (data.apiBase) apiBaseInput.value = normalizeAPIBase(data.apiBase);
    if (data.token) {
      token = data.token;
      setSuccess('Ready');
      return;
    }
    setStatus('Not logged in');
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
