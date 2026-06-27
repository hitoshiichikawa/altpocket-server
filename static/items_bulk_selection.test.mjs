// items_bulk_selection.test.mjs
//
// /ui/items の一括選択モジュール (Issue #118 task 6) を司る
// `static/items_bulk_selection.js` の単体テスト。
//
// `items_status.test.mjs` と同じ規約で、実 DOM を持たない node:test 上で
// 動作させるため、本機能の AC が要求する範囲（change / click / keydown /
// document.querySelector* / element classList / closest / dataset /
// MutationObserver / CustomEvent / window.altpocketToast.error）に絞った
// 最小 fake DOM を用意し、vm.createContext で items_bulk_selection.js を
// 評価する。
//
// AC マッピング:
//   - Req 1.1 / 1.2 / 1.3: チェックボックス change で選択 toggle
//   - Req 1.4: 選択状態の <article>.is-selected 視覚区別
//   - Req 2.1〜2.4: Shift+クリック範囲選択 / 3 条件 fallback
//   - Req 3.4: 選択解除 API
//   - Req 3.6: bulkselection:changed event 発火
//   - Req 6.1〜6.3: キーボード `x` トグル + ガード
//   - Req 7.1〜7.5: タブ / フィルタ / 検索 / ソート / ページ送り / popstate
//     によるリセット
//   - NFR 2.2: 上限 100 件 enforcement
//   - NFR 3.5: Progressive Enhancement (init / fragment 差替で disabled 除去)

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

let _elementSeq = 0;

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
    this._disabled = false;
    this._checked = false;
    this._type = '';
    this.isContentEditable = false;
    this._seq = ++_elementSeq;
    for (const [k, v] of Object.entries(attrs)) {
      if (k === 'class') continue;
      this.setAttribute(k, v);
    }
  }

  // INPUT 要素の checked / disabled / type プロパティをエミュレート
  get disabled() { return this._disabled; }
  set disabled(v) {
    this._disabled = !!v;
    if (this._disabled) this.setAttribute('disabled', '');
    else this.removeAttribute('disabled');
  }

  get checked() { return this._checked; }
  set checked(v) {
    this._checked = !!v;
    if (this._checked) this.setAttribute('checked', '');
    else this.removeAttribute('checked');
  }

  get type() { return this._type || (this.attrs.get('type') || ''); }
  set type(v) {
    this._type = String(v);
    this.setAttribute('type', String(v));
  }

  appendChild(child) {
    child.parent = this;
    this.children.push(child);
    this._notifyMutation([child], []);
    return child;
  }

  removeChild(child) {
    const idx = this.children.indexOf(child);
    if (idx !== -1) {
      this.children.splice(idx, 1);
      child.parent = null;
      child.removed = true;
      this._notifyMutation([], [child]);
    }
  }

  remove() {
    if (this.parent && typeof this.parent.removeChild === 'function') {
      this.parent.removeChild(this);
    } else {
      this.removed = true;
    }
  }

  setAttribute(name, value) {
    this.attrs.set(name, String(value));
    if (name === 'disabled') {
      this._disabled = true;
    }
    if (name === 'type') {
      this._type = String(value);
    }
    if (name.startsWith('data-')) {
      const key = name.slice(5).replace(/-([a-z])/g, (_, c) => c.toUpperCase());
      this.dataset[key] = String(value);
    }
  }

  removeAttribute(name) {
    this.attrs.delete(name);
    if (name === 'disabled') {
      this._disabled = false;
    }
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

  // テスト用: innerHTML 代入で children を入れ替えて MutationObserver を発火
  set innerHTML(_html) {
    // innerHTML を直接 parse する代わりに、children を空にしたうえで
    // MutationRecord を artificially 発火する。テスト本体で別途
    // _replaceWithChildren を呼んで新 SSR を構築する。
    const removed = this.children.slice();
    this.children = [];
    for (const r of removed) {
      r.parent = null;
      r.removed = true;
    }
    this._notifyMutation([], removed);
  }

  // テスト用 helper: children を一括差し替え（fragment swap シミュレーション）
  _replaceChildren(newChildren) {
    const removed = this.children.slice();
    for (const r of removed) {
      r.parent = null;
      r.removed = true;
    }
    this.children = [];
    for (const c of newChildren) {
      c.parent = this;
      this.children.push(c);
    }
    this._notifyMutation(newChildren, removed);
  }

  // FakeMutationObserver の register 経路
  _registerObserver(observer) {
    if (!this._observers) this._observers = [];
    this._observers.push(observer);
  }

  _notifyMutation(addedNodes, removedNodes) {
    if (!this._observers || this._observers.length === 0) return;
    const record = {
      type: 'childList',
      target: this,
      addedNodes,
      removedNodes,
    };
    for (const obs of this._observers) {
      obs._enqueue(record);
    }
  }

  // CustomEvent dispatch (window/region 上に listener が register された場合)
  dispatchEvent(event) {
    const type = event && event.type;
    const handlers = this._listeners && this._listeners.get(type);
    if (!handlers) return true;
    for (const fn of handlers.slice()) {
      try { fn(event); } catch { /* swallow */ }
    }
    return !(event && event.defaultPrevented);
  }

  addEventListener(type, fn) {
    if (!this._listeners) this._listeners = new Map();
    if (!this._listeners.has(type)) this._listeners.set(type, []);
    this._listeners.get(type).push(fn);
  }

  removeEventListener(type, fn) {
    if (!this._listeners) return;
    const arr = this._listeners.get(type);
    if (!arr) return;
    const idx = arr.indexOf(fn);
    if (idx !== -1) arr.splice(idx, 1);
  }
}

