import assert from 'node:assert/strict';
import { webcrypto } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import test from 'node:test';
import vm from 'node:vm';

class FakeElement {
  constructor(id = '', tagName = 'div') {
    this.id = id;
    this.tagName = tagName;
    this.value = '';
    this.textContent = '';
    this.className = '';
    this.hidden = false;
    this.dataset = {};
    this.children = [];
    this._innerHTML = '';
    this.listeners = new Map();
  }

  set innerHTML(v) {
    this._innerHTML = v;
    this.children = [];
  }

  get innerHTML() {
    return this._innerHTML;
  }

  appendChild(child) {
    this.children.push(child);
    return child;
  }

  addEventListener(type, listener) {
    if (!this.listeners.has(type)) {
      this.listeners.set(type, []);
    }
    this.listeners.get(type).push(listener);
  }

  async dispatch(type, extra = {}) {
    const handlers = this.listeners.get(type) || [];
    const event = {
      type,
      target: this,
      defaultPrevented: false,
      preventDefault() {
        this.defaultPrevented = true;
      },
      ...extra,
    };

    for (const handler of handlers) {
      await handler(event);
    }

    return event;
  }

  async click() {
    return this.dispatch('click');
  }
}

function jsonResponse(status, body) {
  const asText = typeof body === 'string' ? body : JSON.stringify(body);
  return {
    ok: status >= 200 && status < 300,
    status,
    async json() {
      return body;
    },
    async text() {
      return asText;
    },
  };
}

function createFetchMock(handlers) {
  const queue = [...handlers];
  const calls = [];

  async function fetch(url, options = {}) {
    calls.push({ url, options });
    if (queue.length === 0) {
      throw new Error(`unexpected fetch: ${url}`);
    }
    const next = queue.shift();
    if (typeof next === 'function') {
      return next(url, options);
    }
    return next;
  }

  return { fetch, calls };
}

function createChromeMock({
  storageData = {},
  launchWebAuthFlowResult = 'https://redirect.local/#id_token=test-id-token',
  launchWebAuthFlowError = null,
  tabURL = 'https://example.com/current',
  permissionsDefaultGranted = true,
  permissionsRequestResult = true,
  enableScripting = false,
  scriptingExecuteResult = null,
  scriptingExecuteError = null,
} = {}) {
  const data = { ...storageData };
  const storageSetCalls = [];
  const storageRemoveCalls = [];
  const identityCalls = [];
  const permissionContainsCalls = [];
  const permissionRequestCalls = [];
  const scriptingExecuteCalls = [];

  const chrome = {
    identity: {
      getRedirectURL() {
        return 'https://redirect.local/callback';
      },
      async launchWebAuthFlow(args) {
        identityCalls.push(args);
        if (launchWebAuthFlowError) {
          throw launchWebAuthFlowError;
        }
        return launchWebAuthFlowResult;
      },
    },
    storage: {
      local: {
        async get(keys) {
          if (Array.isArray(keys)) {
            const result = {};
            for (const key of keys) {
              if (key in data) {
                result[key] = data[key];
              }
            }
            return result;
          }
          return { ...data };
        },
        async set(values) {
          storageSetCalls.push(values);
          Object.assign(data, values);
        },
        async remove(keys) {
          const list = Array.isArray(keys) ? keys : [keys];
          storageRemoveCalls.push(list);
          for (const key of list) {
            delete data[key];
          }
        },
      },
    },
    permissions: {
      async contains(payload) {
        permissionContainsCalls.push(payload);
        return permissionsDefaultGranted;
      },
      async request(payload) {
        permissionRequestCalls.push(payload);
        return permissionsRequestResult;
      },
    },
    tabs: {
      async query() {
        return [{ id: 1, url: tabURL }];
      },
    },
  };

  if (enableScripting) {
    chrome.scripting = {
      async executeScript(args) {
        scriptingExecuteCalls.push(args);
        if (scriptingExecuteError) {
          throw scriptingExecuteError;
        }
        if (scriptingExecuteResult === null) {
          return [{ result: null }];
        }
        return [{ result: scriptingExecuteResult }];
      },
    };
  }

  return {
    chrome,
    storageSetCalls,
    storageRemoveCalls,
    identityCalls,
    permissionContainsCalls,
    permissionRequestCalls,
    scriptingExecuteCalls,
    storageData: data,
  };
}

