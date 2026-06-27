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

// fake MutationObserver: 実 DOM が無い node:test では childList 変化を自動観測
// できないため、観測対象とコールバックを記録し、テストから手動で fire できる
// 最小実装を用意する（items_drag_tag.js の fragment 再描画再表示テスト用）。
class FakeMutationObserver {
  constructor(cb) {
    this.cb = cb;
    this.observed = [];
    FakeMutationObserver.instances.push(this);
  }
  observe(target, opts) { this.observed.push({ target, opts }); }
  disconnect() { /* noop */ }
  fire(records = [{}]) { this.cb(records); }
}
FakeMutationObserver.instances = [];

function loadModule({
  cards = [],
  dropTags = [],
  fetchHandlers = [],
  pointerCoarse = false,
  locationHref = 'http://localhost/ui/items',
  withMutationObserver = false,
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

  if (withMutationObserver) FakeMutationObserver.instances = [];
  const window = {
    document,
    fetch,
    addEventListener() { /* not used */ },
    location: { href: locationHref },
    altpocketToast: null,
    altpocketNormalizeTagName: null,
    matchMedia(query) {
      return { matches: pointerCoarse && query.includes('coarse'), media: query };
    },
    confirm() { return false; },
    alert() { /* swallow */ },
    __altpocketDragTagSkipAutoInit: true,
  };
  if (withMutationObserver) window.MutationObserver = FakeMutationObserver;

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
  // bulk-tag は受信文字列を display 名として保持するため、正規化値ではなく
  // display 名 (data-tag-name) を送り既存タグ表示名の劣化を防ぐ (#115 契約)。
  assert.equal(body.tag, 'Go', 'ドロップ先タグの display 名を送る');

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
  assert.equal(body.tag, 'Go', '同一の display 名を送る');
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
  assert.equal(body.tag, 'Go', 'タップしたタグの display 名を送る');
  // chip 再描画も同一（Req 4.2）
  const chip = env.cardEl(0).querySelector('.tags').querySelector('[data-tag-normalized="go"]');
  assert.ok(chip, 'タッチ代替でも chip が再描画される');
});

test('Req 4.1 / 4.2: タッチ代替手段で Filters ボタン（ボトムシート開閉）を経由してもタグ付与できる', async () => {
  // モバイルではタグ一覧がボトムシート内にあり、ユーザーは trigger tap のあと
  // Filters ボタン (data-sheet-toggle) を tap してシートを開いてからタグを tap する。
  // この中間 tap で tagging モードが解除されると付与できない（高リスク指摘 #143）。
  const env = loadModule({
    cards: [{ id: 'id-1', title: '記事1', url: 'http://a/1', tags: [] }],
    dropTags: [{ name: 'Go', normalized: 'go' }],
    pointerCoarse: true,
    fetchHandlers: [jsonResponse(200, {
      succeeded: [{ item_id: 'id-1', tags: [{ name: 'Go', normalized_name: 'go' }] }],
      failed: [],
    })],
  });
  // Filters ボタン（ボトムシート開閉トグル）を模した要素。実 UI では data-sheet-toggle
  // を持つ非タグ要素で、これを tap してからタグ一覧へ到達する。
  const sheetToggle = new FakeElement('button', { 'data-sheet-toggle': 'filter-sheet' });

  // 1) trigger を tap して tagging モードへ
  await env.click(env.tagAddEl(0));
  // 2) Filters ボタンを tap してシートを開く（中間操作 / モードを解除してはいけない）
  await env.click(sheetToggle);
  // 3) シート内のタグを tap → 付与される
  await env.click(env.dropEl(0));
  await flushMicrotasks();

  assert.equal(env.fetchCalls.length, 1, 'Filters ボタンを経由してもタグ付与が実行される');
  assert.equal(env.fetchCalls[0].url, '/v1/items/bulk-tag', '既存エンドポイントを使う');
  const body = JSON.parse(env.fetchCalls[0].options.body);
  assert.deepEqual(body.item_ids, ['id-1'], '対象カードの item_id を送る');
  assert.equal(body.tag, 'Go', 'タップしたタグの display 名を送る');
});

