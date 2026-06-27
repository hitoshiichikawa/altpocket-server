// items_drag_tag.test.mjs
//
// /ui/items のカードからサイドバータグへのドラッグ&ドロップ・タグ付与モジュール
// (Issue #120) を司る `static/items_drag_tag.js` の単体テスト。
//
// `items_bulk_actions.test.mjs` と同じ規約で、実 DOM を持たない node:test 上で
// 動作させるため、本機能の AC が要求する範囲（dragstart / dragover / dragleave /
// drop / dragend / click / preventDefault / closest / querySelector(All) / dataset /
// classList / fetch / dataTransfer / matchMedia）に絞った fake DOM を用意し、
// vm.createContext で items_drag_tag.js を評価する。
//
// 各 AC（正常付与 / 冪等再ドロップ / タグ外ドロップ=no-op / 通信失敗時に
// カード表示を変えず通知 / タッチ代替手段 / 既存挙動非回帰）に最低 1 ケース。

import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import test from 'node:test';
import vm from 'node:vm';

// --- Fake DOM primitives ------------------------------------------------

class FakeClassList {
  constructor(initial) {
    this._set = new Set((initial || '').split(/\s+/).filter(Boolean));
  }
  add(...names) { for (const n of names) this._set.add(n); }
  remove(...names) { for (const n of names) this._set.delete(n); }
  contains(name) { return this._set.has(name); }
  toggle(name, force) {
    if (force === true) { this._set.add(name); return true; }
    if (force === false) { this._set.delete(name); return false; }
    if (this._set.has(name)) { this._set.delete(name); return false; }
    this._set.add(name); return true;
  }
  toString() { return Array.from(this._set).join(' '); }
}

class FakeElement {
  constructor(tagName, attrs = {}) {
    this.tagName = tagName.toUpperCase();
    this.attrs = new Map();
    this.dataset = {};
    this.classList = new FakeClassList(attrs.class || '');
    this.children = [];
    this.parent = null;
    this.textContent = '';
    this.removed = false;
    this.disabled = false;
    this.hidden = false;
    this._listeners = new Map();
    this.value = '';
    for (const [k, v] of Object.entries(attrs)) {
      if (k === 'class') continue;
      this.setAttribute(k, v);
    }
  }

  appendChild(child) {
    child.parent = this;
    this.children.push(child);
    return child;
  }
  removeChild(child) {
    const idx = this.children.indexOf(child);
    if (idx !== -1) {
      this.children.splice(idx, 1);
      child.parent = null;
    }
  }
  get firstChild() { return this.children[0] || null; }
  replaceChildren(...nodes) {
    for (const c of this.children) c.parent = null;
    this.children.length = 0;
    for (const n of nodes) { n.parent = this; this.children.push(n); }
  }

  setAttribute(name, value) {
    this.attrs.set(name, String(value));
    if (name === 'class') this.classList = new FakeClassList(String(value));
    if (name === 'hidden') this.hidden = true;
    if (name === 'disabled') this.disabled = true;
    if (name.startsWith('data-')) {
      const key = name.slice(5).replace(/-([a-z])/g, (_, c) => c.toUpperCase());
      this.dataset[key] = String(value);
    }
  }
  removeAttribute(name) {
    this.attrs.delete(name);
    if (name === 'hidden') this.hidden = false;
    if (name === 'disabled') this.disabled = false;
    if (name.startsWith('data-')) {
      const key = name.slice(5).replace(/-([a-z])/g, (_, c) => c.toUpperCase());
      delete this.dataset[key];
    }
  }
  getAttribute(name) {
    if (name === 'class') return this.classList.toString();
    return this.attrs.has(name) ? this.attrs.get(name) : null;
  }
  hasAttribute(name) {
    if (name === 'hidden') return this.hidden;
    return this.attrs.has(name);
  }
  matches(selector) { return matchesSelector(this, selector); }
  closest(selector) {
    let node = this;
    while (node) {
      if (matchesSelector(node, selector)) return node;
      node = node.parent;
    }
    return null;
  }
  querySelector(selector) {
    const stack = [...this.children];
    while (stack.length) {
      const n = stack.shift();
      if (matchesSelector(n, selector)) return n;
      if (n.children && n.children.length) stack.push(...n.children);
    }
    return null;
  }
  querySelectorAll(selector) {
    const out = [];
    const stack = [...this.children];
    while (stack.length) {
      const n = stack.shift();
      if (matchesSelector(n, selector)) out.push(n);
      if (n.children && n.children.length) stack.push(...n.children);
    }
    return out;
  }
  addEventListener(type, fn) {
    if (!this._listeners.has(type)) this._listeners.set(type, []);
    this._listeners.get(type).push(fn);
  }
  focus() { /* noop */ }
}

