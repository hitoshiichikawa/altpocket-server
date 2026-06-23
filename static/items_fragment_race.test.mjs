// items_fragment_race.test.mjs
//
// items_search.js (Issue #114) と items_tags.js (Issue #117) は /ui/items の
// 同一ページで同時に動作し、いずれもサーバ側 fragment endpoint を呼んで
// `[data-items-region]` の innerHTML を差し替える。両者が独立に AbortController
// を保持していた段階では、タグクリック直後に検索 debounce 由来の保留 fetch が
// 返ってくると、URL とボタン状態はタグ済みなのに一覧だけ古い検索結果に戻る
// race が発生していた (PR #136 review にて指摘)。
//
// 本テストは、両モジュールが region 要素上の共有 slot
// `__itemsFragmentInflight` を介して AbortController を共有していることを
// 回帰確認する。

import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import test from 'node:test';
import vm from 'node:vm';

// --- Fake DOM primitives (items_tags.test.mjs と等価の最小実装) ---------

class FakeClassList {
  constructor(initial) {
    this._set = new Set((initial || '').split(/\s+/).filter(Boolean));
  }
  add(...names) { for (const n of names) this._set.add(n); }
  remove(...names) { for (const n of names) this._set.delete(n); }
  contains(name) { return this._set.has(name); }
  toString() { return Array.from(this._set).join(' '); }
}

class FakeElement {
  constructor(tagName, attrs = {}) {
    this.tagName = tagName.toUpperCase();
    this.attrs = new Map();
    this.dataset = {};
    this.classList = new FakeClassList(attrs.class || '');
    this.parent = null;
    for (const [k, v] of Object.entries(attrs)) {
      if (k === 'class') continue;
      this.setAttribute(k, v);
    }
  }
  setAttribute(name, value) {
    this.attrs.set(name, String(value));
    if (name.startsWith('data-')) {
      const key = name.slice(5).replace(/-([a-z])/g, (_, c) => c.toUpperCase());
      this.dataset[key] = String(value);
    }
  }
  getAttribute(name) {
    if (name === 'class') return this.classList.toString();
    return this.attrs.has(name) ? this.attrs.get(name) : null;
  }
  closest(selector) {
    let node = this;
    while (node) {
      if (selector === '[data-tag-filter-toggle]' &&
          node.attrs && node.attrs.has('data-tag-filter-toggle')) {
        return node;
      }
      node = node.parent;
    }
    return null;
  }
}

class FakeButton extends FakeElement {
  constructor(normalizedName, { selected = false } = {}) {
    super('button', {
      type: 'button',
      'data-tag-filter-toggle': '',
      'data-tag-normalized': normalizedName,
      'aria-pressed': selected ? 'true' : 'false',
      class: selected ? 'tag tag-filter-toggle is-selected' : 'tag tag-filter-toggle',
    });
  }
}

class FakeInput {
  constructor() {
    this.value = '';
    this.attrs = new Map();
    this.listeners = new Map();
    this.tagName = 'INPUT';
    this.name = 'q';
  }
  addEventListener(type, fn) {
    if (!this.listeners.has(type)) this.listeners.set(type, []);
    this.listeners.get(type).push(fn);
  }
  setAttribute(name, value) { this.attrs.set(name, String(value)); }
  getAttribute(name) { return this.attrs.has(name) ? this.attrs.get(name) : null; }
  async dispatch(type, extra = {}) {
    const handlers = this.listeners.get(type) || [];
    let prevented = false;
    const event = {
      type,
      target: this,
      get defaultPrevented() { return prevented; },
      preventDefault() { prevented = true; },
      ...extra,
    };
    for (const fn of handlers) await fn(event);
    return event;
  }
}

class FakeRegion {
  constructor() {
    this.innerHTML = '';
    this._attr = new Map([['data-items-region', '']]);
  }
  getAttribute(name) { return this._attr.has(name) ? this._attr.get(name) : null; }
}

// --- Shared document/window scaffolding -------------------------------