test('Req 4.3: tagging モード中に無関係な要素を tap するとモードを解除する（誤付与防止）', async () => {
  // シート開閉トグルやフィルタ UI 以外の無関係 tap では従来どおりモードを解除し、
  // その後のタグ tap が誤って付与に至らないこと。
  const env = loadModule({
    cards: [{ id: 'id-1', title: '記事1', url: 'http://a/1', tags: [] }],
    dropTags: [{ name: 'Go', normalized: 'go' }],
    pointerCoarse: true,
    fetchHandlers: [],
  });
  // フィルタ UI と無関係な要素（例: 本文リンク領域）
  const unrelated = new FakeElement('a', { class: 'tile-link' });

  await env.click(env.tagAddEl(0)); // tagging モードへ
  await env.click(unrelated);       // 無関係 tap → モード解除
  await env.click(env.dropEl(0));   // モード外のタグ tap → 付与しない
  await flushMicrotasks();

  assert.equal(env.fetchCalls.length, 0, '無関係 tap でモード解除後はタグ tap で付与しない');
});

test('Req 1.4 / NFR 3.2: 同一カードへ連続付与時、古いレスポンスが後着しても新しい付与の chip を上書きしない', async () => {
  // go をドロップ（レスポンス保留）→ 続けて rust をドロップ（即時に {go, rust}）。
  // その後 go の古いレスポンス（{go} のみ）が後着しても、chip 列を {go} に巻き戻さず
  // rust を保持する（競合時の成功反映が壊れないこと / medium 指摘 #143）。
  let resolveFirst;
  const pendingFirst = new Promise((r) => { resolveFirst = r; });
  const env = loadModule({
    cards: [{ id: 'id-1', title: '記事1', url: 'http://a/1', tags: [] }],
    dropTags: [{ name: 'Go', normalized: 'go' }, { name: 'Rust', normalized: 'rust' }],
    fetchHandlers: [
      () => pendingFirst, // 1 回目（go）— 後で解決
      jsonResponse(200, { // 2 回目（rust）— 即時に付与後集合 {go, rust} を返す
        succeeded: [{ item_id: 'id-1', tags: [
          { name: 'Go', normalized_name: 'go' },
          { name: 'Rust', normalized_name: 'rust' },
        ] }],
        failed: [],
      }),
    ],
  });
  const dt = makeDataTransfer();

  // 1 回目: go（レスポンス保留）
  await env.dragstart(env.cardEl(0), dt);
  await env.drop(env.dropEl(0), dt);
  // 2 回目: rust（即時 {go, rust}）
  await env.dragstart(env.cardEl(0), dt);
  await env.drop(env.dropEl(1), dt);
  await flushMicrotasks();

  // この時点で最新付与のレスポンスにより chip は {go, rust}
  let tagsDiv = env.cardEl(0).querySelector('.tags');
  assert.ok(tagsDiv.querySelector('[data-tag-normalized="go"]'), '最新付与後に go chip がある');
  assert.ok(tagsDiv.querySelector('[data-tag-normalized="rust"]'), '最新付与後に rust chip がある');

  // 古い go レスポンス（{go} のみ）が後着
  resolveFirst(jsonResponse(200, {
    succeeded: [{ item_id: 'id-1', tags: [{ name: 'Go', normalized_name: 'go' }] }],
    failed: [],
  }));
  await flushMicrotasks();

  // stale なレスポンスでは chip を上書きしない → rust が残る
  tagsDiv = env.cardEl(0).querySelector('.tags');
  assert.ok(tagsDiv.querySelector('[data-tag-normalized="rust"]'), '古いレスポンス後着でも rust chip が残る');
  assert.ok(tagsDiv.querySelector('[data-tag-normalized="go"]'), 'go chip も残る');
});