function createDocument() {
  const elements = {
    authControls: new FakeElement('authControls', 'section'),
    login: new FakeElement('login', 'button'),
    signOut: new FakeElement('signOut', 'button'),
    save: new FakeElement('save', 'button'),
    openWebUI: new FakeElement('openWebUI', 'button'),
    status: new FakeElement('status', 'div'),
    tagInput: new FakeElement('tagInput', 'input'),
    tags: new FakeElement('tags', 'div'),
    suggestions: new FakeElement('suggestions', 'div'),
  };

  const document = {
    getElementById(id) {
      return elements[id] || null;
    },
    createElement(tagName) {
      return new FakeElement('', tagName);
    },
  };

  return { document, elements };
}

async function flushTasks() {
  await Promise.resolve();
  await Promise.resolve();
}

async function loadPopupScript(options = {}) {
  const { document, elements } = createDocument();
  const {
    chrome,
    storageSetCalls,
    storageRemoveCalls,
    identityCalls,
    permissionContainsCalls,
    permissionRequestCalls,
    scriptingExecuteCalls,
    storageData,
  } = createChromeMock(options);
  const { fetch, calls } = createFetchMock(options.fetchHandlers || []);

  const context = vm.createContext({
    chrome,
    crypto: webcrypto,
    document,
    fetch,
    URL,
    URLSearchParams,
    console,
    setTimeout,
    clearTimeout,
  });

  const apiBase = options.apiBase ?? 'https://api.example.test';
  let source = readFileSync(resolve(process.cwd(), 'extension/popup.js'), 'utf8');
  source = source.replace("const API_BASE = 'https://YOUR_API_BASE_URL';", `const API_BASE = ${JSON.stringify(apiBase)};`);
  new vm.Script(source, { filename: 'extension/popup.js' }).runInContext(context);

  await flushTasks();

  return {
    elements,
    fetchCalls: calls,
    storageSetCalls,
    storageRemoveCalls,
    identityCalls,
    permissionContainsCalls,
    permissionRequestCalls,
    scriptingExecuteCalls,
    storageData,
  };
}

test('login requires configured API base URL', async () => {
  const env = await loadPopupScript({ apiBase: '' });

  assert.equal(env.elements.authControls.hidden, true);
  assert.equal(env.elements.login.textContent, 'Sign in with Google');
  assert.equal(env.elements.signOut.hidden, true);

  await env.elements.login.click();

  assert.equal(env.elements.status.textContent, 'Extension API base is not configured');
  assert.equal(env.fetchCalls.length, 0);
  assert.equal(env.elements.status.className, 'status status-error');
  assert.equal(env.elements.authControls.hidden, true);
});

test('login exchanges id token and stores API token', async () => {
  const env = await loadPopupScript({
    fetchHandlers: [jsonResponse(200, { token: 'jwt-token' })],
  });

  await env.elements.login.click();

  assert.equal(env.fetchCalls.length, 1);
  assert.equal(env.fetchCalls[0].url, 'https://api.example.test/v1/auth/extension/exchange');
  assert.equal(env.fetchCalls[0].options.method, 'POST');

  const payload = JSON.parse(env.fetchCalls[0].options.body);
  assert.equal(payload.id_token, 'test-id-token');

  assert.equal(env.elements.status.textContent, 'Logged in');
  assert.equal(env.elements.status.className, 'status status-success');
  assert.equal(env.storageSetCalls.length, 1);
  assert.equal(env.storageSetCalls[0].token, 'jwt-token');
  assert.equal(env.storageData.token, 'jwt-token');
  assert.equal(env.elements.authControls.hidden, false);
  assert.equal(env.elements.login.hidden, true);
  assert.equal(env.elements.signOut.hidden, false);
});

test('login requests optional host permission when missing', async () => {
  const env = await loadPopupScript({
    permissionsDefaultGranted: false,
    permissionsRequestResult: true,
    fetchHandlers: [jsonResponse(200, { token: 'jwt-token' })],
  });

  await env.elements.login.click();

  assert.equal(env.permissionContainsCalls.length, 1);
  assert.equal(env.permissionContainsCalls[0].origins[0], 'https://api.example.test/*');
  assert.equal(env.permissionRequestCalls.length, 1);
  assert.equal(env.permissionRequestCalls[0].origins[0], 'https://api.example.test/*');
  assert.equal(env.elements.status.textContent, 'Logged in');
});