function matchesSelector(node, selector) {
  if (!node || !node.attrs) return false;
  if (selector.indexOf(',') !== -1) {
    const parts = selector.split(',').map((s) => s.trim()).filter(Boolean);
    for (const p of parts) if (matchesSelector(node, p)) return true;
    return false;
  }
  const tagOnly = /^([a-z][a-z0-9]*)$/i.exec(selector);
  if (tagOnly) return node.tagName === tagOnly[1].toUpperCase();
  const classMatch = /^([a-z][a-z0-9]*|)\.([\w-]+)$/i.exec(selector);
  if (classMatch) {
    const tag = classMatch[1];
    const cls = classMatch[2];
    if (tag && node.tagName !== tag.toUpperCase()) return false;
    return node.classList && node.classList.contains(cls);
  }
  const attrMatch = /^\[([\w-]+)(?:="([^"]*)")?\]$/.exec(selector);
  if (attrMatch) {
    const name = attrMatch[1];
    const val = attrMatch[2];
    if (!node.attrs.has(name)) return false;
    if (val == null) return true;
    return node.attrs.get(name) === val;
  }
  const tagAttrMatch = /^([a-z][a-z0-9]*)\[([\w-]+)(?:="([^"]*)")?\]$/i.exec(selector);
  if (tagAttrMatch) {
    const tag = tagAttrMatch[1];
    const name = tagAttrMatch[2];
    const val = tagAttrMatch[3];
    if (tag && node.tagName !== tag.toUpperCase()) return false;
    if (!node.attrs.has(name)) return false;
    if (val == null) return true;
    return node.attrs.get(name) === val;
  }
  const multiClassMatch = /^([a-z][a-z0-9]*|)((?:\.[\w-]+)+)$/i.exec(selector);
  if (multiClassMatch) {
    const tag = multiClassMatch[1];
    const classes = multiClassMatch[2].slice(1).split('.');
    if (tag && node.tagName !== tag.toUpperCase()) return false;
    for (const c of classes) if (!node.classList || !node.classList.contains(c)) return false;
    return true;
  }
  return false;
}

// --- DOM builders -------------------------------------------------------

function buildCard(parent, { id, title, url, tags }) {
  const card = new FakeElement('article', {
    class: 'tile item-card',
    'data-item-id': id,
    'data-item-card': '',
    'data-original-url': url || '',
    draggable: 'true',
  });
  const h3 = new FakeElement('h3', { id: 'item-title-' + id });
  h3.textContent = title || '';
  card.appendChild(h3);
  if (tags && tags.length > 0) {
    const tagsContainer = new FakeElement('div', { class: 'tags' });
    for (const t of tags) {
      const btn = new FakeElement('button', {
        type: 'button',
        class: 'tag tag-filter-toggle',
        'data-tag-filter-toggle': '',
        'data-tag-normalized': t.normalized_name,
        'aria-pressed': 'false',
        'aria-label': 'タグで絞り込み: ' + t.name,
      });
      btn.textContent = t.name;
      tagsContainer.appendChild(btn);
    }
    card.appendChild(tagsContainer);
  }
  // touch trigger
  const actions = new FakeElement('div', { class: 'actions item-actions' });
  const tagAdd = new FakeElement('button', {
    type: 'button',
    class: 'btn-secondary card-tag-add',
    'data-card-tag-add': '',
    'data-item-id': id,
    hidden: '',
  });
  tagAdd.textContent = 'タグ付与';
  actions.appendChild(tagAdd);
  card.appendChild(actions);
  parent.appendChild(card);
  return { card, tagAdd };
}

function buildDropTarget(parent, { name, normalized }) {
  const label = new FakeElement('label', {
    class: 'tag-filter-option',
    'data-tag-drop-target': '',
    'data-tag-name': name,
    'data-tag-normalized': normalized,
  });
  const input = new FakeElement('input', { type: 'checkbox', name: 'tag', value: normalized });
  label.appendChild(input);
  parent.appendChild(label);
  return label;
}

