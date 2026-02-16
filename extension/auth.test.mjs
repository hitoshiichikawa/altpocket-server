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
    this.disabled = false;
    this.listeners = new Map();
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
  permissionsDefaultGranted = true,
  permissionsRequestResult = true,
  launchWebAuthFlowResult = 'https://redirect.local/#id_token=test-id-token',
  launchWebAuthFlowError = null,
} = {}) {
  const data = { ...storageData };
  const storageSetCalls = [];
  const permissionContainsCalls = [];
  const permissionRequestCalls = [];
  const identityCalls = [];
  const tabsCreateCalls = [];

  const chrome = {
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
    identity: {
      getRedirectURL() {
        return 'https://redirect.local/callback';
      },
      async launchWebAuthFlow(payload) {
        identityCalls.push(payload);
        if (launchWebAuthFlowError) {
          throw launchWebAuthFlowError;
        }
        return launchWebAuthFlowResult;
      },
    },
    tabs: {
      async create(payload) {
        tabsCreateCalls.push(payload);
      },
    },
  };

  return {
    chrome,
    storageSetCalls,
    permissionContainsCalls,
    permissionRequestCalls,
    identityCalls,
    tabsCreateCalls,
    storageData: data,
  };
}

function createDocument() {
  const elements = {
    apiBase: new FakeElement('apiBase', 'input'),
    startSignIn: new FakeElement('startSignIn', 'button'),
    openWebUI: new FakeElement('openWebUI', 'button'),
    status: new FakeElement('status', 'div'),
  };
  const document = {
    getElementById(id) {
      return elements[id] || null;
    },
  };
  return { document, elements };
}

async function flushTasks() {
  await Promise.resolve();
  await Promise.resolve();
}

async function loadAuthScript(options = {}) {
  const { document, elements } = createDocument();
  const {
    chrome,
    storageSetCalls,
    permissionContainsCalls,
    permissionRequestCalls,
    identityCalls,
    tabsCreateCalls,
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
    window: {
      location: { search: options.locationSearch || '' },
      open() {},
    },
    console,
    setTimeout,
    clearTimeout,
  });

  const source = readFileSync(resolve(process.cwd(), 'extension/auth.js'), 'utf8');
  new vm.Script(source, { filename: 'extension/auth.js' }).runInContext(context);
  await flushTasks();

  return {
    elements,
    fetchCalls: calls,
    storageSetCalls,
    permissionContainsCalls,
    permissionRequestCalls,
    identityCalls,
    tabsCreateCalls,
    storageData,
  };
}

test('auth page init asks for API base when not provided', async () => {
  const env = await loadAuthScript();
  assert.equal(env.elements.status.textContent, 'Set API Base URL and start sign in.');
});

test('auth page signs in successfully and stores token', async () => {
  const env = await loadAuthScript({
    permissionsDefaultGranted: false,
    permissionsRequestResult: true,
    fetchHandlers: [jsonResponse(200, { token: 'jwt-token' })],
  });

  env.elements.apiBase.value = 'https://api.example.test/';
  await env.elements.startSignIn.click();

  assert.equal(env.permissionContainsCalls.length, 1);
  assert.equal(env.permissionContainsCalls[0].origins[0], 'https://api.example.test/*');
  assert.equal(env.permissionRequestCalls.length, 1);
  assert.equal(env.permissionRequestCalls[0].origins[0], 'https://api.example.test/*');
  assert.equal(env.identityCalls.length, 1);
  assert.equal(env.identityCalls[0].interactive, true);
  assert.equal(env.fetchCalls.length, 1);
  assert.equal(env.fetchCalls[0].url, 'https://api.example.test/v1/auth/extension/exchange');
  assert.equal(env.storageSetCalls.length, 2);
  assert.equal(env.storageSetCalls[0].apiBase, 'https://api.example.test');
  assert.equal(env.storageSetCalls[1].token, 'jwt-token');
  assert.equal(env.elements.status.textContent, 'Logged in. Return to the extension popup.');
  assert.equal(env.elements.status.className, 'status status-success');
});

test('auth page handles denied site permission', async () => {
  const env = await loadAuthScript({
    permissionsDefaultGranted: false,
    permissionsRequestResult: false,
  });

  env.elements.apiBase.value = 'https://api.example.test';
  await env.elements.startSignIn.click();

  assert.equal(env.permissionRequestCalls.length, 1);
  assert.equal(env.identityCalls.length, 0);
  assert.equal(env.fetchCalls.length, 0);
  assert.equal(env.elements.status.textContent, 'Site access permission is required');
  assert.equal(env.elements.status.className, 'status status-error');
});