test('save requires login token', async () => {
  const env = await loadPopupScript();
  await env.elements.save.click();

  assert.equal(env.elements.status.textContent, 'Login required');
  assert.equal(env.elements.status.className, 'status status-error');
  assert.equal(env.fetchCalls.length, 0);
  assert.equal(env.elements.authControls.hidden, true);
  assert.equal(env.storageRemoveCalls.length, 1);
  assert.deepEqual(env.storageRemoveCalls[0], ['token']);
});

test('save stops when host permission request is denied', async () => {
  const env = await loadPopupScript({
    storageData: {
      token: 'stored-token',
    },
    permissionsDefaultGranted: false,
    permissionsRequestResult: false,
  });

  await env.elements.save.click();

  assert.equal(env.permissionContainsCalls.length, 1);
  assert.equal(env.permissionRequestCalls.length, 1);
  assert.equal(env.fetchCalls.length, 0);
  assert.equal(env.elements.status.textContent, 'Site access permission is required');
});

test('save current tab sends bearer token and tags', async () => {
  const env = await loadPopupScript({
    storageData: {
      token: 'stored-token',
    },
    fetchHandlers: [jsonResponse(200, {})],
    tabURL: 'https://news.example/item',
  });

  assert.equal(env.elements.status.textContent, 'Ready');
  assert.equal(env.elements.authControls.hidden, false);
  assert.equal(env.elements.login.hidden, true);
  assert.equal(env.elements.signOut.hidden, false);

  env.elements.tagInput.value = 'go';
  await env.elements.tagInput.dispatch('keydown', { key: 'Enter' });

  await env.elements.save.click();

  assert.equal(env.fetchCalls.length, 1);
  assert.equal(env.fetchCalls[0].url, 'https://api.example.test/v1/items');
  assert.equal(env.fetchCalls[0].options.method, 'POST');
  assert.equal(env.fetchCalls[0].options.headers.Authorization, 'Bearer stored-token');

  const payload = JSON.parse(env.fetchCalls[0].options.body);
  assert.equal(payload.url, 'https://news.example/item');
  assert.deepEqual(payload.tags, ['go']);

  assert.equal(env.elements.status.textContent, 'Saved');
  assert.equal(env.elements.status.className, 'status status-success');
  assert.equal(env.elements.authControls.hidden, false);
});

test('save sends captured content asynchronously when item is newly created', async () => {
  const env = await loadPopupScript({
    storageData: {
      token: 'stored-token',
    },
    enableScripting: true,
    scriptingExecuteResult: {
      title: 'Captured title',
      content_full: 'Captured body text',
    },
    fetchHandlers: [
      jsonResponse(200, { item_id: 'item-123', created: true }),
      jsonResponse(204, {}),
    ],
    tabURL: 'https://news.example/item',
  });

  await env.elements.save.click();
  await flushTasks();

  assert.equal(env.fetchCalls.length, 2);
  assert.equal(env.fetchCalls[0].url, 'https://api.example.test/v1/items');
  assert.equal(env.fetchCalls[1].url, 'https://api.example.test/v1/items/item-123/capture');
  assert.equal(env.fetchCalls[1].options.method, 'POST');
  assert.equal(env.fetchCalls[1].options.headers.Authorization, 'Bearer stored-token');

  const capturePayload = JSON.parse(env.fetchCalls[1].options.body);
  assert.equal(capturePayload.title, 'Captured title');
  assert.equal(capturePayload.content_full, 'Captured body text');
  assert.equal(env.scriptingExecuteCalls.length, 1);
  assert.equal(env.scriptingExecuteCalls[0].target.tabId, 1);
});

test('login surfaces exchange network errors', async () => {
  const env = await loadPopupScript({
    fetchHandlers: [() => {
      throw new Error('connect ECONNREFUSED');
    }],
  });

  await env.elements.login.click();

  assert.equal(env.fetchCalls.length, 1);
  assert.equal(env.elements.status.className, 'status status-error');
  assert.match(env.elements.status.textContent, /^Exchange request failed:/);
  assert.equal(env.elements.authControls.hidden, true);
});