function createFakeDocument({ region, dropTargets }) {
  const docListeners = new Map();
  const root = new FakeElement('div', {});
  root.appendChild(region);
  const targetsContainer = new FakeElement('div', { class: 'tag-list' });
  for (const dt of dropTargets) targetsContainer.appendChild(dt);
  root.appendChild(targetsContainer);
  const metaCSRF = new FakeElement('meta', { name: 'csrf-token', content: 'test-csrf' });
  root.appendChild(metaCSRF);

  const doc = {
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
      if (selector === 'meta[name="csrf-token"]') return metaCSRF;
      return root.querySelector(selector);
    },
    querySelectorAll(selector) { return root.querySelectorAll(selector); },
    createElement(tag) { return new FakeElement(tag, {}); },
    _listenerCount(type) { return (docListeners.get(type) || []).length; },
  };
  return doc;
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

function jsonResponse(status, body) {
  return {
    ok: status >= 200 && status < 300,
    status,
    async json() { return body; },
    async text() { return JSON.stringify(body); },
  };
}

function makeDataTransfer() {
  const store = new Map();
  return {
    _store: store,
    effectAllowed: '',
    dropEffect: '',
    setData(type, value) { store.set(type, String(value)); },
    getData(type) { return store.has(type) ? store.get(type) : ''; },
  };
}

async function flushMicrotasks(rounds = 32) {
  for (let i = 0; i < rounds; i += 1) await Promise.resolve();
}

function loadModule({
  cards = [],
  dropTags = [],
  fetchHandlers = [],
  pointerCoarse = false,
} = {}) {
  const region = new FakeElement('section', { class: 'items', 'data-items-region': '' });
  const builtCards = [];
  for (const cd of cards) builtCards.push(buildCard(region, cd));
  const builtTargets = [];
  for (const dt of dropTags) builtTargets.push(buildDropTarget(new FakeElement('div', {}), dt));

  const document = createFakeDocument({ region, dropTargets: builtTargets });
  const { fetch, calls } = createFetchQueue(fetchHandlers);
  const toastCalls = { error: [], success: [], info: [] };
  const toast = {
    error(msg) { toastCalls.error.push(String(msg)); },
    success(msg) { toastCalls.success.push(String(msg)); },
    info(msg) { toastCalls.info.push(String(msg)); },
  };

  const window = {
    document,
    fetch,
    addEventListener() { /* not used */ },
    location: { href: 'http://localhost/ui/items' },
    altpocketToast: null,
    altpocketNormalizeTagName: null,
    matchMedia(query) {
      return { matches: pointerCoarse && query.includes('coarse'), media: query };
    },
    confirm() { return false; },
    alert() { /* swallow */ },
    __altpocketDragTagSkipAutoInit: true,
  };

  const context = vm.createContext({
    document, window,
    URL, URLSearchParams, console,
    Set, Map, Array, JSON, Promise, Error,
    globalThis: {},
  });

  const source = readFileSync(resolve(process.cwd(), 'static/items_drag_tag.js'), 'utf8');
  new vm.Script(source, { filename: 'static/items_drag_tag.js' }).runInContext(context);

  const initFn = window.altpocketDragTagInit;
  const api = initFn ? initFn({ document, window, fetch, toast }) : null;

  function cardEl(i) { return builtCards[i].card; }
  function tagAddEl(i) { return builtCards[i].tagAdd; }
  function dropEl(i) { return builtTargets[i]; }

  return {
    region, document, window, api,
    fetchCalls: calls,
    toastCalls,
    cardEl, tagAddEl, dropEl,
    // event dispatch helpers (document delegated)
    async dragstart(card, dataTransfer) {
      return document.dispatch('dragstart', { target: card, dataTransfer });
    },
    async dragover(target, dataTransfer) {
      return document.dispatch('dragover', { target, dataTransfer });
    },
    async dragleave(target, dataTransfer) {
      return document.dispatch('dragleave', { target, dataTransfer });
    },
    async drop(target, dataTransfer) {
      return document.dispatch('drop', { target, dataTransfer });
    },
    async dragend(card, dataTransfer) {
      return document.dispatch('dragend', { target: card, dataTransfer });
    },
    async click(target) {
      return document.dispatch('click', { target });
    },
  };
}

// --- Tests --------------------------------------------------------------

