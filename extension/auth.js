const CLIENT_ID = 'YOUR_EXTENSION_CLIENT_ID';

const apiBaseInput = document.getElementById('apiBase');
const startSignInBtn = document.getElementById('startSignIn');
const openWebUIBtn = document.getElementById('openWebUI');
const statusEl = document.getElementById('status');

function setStatus(message, level = 'info') {
  const classes = {
    info: 'status status-info',
    success: 'status status-success',
    error: 'status status-error',
  };
  statusEl.textContent = message;
  statusEl.className = classes[level] || classes.info;
}

function normalizeAPIBase(value) {
  return value.trim().replace(/\/+$/, '');
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

function parseFragment(fragment) {
  if (!fragment) return '';
  const params = new URLSearchParams(fragment.replace(/^#/, ''));
  return params.get('id_token');
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

async function ensureAPIAccessPermission(apiBase) {
  const originPattern = apiOriginPattern(apiBase);
  if (!originPattern) {
    setStatus('Set valid API Base URL', 'error');
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
  if (typeof chrome.permissions.request !== 'function') {
    setStatus('Allow site access to this API Base URL', 'error');
    return false;
  }
  const granted = await chrome.permissions.request(request);
  if (!granted) {
    setStatus('Site access permission is required', 'error');
    return false;
  }
  return true;
}

async function runSignIn() {
  const apiBase = normalizeAPIBase(apiBaseInput.value);
  if (!apiBase) {
    setStatus('Set API Base URL', 'error');
    return;
  }

  startSignInBtn.disabled = true;
  apiBaseInput.value = apiBase;

  try {
    await chrome.storage.local.set({ apiBase });

    setStatus('Requesting site access...');
    const hasAccess = await ensureAPIAccessPermission(apiBase);
    if (!hasAccess) {
      return;
    }

    setStatus('Opening Google sign-in...');
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
    if (!resultURL) {
      setStatus('Login canceled', 'error');
      return;
    }

    const parsedResultURL = new URL(resultURL);
    const idToken = parseFragment(parsedResultURL.hash);
    if (!idToken) {
      setStatus('Login failed', 'error');
      return;
    }

    setStatus('Exchanging token...');
    let res;
    try {
      res = await fetch(`${apiBase}/v1/auth/extension/exchange`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ id_token: idToken }),
      });
    } catch (err) {
      setStatus(`Exchange request failed: ${errorMessage(err, 'network error')}`, 'error');
      return;
    }

    const { data } = await readResponseBody(res);
    if (!res.ok) {
      setStatus(apiErrorMessage(res.status, data, 'Exchange failed'), 'error');
      return;
    }
    if (!data || typeof data.token !== 'string' || data.token === '') {
      setStatus('Exchange failed: token missing', 'error');
      return;
    }

    await chrome.storage.local.set({ apiBase, token: data.token });
    setStatus('Logged in. Return to the extension popup.', 'success');
  } catch (err) {
    setStatus(`Login error: ${errorMessage(err, 'unexpected error')}`, 'error');
  } finally {
    startSignInBtn.disabled = false;
  }
}

async function openWebUI() {
  const apiBase = normalizeAPIBase(apiBaseInput.value);
  if (!apiBase) {
    setStatus('Set API Base URL', 'error');
    return;
  }
  apiBaseInput.value = apiBase;
  const webURL = `${apiBase}/ui/items`;
  if (chrome.tabs && typeof chrome.tabs.create === 'function') {
    await chrome.tabs.create({ url: webURL });
    return;
  }
  window.open(webURL, '_blank', 'noopener,noreferrer');
}

startSignInBtn.addEventListener('click', () => runSignIn());
openWebUIBtn.addEventListener('click', () => openWebUI());

(async () => {
  try {
    const params = new URLSearchParams(window.location.search);
    const queryBase = normalizeAPIBase(params.get('api_base') || '');
    const storage = await chrome.storage.local.get(['apiBase']);
    const storedBase = normalizeAPIBase(storage.apiBase || '');
    const initialBase = queryBase || storedBase;
    if (initialBase) {
      apiBaseInput.value = initialBase;
      await runSignIn();
      return;
    }
    setStatus('Set API Base URL and start sign in.');
  } catch (err) {
    setStatus(`Init error: ${errorMessage(err, 'unexpected error')}`, 'error');
  }
})();