function matchesSelector(node, selector) {
  if (!node || !node.attrs) return false;
  const sel = String(selector).trim();
  // カンマ区切り (or)
  if (sel.indexOf(',') !== -1) {
    const parts = sel.split(',').map((s) => s.trim()).filter(Boolean);
    for (const p of parts) {
      if (matchesSelector(node, p)) return true;
    }
    return false;
  }
  // 'input.item-select[disabled]' のような複合 selector
  // → tag + class + attribute をすべて満たすか
  const compoundRe = /^([a-z]*)((?:\.[\w-]+)*)((?:\[[^\]]+\])*)$/i;
  const m = compoundRe.exec(sel);
  if (m) {
    const tag = m[1];
    const classPart = m[2];
    const attrPart = m[3];
    if (tag && node.tagName !== tag.toUpperCase()) return false;
    if (classPart) {
      const classes = classPart.split('.').filter(Boolean);
      for (const c of classes) {
        if (!node.classList || !node.classList.contains(c)) return false;
      }
    }
    if (attrPart) {
      const attrRe = /\[([\w-]+)(?:="([^"]*)")?\]/g;
      let am;
      while ((am = attrRe.exec(attrPart)) !== null) {
        const name = am[1];
        const val = am[2];
        if (!node.attrs.has(name)) return false;
        if (val != null && node.attrs.get(name) !== val) return false;
      }
    }
    return true;
  }
  return false;
}

// --- Fake CustomEvent / PopStateEvent ----------------------------------

class FakeEvent {
  constructor(type, init = {}) {
    this.type = type;
    this.detail = init.detail;
    this._defaultPrevented = false;
    this.shiftKey = !!init.shiftKey;
    this.ctrlKey = !!init.ctrlKey;
    this.altKey = !!init.altKey;
    this.metaKey = !!init.metaKey;
    this.key = init.key;
    this.target = init.target;
  }
  get defaultPrevented() { return this._defaultPrevented; }
  preventDefault() { this._defaultPrevented = true; }
}

// --- Fake MutationObserver ---------------------------------------------

class FakeMutationObserver {
  constructor(callback) {
    this._callback = callback;
    this._queue = [];
    this._target = null;
  }
  observe(target, _opts) {
    this._target = target;
    target._registerObserver(this);
  }
  disconnect() {
    if (this._target && this._target._observers) {
      const idx = this._target._observers.indexOf(this);
      if (idx !== -1) this._target._observers.splice(idx, 1);
    }
    this._target = null;
    this._queue = [];
  }
  takeRecords() {
    const out = this._queue;
    this._queue = [];
    return out;
  }
  _enqueue(record) {
    this._queue.push(record);
  }
  // テスト用: pending queue を callback に流して clear
  _flush() {
    if (this._queue.length === 0) return;
    const records = this._queue;
    this._queue = [];
    this._callback(records, this);
  }
}

// --- Document / Window factories ---------------------------------------

function createFakeRegion() {
  return new FakeElement('section', {
    class: 'items',
    'data-items-region': '',
  });
}

function makeCard({ id, selected = false, hasDisabledCheckbox = true } = {}) {
  const card = new FakeElement('article', {
    class: 'tile item-card',
    'data-item-id': id,
    'data-original-url': `https://example.com/${id}`,
  });
  if (selected) card.classList.add('is-selected');
  const cb = new FakeElement('input', {
    class: 'item-select',
    type: 'checkbox',
    'data-item-select': '',
    'data-item-id': id,
    'aria-label': `select ${id}`,
  });
  if (hasDisabledCheckbox) {
    cb.setAttribute('disabled', '');
  }
  if (selected) cb.checked = true;
  card.appendChild(cb);
  card.checkbox = cb;
  return card;
}

function createFakeDocument({ region, cards, extraInputs = [] }) {
  const docListeners = new Map();
  const root = new FakeElement('div', {});
  for (const c of cards) root.appendChild(c);
  if (region) root.appendChild(region);
  for (const e of extraInputs) root.appendChild(e);

  // activeElement モック (テストから明示的に set)
  let active = null;

  return {
    get activeElement() { return active; },
    set activeElement(v) { active = v; },

    addEventListener(type, fn) {
      if (!docListeners.has(type)) docListeners.set(type, []);
      docListeners.get(type).push(fn);
    },

    removeEventListener(type, fn) {
      const arr = docListeners.get(type);
      if (!arr) return;
      const idx = arr.indexOf(fn);
      if (idx !== -1) arr.splice(idx, 1);
    },

    dispatch(type, eventInit = {}) {
      const handlers = docListeners.get(type) || [];
      const event = new FakeEvent(type, eventInit);
      for (const fn of handlers.slice()) fn(event);
      return event;
    },

    querySelector(selector) {
      if (selector === '[data-items-region]') return region;
      return root.querySelector(selector);
    },

    querySelectorAll(selector) {
      return root.querySelectorAll(selector);
    },
  };
}