test('init() が null DOM では何もしない（region 不在）', () => {
  const window = { document: null };
  const context = vm.createContext({ window, document: null, console, Set, Map, JSON, Promise, Error });
  const source = readFileSync(resolve(process.cwd(), 'static/items_drag_tag.js'), 'utf8');
  new vm.Script(source, { filename: 'static/items_drag_tag.js' }).runInContext(context);
  const api = window.altpocketDragTagInit({ document: null, window });
  assert.equal(api, null, 'document 不在では init は null を返す');
});

test('Req 1.3 / 1.4 / 2.1: カードをタグ要素にドロップすると bulk-tag API で付与し chip を再描画する', async () => {
  const env = loadModule({
    cards: [{ id: 'id-1', title: '記事1', url: 'http://a/1', tags: [] }],
    dropTags: [{ name: 'Go', normalized: 'go' }],
    fetchHandlers: [jsonResponse(200, {
      succeeded: [{ item_id: 'id-1', tags: [{ name: 'Go', normalized_name: 'go' }] }],
      failed: [],
    })],
  });
  const dt = makeDataTransfer();
  await env.dragstart(env.cardEl(0), dt);
  await env.drop(env.dropEl(0), dt);
  await flushMicrotasks();

  // bulk-tag が単一 item_id で呼ばれる
  assert.equal(env.fetchCalls.length, 1, 'fetch が 1 回呼ばれる');
  assert.equal(env.fetchCalls[0].url, '/v1/items/bulk-tag', 'bulk-tag エンドポイントを使う');
  const body = JSON.parse(env.fetchCalls[0].options.body);
  assert.deepEqual(body.item_ids, ['id-1'], '単一 item_id を送る');
  assert.equal(body.tag, 'go', 'ドロップ先タグの正規化値を送る');

  // chip が再描画される（Req 1.4）
  const tagsDiv = env.cardEl(0).querySelector('.tags');
  assert.ok(tagsDiv, 'chip コンテナが作られる');
  const chip = tagsDiv.querySelector('[data-tag-normalized="go"]');
  assert.ok(chip, 'go の chip が描画される');
  assert.equal(chip.textContent, 'Go', 'display 名で描画される');
});

test('Req 1.5: カードをタグ要素以外（ドロップ対象外）にドロップしてもタグ付与しない', async () => {
  const env = loadModule({
    cards: [{ id: 'id-1', title: '記事1', url: 'http://a/1', tags: [] }],
    dropTags: [{ name: 'Go', normalized: 'go' }],
    fetchHandlers: [],
  });
  const dt = makeDataTransfer();
  await env.dragstart(env.cardEl(0), dt);
  // region 自身（ドロップ先でない要素）に drop
  await env.drop(env.region, dt);
  await flushMicrotasks();
  assert.equal(env.fetchCalls.length, 0, 'ドロップ対象外では fetch を呼ばない');
});

test('Req 2.3 / 2.4: 既に当該タグを持つアイテムを同じタグへ再ドロップしてもエラーにならず重複しない', async () => {
  const env = loadModule({
    cards: [{ id: 'id-1', title: '記事1', url: 'http://a/1', tags: [{ name: 'Go', normalized_name: 'go' }] }],
    dropTags: [{ name: 'Go', normalized: 'go' }],
    // bulk-tag は additive かつ冪等。再ドロップでも 200 + 同じ tag 集合を返す。
    fetchHandlers: [jsonResponse(200, {
      succeeded: [{ item_id: 'id-1', tags: [{ name: 'Go', normalized_name: 'go' }] }],
      failed: [],
    })],
  });
  const dt = makeDataTransfer();
  await env.dragstart(env.cardEl(0), dt);
  await env.drop(env.dropEl(0), dt);
  await flushMicrotasks();

  assert.equal(env.toastCalls.error.length, 0, '冪等再ドロップでエラー通知が出ない');
  const chips = env.cardEl(0).querySelector('.tags').querySelectorAll('[data-tag-normalized="go"]');
  assert.equal(chips.length, 1, 'go の chip が重複しない（1 件）');
});

