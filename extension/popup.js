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

function setStatus(msg) {
  statusEl.textContent = msg;
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
  const params = new URLSearchParams(fragment.replace(/^#/, ''));
  return params.get('id_token');
}

async function login() {
  const apiBase = apiBaseInput.value.trim();
  if (!apiBase) {
    setStatus('Set API Base URL');
    return;
  }
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
  const idToken = parseFragment(new URL(resultUrl).hash);
  if (!idToken) {
    setStatus('Login failed');
    return;
  }

  const res = await fetch(`${apiBase}/v1/auth/extension/exchange`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ id_token: idToken }),
  });
  if (!res.ok) {
    const data = await res.json().catch(() => ({}));
    setStatus(data.error || 'Exchange failed');
    return;
  }
  const data = await res.json();
  token = data.token;
  await chrome.storage.local.set({ apiBase, token });
  setStatus('Logged in');
}

async function saveCurrentTab() {
  const apiBase = apiBaseInput.value.trim();
  if (!apiBase || !token) {
    setStatus('Login required');
    return;
  }
  const [tab] = await chrome.tabs.query({ active: true, currentWindow: true });
  if (!tab || !tab.url) {
    setStatus('No tab URL');
    return;
  }

  const res = await fetch(`${apiBase}/v1/items`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      'Authorization': `Bearer ${token}`,
    },
    body: JSON.stringify({ url: tab.url, tags }),
  });
  if (!res.ok) {
    setStatus('Save failed');
    return;
  }
  setStatus('Saved');
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
  const apiBase = apiBaseInput.value.trim();
  const q = tagInput.value.trim();
  if (!apiBase || !token || q.length < 1) {
    suggestionsEl.innerHTML = '';
    return;
  }
  const res = await fetch(`${apiBase}/v1/tags?q=${encodeURIComponent(q)}`, {
    headers: { 'Authorization': `Bearer ${token}` },
  });
  if (!res.ok) {
    suggestionsEl.innerHTML = '';
    return;
  }
  const data = await res.json();
  renderSuggestions(data);
});

tagInput.addEventListener('blur', () => {
  if (tagInput.value.trim()) {
    addTag(tagInput.value);
    tagInput.value = '';
  }
});

(async () => {
  const data = await chrome.storage.local.get(['apiBase', 'token']);
  if (data.apiBase) apiBaseInput.value = data.apiBase;
  if (data.token) token = data.token;
})();