function createFakeWindow({ document, toast } = {}) {
  const winListeners = new Map();
  const win = {
    document,
    addEventListener(type, fn) {
      if (!winListeners.has(type)) winListeners.set(type, []);
      winListeners.get(type).push(fn);
    },
    removeEventListener(type, fn) {
      const arr = winListeners.get(type);
      if (!arr) return;
      const idx = arr.indexOf(fn);
      if (idx !== -1) arr.splice(idx, 1);
    },
    dispatch(type, eventInit = {}) {
      const handlers = winListeners.get(type) || [];
      const event = new FakeEvent(type, eventInit);
      for (const fn of handlers.slice()) fn(event);
      return event;
    },
    altpocketToast: toast || {
      errors: [],
      successes: [],
      error(msg) { this.errors.push(msg); },
      success(msg) { this.successes.push(msg); },
    },
  };
  return win;
}

function loadModule({ region, cards = [], extraInputs = [] } = {}) {
  const reg = region || createFakeRegion();
  const document = createFakeDocument({ region: reg, cards, extraInputs });
  const toast = {
    errors: [],
    successes: [],
    error(msg) { this.errors.push(msg); },
    success(msg) { this.successes.push(msg); },
  };
  const window = createFakeWindow({ document, toast });

  // bulkselection:changed イベントを capture する
  const changedEvents = [];
  reg.addEventListener('bulkselection:changed', (e) => {
    changedEvents.push({ count: e.detail.count, ids: e.detail.ids.slice() });
  });

  // CustomEvent fake は detail を保持するだけで十分
  class FakeCustomEvent extends FakeEvent {
    constructor(type, init = {}) {
      super(type, init);
      this.detail = init.detail;
    }
  }

  // globalThis に export する用の sandbox オブジェクト
  const context = vm.createContext({
    document,
    window,
    MutationObserver: FakeMutationObserver,
    CustomEvent: FakeCustomEvent,
    console,
    Set, Map, Array, Object, String, Number, Boolean,
  });

  // window.MutationObserver / window.CustomEvent も同居させる（モジュール側が
  // どちらの参照を使っても解決できるように）
  window.MutationObserver = FakeMutationObserver;
  window.CustomEvent = FakeCustomEvent;

  const source = readFileSync(resolve(process.cwd(), 'static/items_bulk_selection.js'), 'utf8');
  new vm.Script(source, { filename: 'static/items_bulk_selection.js' }).runInContext(context);

  // モジュール側は window.altpocketBulkSelection に init() 戻り値を載せる
  const api = window.altpocketBulkSelection;
  if (!api) throw new Error('items_bulk_selection.js did not expose window.altpocketBulkSelection');

  return {
    region: reg,
    document,
    window,
    toast,
    cards,
    api,
    changedEvents,
  };
}

// --- Tests --------------------------------------------------------------

test('TestSingleCheckboxToggle: 1 件 click で Set に追加 + .is-selected + count=1', () => {
  const cards = [makeCard({ id: 'a' }), makeCard({ id: 'b' }), makeCard({ id: 'c' })];
  const env = loadModule({ cards });
  const region = env.region;
  // region 自体に cards を attach（root 経由で attach されている前提）— モジュール
  // 側は region.querySelectorAll('.item-card') 等で region 配下のみ走査するので、
  // テスト用には cards を region に移動する。
  for (const c of cards) {
    if (c.parent) c.parent.removeChild(c);
    region.appendChild(c);
  }
  // 初期 changed event（cards 追加で発火しないように、init() 時点ではまだ
  // region 配下に card 無し → changedEvents reset）
  env.changedEvents.length = 0;

  const card = cards[0];
  // ネイティブ click 経路は change を発火する。テストでは change を直接 dispatch する
  card.checkbox.checked = true;
  env.document.dispatch('change', { target: card.checkbox });

  assert.ok(card.classList.contains('is-selected'), '.is-selected が付与される');
  assert.deepEqual(env.api.getSelectedIDs(), ['a']);
  // bulkselection:changed が detail.count=1 で発火
  const last = env.changedEvents[env.changedEvents.length - 1];
  assert.equal(last.count, 1);
  assert.deepEqual(last.ids, ['a']);
});

test('TestUncheckRemoves: 再 click で Set から削除 + class 除去 + count=0', () => {
  const cards = [makeCard({ id: 'a' })];
  const env = loadModule({ cards });
  for (const c of cards) {
    if (c.parent) c.parent.removeChild(c);
    env.region.appendChild(c);
  }
  env.changedEvents.length = 0;

  const card = cards[0];
  card.checkbox.checked = true;
  env.document.dispatch('change', { target: card.checkbox });
  card.checkbox.checked = false;
  env.document.dispatch('change', { target: card.checkbox });

  assert.ok(!card.classList.contains('is-selected'));
  assert.deepEqual(env.api.getSelectedIDs(), []);
  const last = env.changedEvents[env.changedEvents.length - 1];
  assert.equal(last.count, 0);
});