function createSharedDocument({ inputs, buttons, region }) {
  const docListeners = new Map();
  return {
    activeElement: null,
    addEventListener(type, fn) {
      if (!docListeners.has(type)) docListeners.set(type, []);
      docListeners.get(type).push(fn);
    },
    async dispatch(type, eventInit = {}) {
      const handlers = docListeners.get(type) || [];
      let prevented = false;
      const event = {
        type,
        get defaultPrevented() { return prevented; },
        preventDefault() { prevented = true; },
        ...eventInit,
      };
      for (const fn of handlers) await fn(event);
      return event;
    },
    querySelector(selector) {
      if (selector === '[data-items-region]') return region;
      return null;
    },
    querySelectorAll(selector) {
      if (selector === 'input[name="q"]') return inputs.slice();
      if (selector === '[data-tag-filter-toggle]') return buttons.slice();
      if (selector === 'input[type="checkbox"][name="tag"]') return [];
      return [];
    },
  };
}

function createTimer() {
  let nextID = 1;
  const queue = [];
  return {
    setTimeout(fn, _ms) {
      const t = { id: nextID++, fn, cancelled: false };
      queue.push(t);
      return t.id;
    },
    clearTimeout(id) {
      const t = queue.find((v) => v.id === id);
      if (t) t.cancelled = true;
    },
    runAll() {
      const pending = queue.splice(0, queue.length);
      for (const t of pending) {
        if (!t.cancelled) t.fn();
      }
    },
  };
}

function createHistory(initialURL) {
  const stack = [{ state: null, url: initialURL }];
  let index = 0;
  const calls = [];
  return {
    pushState(state, _title, url) {
      calls.push({ kind: 'push', state, url });
      stack.length = index + 1;
      stack.push({ state, url });
      index = stack.length - 1;
    },
    replaceState(state, _title, url) {
      calls.push({ kind: 'replace', state, url });
      stack[index] = { state, url };
    },
    get currentURL() { return stack[index].url; },
    get calls() { return calls; },
  };
}

function createLocation(history) {
  return {
    get href() { return history.currentURL; },
    set href(_v) { /* noop */ },
  };
}

function createFetchQueue(handlers) {
  const queue = [...handlers];
  const calls = [];
  async function fetch(url, options = {}) {
    calls.push({ url, options });
    if (queue.length === 0) throw new Error(`unexpected fetch: ${url}`);
    const next = queue.shift();
    if (typeof next === 'function') return next(url, options);
    return next;
  }
  return { fetch, calls };
}

function fragmentResponse(html) {
  return { ok: true, status: 200, async text() { return html; } };
}

async function flushMicrotasks(rounds = 24) {
  for (let i = 0; i < rounds; i += 1) await Promise.resolve();
}

// 両 JS モジュールを同じ vm context にロードし、document / region を共有させる。
function loadBothModules({ initialURL, fetchHandlers, buttonTags = [] }) {
  const region = new FakeRegion();
  const inputs = [new FakeInput()];
  const buttons = buttonTags.map((name) => new FakeButton(name));
  const document = createSharedDocument({ inputs, buttons, region });
  const timer = createTimer();
  const history = createHistory(initialURL);
  const location = createLocation(history);
  const { fetch, calls } = createFetchQueue(fetchHandlers);

  const winListeners = new Map();
  const window = {
    document, history, location, fetch,
    setTimeout: (...a) => timer.setTimeout(...a),
    clearTimeout: (...a) => timer.clearTimeout(...a),
    addEventListener(type, fn) {
      if (!winListeners.has(type)) winListeners.set(type, []);
      winListeners.get(type).push(fn);
    },
    AbortController,
  };

  const context = vm.createContext({
    document, window, history, location, fetch,
    setTimeout: window.setTimeout,
    clearTimeout: window.clearTimeout,
    URL, URLSearchParams, AbortController, console,
    globalThis: {},
  });

  // 順序: 実ページの <script defer> の順と揃えて search → tags。
  const searchSrc = readFileSync(resolve(process.cwd(), 'static/items_search.js'), 'utf8');
  new vm.Script(searchSrc, { filename: 'static/items_search.js' }).runInContext(context);
  const tagsSrc = readFileSync(resolve(process.cwd(), 'static/items_tags.js'), 'utf8');
  new vm.Script(tagsSrc, { filename: 'static/items_tags.js' }).runInContext(context);

  return {
    region, inputs, buttons, history, timer,
    fetchCalls: calls,
    typeAndDebounce: async (q) => {
      inputs[0].value = q;
      await inputs[0].dispatch('input');
      timer.runAll();
      await flushMicrotasks();
    },
    clickTag: async (name) => {
      const btn = buttons.find((b) => b.dataset.tagNormalized === name);
      if (!btn) throw new Error(`no button for tag=${name}`);
      return document.dispatch('click', { target: btn });
    },
  };
}