test('Req 5.1 / 5.2: 通信失敗時はカードの chip 表示を成功状態にせず失敗を通知する', async () => {
  const env = loadModule({
    cards: [{ id: 'id-1', title: '記事1', url: 'http://a/1', tags: [] }],
    dropTags: [{ name: 'Go', normalized: 'go' }],
    fetchHandlers: [() => { throw new Error('network down'); }],
  });
  const dt = makeDataTransfer();
  await env.dragstart(env.cardEl(0), dt);
  await env.drop(env.dropEl(0), dt);
  await flushMicrotasks();

  // chip は付与されない（Req 5.1）
  const tagsDiv = env.cardEl(0).querySelector('.tags');
  if (tagsDiv) {
    assert.equal(tagsDiv.querySelector('[data-tag-normalized="go"]'), null, '失敗時に go chip を付けない');
  }
  // 失敗通知（Req 5.2）
  assert.equal(env.toastCalls.error.length, 1, '失敗の通知が 1 件出る');
});

test('Req 5.5: server が failed[] を返した（所有していない等）場合はカード表示を変えず通知する', async () => {
  const env = loadModule({
    cards: [{ id: 'id-1', title: '記事1', url: 'http://a/1', tags: [] }],
    dropTags: [{ name: 'Go', normalized: 'go' }],
    fetchHandlers: [jsonResponse(200, {
      succeeded: [],
      failed: [{ item_id: 'id-1', reason: 'not_found' }],
    })],
  });
  const dt = makeDataTransfer();
  await env.dragstart(env.cardEl(0), dt);
  await env.drop(env.dropEl(0), dt);
  await flushMicrotasks();

  const tagsDiv = env.cardEl(0).querySelector('.tags');
  if (tagsDiv) {
    assert.equal(tagsDiv.querySelector('[data-tag-normalized="go"]'), null, 'failed 時に chip を付けない');
  }
  assert.equal(env.toastCalls.error.length, 1, 'failed 時の通知が 1 件出る');
});

test('Req 3.1: ドラッグ開始でカードに is-dragging 視覚状態が付く', async () => {
  const env = loadModule({
    cards: [{ id: 'id-1', title: '記事1', url: 'http://a/1', tags: [] }],
    dropTags: [{ name: 'Go', normalized: 'go' }],
  });
  const dt = makeDataTransfer();
  await env.dragstart(env.cardEl(0), dt);
  assert.ok(env.cardEl(0).classList.contains('is-dragging'), 'is-dragging が付く');
});

test('Req 3.2 / 3.3: dragover でドロップ先候補がハイライトされ dragleave / dragend で解除される', async () => {
  const env = loadModule({
    cards: [{ id: 'id-1', title: '記事1', url: 'http://a/1', tags: [] }],
    dropTags: [{ name: 'Go', normalized: 'go' }],
  });
  const dt = makeDataTransfer();
  await env.dragstart(env.cardEl(0), dt);
  const over = await env.dragover(env.dropEl(0), dt);
  assert.ok(over.defaultPrevented, 'dragover は preventDefault される（drop 許可）');
  assert.ok(env.dropEl(0).classList.contains('is-drop-target'), 'ドロップ先候補ハイライトが付く');

  await env.dragleave(env.dropEl(0), dt);
  assert.equal(env.dropEl(0).classList.contains('is-drop-target'), false, 'dragleave で候補ハイライトが外れる');

  // 再度 over してから dragend で全解除されること
  await env.dragover(env.dropEl(0), dt);
  await env.dragend(env.cardEl(0), dt);
  assert.equal(env.cardEl(0).classList.contains('is-dragging'), false, 'dragend で is-dragging が外れる');
  assert.equal(env.dropEl(0).classList.contains('is-drop-target'), false, 'dragend で候補ハイライトも全解除');
});

test('Req 3.4 / 3.5: ボトムシート側（重複描画）のタグへドロップしても同一付与挙動になる', async () => {
  const env = loadModule({
    cards: [{ id: 'id-1', title: '記事1', url: 'http://a/1', tags: [] }],
    // サイドバー + ボトムシートで go が 2 件重複描画される想定
    dropTags: [{ name: 'Go', normalized: 'go' }, { name: 'Go', normalized: 'go' }],
    fetchHandlers: [jsonResponse(200, {
      succeeded: [{ item_id: 'id-1', tags: [{ name: 'Go', normalized_name: 'go' }] }],
      failed: [],
    })],
  });
  const dt = makeDataTransfer();
  await env.dragstart(env.cardEl(0), dt);
  // 2 個目（ボトムシート側）にドロップ
  await env.drop(env.dropEl(1), dt);
  await flushMicrotasks();
  assert.equal(env.fetchCalls.length, 1, 'ボトムシート側ドロップでも fetch 1 回');
  const body = JSON.parse(env.fetchCalls[0].options.body);
  assert.equal(body.tag, 'go', '同一の正規化値を送る');
});