test('TestShiftClickRange: 1 件選択 → 4 件下を Shift+click → 5 件選択', () => {
  const cards = ['a', 'b', 'c', 'd', 'e', 'f'].map((id) => makeCard({ id }));
  const env = loadModule({ cards });
  for (const c of cards) {
    if (c.parent) c.parent.removeChild(c);
    env.region.appendChild(c);
  }
  env.changedEvents.length = 0;

  // a を click（通常）
  cards[0].checkbox.checked = true;
  env.document.dispatch('click', { target: cards[0].checkbox, shiftKey: false });
  env.document.dispatch('change', { target: cards[0].checkbox });

  // e を Shift+click（anchor a → e の範囲を選択）
  env.document.dispatch('click', { target: cards[4].checkbox, shiftKey: true });

  const ids = env.api.getSelectedIDs();
  assert.equal(ids.length, 5);
  // DOM 順で a, b, c, d, e
  assert.deepEqual(ids.sort(), ['a', 'b', 'c', 'd', 'e']);
  // 各 card の checkbox.checked と .is-selected が同期している
  for (let i = 0; i < 5; i++) {
    assert.equal(cards[i].checkbox.checked, true, `card ${i} checked`);
    assert.ok(cards[i].classList.contains('is-selected'), `card ${i} is-selected`);
  }
});

test('TestShiftClickPreservesExistingSelection: 範囲外の既選択は保持される', () => {
  const cards = ['a', 'b', 'c', 'd', 'e', 'f', 'g'].map((id) => makeCard({ id }));
  const env = loadModule({ cards });
  for (const c of cards) {
    if (c.parent) c.parent.removeChild(c);
    env.region.appendChild(c);
  }

  // g（範囲外）を click
  cards[6].checkbox.checked = true;
  env.document.dispatch('click', { target: cards[6].checkbox, shiftKey: false });
  env.document.dispatch('change', { target: cards[6].checkbox });

  // a を click
  cards[0].checkbox.checked = true;
  env.document.dispatch('click', { target: cards[0].checkbox, shiftKey: false });
  env.document.dispatch('change', { target: cards[0].checkbox });

  // c を Shift+click（anchor a → c の範囲 = a, b, c）
  env.document.dispatch('click', { target: cards[2].checkbox, shiftKey: true });

  const ids = env.api.getSelectedIDs().sort();
  // g は範囲外で保持される
  assert.deepEqual(ids, ['a', 'b', 'c', 'g']);
});

test('TestShiftClickWithoutHistoryActsAsSingleToggle: lastClickedID null → 単一 toggle', () => {
  const cards = ['a', 'b', 'c'].map((id) => makeCard({ id }));
  const env = loadModule({ cards });
  for (const c of cards) {
    if (c.parent) c.parent.removeChild(c);
    env.region.appendChild(c);
  }
  // 何も click していない状態で Shift+click
  cards[1].checkbox.checked = true;
  const ev = env.document.dispatch('click', { target: cards[1].checkbox, shiftKey: true });
  env.document.dispatch('change', { target: cards[1].checkbox });

  // 単一 toggle に降格 → preventDefault は呼ばれていない
  assert.equal(ev.defaultPrevented, false, 'fallback では preventDefault しない');
  const ids = env.api.getSelectedIDs();
  assert.deepEqual(ids, ['b']);
});

test('TestShiftClickWithEmptySelectionActsAsSingleToggle: size=0 → 単一 toggle', () => {
  const cards = ['a', 'b', 'c'].map((id) => makeCard({ id }));
  const env = loadModule({ cards });
  for (const c of cards) {
    if (c.parent) c.parent.removeChild(c);
    env.region.appendChild(c);
  }

  // a を click → 選択 → 再 click で解除（lastClickedID は a のまま）
  cards[0].checkbox.checked = true;
  env.document.dispatch('click', { target: cards[0].checkbox, shiftKey: false });
  env.document.dispatch('change', { target: cards[0].checkbox });
  cards[0].checkbox.checked = false;
  env.document.dispatch('click', { target: cards[0].checkbox, shiftKey: false });
  env.document.dispatch('change', { target: cards[0].checkbox });

  // size=0 の状態で b を Shift+click → 単一 toggle に降格
  cards[1].checkbox.checked = true;
  const ev = env.document.dispatch('click', { target: cards[1].checkbox, shiftKey: true });
  env.document.dispatch('change', { target: cards[1].checkbox });

  assert.equal(ev.defaultPrevented, false);
  assert.deepEqual(env.api.getSelectedIDs(), ['b']);
});

