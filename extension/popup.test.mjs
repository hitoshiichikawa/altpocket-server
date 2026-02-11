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
  return {
    ok: status >= 200 && status < 300,
    status,
    async json() {
      return body;
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
  tabURL = 'https://example.com/current',
} = {}) {
  const data = { ...storageData };
  const storageSetCalls = [];
  const identityCalls = [];

  const chrome = {
    identity: {
      getRedirectURL() {
        return 'https://redirect.local/callback';
      },
      async launchWebAuthFlow(args) {
        identityCalls.push(args);
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
      },
    },
    tabs: {
      async query() {
        return [{ url: tabURL }];
      },
    },
  };

  return { chrome, storageSetCalls, identityCalls, storageData: data };
}

function createDocument() {
  const elements = {
    apiBase: new FakeElement('apiBase', 'input'),
    login: new FakeElement('login', 'button'),
    save: new FakeElement('save', 'button'),
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
  const { chrome, storageSetCalls, identityCalls, storageData } = createChromeMock(options);
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

  const source = readFileSync(resolve(process.cwd(), 'extension/popup.js'), 'utf8');
  new vm.Script(source, { filename: 'extension/popup.js' }).runInContext(context);

  await flushTasks();

  return {
    elements,
    fetchCalls: calls,
    storageSetCalls,
    identityCalls,
    storageData,
  };
}

test('login requires API base URL', async () => {
  const env = await loadPopupScript();

  await env.elements.login.click();

  assert.equal(env.elements.status.textContent, 'Set API Base URL');
  assert.equal(env.fetchCalls.length, 0);
});

test('login exchanges id token and stores API token', async () => {
  const env = await loadPopupScript({
    fetchHandlers: [jsonResponse(200, { token: 'jwt-token' })],
  });

  env.elements.apiBase.value = 'https://api.example.test';
  await env.elements.login.click();

  assert.equal(env.fetchCalls.length, 1);
  assert.equal(env.fetchCalls[0].url, 'https://api.example.test/v1/auth/extension/exchange');
  assert.equal(env.fetchCalls[0].options.method, 'POST');

  const payload = JSON.parse(env.fetchCalls[0].options.body);
  assert.equal(payload.id_token, 'test-id-token');

  assert.equal(env.elements.status.textContent, 'Logged in');
  assert.equal(env.storageSetCalls.length, 1);
  assert.equal(env.storageSetCalls[0].apiBase, 'https://api.example.test');
  assert.equal(env.storageSetCalls[0].token, 'jwt-token');
  assert.equal(env.storageData.token, 'jwt-token');
});

test('save requires login token', async () => {
  const env = await loadPopupScript();

  env.elements.apiBase.value = 'https://api.example.test';
  await env.elements.save.click();

  assert.equal(env.elements.status.textContent, 'Login required');
  assert.equal(env.fetchCalls.length, 0);
});

test('save current tab sends bearer token and tags', async () => {
  const env = await loadPopupScript({
    storageData: {
      apiBase: 'https://api.example.test',
      token: 'stored-token',
    },
    fetchHandlers: [jsonResponse(200, {})],
    tabURL: 'https://news.example/item',
  });

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
});

test('tag suggestions fetch and click-to-add work', async () => {
  const env = await loadPopupScript({
    storageData: {
      apiBase: 'https://api.example.test',
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
