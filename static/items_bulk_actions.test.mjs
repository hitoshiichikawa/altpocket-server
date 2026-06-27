// items_bulk_actions.test.mjs
//
// /ui/items の一括操作 actions モジュール (Issue #118 task 7) を司る
// `static/items_bulk_actions.js` の単体テスト。
//
// `items_status.test.mjs` と同じ規約で、実 DOM を持たない node:test 上で動作させる
// ため、本機能の AC が要求する範囲（click / preventDefault / fetch / closest /
// querySelector(All) / dataset / classList / dialog.showModal / replaceChildren /
// setTimeout / URL / event.detail）に絞った fake DOM を用意し、
// vm.createContext で items_bulk_actions.js を評価する。
//
// テストは tasks.md line 944-1051 の 30 ケースを 1:1 で実装する。

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
    this._dialogOpen = false;
    this._showModalCount = 0;
    this._closeCount = 0;
    this._focusCount = 0;
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
      child.removed = true;
    }
  }
  remove() {
    if (this.parent && typeof this.parent.removeChild === 'function') {
      this.parent.removeChild(this);
    } else {
      this.removed = true;
    }
  }
  get firstChild() { return this.children[0] || null; }
  replaceChildren(...nodes) {
    // 既存の children をすべて削除（each child.parent = null）
    for (const c of this.children) c.parent = null;
    this.children.length = 0;
    for (const n of nodes) {
      n.parent = this;
      this.children.push(n);
    }
  }

  setAttribute(name, value) {
    this.attrs.set(name, String(value));
    if (name === 'class') {
      // classList を再構築する（class 属性の正本は classList と attrs の両方）
      this.classList = new FakeClassList(String(value));
    }
    if (name === 'hidden') {
      this.hidden = true;
    }
    if (name === 'disabled') {
      this.disabled = true;
    }
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
  matches(selector) {
    return matchesSelector(this, selector);
  }
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
  dispatchEvent(event) {
    const arr = this._listeners.get(event.type) || [];
    for (const fn of arr) fn(event);
    return true;
  }
  // dialog API
  showModal() {
    this._dialogOpen = true;
    this._showModalCount += 1;
    this.setAttribute('open', '');
  }
  close() {
    this._dialogOpen = false;
    this._closeCount += 1;
    this.removeAttribute('open');
  }
  focus() { this._focusCount += 1; }
}

function matchesSelector(node, selector) {
  if (!node || !node.attrs) return false;
  // カンマ区切り or
  if (selector.indexOf(',') !== -1) {
    const parts = selector.split(',').map((s) => s.trim()).filter(Boolean);
    for (const p of parts) {
      if (matchesSelector(node, p)) return true;
    }
    return false;
  }
  // 単純なタグ名: 'button' / 'h3' / 'dialog'
  const tagOnly = /^([a-z][a-z0-9]*)$/i.exec(selector);
  if (tagOnly) {
    return node.tagName === tagOnly[1].toUpperCase();
  }
  // class + tag: 'button.foo' / 'article.bar' / '.foo'
  const classMatch = /^([a-z][a-z0-9]*|)\.([\w-]+)$/i.exec(selector);
  if (classMatch) {
    const tag = classMatch[1];
    const cls = classMatch[2];
    if (tag && node.tagName !== tag.toUpperCase()) return false;
    return node.classList && node.classList.contains(cls);
  }
  // attr: '[data-foo]' / '[data-foo="bar"]'
  const attrMatch = /^\[([\w-]+)(?:="([^"]*)")?\]$/.exec(selector);
  if (attrMatch) {
    const name = attrMatch[1];
    const val = attrMatch[2];
    if (!node.attrs.has(name)) return false;
    if (val == null) return true;
    return node.attrs.get(name) === val;
  }
  // tagName[attr] / tagName[attr="value"]
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
  // attribute prefix: '[id^="item-title-"]'
  const attrPrefixMatch = /^\[([\w-]+)\^="([^"]*)"\]$/.exec(selector);
  if (attrPrefixMatch) {
    const name = attrPrefixMatch[1];
    const val = attrPrefixMatch[2];
    if (!node.attrs.has(name)) return false;
    return String(node.attrs.get(name)).startsWith(val);
  }
  // tag[attr^="val"] : 'h3[id^="item-title-"]'
  const tagAttrPrefixMatch = /^([a-z][a-z0-9]*)\[([\w-]+)\^="([^"]*)"\]$/i.exec(selector);
  if (tagAttrPrefixMatch) {
    const tag = tagAttrPrefixMatch[1];
    const name = tagAttrPrefixMatch[2];
    const val = tagAttrPrefixMatch[3];
    if (tag && node.tagName !== tag.toUpperCase()) return false;
    if (!node.attrs.has(name)) return false;
    return String(node.attrs.get(name)).startsWith(val);
  }
  // tag.class.class: 'button.tag.tag-filter-toggle'
  const multiClassMatch = /^([a-z][a-z0-9]*|)((?:\.[\w-]+)+)$/i.exec(selector);
  if (multiClassMatch) {
    const tag = multiClassMatch[1];
    const classes = multiClassMatch[2].slice(1).split('.');
    if (tag && node.tagName !== tag.toUpperCase()) return false;
    for (const c of classes) {
      if (!node.classList || !node.classList.contains(c)) return false;
    }
    return true;
  }
  return false;
}

// --- Document/Window factories -----------------------------------------

function buildToolbar(parent) {
  const toolbar = new FakeElement('div', { class: 'bulk-toolbar', 'data-bulk-toolbar': '', hidden: '' });
  const countSpan = new FakeElement('span', { 'data-bulk-count': '' });
  countSpan.textContent = '0';
  const btnDelete = new FakeElement('button', { type: 'button', class: 'btn-danger bulk-delete' });
  btnDelete.textContent = '一括削除';
  const btnTag = new FakeElement('button', { type: 'button', class: 'btn-secondary bulk-tag' });
  btnTag.textContent = '一括タグ付け';
  const btnClear = new FakeElement('button', { type: 'button', class: 'btn-tertiary bulk-clear' });
  btnClear.textContent = '選択解除';
  toolbar.appendChild(countSpan);
  toolbar.appendChild(btnDelete);
  toolbar.appendChild(btnTag);
  toolbar.appendChild(btnClear);
  parent.appendChild(toolbar);
  return { toolbar, countSpan, btnDelete, btnTag, btnClear };
}

function buildTagDialog(parent) {
  const dialog = new FakeElement('dialog', { class: 'bulk-tag-dialog', 'data-bulk-tag-dialog': '' });
  const form = new FakeElement('form', { method: 'dialog', 'data-bulk-tag-form': '' });
  const input = new FakeElement('input', { type: 'text', 'data-bulk-tag-input': '' });
  const cancel = new FakeElement('button', { type: 'button', class: 'btn-secondary', 'data-bulk-tag-cancel': '' });
  cancel.textContent = 'キャンセル';
  const confirm = new FakeElement('button', { type: 'submit', class: 'btn-primary', 'data-bulk-tag-confirm': '' });
  confirm.textContent = '付与';
  form.appendChild(input);
  form.appendChild(cancel);
  form.appendChild(confirm);
  dialog.appendChild(form);
  parent.appendChild(dialog);
  return { dialog, form, input, cancel, confirm };
}