test('TestShiftClickWithStaleAnchorActsAsSingleToggle: anchor article が DOM 不在 → 単一 toggle', () => {
  const cards = ['a', 'b', 'c'].map((id) => makeCard({ id }));
  const env = loadModule({ cards });
  for (const c of cards) {
    if (c.parent) c.parent.removeChild(c);
    env.region.appendChild(c);
  }

  // a を click → lastClickedID = 'a'
  cards[0].checkbox.checked = true;
  env.document.dispatch('click', { target: cards[0].checkbox, shiftKey: false });
  env.document.dispatch('change', { target: cards[0].checkbox });

  // a を DOM から取り除く（fragment reset を経由しない擬似シナリオ）
  // → 内部状態: lastClickedID = 'a', size = 0（a の checkbox.unchecked は別経路で
  // 同期されていないため、selectionSet には a が残ったまま）
  // ただし size > 0 + lastClickedID !== null だが anchor article が DOM 不在 → fallback
  env.region.removeChild(cards[0]);
  // selectionSet に a が残ったまま c を Shift+click すれば、本来は a→c の範囲算出
  // を試みるが、querySelector('article[data-item-id="a"]') が null になるので fallback

  cards[2].checkbox.checked = true;
  const ev = env.document.dispatch('click', { target: cards[2].checkbox, shiftKey: true });
  env.document.dispatch('change', { target: cards[2].checkbox });

  assert.equal(ev.defaultPrevented, false, 'stale anchor では fallback (preventDefault しない)');
  // c のみが追加される（範囲算出は走らない）
  const ids = env.api.getSelectedIDs();
  assert.ok(ids.includes('c'));
  assert.ok(!ids.includes('b'), 'b は範囲算出で追加されない');
});

test('TestShiftClickUpdatesLastClickedAnchor: Shift+5 後の Shift+8 が 5→8 範囲', () => {
  const ids = ['a', 'b', 'c', 'd', 'e', 'f', 'g', 'h', 'i'];
  const cards = ids.map((id) => makeCard({ id }));
  const env = loadModule({ cards });
  for (const c of cards) {
    if (c.parent) c.parent.removeChild(c);
    env.region.appendChild(c);
  }

  // a を click → lastClickedID = a
  cards[0].checkbox.checked = true;
  env.document.dispatch('click', { target: cards[0].checkbox, shiftKey: false });
  env.document.dispatch('change', { target: cards[0].checkbox });

  // Shift+e (index 4) → 範囲 a→e、lastClickedID = e に更新
  env.document.dispatch('click', { target: cards[4].checkbox, shiftKey: true });

  // Shift+h (index 7) → 範囲 e→h（a→h ではない）
  env.document.dispatch('click', { target: cards[7].checkbox, shiftKey: true });

  const selected = env.api.getSelectedIDs().sort();
  // 期待: a, b, c, d, e, f, g, h（Shift+e で a-e, Shift+h で e-h, 合算）
  assert.deepEqual(selected, ['a', 'b', 'c', 'd', 'e', 'f', 'g', 'h']);
  // i は範囲外
  assert.ok(!selected.includes('i'));
});

test('TestKeyboardXTogglesFocusedCard: フォーカス中カードで x → toggle', () => {
  const cards = ['a', 'b'].map((id) => makeCard({ id }));
  const env = loadModule({ cards });
  for (const c of cards) {
    if (c.parent) c.parent.removeChild(c);
    env.region.appendChild(c);
  }

  // activeElement を card a 配下の checkbox に
  env.document.activeElement = cards[0].checkbox;
  env.document.dispatch('keydown', { key: 'x', target: cards[0].checkbox });

  assert.deepEqual(env.api.getSelectedIDs(), ['a']);
  assert.ok(cards[0].classList.contains('is-selected'));

  // 再度 x で解除
  env.document.dispatch('keydown', { key: 'x', target: cards[0].checkbox });
  assert.deepEqual(env.api.getSelectedIDs(), []);
});

test('TestKeyboardXIgnoresInputFocus: <input type="text"> フォーカス時の x は no-op', () => {
  const cards = [makeCard({ id: 'a' })];
  const textInput = new FakeElement('input', { type: 'text', name: 'q' });
  const env = loadModule({ cards, extraInputs: [textInput] });
  for (const c of cards) {
    if (c.parent) c.parent.removeChild(c);
    env.region.appendChild(c);
  }
  // textInput は extraInputs で root 配下に attach 済み

  env.document.activeElement = textInput;
  env.document.dispatch('keydown', { key: 'x', target: textInput });

  assert.deepEqual(env.api.getSelectedIDs(), []);
});

test('TestKeyboardXIgnoresModifierCombo: Ctrl+x / Meta+x / Alt+x → no-op', () => {
  const cards = [makeCard({ id: 'a' })];
  const env = loadModule({ cards });
  for (const c of cards) {
    if (c.parent) c.parent.removeChild(c);
    env.region.appendChild(c);
  }

  env.document.activeElement = cards[0].checkbox;
  env.document.dispatch('keydown', { key: 'x', target: cards[0].checkbox, ctrlKey: true });
  env.document.dispatch('keydown', { key: 'x', target: cards[0].checkbox, metaKey: true });
  env.document.dispatch('keydown', { key: 'x', target: cards[0].checkbox, altKey: true });

  assert.deepEqual(env.api.getSelectedIDs(), []);
});