test('login surfaces user_not_registered from exchange', async () => {
  const env = await loadPopupScript({
    fetchHandlers: [jsonResponse(403, { error: 'user_not_registered' })],
  });

  await env.elements.login.click();

  assert.equal(env.elements.status.className, 'status status-error');
  assert.equal(env.elements.status.textContent, 'Account is not registered on this server');
  assert.equal(env.elements.authControls.hidden, true);
});

test('save surfaces API error payload', async () => {
  const env = await loadPopupScript({
    storageData: {
      token: 'stored-token',
    },
    fetchHandlers: [jsonResponse(429, { error: 'rate_limited' })],
  });

  await env.elements.save.click();

  assert.equal(env.elements.status.className, 'status status-error');
  assert.equal(env.elements.status.textContent, 'rate_limited');
  assert.equal(env.elements.authControls.hidden, false);
});

test('tag suggestions fetch and click-to-add work', async () => {
  const env = await loadPopupScript({
    storageData: {
      token: 'stored-token',
    },
    fetchHandlers: [jsonResponse(200, [{ name: 'go' }])],
  });

  env.elements.tagInput.value = 'g';
  await env.elements.tagInput.dispatch('input');

  assert.equal(env.fetchCalls.length, 1);
  assert.equal(env.fetchCalls[0].url, 'https://api.example.test/v1/tags?q=g');

  assert.equal(env.elements.suggestions.children.length, 1);
  const suggestionButton = env.elements.suggestions.children[0];
  assert.equal(suggestionButton.textContent, 'go');

  await suggestionButton.click();

  assert.equal(env.elements.tags.children.length, 1);
  assert.equal(env.elements.tags.children[0].textContent, 'go');
  assert.equal(env.elements.tagInput.value, '');
});

test('init without token stays unauthenticated and hides save UI', async () => {
  const env = await loadPopupScript();

  assert.equal(env.elements.status.textContent, 'Not logged in');
  assert.equal(env.elements.status.className, 'status status-info');
  assert.equal(env.elements.authControls.hidden, true);
  assert.equal(env.elements.login.textContent, 'Sign in with Google');
  assert.equal(env.elements.signOut.hidden, true);
});

test('authenticated click on auth button signs out and clears token', async () => {
  const env = await loadPopupScript({
    storageData: {
      token: 'stored-token',
    },
  });

  assert.equal(env.elements.signOut.hidden, false);
  await env.elements.signOut.click();

  assert.equal(env.elements.status.textContent, 'Signed out');
  assert.equal(env.elements.status.className, 'status status-info');
  assert.equal(env.elements.authControls.hidden, true);
  assert.equal(env.elements.login.hidden, false);
  assert.equal(env.elements.signOut.hidden, true);
  assert.equal(env.storageRemoveCalls.length, 1);
  assert.equal(env.storageData.token, undefined);
});

test('save with expired token returns to unauthenticated state', async () => {
  const env = await loadPopupScript({
    storageData: {
      token: 'expired-token',
    },
    fetchHandlers: [jsonResponse(401, { error: 'unauthorized' })],
  });

  await env.elements.save.click();

  assert.equal(env.elements.status.textContent, 'Session expired. Please sign in again.');
  assert.equal(env.elements.status.className, 'status status-error');
  assert.equal(env.elements.authControls.hidden, true);
  assert.equal(env.storageRemoveCalls.length, 1);
  assert.equal(env.storageData.token, undefined);
});

test('tag suggestions auth failure returns to unauthenticated state', async () => {
  const env = await loadPopupScript({
    storageData: {
      token: 'expired-token',
    },
    fetchHandlers: [jsonResponse(401, { error: 'unauthorized' })],
  });

  env.elements.tagInput.value = 'g';
  await env.elements.tagInput.dispatch('input');

  assert.equal(env.elements.status.textContent, 'Session expired. Please sign in again.');
  assert.equal(env.elements.status.className, 'status status-error');
  assert.equal(env.elements.authControls.hidden, true);
  assert.equal(env.storageRemoveCalls.length, 1);
});