// --- Tests -------------------------------------------------------------

test('要件 2.1 / 5.3: タグクリックは、検索 debounce 由来の保留 fetch を abort する (cross-module race 対策)', async () => {
  let searchSignal = null;
  const env = loadBothModules({
    initialURL: 'http://test.invalid/ui/items',
    fetchHandlers: [
      // 検索 debounce 起源の fetch: 永久に pending させ、タグクリックで
      // abort されることを検証する。
      (_url, options) => {
        searchSignal = options.signal;
        return new Promise(() => { /* never resolves */ });
      },
      // タグクリック起源の fetch: 即座に解決し、region を新しい
      // フラグメントで埋める。
      fragmentResponse('<x>tag-filtered</x>'),
    ],
    buttonTags: ['go'],
  });

  // 1. ユーザが検索を入力し debounce が走って fetch が始まる (まだ未解決)。
  await env.typeAndDebounce('rust');
  assert.equal(env.fetchCalls.length, 1, '検索 fetch が 1 件発火している');
  assert.ok(searchSignal, '検索 fetch には AbortSignal が渡されている');
  assert.equal(searchSignal.aborted, false, '検索 fetch はまだ aborted ではない');

  // 2. ユーザがタグをクリックする。これにより上記 (1) の検索 fetch が
  //    abort され、タグ起源の fetch が開始される。
  await env.clickTag('go');
  await flushMicrotasks();

  assert.equal(searchSignal.aborted, true, 'タグクリックが検索 fetch を abort した');
  assert.equal(env.fetchCalls.length, 2, 'タグ起源の fetch が新たに発火している');
  assert.equal(
    env.region.innerHTML,
    '<x>tag-filtered</x>',
    '最新 (タグ起源) の fragment が region に反映されている',
  );
});

test('要件 2.1 / 5.3: 検索 fetch は、タグクリック由来の保留 fetch を abort する (逆方向の cross-module race 対策)', async () => {
  let tagSignal = null;
  const env = loadBothModules({
    initialURL: 'http://test.invalid/ui/items',
    fetchHandlers: [
      // タグクリック起源の fetch: 永久に pending させる。
      (_url, options) => {
        tagSignal = options.signal;
        return new Promise(() => { /* never resolves */ });
      },
      // 検索 debounce 起源の fetch: 即座に解決する。
      fragmentResponse('<x>search-filtered</x>'),
    ],
    buttonTags: ['go'],
  });

  // 1. タグクリック起源の fetch が先に走り pending になる。
  await env.clickTag('go');
  await flushMicrotasks();
  assert.equal(env.fetchCalls.length, 1, 'タグ fetch が 1 件発火している');
  assert.ok(tagSignal, 'タグ fetch には AbortSignal が渡されている');
  assert.equal(tagSignal.aborted, false, 'タグ fetch はまだ aborted ではない');

  // 2. ユーザが検索を入力し debounce が走る。タグ fetch が abort される。
  await env.typeAndDebounce('rust');

  assert.equal(tagSignal.aborted, true, '検索 fetch がタグ fetch を abort した');
  assert.equal(env.fetchCalls.length, 2, '検索 fetch が新たに発火している');
  assert.equal(
    env.region.innerHTML,
    '<x>search-filtered</x>',
    '最新 (検索起源) の fragment が region に反映されている',
  );
});

test('region 要素上の __itemsFragmentInflight slot が両モジュールから共有されている', async () => {
  // 仕組み自体の白箱テスト: 両モジュールがロードされた時点で
  // region.__itemsFragmentInflight slot が一意に作られ、後から進行中
  // fetch が記録されることを確認する。
  const env = loadBothModules({
    initialURL: 'http://test.invalid/ui/items',
    fetchHandlers: [
      (_url, _options) => new Promise(() => { /* pending */ }),
    ],
    buttonTags: ['go'],
  });

  // 初期状態では slot は存在するが ctrl は null。
  assert.ok(env.region.__itemsFragmentInflight, 'slot が region 上に作られている');
  assert.equal(env.region.__itemsFragmentInflight.ctrl, null, '初期は null');

  // タグクリックで pending fetch が走り、slot に AbortController が
  // 記録される。
  await env.clickTag('go');
  await flushMicrotasks();

  assert.ok(env.region.__itemsFragmentInflight.ctrl, 'タグクリック後は ctrl が入っている');
  assert.equal(env.region.__itemsFragmentInflight.ctrl.signal.aborted, false);
});