test('TestUpperLimitRejectsBeyond100: 100 件選択 + 101 件目単一 click → 抑止 + toast.error', () => {
  const ids = [];
  for (let i = 0; i < 101; i++) ids.push(`item-${i}`);
  const cards = ids.map((id) => makeCard({ id }));
  const env = loadModule({ cards });
  for (const c of cards) {
    if (c.parent) c.parent.removeChild(c);
    env.region.appendChild(c);
  }

  // 100 件を programmatic に選択
  for (let i = 0; i < 100; i++) {
    cards[i].checkbox.checked = true;
    env.document.dispatch('change', { target: cards[i].checkbox });
  }
  assert.equal(env.api.getSelectedIDs().length, 100);

  // 101 件目を click → 抑止
  cards[100].checkbox.checked = true;
  env.document.dispatch('change', { target: cards[100].checkbox });

  assert.equal(env.api.getSelectedIDs().length, 100, 'Set.size は 100 のまま');
  // 101 件目の checkbox は unchecked に戻されている（モジュール側で revert）
  assert.equal(cards[100].checkbox.checked, false);
  // toast.error が呼ばれている
  assert.ok(env.toast.errors.some((m) => m.includes('一括操作は最大 100 件まで')),
    `toast.error に '一括操作は最大 100 件まで' を含む文言が期待されるが actual=${JSON.stringify(env.toast.errors)}`);
});

test('TestShiftRangeAcrossUpperLimitRejectsEntireRange: 80 件 + Shift で 21 件分 → 範囲全体 reject + toast.error', () => {
  const ids = [];
  for (let i = 0; i < 101; i++) ids.push(`item-${i}`);
  const cards = ids.map((id) => makeCard({ id }));
  const env = loadModule({ cards });
  for (const c of cards) {
    if (c.parent) c.parent.removeChild(c);
    env.region.appendChild(c);
  }

  // 80 件選択（item-0 〜 item-79）— 全部 change 経由
  for (let i = 0; i < 80; i++) {
    cards[i].checkbox.checked = true;
    env.document.dispatch('change', { target: cards[i].checkbox });
  }
  assert.equal(env.api.getSelectedIDs().length, 80);

  // lastClickedID anchor を item-79 に設定（通常 click を発火）
  env.document.dispatch('click', { target: cards[79].checkbox, shiftKey: false });

  // Shift+click on item-100 → 範囲 79→100 = 22 件追加
  // 合算 80 + 22 - 1（item-79 は既選択）= 101 → 範囲全体 reject
  // ただし item-79 は既選択なので、新規追加は item-80〜item-100 = 21 件、合計 101
  env.document.dispatch('click', { target: cards[100].checkbox, shiftKey: true });

  assert.equal(env.api.getSelectedIDs().length, 80, 'Set.size は 80 のまま');
  // 範囲 (item-80 〜 item-100) の checkbox はどれも checked にならない
  for (let i = 80; i <= 100; i++) {
    assert.equal(cards[i].checkbox.checked, false, `card ${i} は unchecked のまま`);
    assert.ok(!cards[i].classList.contains('is-selected'), `card ${i} は .is-selected なし`);
  }
  // toast.error
  assert.ok(env.toast.errors.some((m) => m.includes('範囲選択により上限を超えるため処理されませんでした')),
    `期待 toast 文言: '範囲選択により上限を超えるため処理されませんでした'  actual=${JSON.stringify(env.toast.errors)}`);
});

test('TestProgressiveEnhancementRemovesDisabled: init 直後に disabled checkbox 全 enable', () => {
  const cards = ['a', 'b', 'c'].map((id) => makeCard({ id, hasDisabledCheckbox: true }));
  // region 経由で attach
  const region = createFakeRegion();
  for (const c of cards) region.appendChild(c);
  // すべての checkbox が disabled で SSR されていることを pre-condition assert
  for (const c of cards) {
    assert.equal(c.checkbox.disabled, true, 'pre-condition: SSR disabled');
  }

  const env = loadModule({ region, cards: [] });
  // init 直後、すべての checkbox が enabled になっていること
  for (const c of cards) {
    assert.equal(c.checkbox.disabled, false, 'init 後に enabled');
    assert.equal(c.checkbox.getAttribute('disabled'), null);
  }
});

test('TestFragmentSwapReEnablesNewDisabledCheckboxes: 差替後の新 SSR の disabled も enable', () => {
  const region = createFakeRegion();
  const env = loadModule({ region, cards: [] });

  // 新 SSR markup として disabled 付き checkbox を含む 3 card
  const newCards = ['x', 'y', 'z'].map((id) => makeCard({ id, hasDisabledCheckbox: true }));
  for (const c of newCards) {
    assert.equal(c.checkbox.disabled, true);
  }

  // fragment swap シミュレーション（addedNodes.length > 0）
  region._replaceChildren(newCards);
  // MutationObserver の queue を flush
  // モジュール側の observer は region._observers に登録されている
  for (const obs of (region._observers || [])) obs._flush();

  for (const c of newCards) {
    assert.equal(c.checkbox.disabled, false, 'fragment swap 後の checkbox が enabled');
  }
});