test('Req 1.4 / NFR 3.2: 後発リクエストの応答が先に返り部分集合でも、先発で付与済みのタグを取りこぼさない', async () => {
  // 送信順は go(先) → rust(後) だが、サーバの処理順次第で後発 rust の応答が先に返り、
  // しかも rust だけ commit した時点の部分集合 {rust}（go を含まない）を返すことがある。
  // その後に先発 go の応答 {go, rust} が後着する。送信順の「最新世代」だけを採用する方式
  // では後発 rust の {rust} で chip を確定させ、永続化済みの go を UI から落としてしまう。
  // 確定タグ集合の union を取ることで、応答がどの順序で返っても go を取りこぼさない（#143）。
  let resolveGo;
  let resolveRust;
  const pendingGo = new Promise((r) => { resolveGo = r; });
  const pendingRust = new Promise((r) => { resolveRust = r; });
  const env = loadModule({
    cards: [{ id: 'id-1', title: '記事1', url: 'http://a/1', tags: [] }],
    dropTags: [{ name: 'Go', normalized: 'go' }, { name: 'Rust', normalized: 'rust' }],
    fetchHandlers: [
      () => pendingGo, // 1 回目（go）— 後で解決（{go, rust} を返す）
      () => pendingRust, // 2 回目（rust）— 先に解決するが部分集合 {rust} を返す
    ],
  });
  const dt = makeDataTransfer();

  // 送信順: go → rust（両方 in-flight）
  await env.dragstart(env.cardEl(0), dt);
  await env.drop(env.dropEl(0), dt);
  await env.dragstart(env.cardEl(0), dt);
  await env.drop(env.dropEl(1), dt);

  // 後発 rust の応答が先に返る（go を含まない部分集合）
  resolveRust(jsonResponse(200, {
    succeeded: [{ item_id: 'id-1', tags: [{ name: 'Rust', normalized_name: 'rust' }] }],
    failed: [],
  }));
  await flushMicrotasks();

  // 先発 go の応答が後着する（付与後集合 {go, rust}）
  resolveGo(jsonResponse(200, {
    succeeded: [{ item_id: 'id-1', tags: [
      { name: 'Go', normalized_name: 'go' },
      { name: 'Rust', normalized_name: 'rust' },
    ] }],
    failed: [],
  }));
  await flushMicrotasks();

  const tagsDiv = env.cardEl(0).querySelector('.tags');
  assert.ok(tagsDiv.querySelector('[data-tag-normalized="rust"]'), 'rust chip がある');
  assert.ok(tagsDiv.querySelector('[data-tag-normalized="go"]'), '部分集合応答が先着でも go chip を取りこぼさない');
});

test('Req 1.4 / NFR 3.2: 後発の付与が失敗し先発が成功した場合、先発で付与したタグを反映する', async () => {
  // go(先) → rust(後) の送信順で、後発 rust が 500 で失敗し、先発 go が成功する。
  // 送信順の「最新世代（rust）」だけを採用する方式では、rust の失敗で busy を解除しつつ
  // 先発 go の成功を stale 扱いで反映せず、永続化済みの go が UI に出ない。確定集合の
  // union 方式では go の成功をそのまま反映する（#143）。
  let resolveGo;
  let resolveRust;
  const pendingGo = new Promise((r) => { resolveGo = r; });
  const pendingRust = new Promise((r) => { resolveRust = r; });
  const env = loadModule({
    cards: [{ id: 'id-1', title: '記事1', url: 'http://a/1', tags: [] }],
    dropTags: [{ name: 'Go', normalized: 'go' }, { name: 'Rust', normalized: 'rust' }],
    fetchHandlers: [
      () => pendingGo, // 1 回目（go）— 成功
      () => pendingRust, // 2 回目（rust）— 500 で失敗
    ],
  });
  const dt = makeDataTransfer();

  await env.dragstart(env.cardEl(0), dt);
  await env.drop(env.dropEl(0), dt);
  await env.dragstart(env.cardEl(0), dt);
  await env.drop(env.dropEl(1), dt);

  // 後発 rust が失敗
  resolveRust(jsonResponse(500, { error: 'err' }));
  await flushMicrotasks();
  // 先発 go が成功
  resolveGo(jsonResponse(200, {
    succeeded: [{ item_id: 'id-1', tags: [{ name: 'Go', normalized_name: 'go' }] }],
    failed: [],
  }));
  await flushMicrotasks();

  const tagsDiv = env.cardEl(0).querySelector('.tags');
  assert.ok(tagsDiv.querySelector('[data-tag-normalized="go"]'), '後発失敗でも先発 go の成功を反映する');
  assert.equal(env.toastCalls.error.length, 1, '失敗した後発について 1 件の失敗通知が出る');
  assert.equal(env.cardEl(0).classList.contains('is-tagging'), false, '全 in-flight 決着で busy が解除される');
});

test('外部テキストをタグ要素にドロップしても付与しない（不明 item id を弾く）', async () => {
  // dataTransfer の text/plain にカード由来でない任意文字列（外部ドラッグ）が入った
  // 場合、region に実在しない id なので bulk-tag を呼ばない（low 指摘 #143）。
  const env = loadModule({
    cards: [{ id: 'id-1', title: '記事1', url: 'http://a/1', tags: [] }],
    dropTags: [{ name: 'Go', normalized: 'go' }],
    fetchHandlers: [],
  });
  const dt = makeDataTransfer();
  // dragstart を経由せず、外部由来のテキストを直接 dataTransfer に載せる
  dt.setData('text/plain', 'not-a-card"]');
  await env.drop(env.dropEl(0), dt);
  await flushMicrotasks();
  assert.equal(env.fetchCalls.length, 0, '不明 id（外部テキスト）のドロップでは付与しない');
});

