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
    this.hidden = false;
    this.disabled = false;
    this.dataset = {};
    this.children = [];
    this.attributes = new Map();
    this.listeners = new Map();
    this._className = '';
    this._classSet = new Set();
    this._innerHTML = '';

    this.classList = {
      add: (...names) => {
        for (const name of names) {
          const normalized = String(name || '').trim();
          if (normalized) this._classSet.add(normalized);
        }
        this._syncClassName();
      },
      remove: (...names) => {
        for (const name of names) {
          const normalized = String(name || '').trim();
          if (normalized) this._classSet.delete(normalized);
        }
        this._syncClassName();
      },
      contains: (name) => this._classSet.has(String(name || '').trim()),
    };
  }

  _syncClassName() {
    this._className = Array.from(this._classSet).join(' ');
  }

  set className(value) {
    const parts = String(value || '')
      .split(/\s+/)
      .map((v) => v.trim())
      .filter(Boolean);
    this._classSet = new Set(parts);
    this._syncClassName();
  }

  get className() {
    return this._className;
  }

  set innerHTML(value) {
    this._innerHTML = String(value || '');
    this.children = [];
  }

  get innerHTML() {
    return this._innerHTML;
  }

  setAttribute(name, value) {
    this.attributes.set(String(name), String(value));
  }

  getAttribute(name) {
    if (!this.attributes.has(String(name))) return null;
    return this.attributes.get(String(name));
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

function createTimerMock() {
  let nextID = 1;
  const timers = [];

  return {
    setTimeout(fn, ms) {
      const timer = { id: nextID++, fn, ms, cancelled: false };
      timers.push(timer);
      return timer.id;
    },
    clearTimeout(id) {
      const timer = timers.find((v) => v.id === id);
      if (timer) timer.cancelled = true;
    },
    runAll() {
      const pending = timers.splice(0, timers.length);
      for (const timer of pending) {
        if (!timer.cancelled) {
          timer.fn();
        }
      }
    },
  };
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
  scriptingExecuteResults = null,
  scriptingExecuteError = null,
} = {}) {
  const data = { ...storageData };
  const storageSetCalls = [];
  const storageRemoveCalls = [];
  const identityCalls = [];
  const clearAuthTokenCalls = [];
  const permissionContainsCalls = [];
  const permissionRequestCalls = [];
  const tabsCreateCalls = [];
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
      async clearAllCachedAuthTokens() {
        clearAuthTokenCalls.push(true);
      },
    },
    storage: {
      local: {
        async get(keys) {
          if (keys == null) {
            return { ...data };
          }
          if (Array.isArray(keys)) {
            const result = {};
            for (const key of keys) {
              if (key in data) result[key] = data[key];
            }
            return result;
          }
          if (typeof keys === 'string') {
            return keys in data ? { [keys]: data[keys] } : {};
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
      async create(payload) {
        tabsCreateCalls.push(payload);
      },
    },
  };

  if (enableScripting) {
    let scriptingCallIndex = 0;
    chrome.scripting = {
      async executeScript(args) {
        scriptingExecuteCalls.push(args);
        if (scriptingExecuteError) {
          throw scriptingExecuteError;
        }
        let result;
        if (Array.isArray(scriptingExecuteResults)) {
          result = scriptingCallIndex < scriptingExecuteResults.length
            ? scriptingExecuteResults[scriptingCallIndex]
            : null;
          scriptingCallIndex++;
        } else {
          result = scriptingExecuteResult;
        }
        if (result == null) {
          return [{ result: null }];
        }
        return [{ result }];
      },
    };
  }

  return {
    chrome,
    storageSetCalls,
    storageRemoveCalls,
    identityCalls,
    clearAuthTokenCalls,
    permissionContainsCalls,
    permissionRequestCalls,
    tabsCreateCalls,
    scriptingExecuteCalls,
    storageData: data,
  };
}

function createDocument() {
  const elements = {
    loginScreen: new FakeElement('loginScreen', 'section'),
    readerScreen: new FakeElement('readerScreen', 'section'),
    login: new FakeElement('login', 'button'),
    signOut: new FakeElement('signOut', 'button'),
    openWebUI: new FakeElement('openWebUI', 'button'),
    utilityStatus: new FakeElement('utilityStatus', 'span'),
    save: new FakeElement('save', 'button'),
    tagInput: new FakeElement('tagInput', 'input'),
    suggestions: new FakeElement('suggestions', 'div'),
    tags: new FakeElement('tags', 'div'),
    searchInput: new FakeElement('searchInput', 'input'),
    resultMeta: new FakeElement('resultMeta', 'span'),
    itemList: new FakeElement('itemList', 'ul'),
  };

  elements.readerScreen.hidden = true;
  elements.resultMeta.textContent = '0件';

  const body = new FakeElement('', 'body');
  body.dataset = {};

  const document = {
    body,
    getElementById(id) {
      return elements[id] || null;
    },
    createElement(tagName) {
      return new FakeElement('', tagName);
    },
  };

  return { document, elements };
}

async function flushMicrotasks(rounds = 24) {
  for (let i = 0; i < rounds; i += 1) {
    await Promise.resolve();
  }
}

async function loadSidepanelScript(options = {}) {
  const { document, elements } = createDocument();
  const timer = createTimerMock();
  const {
    chrome,
    storageSetCalls,
    storageRemoveCalls,
    identityCalls,
    clearAuthTokenCalls,
    permissionContainsCalls,
    permissionRequestCalls,
    tabsCreateCalls,
    scriptingExecuteCalls,
    storageData,
  } = createChromeMock(options);
  const { fetch, calls } = createFetchMock(options.fetchHandlers || []);
  const alerts = [];
  const windowOpenCalls = [];

  const context = vm.createContext({
    chrome,
    crypto: webcrypto,
    document,
    fetch,
    URL,
    URLSearchParams,
    console,
    setTimeout: (...args) => timer.setTimeout(...args),
    clearTimeout: (...args) => timer.clearTimeout(...args),
    alert: (msg) => alerts.push(String(msg)),
    window: {
      open: (...args) => {
        windowOpenCalls.push(args);
      },
    },
  });

  const apiBase = options.apiBase ?? 'https://api.example.test';
  let source = readFileSync(resolve(process.cwd(), 'extension/sidepanel.js'), 'utf8');
  source = source.replace("const API_BASE = 'https://YOUR_API_BASE_URL';", `const API_BASE = ${JSON.stringify(apiBase)};`);
  new vm.Script(source, { filename: 'extension/sidepanel.js' }).runInContext(context);

  await flushMicrotasks();

  return {
    elements,
    fetchCalls: calls,
    storageSetCalls,
    storageRemoveCalls,
    identityCalls,
    clearAuthTokenCalls,
    permissionContainsCalls,
    permissionRequestCalls,
    tabsCreateCalls,
    scriptingExecuteCalls,
    storageData,
    alerts,
    windowOpenCalls,
    timer,
    document,
  };
}

test('init without token stays on login-only screen and makes no API call', async () => {
  const env = await loadSidepanelScript();

  assert.equal(env.document.body.dataset.screen, 'login');
  assert.equal(env.elements.loginScreen.hidden, false);
  assert.equal(env.elements.readerScreen.hidden, true);
  assert.equal(env.fetchCalls.length, 0);
});

test('init with stored token opens reader screen and fetches newest list', async () => {
  const env = await loadSidepanelScript({
    storageData: { token: 'stored-token' },
    fetchHandlers: [jsonResponse(200, { items: [], pagination: { total: 0 } })],
  });

  assert.equal(env.document.body.dataset.screen, 'reader');
  assert.equal(env.elements.loginScreen.hidden, true);
  assert.equal(env.elements.readerScreen.hidden, false);
  assert.equal(env.fetchCalls.length, 1);

  const req = env.fetchCalls[0];
  const u = new URL(req.url);
  assert.equal(u.pathname, '/v1/items');
  assert.equal(u.searchParams.get('sort'), 'newest');
  assert.equal(u.searchParams.get('page'), '1');
  assert.equal(u.searchParams.get('per_page'), '50');
  assert.equal(req.options.headers.Authorization, 'Bearer stored-token');
});

test('login success exchanges token and transitions to reader screen', async () => {
  const env = await loadSidepanelScript({
    fetchHandlers: [
      jsonResponse(200, { token: 'jwt-token' }),
      jsonResponse(200, { items: [], pagination: { total: 0 } }),
    ],
  });

  await env.elements.login.click();
  await flushMicrotasks();

  assert.equal(env.fetchCalls.length, 2);
  assert.equal(env.fetchCalls[0].url, 'https://api.example.test/v1/auth/extension/exchange');
  assert.equal(env.fetchCalls[0].options.method, 'POST');
  const payload = JSON.parse(env.fetchCalls[0].options.body);
  assert.equal(payload.id_token, 'test-id-token');

  assert.equal(env.storageSetCalls.length, 1);
  assert.equal(env.storageSetCalls[0].token, 'jwt-token');
  assert.equal(env.document.body.dataset.screen, 'reader');
});

test('login does not transition to reader screen when exchanged token is whitespace-only', async () => {
  const env = await loadSidepanelScript({
    fetchHandlers: [jsonResponse(200, { token: '   ' })],
  });

  await env.elements.login.click();
  await flushMicrotasks();

  assert.equal(env.document.body.dataset.screen, 'login');
  assert.equal(env.storageSetCalls.length, 0);
  assert.equal(env.fetchCalls.length, 1);
  assert.deepEqual(env.alerts, ['Exchange failed: token missing']);
});

test('login with user_not_registered shows message and stays on login screen', async () => {
  const env = await loadSidepanelScript({
    fetchHandlers: [jsonResponse(403, { error: 'user_not_registered' })],
  });

  await env.elements.login.click();
  await flushMicrotasks();

  assert.equal(env.document.body.dataset.screen, 'login');
  assert.deepEqual(env.alerts, ['Account is not registered on this server']);
  assert.equal(env.storageSetCalls.length, 0);
});

test('search input triggers relevance query and uses API ordering as-is', async () => {
  const env = await loadSidepanelScript({
    storageData: { token: 'stored-token' },
    fetchHandlers: [
      jsonResponse(200, { items: [], pagination: { total: 0 } }),
      jsonResponse(200, { items: [{ title: 'A' }, { title: 'B' }], pagination: { total: 2 } }),
    ],
  });

  env.elements.searchInput.value = 'golang';
  await env.elements.searchInput.dispatch('input');
  env.timer.runAll();
  await flushMicrotasks();

  assert.equal(env.fetchCalls.length, 2);
  const req = env.fetchCalls[1];
  const u = new URL(req.url);
  assert.equal(u.searchParams.get('q'), 'golang');
  assert.equal(u.searchParams.get('sort'), 'relevance');
  assert.equal(env.elements.resultMeta.textContent, '2件');
});

test('save current tab posts item with prefill and sends async capture for newly created item', async () => {
  const env = await loadSidepanelScript({
    storageData: { token: 'stored-token' },
    enableScripting: true,
    scriptingExecuteResults: [
      { title: 'Page Title', excerpt: 'Preview text from article' },
      { title: 'Captured title', content_full: 'Captured body text' },
    ],
    fetchHandlers: [
      jsonResponse(200, { items: [], pagination: { total: 0 } }),
      jsonResponse(200, { item_id: 'item-123', created: true }),
      jsonResponse(204, ''),
    ],
    tabURL: 'https://news.example/item',
  });

  env.elements.tagInput.value = 'go';
  await env.elements.tagInput.dispatch('keydown', { key: 'Enter' });

  await env.elements.save.click();
  await flushMicrotasks();

  assert.equal(env.fetchCalls.length, 3);
  assert.equal(env.fetchCalls[1].url, 'https://api.example.test/v1/items');
  const savePayload = JSON.parse(env.fetchCalls[1].options.body);
  assert.equal(savePayload.url, 'https://news.example/item');
  assert.deepEqual(savePayload.tags, ['go']);
  assert.equal(savePayload.title, 'Page Title');
  assert.equal(savePayload.excerpt, 'Preview text from article');

  assert.equal(env.fetchCalls[2].url, 'https://api.example.test/v1/items/item-123/capture');
  const capturePayload = JSON.parse(env.fetchCalls[2].options.body);
  assert.equal(capturePayload.title, 'Captured title');
  assert.equal(capturePayload.content_full, 'Captured body text');
  assert.equal(env.scriptingExecuteCalls.length, 2);
  assert.equal(env.elements.utilityStatus.textContent, 'Saved');
  assert.equal(env.elements.utilityStatus.classList.contains('is-success'), true);
});

test('401 during items fetch logs out and returns to login screen', async () => {
  const env = await loadSidepanelScript({
    storageData: { token: 'expired-token', refresh_token: 'r1', keep: 'ok' },
    fetchHandlers: [jsonResponse(401, { error: 'unauthorized' })],
  });

  await flushMicrotasks();

  assert.equal(env.document.body.dataset.screen, 'login');
  assert.equal(env.storageRemoveCalls.length, 1);
  assert.equal(env.storageRemoveCalls[0].includes('token'), true);
  assert.equal(env.storageRemoveCalls[0].includes('refresh_token'), true);
  assert.equal(env.storageData.keep, 'ok');
});

test('manual sign-out clears all token-like keys and returns to login-only screen', async () => {
  const env = await loadSidepanelScript({
    storageData: {
      token: 'stored-token',
      refresh_token: 'refresh',
      sessionToken: 'session-like',
      keep: 'safe',
    },
    fetchHandlers: [jsonResponse(200, { items: [], pagination: { total: 0 } })],
  });

  await env.elements.signOut.click();
  await flushMicrotasks();

  assert.equal(env.document.body.dataset.screen, 'login');
  assert.equal(env.storageRemoveCalls.length, 1);
  assert.equal(env.storageRemoveCalls[0].includes('token'), true);
  assert.equal(env.storageRemoveCalls[0].includes('refresh_token'), true);
  assert.equal(env.storageRemoveCalls[0].includes('sessionToken'), true);
  assert.equal(env.storageData.keep, 'safe');
  assert.equal(env.clearAuthTokenCalls.length, 1);
});

test('init with token and missing API permission shows guidance and skips list fetch', async () => {
  const env = await loadSidepanelScript({
    storageData: { token: 'stored-token' },
    permissionsDefaultGranted: false,
  });

  await flushMicrotasks();

  assert.equal(env.document.body.dataset.screen, 'reader');
  assert.equal(env.fetchCalls.length, 0);
  assert.equal(env.elements.utilityStatus.textContent, 'Allow site access to API base');
  assert.equal(env.elements.utilityStatus.classList.contains('is-error'), true);
});

test('save current tab shows network error when create request fails', async () => {
  const env = await loadSidepanelScript({
    storageData: { token: 'stored-token' },
    fetchHandlers: [
      jsonResponse(200, { items: [], pagination: { total: 0 } }),
      () => {
        throw new Error('offline');
      },
    ],
  });

  await env.elements.save.click();
  await flushMicrotasks();

  assert.equal(env.fetchCalls.length, 2);
  assert.equal(env.fetchCalls[1].url, 'https://api.example.test/v1/items');
  assert.equal(env.elements.utilityStatus.textContent, 'Save request failed: offline');
  assert.equal(env.elements.utilityStatus.classList.contains('is-error'), true);
});

test('login network failure keeps login screen and resets button for retry', async () => {
  const env = await loadSidepanelScript({
    fetchHandlers: [
      () => {
        throw new Error('offline');
      },
    ],
  });

  await env.elements.login.click();
  await flushMicrotasks();

  assert.equal(env.document.body.dataset.screen, 'login');
  assert.equal(env.elements.login.disabled, false);
  assert.equal(env.elements.login.textContent, 'Sign in with Google');
  assert.deepEqual(env.alerts, ['Login error']);
});

test('go to website opens web items page in a new tab', async () => {
  const env = await loadSidepanelScript();

  await env.elements.openWebUI.click();
  await flushMicrotasks();

  assert.equal(env.tabsCreateCalls.length, 1);
  assert.equal(env.tabsCreateCalls[0].url, 'https://api.example.test/ui/items');
});

test('save current tab skips capture request when item already exists', async () => {
  const env = await loadSidepanelScript({
    storageData: { token: 'stored-token' },
    enableScripting: true,
    scriptingExecuteResults: [
      { title: 'Page Title', excerpt: 'Preview text' },
    ],
    fetchHandlers: [
      jsonResponse(200, { items: [], pagination: { total: 0 } }),
      jsonResponse(200, { item_id: 'item-123', created: false }),
    ],
    tabURL: 'https://news.example/item',
  });

  await env.elements.save.click();
  await flushMicrotasks();

  assert.equal(env.fetchCalls.length, 2);
  assert.equal(env.fetchCalls[1].url, 'https://api.example.test/v1/items');
  assert.equal(env.scriptingExecuteCalls.length, 1);
  assert.equal(env.elements.utilityStatus.textContent, 'Saved');
  assert.equal(env.elements.utilityStatus.classList.contains('is-success'), true);
});

test('save keeps success state when capture extraction returns no content', async () => {
  const env = await loadSidepanelScript({
    storageData: { token: 'stored-token' },
    enableScripting: true,
    scriptingExecuteResults: [null, null],
    fetchHandlers: [
      jsonResponse(200, { items: [], pagination: { total: 0 } }),
      jsonResponse(200, { item_id: 'item-123', created: true }),
    ],
    tabURL: 'https://news.example/item',
  });

  await env.elements.save.click();
  await flushMicrotasks();

  assert.equal(env.fetchCalls.length, 2);
  assert.equal(env.scriptingExecuteCalls.length, 2);
  assert.equal(env.elements.utilityStatus.textContent, 'Saved');
  assert.equal(env.elements.utilityStatus.classList.contains('is-success'), true);
});

test('duplicate tag entries are deduplicated before save request', async () => {
  const env = await loadSidepanelScript({
    storageData: { token: 'stored-token' },
    fetchHandlers: [
      jsonResponse(200, { items: [], pagination: { total: 0 } }),
      jsonResponse(200, { item_id: 'item-123', created: false }),
    ],
  });

  env.elements.tagInput.value = 'go';
  await env.elements.tagInput.dispatch('keydown', { key: 'Enter' });
  env.elements.tagInput.value = 'go';
  await env.elements.tagInput.dispatch('keydown', { key: 'Enter' });

  await env.elements.save.click();
  await flushMicrotasks();

  const savePayload = JSON.parse(env.fetchCalls[1].options.body);
  assert.deepEqual(savePayload.tags, ['go']);
});

test('tag suggestion click adds the suggested tag to save request', async () => {
  const env = await loadSidepanelScript({
    storageData: { token: 'stored-token' },
    fetchHandlers: [
      jsonResponse(200, { items: [], pagination: { total: 0 } }),
      jsonResponse(200, [{ id: 't1', name: 'golang', normalized_name: 'golang' }]),
      jsonResponse(200, { item_id: 'item-123', created: false }),
    ],
  });

  env.elements.tagInput.value = 'go';
  await env.elements.tagInput.dispatch('input');
  await flushMicrotasks();

  assert.equal(env.fetchCalls[1].url, 'https://api.example.test/v1/tags?q=go');
  assert.equal(env.elements.suggestions.children.length, 1);

  await env.elements.suggestions.children[0].click();
  await flushMicrotasks();

  await env.elements.save.click();
  await flushMicrotasks();

  const savePayload = JSON.parse(env.fetchCalls[2].options.body);
  assert.deepEqual(savePayload.tags, ['golang']);
});

test('items list view renders title tags and original link only', async () => {
  const env = await loadSidepanelScript({
    storageData: { token: 'stored-token' },
    fetchHandlers: [
      jsonResponse(200, {
        items: [
          {
            id: 'item-1',
            title: 'Readable Title',
            url: 'https://example.com/read',
            tags: [{ id: 'tag-1', name: 'go', normalized_name: 'go' }],
            content_full: 'hidden body',
            fetch_status: 'pending',
          },
        ],
        pagination: { total: 1 },
      }),
    ],
  });

  assert.equal(env.elements.itemList.children.length, 1);
  const rendered = env.elements.itemList.children[0].innerHTML;
  assert.match(rendered, /Readable Title/);
  assert.match(rendered, /Show original/);
  assert.doesNotMatch(rendered, /hidden body/);
  assert.doesNotMatch(rendered, /pending/);
});

test('list fetch network error shows identifiable message', async () => {
  const env = await loadSidepanelScript({
    storageData: { token: 'stored-token' },
    fetchHandlers: [
      jsonResponse(200, { items: [], pagination: { total: 0 } }),
      () => {
        throw new Error('offline');
      },
    ],
  });

  env.elements.searchInput.value = 'golang';
  await env.elements.searchInput.dispatch('input');
  env.timer.runAll();
  await flushMicrotasks();

  assert.equal(env.elements.utilityStatus.textContent, 'Network error');
  assert.equal(env.elements.utilityStatus.classList.contains('is-error'), true);
});

test('save with denied interactive permission shows permission-required status and aborts', async () => {
  const env = await loadSidepanelScript({
    storageData: { token: 'stored-token' },
    permissionsDefaultGranted: false,
    permissionsRequestResult: false,
  });

  await env.elements.save.click();
  await flushMicrotasks();

  assert.equal(env.permissionRequestCalls.length, 1);
  assert.equal(env.elements.utilityStatus.textContent, 'Site access permission is required');
  assert.equal(env.elements.utilityStatus.classList.contains('is-error'), true);
  assert.equal(env.fetchCalls.length, 0);
});

test('sidepanel layout keeps utility, save, divider, and search/list sections in order', async () => {
  const html = readFileSync(resolve(process.cwd(), 'extension/sidepanel.html'), 'utf8');

  const utilityAt = html.indexOf('class="utility-bar"');
  const saveAt = html.indexOf('class="save-panel"');
  const dividerAt = html.indexOf('class="section-divider"');
  const searchAt = html.indexOf('class="search-panel"');
  const listAt = html.indexOf('class="list-panel"');

  assert.notEqual(utilityAt, -1);
  assert.notEqual(saveAt, -1);
  assert.notEqual(dividerAt, -1);
  assert.notEqual(searchAt, -1);
  assert.notEqual(listAt, -1);
  assert.equal(utilityAt < saveAt, true);
  assert.equal(saveAt < dividerAt, true);
  assert.equal(dividerAt < searchAt, true);
  assert.equal(searchAt < listAt, true);
});

test('save without scripting sends empty title and excerpt in payload', async () => {
  const env = await loadSidepanelScript({
    storageData: { token: 'stored-token' },
    enableScripting: false,
    fetchHandlers: [
      jsonResponse(200, { items: [], pagination: { total: 0 } }),
      jsonResponse(200, { item_id: 'item-456', created: true }),
    ],
    tabURL: 'https://news.example/article',
  });

  await env.elements.save.click();
  await flushMicrotasks();

  assert.equal(env.fetchCalls.length, 2);
  const savePayload = JSON.parse(env.fetchCalls[1].options.body);
  assert.equal(savePayload.url, 'https://news.example/article');
  assert.equal(savePayload.title, '');
  assert.equal(savePayload.excerpt, '');
});

test('save skips prefill for chrome:// URLs and sends empty title/excerpt', async () => {
  const env = await loadSidepanelScript({
    storageData: { token: 'stored-token' },
    enableScripting: true,
    scriptingExecuteResults: [
      { title: 'Should not appear', excerpt: 'Should not appear' },
    ],
    fetchHandlers: [
      jsonResponse(200, { items: [], pagination: { total: 0 } }),
      jsonResponse(200, { item_id: 'item-789', created: false }),
    ],
    tabURL: 'chrome://extensions/',
  });

  await env.elements.save.click();
  await flushMicrotasks();

  assert.equal(env.fetchCalls.length, 2);
  const savePayload = JSON.parse(env.fetchCalls[1].options.body);
  assert.equal(savePayload.title, '');
  assert.equal(savePayload.excerpt, '');
  assert.equal(env.scriptingExecuteCalls.length, 0);
});

test('sidepanel does not provide edit delete or refetch actions', async () => {
  const html = readFileSync(resolve(process.cwd(), 'extension/sidepanel.html'), 'utf8');
  const script = readFileSync(resolve(process.cwd(), 'extension/sidepanel.js'), 'utf8');

  assert.doesNotMatch(html, /Edit|Delete|Refetch/i);
  assert.doesNotMatch(script, /\/refetch|DELETE\s|handleDelete/i);
});