test('TestFragmentSwapResetsSelection: innerHTML 差替で Set.clear() + count=0 + lastClickedID リセット', () => {
  const cards = ['a', 'b'].map((id) => makeCard({ id }));
  const env = loadModule({ cards });
  for (const c of cards) {
    if (c.parent) c.parent.removeChild(c);
    env.region.appendChild(c);
  }

  // a を選択
  cards[0].checkbox.checked = true;
  env.document.dispatch('click', { target: cards[0].checkbox, shiftKey: false });
  env.document.dispatch('change', { target: cards[0].checkbox });
  assert.equal(env.api.getSelectedIDs().length, 1);

  // fragment swap （新 SSR は b' のみ）
  const newCards = [makeCard({ id: 'b2' })];
  env.region._replaceChildren(newCards);
  for (const obs of (env.region._observers || [])) obs._flush();

  assert.deepEqual(env.api.getSelectedIDs(), []);
  // bulkselection:changed が detail.count=0 で発火
  const last = env.changedEvents[env.changedEvents.length - 1];
  assert.equal(last.count, 0);

  // lastClickedID が null にリセットされていることを間接的に observe:
  // 直後の shift+click が単一 toggle に降格する（範囲算出されない）
  newCards[0].checkbox.checked = true;
  const ev = env.document.dispatch('click', { target: newCards[0].checkbox, shiftKey: true });
  env.document.dispatch('change', { target: newCards[0].checkbox });
  assert.equal(ev.defaultPrevented, false, 'lastClickedID リセット後の shift+click は fallback');
});

test('TestPopstateResetsSelection: popstate event で Set.clear() + count=0 + lastClickedID リセット', () => {
  const cards = ['a', 'b', 'c'].map((id) => makeCard({ id }));
  const env = loadModule({ cards });
  for (const c of cards) {
    if (c.parent) c.parent.removeChild(c);
    env.region.appendChild(c);
  }

  // a と b を選択
  cards[0].checkbox.checked = true;
  env.document.dispatch('click', { target: cards[0].checkbox, shiftKey: false });
  env.document.dispatch('change', { target: cards[0].checkbox });
  cards[1].checkbox.checked = true;
  env.document.dispatch('click', { target: cards[1].checkbox, shiftKey: false });
  env.document.dispatch('change', { target: cards[1].checkbox });
  assert.equal(env.api.getSelectedIDs().length, 2);

  // popstate を発火
  env.window.dispatch('popstate', {});

  assert.deepEqual(env.api.getSelectedIDs(), []);
  const last = env.changedEvents[env.changedEvents.length - 1];
  assert.equal(last.count, 0);

  // lastClickedID リセットを間接的に observe
  cards[2].checkbox.checked = true;
  const ev = env.document.dispatch('click', { target: cards[2].checkbox, shiftKey: true });
  env.document.dispatch('change', { target: cards[2].checkbox });
  assert.equal(ev.defaultPrevented, false);
});

test('TestFragmentSwapDuringActionBracketStillResets: bracket 中の fragment swap も reset', () => {
  const cards = ['a', 'b'].map((id) => makeCard({ id }));
  const env = loadModule({ cards });
  for (const c of cards) {
    if (c.parent) c.parent.removeChild(c);
    env.region.appendChild(c);
  }

  // a, b を選択
  for (const c of cards) {
    c.checkbox.checked = true;
    env.document.dispatch('click', { target: c.checkbox, shiftKey: false });
    env.document.dispatch('change', { target: c.checkbox });
  }
  assert.equal(env.api.getSelectedIDs().length, 2);

  // beginActionMutation で bracket カウンタ +1
  env.api.beginActionMutation();

  // bracket 中に fragment swap を実行
  const newCards = ['x', 'y'].map((id) => makeCard({ id, hasDisabledCheckbox: true }));
  env.region._replaceChildren(newCards);
  // observer の callback を flush（micro tasked record が処理される）
  for (const obs of (env.region._observers || [])) obs._flush();

  // bracket 中でも fragment swap (addedNodes.length > 0) は reset を発火する
  assert.deepEqual(env.api.getSelectedIDs(), [], 'bracket 中 fragment swap でも Set.clear()');
  // 新 SSR の disabled checkbox も enable される
  for (const c of newCards) {
    assert.equal(c.checkbox.disabled, false, 'bracket 中 fragment swap でも checkbox enable');
  }

  // bracket をクローズ（discard が無事に進むこと）
  env.api.endActionMutation();
});

test('TestEndActionMutationProcessesQueuedFragmentSwapBeforeDiscard: bracket 中の queued record を takeRecords() 経由で処理', () => {
  const cards = ['a', 'b'].map((id) => makeCard({ id }));
  const env = loadModule({ cards });
  for (const c of cards) {
    if (c.parent) c.parent.removeChild(c);
    env.region.appendChild(c);
  }

  // a を選択
  cards[0].checkbox.checked = true;
  env.document.dispatch('click', { target: cards[0].checkbox, shiftKey: false });
  env.document.dispatch('change', { target: cards[0].checkbox });
  assert.equal(env.api.getSelectedIDs().length, 1);

  // bracket open
  env.api.beginActionMutation();
  // bracket 中に fragment swap （MutationObserver callback はまだ発火していない、
  // queue に積まれているだけ）
  const newCards = ['x', 'y'].map((id) => makeCard({ id, hasDisabledCheckbox: true }));
  env.region._replaceChildren(newCards);
  // ★ ここで observer._flush() を呼ばない → queue に record が貯まったまま

  // endActionMutation 冒頭で takeRecords() が呼ばれ、queued fragment swap record が
  // 処理されることを assert
  env.api.endActionMutation();

  assert.deepEqual(env.api.getSelectedIDs(), [], 'queued fragment swap record で Set.clear()');
  for (const c of newCards) {
    assert.equal(c.checkbox.disabled, false, 'queued record でも checkbox enable');
  }
});