test('Req 1.5 入力元検証: 外部ドラッグの text/plain が既存 item id と一致してもカード起点でなければ付与しない', async () => {
  // 外部アプリ由来のテキストドラッグで、たまたま text/plain が表示中カードの item id と
  // 一致した場合でも、本モジュールの dragstart を経由していない（カード起点でない）
  // ドロップは付与対象から除外する。dataTransfer は外部から任意に注入できるため、
  // 内部で保持したカード起点ドラッグの id だけを採用する (medium 指摘 #143)。
  const env = loadModule({
    cards: [{ id: 'id-1', title: '記事1', url: 'http://a/1', tags: [] }],
    dropTags: [{ name: 'Go', normalized: 'go' }],
    fetchHandlers: [],
  });
  const dt = makeDataTransfer();
  // dragstart を経由せず、既存カードと同一の id を外部テキストとして直接載せる
  dt.setData('text/plain', 'id-1');
  await env.drop(env.dropEl(0), dt);
  await flushMicrotasks();
  assert.equal(env.fetchCalls.length, 0, 'カード起点でないドロップは既存 id 一致でも付与しない');
});

test('Req 1.5 入力元検証: dragend 後は同一 id の外部ドロップが来ても付与しない（フラグが消費される）', async () => {
  // 正規のカード起点ドラッグ→ドロップで付与した後、dragend で入力元フラグが解放される。
  // 続けて同一 id を載せた外部テキストを直接ドロップしても、stale フラグで付与が走らない
  // ことを確認する（フラグの消費・解放ライフサイクル / medium 指摘 #143）。
  const env = loadModule({
    cards: [{ id: 'id-1', title: '記事1', url: 'http://a/1', tags: [] }],
    dropTags: [{ name: 'Go', normalized: 'go' }],
    // 付与は 1 回だけを期待する。2 回目が走ると fetch queue 枯渇で throw し test が落ちる。
    fetchHandlers: [jsonResponse(200, {
      succeeded: [{ item_id: 'id-1', tags: [{ name: 'Go', normalized_name: 'go' }] }],
      failed: [],
    })],
  });
  // 1) 正規のカード起点ドラッグ→ドロップ→dragend
  const dt = makeDataTransfer();
  await env.dragstart(env.cardEl(0), dt);
  await env.drop(env.dropEl(0), dt);
  await env.dragend(env.cardEl(0), dt);
  await flushMicrotasks();
  assert.equal(env.fetchCalls.length, 1, '正規のカード起点ドロップで 1 回付与する');

  // 2) dragend 後、外部テキスト（同一 id）を直接ドロップ → 付与は増えない
  const extDt = makeDataTransfer();
  extDt.setData('text/plain', 'id-1');
  await env.drop(env.dropEl(0), extDt);
  await flushMicrotasks();
  assert.equal(env.fetchCalls.length, 1, 'dragend 後の外部ドロップでは付与が増えない');
});