test('Req 4.1: タッチ環境（pointer:coarse）では card-tag-add トリガを表示する', async () => {
  const env = loadModule({
    cards: [{ id: 'id-1', title: '記事1', url: 'http://a/1', tags: [] }],
    dropTags: [{ name: 'Go', normalized: 'go' }],
    pointerCoarse: true,
  });
  assert.equal(env.tagAddEl(0).hidden, false, 'タッチ環境では touch trigger が表示される');
});

test('Req 4.1: 非タッチ環境では card-tag-add トリガは非表示のまま', async () => {
  const env = loadModule({
    cards: [{ id: 'id-1', title: '記事1', url: 'http://a/1', tags: [] }],
    dropTags: [{ name: 'Go', normalized: 'go' }],
    pointerCoarse: false,
  });
  assert.equal(env.tagAddEl(0).hidden, true, '非タッチ環境では touch trigger は隠れたまま');
});

test('Req 4.2 / 4.3: タッチ代替手段（trigger→タグ tap）でドロップと同一の付与・冪等挙動になる', async () => {
  const env = loadModule({
    cards: [{ id: 'id-1', title: '記事1', url: 'http://a/1', tags: [] }],
    dropTags: [{ name: 'Go', normalized: 'go' }],
    pointerCoarse: true,
    fetchHandlers: [jsonResponse(200, {
      succeeded: [{ item_id: 'id-1', tags: [{ name: 'Go', normalized_name: 'go' }] }],
      failed: [],
    })],
  });
  // 1) trigger を tap して tagging モードへ
  await env.click(env.tagAddEl(0));
  // 2) サイドバーのタグを tap → 付与
  await env.click(env.dropEl(0));
  await flushMicrotasks();

  assert.equal(env.fetchCalls.length, 1, 'タッチ代替でも bulk-tag を 1 回呼ぶ');
  assert.equal(env.fetchCalls[0].url, '/v1/items/bulk-tag', '既存エンドポイントを使う');
  const body = JSON.parse(env.fetchCalls[0].options.body);
  assert.deepEqual(body.item_ids, ['id-1'], '対象カードの item_id を送る');
  assert.equal(body.tag, 'go', 'タップしたタグの正規化値を送る');
  // chip 再描画も同一（Req 4.2）
  const chip = env.cardEl(0).querySelector('.tags').querySelector('[data-tag-normalized="go"]');
  assert.ok(chip, 'タッチ代替でも chip が再描画される');
});

test('Req 4.3 / 4.2: tagging モード中でないタグ tap は付与しない（モード解除後の誤発火防止）', async () => {
  const env = loadModule({
    cards: [{ id: 'id-1', title: '記事1', url: 'http://a/1', tags: [] }],
    dropTags: [{ name: 'Go', normalized: 'go' }],
    pointerCoarse: true,
    fetchHandlers: [],
  });
  // trigger を押さずにいきなりタグを tap → 何も起きない
  await env.click(env.dropEl(0));
  await flushMicrotasks();
  assert.equal(env.fetchCalls.length, 0, 'tagging モード外のタグ tap では付与しない');
});

test('NFR 2.2 / 2.3: 既存タグボタン / チェックボックスへの click を drag-tag が intercept しない', async () => {
  // drag-tag は dragstart/dragover/drop と「tagging モード中の tap」だけを扱う。
  // tagging モード外の通常 click は他モジュール（items_tags.js 等）に委ねる。
  const env = loadModule({
    cards: [{ id: 'id-1', title: '記事1', url: 'http://a/1', tags: [{ name: 'Go', normalized_name: 'go' }] }],
    dropTags: [{ name: 'Go', normalized: 'go' }],
    pointerCoarse: false,
    fetchHandlers: [],
  });
  // カード上タグ chip を click（絞り込みトグルは items_tags.js の責務）
  const chip = env.cardEl(0).querySelector('.tags').querySelector('[data-tag-normalized="go"]');
  await env.click(chip);
  await flushMicrotasks();
  assert.equal(env.fetchCalls.length, 0, 'drag-tag は通常 click で fetch しない（非回帰）');
});