test('TestInitialStateIsEmpty: init 直後の getSelectedIDs() が空配列', () => {
  const cards = ['a', 'b'].map((id) => makeCard({ id }));
  const env = loadModule({ cards });
  // region への attach 前 / 後どちらでも初期は空
  assert.deepEqual(env.api.getSelectedIDs(), []);
});

test('TestRemoveFromSelectionSyncsDOM: removeFromSelection() で article.is-selected と checkbox.checked が解除される (Req 5.6 / 5.8)', () => {
  // 一括タグ付け成功 / 部分失敗で actions モジュールが removeFromSelection(succeeded)
  // を呼ぶ場面の DOM 同期。article は DOM に残ったままで、selection だけ解除されるパス。
  const cards = ['a', 'b', 'c'].map((id) => makeCard({ id }));
  const env = loadModule({ cards });
  for (const c of cards) {
    if (c.parent) c.parent.removeChild(c);
    env.region.appendChild(c);
  }

  // 3 件を選択
  for (const c of cards) {
    c.checkbox.checked = true;
    env.document.dispatch('change', { target: c.checkbox });
  }
  assert.equal(env.api.getSelectedIDs().length, 3);

  // 'a' と 'b' を removeFromSelection（'c' は残置）
  env.changedEvents.length = 0;
  env.api.removeFromSelection(['a', 'b']);

  // Set からは 'c' のみ残る
  assert.deepEqual(env.api.getSelectedIDs(), ['c']);
  // 'a' / 'b' の DOM は .is-selected が外れ、checkbox.checked = false
  assert.ok(!cards[0].classList.contains('is-selected'), 'a is-selected removed');
  assert.equal(cards[0].checkbox.checked, false, 'a checkbox unchecked');
  assert.ok(!cards[1].classList.contains('is-selected'), 'b is-selected removed');
  assert.equal(cards[1].checkbox.checked, false, 'b checkbox unchecked');
  // 'c' は触られない
  assert.ok(cards[2].classList.contains('is-selected'), 'c is-selected kept');
  assert.equal(cards[2].checkbox.checked, true, 'c checkbox stays checked');
  // count=1 の event が最後に出ている
  const last = env.changedEvents[env.changedEvents.length - 1];
  assert.equal(last.count, 1);
});

test('TestRemoveFromSelectionNoArticleIsNoop: 対象 article が DOM 不在の id は Set のみ更新 (bulk-delete 成功後の fade-out 完了 ケース)', () => {
  // 一括削除成功で fade-out + remove が完了済みの article に対する
  // removeFromSelection は、findCard が null を返すパスで DOM 操作は no-op
  // となり、Set だけが更新される。
  const cards = ['a', 'b'].map((id) => makeCard({ id }));
  const env = loadModule({ cards });
  for (const c of cards) {
    if (c.parent) c.parent.removeChild(c);
    env.region.appendChild(c);
  }
  // 初期 appendChild で fragment-swap record が enqueue 済み。これを引いて
  // おかないと、後段の endActionMutation が takeRecords でこれらを drain して
  // 全件 reset を発火してしまう（テストフィクスチャ固有の都合）。
  for (const obs of (env.region._observers || [])) obs.takeRecords();

  cards[0].checkbox.checked = true;
  env.document.dispatch('change', { target: cards[0].checkbox });
  cards[1].checkbox.checked = true;
  env.document.dispatch('change', { target: cards[1].checkbox });
  assert.equal(env.api.getSelectedIDs().length, 2);

  // 'a' を DOM から取り除く（bulk-delete 成功 + fade-out 完了をシミュレート）
  // bracket 中扱いにすることで MutationObserver の reset を抑止する
  env.api.beginActionMutation();
  env.region.removeChild(cards[0]);
  env.api.endActionMutation();
  env.changedEvents.length = 0;

  // removeFromSelection(['a']) は DOM 不在でも throw せず、Set からは消える
  assert.doesNotThrow(() => env.api.removeFromSelection(['a']));
  assert.deepEqual(env.api.getSelectedIDs(), ['b']);
  // 'b' は触られない
  assert.ok(cards[1].classList.contains('is-selected'));
  assert.equal(cards[1].checkbox.checked, true);
});

test('TestClearAllProgrammatic: clear() 呼出 → Set 空 + 全 checkbox unchecked + .is-selected 除去', () => {
  const cards = ['a', 'b', 'c'].map((id) => makeCard({ id }));
  const env = loadModule({ cards });
  for (const c of cards) {
    if (c.parent) c.parent.removeChild(c);
    env.region.appendChild(c);
  }

  // 全選択
  for (const c of cards) {
    c.checkbox.checked = true;
    env.document.dispatch('change', { target: c.checkbox });
  }
  assert.equal(env.api.getSelectedIDs().length, 3);

  // clear()
  env.changedEvents.length = 0;
  env.api.clear();

  assert.deepEqual(env.api.getSelectedIDs(), []);
  for (const c of cards) {
    assert.equal(c.checkbox.checked, false, `${c.dataset.itemId} checkbox unchecked`);
    assert.ok(!c.classList.contains('is-selected'), `${c.dataset.itemId} .is-selected 除去`);
  }
  const last = env.changedEvents[env.changedEvents.length - 1];
  assert.equal(last.count, 0);
});