function buildFailureDialog(parent) {
  const dialog = new FakeElement('dialog', { class: 'bulk-failure-dialog', 'data-bulk-failure-dialog': '', role: 'alertdialog' });
  const title = new FakeElement('h2', { 'data-bulk-failure-title': '' });
  title.textContent = '失敗した項目';
  const list = new FakeElement('ul', { 'data-bulk-failure-list': '', role: 'list' });
  const close = new FakeElement('button', { type: 'button', class: 'btn-primary', 'data-bulk-failure-close': '' });
  close.textContent = 'OK';
  dialog.appendChild(title);
  dialog.appendChild(list);
  dialog.appendChild(close);
  parent.appendChild(dialog);
  return { dialog, title, list, close };
}

function buildCard(parent, { id, title, url, tags }) {
  const card = new FakeElement('article', {
    class: 'tile item-card',
    'data-item-id': id,
    'data-original-url': url || '',
  });
  // title h3
  const h3 = new FakeElement('h3', { id: 'item-title-' + id });
  h3.textContent = title || '';
  card.appendChild(h3);
  // tags container（既存 chip があれば再構築の対象になる）
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
  parent.appendChild(card);
  return card;
}

function createFakeDocument({ region, toolbar, tagDialog, failureDialog, cards, csrfToken = 'test-csrf' }) {
  const docListeners = new Map();
  const root = new FakeElement('div', {});
  for (const c of cards) region.appendChild(c);
  root.appendChild(region);
  // toolbar / dialog はテスト全体で document の querySelector から見える必要があるが、
  // tree 上は root の child として配置する。
  if (toolbar) root.appendChild(toolbar);
  if (tagDialog) root.appendChild(tagDialog);
  if (failureDialog) root.appendChild(failureDialog);

  // meta csrf token
  const metaCSRF = new FakeElement('meta', { name: 'csrf-token', content: csrfToken });
  root.appendChild(metaCSRF);

  const doc = {
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
      if (selector === '[data-bulk-toolbar]') return toolbar;
      if (selector === '[data-bulk-tag-dialog]') return tagDialog;
      if (selector === '[data-bulk-failure-dialog]') return failureDialog;
      if (selector === 'meta[name="csrf-token"]') return metaCSRF;
      return root.querySelector(selector);
    },
    querySelectorAll(selector) { return root.querySelectorAll(selector); },
    createElement(tag) { return new FakeElement(tag, {}); },
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

function makeSelectionStub({ initialIds = [] } = {}) {
  let ids = [...initialIds];
  const calls = {
    clear: 0,
    removeFromSelection: [],
    beginActionMutation: 0,
    endActionMutation: 0,
  };
  return {
    getSelectedIDs() { return ids.slice(); },
    clear() { calls.clear += 1; ids = []; },
    removeFromSelection(removeIds) {
      calls.removeFromSelection.push(removeIds.slice());
      const set = new Set(removeIds);
      ids = ids.filter((x) => !set.has(x));
    },
    beginActionMutation() { calls.beginActionMutation += 1; },
    endActionMutation() { calls.endActionMutation += 1; },
    // テストヘルパー
    _setIds(newIds) { ids = [...newIds]; },
    _getIds() { return ids.slice(); },
    _calls: calls,
  };
}

async function flushMicrotasks(rounds = 32) {
  for (let i = 0; i < rounds; i += 1) await Promise.resolve();
}

function createFakeTimers() {
  const queue = [];
  let id = 1;
  return {
    setTimeout(fn, _ms) { const tid = id++; queue.push({ id: tid, fn }); return tid; },
    flushAll() {
      while (queue.length) {
        const { fn } = queue.shift();
        try { fn(); } catch { /* swallow */ }
      }
    },
    pending() { return queue.length; },
  };
}

function loadModule({
  cards = [],
  fetchHandlers = [],
  selection = null,
  altpocketConfirm = null,
  altpocketNormalizeTagName = null,
  locationHref = 'http://localhost/ui/items',
  withTagDialog = true,
  withFailureDialog = true,
  withToolbar = true,
} = {}) {
  const region = new FakeElement('section', { class: 'items', 'data-items-region': '' });
  const tb = withToolbar ? buildToolbar(new FakeElement('div', {})) : null;
  const td = withTagDialog ? buildTagDialog(new FakeElement('div', {})) : null;
  const fd = withFailureDialog ? buildFailureDialog(new FakeElement('div', {})) : null;
  const toolbar = tb ? tb.toolbar : null;
  const tagDialog = td ? td.dialog : null;
  const failureDialog = fd ? fd.dialog : null;

  const document = createFakeDocument({
    region, toolbar, tagDialog, failureDialog, cards,
  });

  const { fetch, calls } = createFetchQueue(fetchHandlers);
  const fakeTimers = createFakeTimers();
  const toastCalls = { error: [], success: [], info: [] };
  const toast = {
    error(msg) { toastCalls.error.push(String(msg)); },
    success(msg) { toastCalls.success.push(String(msg)); },
    info(msg) { toastCalls.info.push(String(msg)); },
  };

  // window object
  const window = {
    document,
    fetch,
    addEventListener() { /* not used */ },
    setTimeout: fakeTimers.setTimeout,
    location: { href: locationHref },
    altpocketBulkSelection: selection || null,
    altpocketToast: null, // toast は opts で直接注入する
    altpocketConfirm: altpocketConfirm,
    altpocketNormalizeTagName: altpocketNormalizeTagName,
    confirm() { return false; },
    alert() { /* swallow */ },
    // auto-init を抑止する。テストは明示的な init() で handler を 1 度だけ
    // register する（auto-init による重複登録を防ぐ）。
    __altpocketBulkActionsSkipAutoInit: true,
  };

  const context = vm.createContext({
    document, window,
    URL, URLSearchParams, console,
    Set, Map, Array, JSON, Promise, Error,
    globalThis: {},
  });

  const source = readFileSync(resolve(process.cwd(), 'static/items_bulk_actions.js'), 'utf8');
  new vm.Script(source, { filename: 'static/items_bulk_actions.js' }).runInContext(context);

  // 評価時の auto init() が起動するが、テストでは opts 注入のため再 init する。
  // module は window.altpocketBulkActionsInit を公開している。
  const initFn = window.altpocketBulkActionsInit;
  const api = initFn ? initFn({
    document, window, fetch, toast, setTimeout: fakeTimers.setTimeout,
  }) : null;

  return {
    region, cards, document, window,
    toolbar, tagDialog, failureDialog,
    tagInput: td ? td.input : null,
    tagForm: td ? td.form : null,
    tagCancel: td ? td.cancel : null,
    tagConfirm: td ? td.confirm : null,
    failureTitle: fd ? fd.title : null,
    failureList: fd ? fd.list : null,
    failureClose: fd ? fd.close : null,
    btnDelete: tb ? tb.btnDelete : null,
    btnTag: tb ? tb.btnTag : null,
    btnClear: tb ? tb.btnClear : null,
    countSpan: tb ? tb.countSpan : null,
    fetchCalls: calls,
    toastCalls,
    timers: fakeTimers,
    api,
    // helpers
    dispatchBulkChange(count, ids) {
      const ev = { type: 'bulkselection:changed', detail: { count, ids: ids || [] } };
      region.dispatchEvent(ev);
    },
    async clickButton(btn) {
      // toolbar の addEventListener('click') ハンドラを発火させる
      const ev = {
        type: 'click',
        target: btn,
        preventDefault() { /* noop */ },
      };
      const listeners = toolbar._listeners.get('click') || [];
      for (const fn of listeners) await fn(ev);
    },
    async submitTagForm() {
      let prevented = false;
      const ev = {
        type: 'submit',
        target: td.form,
        preventDefault() { prevented = true; },
        get defaultPrevented() { return prevented; },
      };
      const listeners = td.form._listeners.get('submit') || [];
      for (const fn of listeners) await fn(ev);
      return { defaultPrevented: prevented };
    },
    async clickTagCancel() {
      const ev = { type: 'click', target: td.cancel };
      const listeners = td.cancel._listeners.get('click') || [];
      for (const fn of listeners) await fn(ev);
    },
    async clickFailureClose() {
      const ev = { type: 'click', target: fd.close };
      const listeners = fd.close._listeners.get('click') || [];
      for (const fn of listeners) await fn(ev);
    },
  };
}

// --- 共通 fixture ヘルパー ----------------------------------------------

function makeCardFixture(id, title, url, tags) {
  return { id, title, url, tags: tags || [] };
}

// 確認 dialog の object stub。show(...) を spy し、approve callback を任意で発火する。
function makeConfirmStub({ autoApprove = true } = {}) {
  const calls = [];
  return {
    calls,
    show(title, description, onConfirm, actionLabel, actionClass) {
      calls.push({ title, description, onConfirm, actionLabel, actionClass });
      if (autoApprove && typeof onConfirm === 'function') onConfirm();
    },
  };
}

function makeConfirmStubCancel() {
  const calls = [];
  return {
    calls,
    show(title, description, onConfirm, actionLabel, actionClass) {
      calls.push({ title, description, onConfirm, actionLabel, actionClass });
      // approve callback を呼ばない（cancel 経路）
    },
  };
}

// --- Tests --------------------------------------------------------------

test('TestDeleteButtonShowsConfirm: bulk-delete click → confirm 表示 + 件数', async () => {
  const c1 = buildCardModel('id-1', 'タイトル1', 'http://a/1');
  const c2 = buildCardModel('id-2', 'タイトル2', 'http://a/2');
  const sel = makeSelectionStub({ initialIds: ['id-1', 'id-2'] });
  const conf = makeConfirmStub();
  const env = loadModule({
    cards: [c1, c2],
    fetchHandlers: [jsonResponse(200, { succeeded: ['id-1', 'id-2'], failed: [] })],
    selection: sel,
    altpocketConfirm: conf,
  });
  await env.clickButton(env.btnDelete);
  await flushMicrotasks();
  assert.equal(conf.calls.length, 1, 'confirm.show が 1 回呼ばれる');
  // 件数を含む description
  assert.ok(conf.calls[0].description.includes('2'), 'description に件数を含む');
});

function buildCardModel(id, title, url, tags) {
  const card = new FakeElement('article', {
    class: 'tile item-card',
    'data-item-id': id,
    'data-original-url': url || '',
  });
  const h3 = new FakeElement('h3', { id: 'item-title-' + id });
  h3.textContent = title || '';
  card.appendChild(h3);
  if (tags && tags.length) {
    const c = new FakeElement('div', { class: 'tags' });
    for (const t of tags) {
      const b = new FakeElement('button', {
        type: 'button',
        class: 'tag tag-filter-toggle',
        'data-tag-filter-toggle': '',
        'data-tag-normalized': t.normalized_name,
      });
      b.textContent = t.name;
      c.appendChild(b);
    }
    card.appendChild(c);
  }
  return card;
}

test('TestDeleteConfirmCallsAPI: approve で fetch POST /v1/items/bulk-delete + body.item_ids', async () => {
  const c1 = buildCardModel('id-A', 'A', 'http://x/A');
  const c2 = buildCardModel('id-B', 'B', 'http://x/B');
  const sel = makeSelectionStub({ initialIds: ['id-A', 'id-B'] });
  const conf = makeConfirmStub();
  const env = loadModule({
    cards: [c1, c2],
    fetchHandlers: [jsonResponse(200, { succeeded: ['id-A', 'id-B'], failed: [] })],
    selection: sel,
    altpocketConfirm: conf,
  });
  await env.clickButton(env.btnDelete);
  await flushMicrotasks();
  assert.equal(env.fetchCalls.length, 1);
  assert.equal(env.fetchCalls[0].url, '/v1/items/bulk-delete');
  assert.equal(env.fetchCalls[0].options.method, 'POST');
  const body = JSON.parse(env.fetchCalls[0].options.body);
  assert.deepEqual(body.item_ids, ['id-A', 'id-B']);
});

test('TestDeleteAllSuccessRemovesCardsAndDeselectsSnapshot: 全成功で article 削除 + removeFromSelection(requestIds) ＋ clear は呼ばれない', async () => {
  const c1 = buildCardModel('id-1', 'T1', 'http://t/1');
  const c2 = buildCardModel('id-2', 'T2', 'http://t/2');
  const sel = makeSelectionStub({ initialIds: ['id-1', 'id-2'] });
  const conf = makeConfirmStub();
  const env = loadModule({
    cards: [c1, c2],
    fetchHandlers: [jsonResponse(200, { succeeded: ['id-1', 'id-2'], failed: [] })],
    selection: sel,
    altpocketConfirm: conf,
  });
  await env.clickButton(env.btnDelete);
  await flushMicrotasks();
  env.timers.flushAll();
  // 両 card が remove されている
  assert.equal(c1.removed, true);
  assert.equal(c2.removed, true);
  // removeFromSelection が呼ばれた（clear は呼ばれない）
  assert.equal(sel._calls.clear, 0, 'clear() は呼ばれない');
  assert.equal(sel._calls.removeFromSelection.length, 1);
  assert.deepEqual(sel._calls.removeFromSelection[0], ['id-1', 'id-2']);
});

test('TestDeleteAllSuccessPreservesInFlightNewSelection: fetch pending 中に新規選択 C が追加されても、succeeded の A/B だけが解除されて C は残る', async () => {
  const cA = buildCardModel('A', 'A-title', 'http://a/A');
  const cB = buildCardModel('B', 'B-title', 'http://a/B');
  const cC = buildCardModel('C', 'C-title', 'http://a/C');
  const sel = makeSelectionStub({ initialIds: ['A', 'B'] });
  const conf = makeConfirmStub();
  let resolveFetch;
  const pending = new Promise((r) => { resolveFetch = r; });
  const env = loadModule({
    cards: [cA, cB, cC],
    fetchHandlers: [() => pending],
    selection: sel,
    altpocketConfirm: conf,
  });
  await env.clickButton(env.btnDelete);
  // pending 中にユーザーが C を新規選択
  sel._setIds(['A', 'B', 'C']);
  // fetch が完了
  resolveFetch(jsonResponse(200, { succeeded: ['A', 'B'], failed: [] }));
  await flushMicrotasks();
  env.timers.flushAll();
  // removeFromSelection は ['A', 'B'] のみ（snapshot 由来）。clear() は呼ばれない。
  assert.equal(sel._calls.clear, 0);
  assert.equal(sel._calls.removeFromSelection.length, 1);
  assert.deepEqual(sel._calls.removeFromSelection[0], ['A', 'B']);
  // 残った ids に C が含まれる
  assert.ok(sel._getIds().includes('C'), 'C は残置されている');
});

test('TestDeletePartialFailureKeepsFailedSelected: 部分失敗で succeeded だけ remove、failed は selection 残置、failure dialog が全件 populate', async () => {
  const cA = buildCardModel('A', 'titleA', 'http://x/A');
  const cB = buildCardModel('B', 'titleB', 'http://x/B');
  const cC = buildCardModel('C', 'titleC', 'http://x/C');
  const sel = makeSelectionStub({ initialIds: ['A', 'B', 'C'] });
  const conf = makeConfirmStub();
  const env = loadModule({
    cards: [cA, cB, cC],
    fetchHandlers: [jsonResponse(200, {
      succeeded: ['A'],
      failed: [{ item_id: 'B', reason: 'not_found' }, { item_id: 'C', reason: 'not_found' }],
    })],
    selection: sel,
    altpocketConfirm: conf,
  });
  await env.clickButton(env.btnDelete);
  await flushMicrotasks();
  env.timers.flushAll();
  // A は remove、B / C は残る
  assert.equal(cA.removed, true);
  assert.equal(cB.removed, false);
  assert.equal(cC.removed, false);
  // removeFromSelection は succeeded のみ
  assert.deepEqual(sel._calls.removeFromSelection[0], ['A']);
  // failure dialog open + <li> 2 件
  assert.equal(env.failureDialog._showModalCount, 1);
  assert.equal(env.failureList.children.length, 2);
  const liTexts = env.failureList.children.map((li) => li.textContent);
  assert.ok(liTexts.includes('titleB'));
  assert.ok(liTexts.includes('titleC'));
});

test('TestDeleteCancelDoesNothing: confirm cancel で fetch 未呼出 + 選択保持', async () => {
  const cA = buildCardModel('A', 'A', 'http://a/A');
  const sel = makeSelectionStub({ initialIds: ['A'] });
  const conf = makeConfirmStubCancel();
  const env = loadModule({
    cards: [cA],
    fetchHandlers: [],
    selection: sel,
    altpocketConfirm: conf,
  });
  await env.clickButton(env.btnDelete);
  await flushMicrotasks();
  assert.equal(env.fetchCalls.length, 0, 'fetch は呼ばれない');
  assert.deepEqual(sel._getIds(), ['A'], '選択保持');
});

test('TestDeleteConfirmUsesShowSignature: object.show(...) が呼ばれ、description に件数を含む', async () => {
  const cA = buildCardModel('A', 'A', 'http://a/A');
  const cB = buildCardModel('B', 'B', 'http://a/B');
  const cC = buildCardModel('C', 'C', 'http://a/C');
  const sel = makeSelectionStub({ initialIds: ['A', 'B', 'C'] });
  const conf = makeConfirmStub();
  const env = loadModule({
    cards: [cA, cB, cC],
    fetchHandlers: [jsonResponse(200, { succeeded: ['A', 'B', 'C'], failed: [] })],
    selection: sel,
    altpocketConfirm: conf,
  });
  await env.clickButton(env.btnDelete);
  await flushMicrotasks();
  assert.equal(conf.calls.length, 1);
  // object.show(title, description, onConfirm, actionLabel, actionClass)
  assert.ok(conf.calls[0].title);
  assert.ok(conf.calls[0].description.includes('3'));
  assert.equal(typeof conf.calls[0].onConfirm, 'function');
  assert.equal(conf.calls[0].actionLabel, 'Delete');
  assert.equal(conf.calls[0].actionClass, 'btn-danger');
});

test('TestDeleteRateLimitedShowsFailureDialog: 429 → failure dialog で snapshot 全件 populate + selection 残置', async () => {
  const cA = buildCardModel('A', 'A-title', 'http://x/A');
  const cB = buildCardModel('B', 'B-title', 'http://x/B');
  const sel = makeSelectionStub({ initialIds: ['A', 'B'] });
  const conf = makeConfirmStub();
  const env = loadModule({
    cards: [cA, cB],
    fetchHandlers: [jsonResponse(429, { error: 'rate_limited' })],
    selection: sel,
    altpocketConfirm: conf,
  });
  await env.clickButton(env.btnDelete);
  await flushMicrotasks();
  assert.equal(env.failureDialog._showModalCount, 1);
  assert.equal(env.failureList.children.length, 2);
  // selection 残置
  assert.deepEqual(sel._getIds(), ['A', 'B']);
});

test('TestDeleteServerErrorUsesRequestIdsNotLiveSelection: fetch pending 中に live が [B,C] に変わっても failure dialog は snapshot の [A,B]', async () => {
  const cA = buildCardModel('A', 'Atitle', 'http://x/A');
  const cB = buildCardModel('B', 'Btitle', 'http://x/B');
  const cC = buildCardModel('C', 'Ctitle', 'http://x/C');
  const sel = makeSelectionStub({ initialIds: ['A', 'B'] });
  const conf = makeConfirmStub();
  let resolveFetch;
  const pending = new Promise((r) => { resolveFetch = r; });
  const env = loadModule({
    cards: [cA, cB, cC],
    fetchHandlers: [() => pending],
    selection: sel,
    altpocketConfirm: conf,
  });
  await env.clickButton(env.btnDelete);
  // pending 中に live が変化
  sel._setIds(['B', 'C']);
  resolveFetch(jsonResponse(500, { error: 'db_error' }));
  await flushMicrotasks();
  // failure dialog の <li> は snapshot 由来の A / B（C ではない）
  const texts = env.failureList.children.map((li) => li.textContent);
  assert.ok(texts.includes('Atitle'));
  assert.ok(texts.includes('Btitle'));
  assert.ok(!texts.includes('Ctitle'));
  // selection は触らない → live のまま [B, C]
  assert.deepEqual(sel._getIds(), ['B', 'C']);
});

test('TestDeleteServerErrorShowsFailureDialog: 500 db_error → failure dialog + selection 残置', async () => {
  const cA = buildCardModel('A', 'A', 'http://x/A');
  const sel = makeSelectionStub({ initialIds: ['A'] });
  const conf = makeConfirmStub();
  const env = loadModule({
    cards: [cA],
    fetchHandlers: [jsonResponse(500, { error: 'db_error' })],
    selection: sel,
    altpocketConfirm: conf,
  });
  await env.clickButton(env.btnDelete);
  await flushMicrotasks();
  assert.equal(env.failureDialog._showModalCount, 1);
  assert.deepEqual(sel._getIds(), ['A']);
});

test('TestDeleteServerErrorShowsFailureDialog (network reject): fetch reject でも failure dialog', async () => {
  const cA = buildCardModel('A', 'A', 'http://x/A');
  const sel = makeSelectionStub({ initialIds: ['A'] });
  const conf = makeConfirmStub();
  const env = loadModule({
    cards: [cA],
    fetchHandlers: [() => { throw new Error('network down'); }],
    selection: sel,
    altpocketConfirm: conf,
  });
  await env.clickButton(env.btnDelete);
  await flushMicrotasks();
  assert.equal(env.failureDialog._showModalCount, 1);
});

test('TestDeleteForbiddenBearerRejectShowsFailureDialog: 403 forbidden → failure dialog', async () => {
  const cA = buildCardModel('A', 'A', 'http://x/A');
  const sel = makeSelectionStub({ initialIds: ['A'] });
  const conf = makeConfirmStub();
  const env = loadModule({
    cards: [cA],
    fetchHandlers: [jsonResponse(403, { error: 'forbidden' })],
    selection: sel,
    altpocketConfirm: conf,
  });
  await env.clickButton(env.btnDelete);
  await flushMicrotasks();
  assert.equal(env.failureDialog._showModalCount, 1);
});

test('TestDeleteUnauthorizedShowsFailureDialog: 401 unauthorized → failure dialog', async () => {
  const cA = buildCardModel('A', 'A', 'http://x/A');
  const sel = makeSelectionStub({ initialIds: ['A'] });
  const conf = makeConfirmStub();
  const env = loadModule({
    cards: [cA],
    fetchHandlers: [jsonResponse(401, { error: 'unauthorized' })],
    selection: sel,
    altpocketConfirm: conf,
  });
  await env.clickButton(env.btnDelete);
  await flushMicrotasks();
  assert.equal(env.failureDialog._showModalCount, 1);
});

test('TestDeleteInvalidRequestShowsToastNotDialog: 400 invalid_request → toast のみ、dialog 出さず、selection 保持', async () => {
  const cA = buildCardModel('A', 'A', 'http://x/A');
  const sel = makeSelectionStub({ initialIds: ['A'] });
  const conf = makeConfirmStub();
  const env = loadModule({
    cards: [cA],
    fetchHandlers: [jsonResponse(400, { error: 'invalid_request' })],
    selection: sel,
    altpocketConfirm: conf,
  });
  await env.clickButton(env.btnDelete);
  await flushMicrotasks();
  assert.equal(env.failureDialog._showModalCount, 0, 'failure dialog は出ない');
  assert.ok(env.toastCalls.error.length >= 1, 'toast.error が呼ばれる');
  assert.deepEqual(sel._getIds(), ['A']);
});

test('TestDeletePayloadTooLargeShowsToastNotDialog: 400 payload_too_large → toast のみ、dialog 出さず、selection 保持', async () => {
  const cA = buildCardModel('A', 'A', 'http://x/A');
  const sel = makeSelectionStub({ initialIds: ['A'] });
  const conf = makeConfirmStub();
  const env = loadModule({
    cards: [cA],
    fetchHandlers: [jsonResponse(400, { error: 'payload_too_large' })],
    selection: sel,
    altpocketConfirm: conf,
  });
  await env.clickButton(env.btnDelete);
  await flushMicrotasks();
  assert.equal(env.failureDialog._showModalCount, 0);
  assert.ok(env.toastCalls.error.length >= 1);
  assert.deepEqual(sel._getIds(), ['A']);
});

test('TestTagButtonOpensDialog: bulk-tag click → tagDialog.showModal', async () => {
  const cA = buildCardModel('A', 'A', 'http://x/A');
  const sel = makeSelectionStub({ initialIds: ['A'] });
  const env = loadModule({
    cards: [cA],
    fetchHandlers: [],
    selection: sel,
  });
  await env.clickButton(env.btnTag);
  assert.equal(env.tagDialog._showModalCount, 1);
});

test('TestTagDialogEmptyInputIsNoOp: 空 / 全角空白で fetch 未呼出 + input focus 戻る', async () => {
  const cA = buildCardModel('A', 'A', 'http://x/A');
  const sel = makeSelectionStub({ initialIds: ['A'] });
  const env = loadModule({
    cards: [cA],
    fetchHandlers: [],
    selection: sel,
  });
  await env.clickButton(env.btnTag);
  env.tagInput.value = '   ';
  await env.submitTagForm();
  await flushMicrotasks();
  assert.equal(env.fetchCalls.length, 0);
  assert.ok(env.tagInput._focusCount >= 1);

  // 全角空白
  env.tagInput.value = '　 ';
  await env.submitTagForm();
  await flushMicrotasks();
  assert.equal(env.fetchCalls.length, 0);
});

test('TestTagDialogConfirmCallsAPI: 非空入力で fetch /v1/items/bulk-tag + body.item_ids/tag (原文字列)', async () => {
  const cA = buildCardModel('A', 'A', 'http://x/A');
  const sel = makeSelectionStub({ initialIds: ['A'] });
  const env = loadModule({
    cards: [cA],
    fetchHandlers: [jsonResponse(200, { succeeded: [{ item_id: 'A', tags: [] }], failed: [] })],
    selection: sel,
  });
  await env.clickButton(env.btnTag);
  env.tagInput.value = 'GoLang';
  await env.submitTagForm();
  await flushMicrotasks();
  assert.equal(env.fetchCalls.length, 1);
  assert.equal(env.fetchCalls[0].url, '/v1/items/bulk-tag');
  assert.equal(env.fetchCalls[0].options.method, 'POST');
  const body = JSON.parse(env.fetchCalls[0].options.body);
  assert.deepEqual(body.item_ids, ['A']);
  assert.equal(body.tag, 'GoLang', '原文字列を送る（normalize していない）');
});

test('TestTagSuccessRebuildsChipsWithFilterToggleContract: chip 構築は button.tag.tag-filter-toggle + 属性一式', async () => {
  const cA = buildCardModel('A', 'titleA', 'http://x/A', [{ id: 't-old', name: 'old', normalized_name: 'old' }]);
  const sel = makeSelectionStub({ initialIds: ['A'] });
  const env = loadModule({
    cards: [cA],
    fetchHandlers: [jsonResponse(200, {
      succeeded: [{ item_id: 'A', tags: [{ id: 't-new', name: 'Python', normalized_name: 'python' }] }],
      failed: [],
    })],
    selection: sel,
  });
  await env.clickButton(env.btnTag);
  env.tagInput.value = 'Python';
  await env.submitTagForm();
  await flushMicrotasks();
  const tagsContainer = cA.querySelector('.tags');
  assert.ok(tagsContainer);
  const chips = tagsContainer.querySelectorAll('button');
  assert.equal(chips.length, 1);
  const chip = chips[0];
  assert.equal(chip.tagName, 'BUTTON');
  assert.ok(chip.classList.contains('tag'));
  assert.ok(chip.classList.contains('tag-filter-toggle'));
  assert.ok(!chip.classList.contains('is-selected'), 'URL に active tag が無いので is-selected なし');
  assert.equal(chip.getAttribute('data-tag-filter-toggle'), '');
  assert.equal(chip.getAttribute('data-tag-normalized'), 'python');
  assert.equal(chip.getAttribute('aria-label'), 'タグで絞り込み: Python');
  assert.equal(chip.getAttribute('aria-pressed'), 'false');
  assert.equal(chip.textContent, 'Python');
});

test('TestTagSuccessPreservesActiveFilterChipSelectedState: URL ?tag=GoLang&tag=Rust で golang/rust chip は is-selected/aria-pressed=true', async () => {
  const cA = buildCardModel('A', 'A', 'http://x/A');
  const sel = makeSelectionStub({ initialIds: ['A'] });
  const env = loadModule({
    cards: [cA],
    fetchHandlers: [jsonResponse(200, {
      succeeded: [{
        item_id: 'A',
        tags: [
          { id: 't1', name: 'GoLang', normalized_name: 'golang' },
          { id: 't2', name: 'Rust', normalized_name: 'rust' },
          { id: 't3', name: 'Python', normalized_name: 'python' },
        ],
      }],
      failed: [],
    })],
    selection: sel,
    locationHref: 'http://localhost/ui/items?tag=GoLang&tag=Rust',
    altpocketNormalizeTagName: (s) => (s || '').normalize('NFKC').toLowerCase().trim(),
  });
  await env.clickButton(env.btnTag);
  env.tagInput.value = 'GoLang';
  await env.submitTagForm();
  await flushMicrotasks();
  const chips = cA.querySelector('.tags').querySelectorAll('button');
  const byNorm = {};
  for (const c of chips) byNorm[c.getAttribute('data-tag-normalized')] = c;
  assert.ok(byNorm.golang.classList.contains('is-selected'));
  assert.equal(byNorm.golang.getAttribute('aria-pressed'), 'true');
  assert.ok(byNorm.rust.classList.contains('is-selected'));
  assert.equal(byNorm.rust.getAttribute('aria-pressed'), 'true');
  assert.ok(!byNorm.python.classList.contains('is-selected'));
  assert.equal(byNorm.python.getAttribute('aria-pressed'), 'false');
});

test('TestTagSuccessPreservesActiveFilterChipSelectedState (全角): ?tag=ＧｏＬａｎｇ → golang chip が is-selected', async () => {
  const cA = buildCardModel('A', 'A', 'http://x/A');
  const sel = makeSelectionStub({ initialIds: ['A'] });
  const env = loadModule({
    cards: [cA],
    fetchHandlers: [jsonResponse(200, {
      succeeded: [{
        item_id: 'A',
        tags: [{ id: 't1', name: 'GoLang', normalized_name: 'golang' }],
      }],
      failed: [],
    })],
    selection: sel,
    locationHref: 'http://localhost/ui/items?tag=' + encodeURIComponent('ＧｏＬａｎｇ'),
    altpocketNormalizeTagName: (s) => (s || '').normalize('NFKC').toLowerCase().trim(),
  });
  await env.clickButton(env.btnTag);
  env.tagInput.value = 'GoLang';
  await env.submitTagForm();
  await flushMicrotasks();
  const chips = cA.querySelector('.tags').querySelectorAll('button');
  assert.equal(chips.length, 1);
  assert.ok(chips[0].classList.contains('is-selected'));
  assert.equal(chips[0].getAttribute('aria-pressed'), 'true');
});

test('TestTagSuccessRespectsLegacyTagsCsvParam: ?tags=go,rust + 混在 ?tag=go&tags=rust,python の active filter chip 連携', async () => {
  const cA = buildCardModel('A', 'A', 'http://x/A');
  const sel = makeSelectionStub({ initialIds: ['A'] });
  const env = loadModule({
    cards: [cA],
    fetchHandlers: [jsonResponse(200, {
      succeeded: [{
        item_id: 'A',
        tags: [
          { id: 't1', name: 'Go', normalized_name: 'go' },
          { id: 't2', name: 'Rust', normalized_name: 'rust' },
          { id: 't3', name: 'Python', normalized_name: 'python' },
        ],
      }],
      failed: [],
    })],
    selection: sel,
    locationHref: 'http://localhost/ui/items?tags=go,rust',
    altpocketNormalizeTagName: (s) => (s || '').normalize('NFKC').toLowerCase().trim(),
  });
  await env.clickButton(env.btnTag);
  env.tagInput.value = 'Go';
  await env.submitTagForm();
  await flushMicrotasks();
  const chips = cA.querySelector('.tags').querySelectorAll('button');
  const byNorm = {};
  for (const c of chips) byNorm[c.getAttribute('data-tag-normalized')] = c;
  assert.ok(byNorm.go.classList.contains('is-selected'));
  assert.equal(byNorm.go.getAttribute('aria-pressed'), 'true');
  assert.ok(byNorm.rust.classList.contains('is-selected'));
  assert.ok(!byNorm.python.classList.contains('is-selected'));
  assert.equal(byNorm.python.getAttribute('aria-pressed'), 'false');

  // 混在ケース: ?tag=go&tags=rust,python → go / rust / python の全部が active 集合
  const cB = buildCardModel('B', 'B', 'http://x/B');
  const sel2 = makeSelectionStub({ initialIds: ['B'] });
  const env2 = loadModule({
    cards: [cB],
    fetchHandlers: [jsonResponse(200, {
      succeeded: [{
        item_id: 'B',
        tags: [
          { id: 't1', name: 'Go', normalized_name: 'go' },
          { id: 't2', name: 'Rust', normalized_name: 'rust' },
          { id: 't3', name: 'Python', normalized_name: 'python' },
        ],
      }],
      failed: [],
    })],
    selection: sel2,
    locationHref: 'http://localhost/ui/items?tag=go&tags=rust,python',
    altpocketNormalizeTagName: (s) => (s || '').normalize('NFKC').toLowerCase().trim(),
  });
  await env2.clickButton(env2.btnTag);
  env2.tagInput.value = 'Go';
  await env2.submitTagForm();
  await flushMicrotasks();
  const chips2 = cB.querySelector('.tags').querySelectorAll('button');
  for (const c of chips2) {
    assert.ok(c.classList.contains('is-selected'), c.getAttribute('data-tag-normalized') + ' が is-selected');
    assert.equal(c.getAttribute('aria-pressed'), 'true');
  }
});

test('TestTagSuccessDeselectsSnapshotAndClosesDialog: 全成功で removeFromSelection(requestIds) + dialog close + 新規選択 B 残置', async () => {
  const cA = buildCardModel('A', 'A', 'http://x/A');
  const cB = buildCardModel('B', 'B', 'http://x/B');
  const sel = makeSelectionStub({ initialIds: ['A'] });
  let resolveFetch;
  const pending = new Promise((r) => { resolveFetch = r; });
  const env = loadModule({
    cards: [cA, cB],
    fetchHandlers: [() => pending],
    selection: sel,
  });
  await env.clickButton(env.btnTag);
  env.tagInput.value = 'foo';
  // submit して fetch pending 中に B を新規選択
  const submitP = env.submitTagForm();
  sel._setIds(['A', 'B']);
  resolveFetch(jsonResponse(200, { succeeded: [{ item_id: 'A', tags: [] }], failed: [] }));
  await submitP;
  await flushMicrotasks();
  assert.equal(sel._calls.clear, 0, 'clear は呼ばれない');
  assert.deepEqual(sel._calls.removeFromSelection[0], ['A']);
  assert.ok(sel._getIds().includes('B'), 'B 残置');
  // dialog close
  assert.equal(env.tagDialog._closeCount, 1);
});

test('TestTagPartialFailureKeepsFailedSelected: 部分失敗で succeeded の chip 反映 + failed selection 残置 + failure dialog 全件', async () => {
  const cA = buildCardModel('A', 'A-title', 'http://x/A', [{ id: 't-old', name: 'old', normalized_name: 'old' }]);
  const cB = buildCardModel('B', 'B-title', 'http://x/B', [{ id: 't-old', name: 'old', normalized_name: 'old' }]);
  const sel = makeSelectionStub({ initialIds: ['A', 'B'] });
  const env = loadModule({
    cards: [cA, cB],
    fetchHandlers: [jsonResponse(200, {
      succeeded: [{ item_id: 'A', tags: [{ id: 't1', name: 'Go', normalized_name: 'go' }] }],
      failed: [{ item_id: 'B', reason: 'not_found' }],
    })],
    selection: sel,
  });
  await env.clickButton(env.btnTag);
  env.tagInput.value = 'Go';
  await env.submitTagForm();
  await flushMicrotasks();
  // A の chips 再構築済み
  const chipsA = cA.querySelector('.tags').querySelectorAll('button');
  assert.equal(chipsA.length, 1);
  assert.equal(chipsA[0].textContent, 'Go');
  // B の chips は触らない
  const chipsB = cB.querySelector('.tags').querySelectorAll('button');
  assert.equal(chipsB.length, 1);
  assert.equal(chipsB[0].textContent, 'old');
  // selection: B 残置
  assert.deepEqual(sel._calls.removeFromSelection[0], ['A']);
  assert.ok(sel._getIds().includes('B'));
  // failure dialog open with 1 entry
  assert.equal(env.failureDialog._showModalCount, 1);
  assert.equal(env.failureList.children.length, 1);
  assert.equal(env.failureList.children[0].textContent, 'B-title');
});

test('TestTagRateLimitedShowsFailureDialog: 429 → failure dialog + selection 残置', async () => {
  const cA = buildCardModel('A', 'A', 'http://x/A');
  const sel = makeSelectionStub({ initialIds: ['A'] });
  const env = loadModule({
    cards: [cA],
    fetchHandlers: [jsonResponse(429, { error: 'rate_limited' })],
    selection: sel,
  });
  await env.clickButton(env.btnTag);
  env.tagInput.value = 'Go';
  await env.submitTagForm();
  await flushMicrotasks();
  assert.equal(env.failureDialog._showModalCount, 1);
  assert.deepEqual(sel._getIds(), ['A']);
});

test('TestTagServerErrorShowsFailureDialog: 500 / network fail → failure dialog', async () => {
  const cA = buildCardModel('A', 'A', 'http://x/A');
  const sel = makeSelectionStub({ initialIds: ['A'] });
  const env = loadModule({
    cards: [cA],
    fetchHandlers: [jsonResponse(500, { error: 'db_error' })],
    selection: sel,
  });
  await env.clickButton(env.btnTag);
  env.tagInput.value = 'Go';
  await env.submitTagForm();
  await flushMicrotasks();
  assert.equal(env.failureDialog._showModalCount, 1);

  // network failure
  const cB = buildCardModel('B', 'B', 'http://x/B');
  const sel2 = makeSelectionStub({ initialIds: ['B'] });
  const env2 = loadModule({
    cards: [cB],
    fetchHandlers: [() => { throw new Error('net down'); }],
    selection: sel2,
  });
  await env2.clickButton(env2.btnTag);
  env2.tagInput.value = 'Go';
  await env2.submitTagForm();
  await flushMicrotasks();
  assert.equal(env2.failureDialog._showModalCount, 1);
});

test('TestTagInvalidTagOpenedDialogStaysAndFocusInput: 400 invalid_tag → dialog 残置 + input focus + toast + failure dialog 出さない', async () => {
  const cA = buildCardModel('A', 'A', 'http://x/A');
  const sel = makeSelectionStub({ initialIds: ['A'] });
  const env = loadModule({
    cards: [cA],
    fetchHandlers: [jsonResponse(400, { error: 'invalid_tag' })],
    selection: sel,
  });
  await env.clickButton(env.btnTag);
  env.tagInput.value = 'X';
  const focusBefore = env.tagInput._focusCount;
  await env.submitTagForm();
  await flushMicrotasks();
  // dialog は閉じていない
  assert.equal(env.tagDialog._closeCount, 0);
  // failure dialog は出ていない
  assert.equal(env.failureDialog._showModalCount, 0);
  // input focus 戻し
  assert.ok(env.tagInput._focusCount > focusBefore);
  // toast.error
  assert.ok(env.toastCalls.error.length >= 1);
});

test('TestTagInvalidRequestShowsToastNotDialog: 400 invalid_request → toast のみ、dialog 出さず、selection 保持', async () => {
  const cA = buildCardModel('A', 'A', 'http://x/A');
  const sel = makeSelectionStub({ initialIds: ['A'] });
  const env = loadModule({
    cards: [cA],
    fetchHandlers: [jsonResponse(400, { error: 'invalid_request' })],
    selection: sel,
  });
  await env.clickButton(env.btnTag);
  env.tagInput.value = 'Go';
  await env.submitTagForm();
  await flushMicrotasks();
  assert.equal(env.failureDialog._showModalCount, 0);
  assert.ok(env.toastCalls.error.length >= 1);
  assert.deepEqual(sel._getIds(), ['A']);
});

test('TestTagPayloadTooLargeShowsToastNotDialog: 400 payload_too_large → toast のみ、selection 保持', async () => {
  const cA = buildCardModel('A', 'A', 'http://x/A');
  const sel = makeSelectionStub({ initialIds: ['A'] });
  const env = loadModule({
    cards: [cA],
    fetchHandlers: [jsonResponse(400, { error: 'payload_too_large' })],
    selection: sel,
  });
  await env.clickButton(env.btnTag);
  env.tagInput.value = 'Go';
  await env.submitTagForm();
  await flushMicrotasks();
  assert.equal(env.failureDialog._showModalCount, 0);
  assert.ok(env.toastCalls.error.length >= 1);
  assert.deepEqual(sel._getIds(), ['A']);
});

test('TestFailureDialogPopulatesAllItemsWithoutTruncation: 6 / 50 / 100 件で <li> がそのままの件数', async () => {
  for (const n of [6, 50, 100]) {
    const cards = [];
    const ids = [];
    for (let i = 0; i < n; i += 1) {
      const id = 'id-' + i;
      ids.push(id);
      cards.push(buildCardModel(id, 'title-' + i, 'http://x/' + i));
    }
    const sel = makeSelectionStub({ initialIds: ids });
    const conf = makeConfirmStub();
    const env = loadModule({
      cards,
      fetchHandlers: [jsonResponse(429, { error: 'rate_limited' })],
      selection: sel,
      altpocketConfirm: conf,
    });
    await env.clickButton(env.btnDelete);
    await flushMicrotasks();
    assert.equal(env.failureDialog._showModalCount, 1, '件数=' + n);
    assert.equal(env.failureList.children.length, n, '件数=' + n + ' で li 全件');
  }
});

test('TestFailureDialogUsesTextContentNotInnerHTML: <script> を含む title でも li が script 要素にならない', async () => {
  const cA = buildCardModel('A', '<script>alert(1)</script>', 'http://x/A');
  const sel = makeSelectionStub({ initialIds: ['A'] });
  const conf = makeConfirmStub();
  const env = loadModule({
    cards: [cA],
    fetchHandlers: [jsonResponse(429, { error: 'rate_limited' })],
    selection: sel,
    altpocketConfirm: conf,
  });
  await env.clickButton(env.btnDelete);
  await flushMicrotasks();
  const li = env.failureList.children[0];
  assert.equal(li.tagName, 'LI');
  // textContent としてそのまま入っている。子要素として script は生成されない。
  assert.equal(li.textContent, '<script>alert(1)</script>');
  assert.equal(li.children.length, 0, 'script 要素は生成されない');
});

test('TestToolbarShowsHidesOnSelectionChange: count=0 で hidden、count>0 で表示 + 件数', async () => {
  const cA = buildCardModel('A', 'A', 'http://x/A');
  const sel = makeSelectionStub({ initialIds: [] });
  const env = loadModule({
    cards: [cA],
    fetchHandlers: [],
    selection: sel,
  });
  // init 時は count=0 → hidden
  assert.equal(env.toolbar.hidden, true);
  // count=3 にする
  env.dispatchBulkChange(3, ['x', 'y', 'z']);
  assert.equal(env.toolbar.hidden, false);
  assert.equal(env.countSpan.textContent, '3');
  // count=0 に戻す
  env.dispatchBulkChange(0, []);
  assert.equal(env.toolbar.hidden, true);
});

test('TestClearButtonCallsSelectionClear: bulk-clear click → selection.clear', async () => {
  const cA = buildCardModel('A', 'A', 'http://x/A');
  const sel = makeSelectionStub({ initialIds: ['A'] });
  const env = loadModule({
    cards: [cA],
    fetchHandlers: [],
    selection: sel,
  });
  await env.clickButton(env.btnClear);
  assert.equal(sel._calls.clear, 1);
});

test('TestDeleteFailureUsesSnapshotWhenCardRemovedDuringFlight: fetch pending 中にカードが DOM から消えても snapshot で title/url 提示 (Req 4.7)', async () => {
  // 一括削除リクエスト中に、ユーザーがタブ切替・タグフィルタ・検索クエリ変更
  // 等で fragment swap を起こすと、failed 通知時点で対象 article が DOM 不在に
  // なる。snapshot が無いと id-only fallback に倒れて Req 4.7 違反になるため、
  // click 時点の title/url snapshot から復元できることを固定する。
  const cA = buildCardModel('A', 'Title-A', 'http://x/A');
  const cB = buildCardModel('B', 'Title-B', 'http://x/B');
  const sel = makeSelectionStub({ initialIds: ['A', 'B'] });
  const conf = makeConfirmStub();
  let resolveFetch;
  const pending = new Promise((r) => { resolveFetch = r; });
  const env = loadModule({
    cards: [cA, cB],
    fetchHandlers: [() => pending],
    selection: sel,
    altpocketConfirm: conf,
  });
  await env.clickButton(env.btnDelete);
  // pending 中に fragment swap 相当の DOM 消失をシミュレート
  cA.parent && cA.parent.removeChild(cA);
  cB.parent && cB.parent.removeChild(cB);
  // 全件失敗（500）でレスポンスが返る
  resolveFetch(jsonResponse(500, { error: 'db_error' }));
  await flushMicrotasks();
  env.timers.flushAll();
  await flushMicrotasks();
  // failure dialog に title が出ている（id-only fallback ではない）
  assert.equal(env.failureDialog._showModalCount, 1);
  assert.equal(env.failureList.children.length, 2);
  const liTexts = env.failureList.children.map((li) => li.textContent);
  assert.ok(liTexts.includes('Title-A'), 'snapshot から Title-A 復元');
  assert.ok(liTexts.includes('Title-B'), 'snapshot から Title-B 復元');
});

test('TestTagFailureUsesSnapshotWhenCardRemovedDuringFlight: 一括タグ付け中に DOM 消失 → snapshot から title/url 復元 (Req 5.7)', async () => {
  const cA = buildCardModel('A', 'Tag-A', 'http://x/A');
  const cB = buildCardModel('B', 'Tag-B', 'http://x/B');
  const sel = makeSelectionStub({ initialIds: ['A', 'B'] });
  let resolveFetch;
  const pending = new Promise((r) => { resolveFetch = r; });
  const env = loadModule({
    cards: [cA, cB],
    fetchHandlers: [() => pending],
    selection: sel,
  });
  await env.clickButton(env.btnTag);
  env.tagInput.value = 'foo';
  const submitP = env.submitTagForm();
  // fetch pending 中に DOM 消失
  cA.parent && cA.parent.removeChild(cA);
  cB.parent && cB.parent.removeChild(cB);
  // 部分失敗でレスポンスが返る（B が failed）
  resolveFetch(jsonResponse(200, {
    succeeded: [{ item_id: 'A', tags: [] }],
    failed: [{ item_id: 'B', reason: 'not_found' }],
  }));
  await submitP;
  await flushMicrotasks();
  // failure dialog に B の title が表示される（id-only fallback ではない）
  assert.equal(env.failureDialog._showModalCount, 1);
  assert.equal(env.failureList.children.length, 1);
  assert.equal(env.failureList.children[0].textContent, 'Tag-B');
});

test('TestSnapshotItemDetailsCollectsTitleAndUrl: snapshotItemDetails() が click 時点の DOM を Map に固定する', () => {
  const cA = buildCardModel('A', 'Title-A', 'http://x/A');
  const cB = buildCardModel('B', 'Title-B', 'http://x/B');
  const env = loadModule({
    cards: [cA, cB],
    selection: makeSelectionStub({ initialIds: [] }),
  });
  const snap = env.api._debug.snapshotItemDetails(['A', 'B']);
  assert.equal(snap.size, 2);
  const a = snap.get('A');
  assert.equal(a.id, 'A');
  assert.equal(a.title, 'Title-A');
  assert.equal(a.url, 'http://x/A');
  const b = snap.get('B');
  assert.equal(b.id, 'B');
  assert.equal(b.title, 'Title-B');
  assert.equal(b.url, 'http://x/B');
  // 空配列は空 Map
  assert.equal(env.api._debug.snapshotItemDetails([]).size, 0);
});

test('TestToolbarButtonsDisabledDuringInFlightRequest: fetch pending 中は 3 ボタン disabled、resolve 後に解除', async () => {
  const cA = buildCardModel('A', 'A', 'http://x/A');
  const sel = makeSelectionStub({ initialIds: ['A'] });
  const conf = makeConfirmStub();
  let resolveFetch;
  const pending = new Promise((r) => { resolveFetch = r; });
  const env = loadModule({
    cards: [cA],
    fetchHandlers: [() => pending],
    selection: sel,
    altpocketConfirm: conf,
  });
  await env.clickButton(env.btnDelete);
  // pending 中: 3 ボタンが disabled 属性を持つ
  assert.equal(env.btnDelete.disabled, true);
  assert.equal(env.btnTag.disabled, true);
  assert.equal(env.btnClear.disabled, true);
  // resolve（全成功）後
  resolveFetch(jsonResponse(200, { succeeded: ['A'], failed: [] }));
  await flushMicrotasks();
  env.timers.flushAll();
  await flushMicrotasks();
  assert.equal(env.btnDelete.disabled, false, '解除');
  assert.equal(env.btnTag.disabled, false);
  assert.equal(env.btnClear.disabled, false);
});