test('Req 1.5 入力元検証: タグ外ドロップ後の外部ドロップでも付与しない（dragend がフラグを解放する）', async () => {
  // カード起点でドラッグを開始したがタグ要素以外で離した場合、onDrop は dropTarget なしで
  // 早期 return しフラグを消費しない。その後 dragend がフラグを解放するため、続く非カード
  // 起点（既存 id 一致）の外部ドロップでも付与が走らないことを確認する (#143)。
  const env = loadModule({
    cards: [{ id: 'id-1', title: '記事1', url: 'http://a/1', tags: [] }],
    dropTags: [{ name: 'Go', normalized: 'go' }],
    fetchHandlers: [],
  });
  const dt = makeDataTransfer();
  // カード起点でドラッグ開始 → タグ要素以外（region）にドロップ → dragend で中断
  await env.dragstart(env.cardEl(0), dt);
  await env.drop(env.region, dt);
  await env.dragend(env.cardEl(0), dt);
  await flushMicrotasks();

  // dragend 後、同一 id を載せた外部テキストをタグへ直接ドロップ → 付与しない
  const extDt = makeDataTransfer();
  extDt.setData('text/plain', 'id-1');
  await env.drop(env.dropEl(0), extDt);
  await flushMicrotasks();
  assert.equal(env.fetchCalls.length, 0, 'タグ外ドロップ→dragend 後の外部ドロップでも付与しない');
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

test('Req 2.6 / #115: 既存タグの display 名を保持するため正規化値ではなく display 名を送る', async () => {
  // `Go Lang`（空白・大文字混じり）をドロップしたとき、bulk-tag に正規化値
  // `go lang` を送ると server が display 名を入力文字列で上書きし表示名が劣化する。
  // display 名 `Go Lang` を送ることで既存タグの表示名保持契約を満たす。
  const env = loadModule({
    cards: [{ id: 'id-1', title: '記事1', url: 'http://a/1', tags: [] }],
    dropTags: [{ name: 'Go Lang', normalized: 'go lang' }],
    fetchHandlers: [jsonResponse(200, {
      succeeded: [{ item_id: 'id-1', tags: [{ name: 'Go Lang', normalized_name: 'go lang' }] }],
      failed: [],
    })],
  });
  const dt = makeDataTransfer();
  await env.dragstart(env.cardEl(0), dt);
  await env.drop(env.dropEl(0), dt);
  await flushMicrotasks();

  const body = JSON.parse(env.fetchCalls[0].options.body);
  assert.equal(body.tag, 'Go Lang', '正規化値 (go lang) ではなく display 名 (Go Lang) を送る');
});

test('NFR 2.2 / #117: 絞り込み中タグへの再ドロップで chip の選択状態 (is-selected/aria-pressed) を維持する', async () => {
  // `?tag=go` で絞り込み中。既に go を持つカードへ go を再ドロップしたとき、
  // 再構築 chip は SSR 同様に is-selected + aria-pressed=true を保つ必要がある。
  const env = loadModule({
    locationHref: 'http://localhost/ui/items?tag=go',
    cards: [{ id: 'id-1', title: '記事1', url: 'http://a/1', tags: [{ name: 'Go', normalized_name: 'go' }] }],
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

  const chip = env.cardEl(0).querySelector('.tags').querySelector('[data-tag-normalized="go"]');
  assert.ok(chip, 're-drop 後も go chip が描画される');
  assert.ok(chip.classList.contains('is-selected'), '絞り込み中タグの chip は is-selected を維持する');
  assert.equal(chip.getAttribute('aria-pressed'), 'true', '絞り込み中タグの chip は aria-pressed=true を維持する');
});

test('NFR 2.2 / #117: 絞り込みしていないタグの再構築 chip は未選択 (aria-pressed=false) のまま', async () => {
  // 絞り込みなし URL では、付与後 chip は SSR 同様に未選択状態で描画される。
  const env = loadModule({
    locationHref: 'http://localhost/ui/items',
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

  const chip = env.cardEl(0).querySelector('.tags').querySelector('[data-tag-normalized="go"]');
  assert.equal(chip.classList.contains('is-selected'), false, '絞り込み外タグは is-selected を付けない');
  assert.equal(chip.getAttribute('aria-pressed'), 'false', '絞り込み外タグは aria-pressed=false');
});

test('Req 4.1 / NFR 2.1: fragment 再描画後もタッチ代替トリガを再表示する (MutationObserver)', async () => {
  // 検索 / 絞り込み / 状態タブ / ページ送りは region.innerHTML を差し替え、新カードの
  // [data-card-tag-add] は初期 hidden に戻る。fragment 差し替えを観測して再表示する。
  const env = loadModule({
    cards: [{ id: 'id-1', title: '記事1', url: 'http://a/1', tags: [] }],
    dropTags: [{ name: 'Go', normalized: 'go' }],
    pointerCoarse: true,
    withMutationObserver: true,
  });
  // 初期表示は revealTouchTriggers で表示済み
  assert.equal(env.tagAddEl(0).hidden, false, '初期化時に既存トリガが表示される');

  // fragment 差し替えをシミュレート: region の children を新カードに置換する
  env.region.replaceChildren();
  const { tagAdd: newTrigger } = buildCard(env.region, { id: 'id-2', title: '記事2', url: 'http://a/2', tags: [] });
  assert.equal(newTrigger.hidden, true, '差し替え直後の新カードのトリガは hidden');

  // MutationObserver を fire（実 DOM が無いため手動）
  assert.ok(FakeMutationObserver.instances.length >= 1, 'region を監視する observer が作られる');
  FakeMutationObserver.instances[FakeMutationObserver.instances.length - 1].fire();

  assert.equal(newTrigger.hidden, false, 'fragment 再描画後に新カードのトリガが再表示される');
});

test('Req 4.1: 非タッチ環境では MutationObserver を作らない（SSR の hidden を尊重）', async () => {
  FakeMutationObserver.instances = [];
  const env = loadModule({
    cards: [{ id: 'id-1', title: '記事1', url: 'http://a/1', tags: [] }],
    dropTags: [{ name: 'Go', normalized: 'go' }],
    pointerCoarse: false,
    withMutationObserver: true,
  });
  void env;
  assert.equal(FakeMutationObserver.instances.length, 0, '非タッチ環境では observer を作らない');
});

test('NFR 3.1: ドロップ直後にカードへ busy 状態 (is-tagging/aria-busy) を同期付与し、完了で解除する', async () => {
  // 遅い通信でも「処理を開始した」フィードバックを即時に出すため、fetch 解決を待たず
  // 同期的に busy 状態を付与する。完了後（成功）に解除されること。
  let resolveFetch;
  const pending = new Promise((r) => { resolveFetch = r; });
  const env = loadModule({
    cards: [{ id: 'id-1', title: '記事1', url: 'http://a/1', tags: [] }],
    dropTags: [{ name: 'Go', normalized: 'go' }],
    fetchHandlers: [() => pending],
  });
  const dt = makeDataTransfer();
  await env.dragstart(env.cardEl(0), dt);
  await env.drop(env.dropEl(0), dt);

  // fetch 未解決の時点で busy が付いている（300ms 以内の同期フィードバック）
  assert.ok(env.cardEl(0).classList.contains('is-tagging'), '処理開始時に is-tagging が同期付与される');
  assert.equal(env.cardEl(0).getAttribute('aria-busy'), 'true', '処理開始時に aria-busy=true');

  // 成功応答で解決 → busy 解除
  resolveFetch(jsonResponse(200, {
    succeeded: [{ item_id: 'id-1', tags: [{ name: 'Go', normalized_name: 'go' }] }],
    failed: [],
  }));
  await flushMicrotasks();

  assert.equal(env.cardEl(0).classList.contains('is-tagging'), false, '完了で is-tagging が解除される');
  assert.equal(env.cardEl(0).getAttribute('aria-busy'), null, '完了で aria-busy が解除される');
});

test('Req 5.3: セッション失効 (401) の非 200 応答ではカード表示を変えず通知し busy を解除する', async () => {
  // server 側でセッション失効 → 401。bulk-tag の非 200 分岐 (status !== 200) を
  // 直接検証する。chip を成功状態にせず、失敗通知を出し、busy を残さない。
  const env = loadModule({
    cards: [{ id: 'id-1', title: '記事1', url: 'http://a/1', tags: [] }],
    dropTags: [{ name: 'Go', normalized: 'go' }],
    fetchHandlers: [jsonResponse(401, { error: 'unauthorized' })],
  });
  const dt = makeDataTransfer();
  await env.dragstart(env.cardEl(0), dt);
  await env.drop(env.dropEl(0), dt);
  await flushMicrotasks();

  const tagsDiv = env.cardEl(0).querySelector('.tags');
  if (tagsDiv) {
    assert.equal(tagsDiv.querySelector('[data-tag-normalized="go"]'), null, '401 時に go chip を付けない');
  }
  assert.equal(env.toastCalls.error.length, 1, '401 時に失敗通知が 1 件出る');
  assert.equal(env.cardEl(0).classList.contains('is-tagging'), false, '401 後に busy が解除される');
});

test('Req 5.x: 認可エラー (403) / サーバエラー (500) の非 200 分岐でも失敗通知する', async () => {
  for (const status of [403, 500]) {
    const env = loadModule({
      cards: [{ id: 'id-1', title: '記事1', url: 'http://a/1', tags: [] }],
      dropTags: [{ name: 'Go', normalized: 'go' }],
      fetchHandlers: [jsonResponse(status, { error: 'err' })],
    });
    const dt = makeDataTransfer();
    await env.dragstart(env.cardEl(0), dt);
    await env.drop(env.dropEl(0), dt);
    await flushMicrotasks();
    assert.equal(env.toastCalls.error.length, 1, `${status} 応答で失敗通知が 1 件出る`);
    const tagsDiv = env.cardEl(0).querySelector('.tags');
    if (tagsDiv) {
      assert.equal(tagsDiv.querySelector('[data-tag-normalized="go"]'), null, `${status} 時に chip を付けない`);
    }
  }
});
