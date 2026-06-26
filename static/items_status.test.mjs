// items_status.test.mjs
//
// /ui/items の状態切替ボタン (Issue #119 task 8) を司る
// `static/items_status_actions.js` の単体テスト。
//
// `items_active_filters.test.mjs` と同じ規約で、実 DOM を持たない node:test 上で
// 動作させるため、本機能の AC が要求する範囲（click / preventDefault / fetch /
// document.querySelector* / element classList / aria-* / dataset / closest /
// querySelectorAll / setTimeout）に絞った最小 fake DOM を用意し、
// vm.createContext で items_status_actions.js を評価する。
//
// AC マッピング:
//   - Req 2.3: unread カードで mark-read-toggle → PATCH body {"status":"read"}
//   - Req 2.4: read カードで mark-read-toggle → PATCH body {"status":"unread"}
//   - Req 2.5: unread/read カードで archive-toggle → PATCH body {"status":"archived"}
//   - Req 2.6: archived カードで mark-read-toggle/archive-toggle → {"status":"unread"}
//   - Req 2.7: PATCH 失敗時に data-status / data-current-status / label / aria-label /
//     badge を一切書き換えず toast.error を表示
//   - Req 2.8: 成功時に現在の status タブ条件で表示すべきでなくなった item が
//     fade-out で DOM 削除（タブ条件は [data-items-region].dataset.currentStatus）
//   - NFR 1.3: click 直後（PATCH 応答前）にボタン disabled + card に
//     is-status-updating が付与され、応答 resolve / reject 後に外れる

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

  // closest は本テスト範囲で必要な class / attribute セレクタのみサポート。
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
}

function matchesSelector(node, selector) {
  if (!node || !node.attrs) return false;
  // カンマ区切り (例: '.item-card, .detail-card') は or 結合として解釈する。
  // closest() は 'A, B' を「A または B のいずれかに最初にマッチした祖先」として
  // 扱うため、各サブセレクタを順に試して 1 つでもマッチすれば true を返す。
  if (selector.indexOf(',') !== -1) {
    const parts = selector.split(',').map((s) => s.trim()).filter(Boolean);
    for (const p of parts) {
      if (matchesSelector(node, p)) return true;
    }
    return false;
  }
  // class セレクタ: '.foo' / 'button.foo' / 'article.bar'
  const classMatch = /^([a-z]*)\.([\w-]+)$/i.exec(selector);
  if (classMatch) {
    const tag = classMatch[1];
    const cls = classMatch[2];
    if (tag && node.tagName !== tag.toUpperCase()) return false;
    return node.classList && node.classList.contains(cls);
  }
  // attr セレクタ: '[data-foo]' / '[data-foo="bar"]'
  const attrMatch = /^\[([\w-]+)(?:="([^"]*)")?\]$/.exec(selector);
  if (attrMatch) {
    const name = attrMatch[1];
    const val = attrMatch[2];
    if (!node.attrs.has(name)) return false;
    if (val == null) return true;
    return node.attrs.get(name) === val;
  }
  return false;
}

class FakeButton extends FakeElement {
  constructor(kind, { itemId, currentStatus }) {
    const cls = kind === 'mark-read' ? 'btn-secondary mark-read-toggle' : 'btn-secondary archive-toggle';
    const labels = kind === 'mark-read'
      ? (currentStatus === 'unread' ? { label: 'Mark read', aria: '既読にする' } : { label: 'Mark unread', aria: '未読に戻す' })
      : (currentStatus === 'archived' ? { label: 'Unarchive', aria: 'アーカイブ解除' } : { label: 'Archive', aria: 'アーカイブする' });
    super('button', {
      type: 'button',
      class: cls,
      'data-item-id': String(itemId),
      'data-current-status': currentStatus,
      'aria-label': labels.aria,
    });
    this.textContent = labels.label;
  }
}

class FakeStatusBadge extends FakeElement {
  constructor(status) {
    super('span', {
      class: 'item-status-badge',
      'data-status': status,
      role: 'status',
      'aria-label': '状態: ' + status,
    });
    this.textContent = status;
  }
}

class FakeCard extends FakeElement {
  constructor({ itemId, status }) {
    super('article', {
      class: 'tile item-card',
      'data-status': status,
    });
    const markRead = new FakeButton('mark-read', { itemId, currentStatus: status });
    const archive = new FakeButton('archive', { itemId, currentStatus: status });
    const badge = new FakeStatusBadge(status);
    this.appendChild(markRead);
    this.appendChild(archive);
    this.appendChild(badge);
    this.markReadBtn = markRead;
    this.archiveBtn = archive;
    this.badge = badge;
  }
}

// 詳細画面 (templates/item_detail.html) のカードコンテナ。
// 一覧画面と異なり `article.card.detail-card` で、`data-status` 初期値も
// `.item-status-badge` も持たない（detail page には status pill しかない）。
// Reviewer 指摘 #1 (medium, items_status_actions.js:243) の回帰確認用に使う。
class FakeDetailCard extends FakeElement {
  constructor({ itemId, status }) {
    super('article', {
      class: 'card detail-card',
    });
    const markRead = new FakeButton('mark-read', { itemId, currentStatus: status });
    const archive = new FakeButton('archive', { itemId, currentStatus: status });
    this.appendChild(markRead);
    this.appendChild(archive);
    this.markReadBtn = markRead;
    this.archiveBtn = archive;
  }
}

class FakeRegion extends FakeElement {
  constructor(tab) {
    super('section', {
      class: 'items',
      'data-items-region': '',
    });
    if (tab != null) {
      this.setAttribute('data-current-status', tab);
    }
  }
}

// --- Document/Window factories -----------------------------------------

function createFakeDocument({ region, cards }) {
  const docListeners = new Map();
  // root にカードと region を attach
  const root = new FakeElement('div', {});
  for (const c of cards) root.appendChild(c);
  if (region) root.appendChild(region);

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
      if (selector === 'meta[name="csrf-token"]') return null;
      return root.querySelector(selector);
    },

    querySelectorAll(selector) {
      return root.querySelectorAll(selector);
    },
  };
}

function createFetchQueue(handlers) {
  const queue = [...handlers];
  const calls = [];
  async function fetch(url, options = {}) {
    calls.push({ url, options });
    if (queue.length === 0) {
      throw new Error(`unexpected fetch: ${url}`);
    }
    const next = queue.shift();
    if (typeof next === 'function') return next(url, options);
    return next;
  }
  return { fetch, calls };
}

function jsonOkResponse() {
  return { ok: true, status: 200, async json() { return {}; }, async text() { return ''; } };
}

function jsonFailResponse(status = 500) {
  return { ok: false, status, async json() { return { error: 'fail' }; }, async text() { return ''; } };
}

async function flushMicrotasks(rounds = 24) {
  for (let i = 0; i < rounds; i += 1) await Promise.resolve();
}

// fake setTimeout: ID 連番で登録、flushAllTimeouts で順次発火
function createFakeTimers() {
  const queue = [];
  let id = 1;
  return {
    setTimeout(fn, _ms) {
      const tid = id++;
      queue.push({ id: tid, fn });
      return tid;
    },
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
  cards,
  tab = null, // [data-items-region].dataset.currentStatus
  fetchHandlers = [],
  toast = null,
  timers = null,
} = {}) {
  const region = new FakeRegion(tab);
  const document = createFakeDocument({ region, cards });
  const { fetch, calls } = createFetchQueue(fetchHandlers);
  const fakeToast = toast || {
    errors: [],
    successes: [],
    error(msg) { this.errors.push(msg); },
    success(msg) { this.successes.push(msg); },
  };
  const fakeTimers = timers || createFakeTimers();

  const window = {
    document,
    fetch,
    addEventListener() { /* not used */ },
    setTimeout: fakeTimers.setTimeout,
    alert() { /* not used directly; toast is injected via opts */ },
  };

  const context = vm.createContext({
    document, window,
    URL, URLSearchParams, console,
    globalThis: {},
  });

  const source = readFileSync(resolve(process.cwd(), 'static/items_status_actions.js'), 'utf8');

  // 評価時は IIFE が auto-init するが、本モジュールは init() を opts なしで呼ぶ
  // ため、テスト用 fake toast / fake setTimeout の注入のため、評価後に再 init を
  // 行う設計にもできる。ただし元実装は init を export していないので、テストでは
  // 評価時の自動 init を有効活用する。fake toast / fake timers を window 経由で
  // 仕込んでおく必要があるため、window.fetch / window.setTimeout / window.alert を
  // テスト fixture でセット済みであることを利用する。
  //
  // ただし toast はモジュール側で window.alert に fallback する設計なので、
  // window.alert を toast の sink として扱うことで「toast.error が呼ばれた回数」を
  // 観測する。これにより init() opts 経由の toast 注入が無くてもテスト可能。
  let alertCount = 0;
  let alertMessages = [];
  window.alert = (msg) => {
    alertCount += 1;
    alertMessages.push(String(msg));
  };

  new vm.Script(source, { filename: 'static/items_status_actions.js' }).runInContext(context);

  return {
    region, cards, document,
    fetchCalls: calls,
    timers: fakeTimers,
    getAlertCount: () => alertCount,
    getAlertMessages: () => alertMessages.slice(),
    clickButton: async (card, kind) => {
      const btn = kind === 'mark-read' ? card.markReadBtn : card.archiveBtn;
      return document.dispatch('click', { target: btn });
    },
  };
}

// --- Tests --------------------------------------------------------------

test('Req 2.3: unread カードで mark-read-toggle → PATCH body {"status":"read"}', async () => {
  const card = new FakeCard({ itemId: 'item-1', status: 'unread' });
  const env = loadModule({
    cards: [card],
    fetchHandlers: [jsonOkResponse()],
  });

  await env.clickButton(card, 'mark-read');
  await flushMicrotasks();

  assert.equal(env.fetchCalls.length, 1);
  assert.equal(env.fetchCalls[0].url, '/v1/items/item-1/status');
  assert.equal(env.fetchCalls[0].options.method, 'PATCH');
  const body = JSON.parse(env.fetchCalls[0].options.body);
  assert.deepEqual(body, { status: 'read' });
});

test('Req 2.4: read カードで mark-read-toggle → PATCH body {"status":"unread"}', async () => {
  const card = new FakeCard({ itemId: 'item-2', status: 'read' });
  const env = loadModule({
    cards: [card],
    fetchHandlers: [jsonOkResponse()],
  });

  await env.clickButton(card, 'mark-read');
  await flushMicrotasks();

  const body = JSON.parse(env.fetchCalls[0].options.body);
  assert.deepEqual(body, { status: 'unread' });
});

test('Req 2.6: archived カードで mark-read-toggle → PATCH body {"status":"unread"}', async () => {
  const card = new FakeCard({ itemId: 'item-3', status: 'archived' });
  const env = loadModule({
    cards: [card],
    fetchHandlers: [jsonOkResponse()],
  });

  await env.clickButton(card, 'mark-read');
  await flushMicrotasks();

  const body = JSON.parse(env.fetchCalls[0].options.body);
  assert.deepEqual(body, { status: 'unread' });
});

test('Req 2.5: unread カードで archive-toggle → PATCH body {"status":"archived"}', async () => {
  const card = new FakeCard({ itemId: 'item-4', status: 'unread' });
  const env = loadModule({
    cards: [card],
    fetchHandlers: [jsonOkResponse()],
  });

  await env.clickButton(card, 'archive');
  await flushMicrotasks();

  const body = JSON.parse(env.fetchCalls[0].options.body);
  assert.deepEqual(body, { status: 'archived' });
});

test('Req 2.5 (read state regression): read カードで archive-toggle → PATCH body {"status":"archived"}', async () => {
  const card = new FakeCard({ itemId: 'item-5', status: 'read' });
  const env = loadModule({
    cards: [card],
    fetchHandlers: [jsonOkResponse()],
  });

  await env.clickButton(card, 'archive');
  await flushMicrotasks();

  const body = JSON.parse(env.fetchCalls[0].options.body);
  assert.deepEqual(body, { status: 'archived' });
});

test('Req 2.6 (archive→unread): archived カードで archive-toggle → PATCH body {"status":"unread"}', async () => {
  const card = new FakeCard({ itemId: 'item-6', status: 'archived' });
  const env = loadModule({
    cards: [card],
    fetchHandlers: [jsonOkResponse()],
  });

  await env.clickButton(card, 'archive');
  await flushMicrotasks();

  const body = JSON.parse(env.fetchCalls[0].options.body);
  assert.deepEqual(body, { status: 'unread' });
});

test('Req 2.8 + Reviewer 指摘 #1: 成功時に data-status と同一カード内 2 ボタンの data-current-status の両方が更新される', async () => {
  const card = new FakeCard({ itemId: 'item-7', status: 'unread' });
  const env = loadModule({
    cards: [card],
    fetchHandlers: [jsonOkResponse()],
  });

  await env.clickButton(card, 'mark-read');
  await flushMicrotasks();

  // card.data-status は read に更新される
  assert.equal(card.getAttribute('data-status'), 'read');
  // mark-read-toggle / archive-toggle 両方の data-current-status が read に更新される
  assert.equal(card.markReadBtn.getAttribute('data-current-status'), 'read');
  assert.equal(card.archiveBtn.getAttribute('data-current-status'), 'read');
  // label / aria-label も更新（mark-read: Mark unread / 未読に戻す、archive: Archive / アーカイブする）
  assert.equal(card.markReadBtn.textContent, 'Mark unread');
  assert.equal(card.markReadBtn.getAttribute('aria-label'), '未読に戻す');
  assert.equal(card.archiveBtn.textContent, 'Archive');
  assert.equal(card.archiveBtn.getAttribute('aria-label'), 'アーカイブする');
  // badge も更新
  assert.equal(card.badge.textContent, 'read');
  assert.equal(card.badge.getAttribute('data-status'), 'read');
  assert.equal(card.badge.getAttribute('aria-label'), '状態: read');
});

test('Reviewer 指摘 #1 高 (連続 click 回帰): unread カードで mark-read 成功 → 同ボタン再 click → 2 回目 body は {"status":"unread"}', async () => {
  const card = new FakeCard({ itemId: 'item-8', status: 'unread' });
  const env = loadModule({
    cards: [card],
    // tab=all 相当（DOM 削除されず残る）にして連続 click を許容する
    tab: 'all',
    fetchHandlers: [jsonOkResponse(), jsonOkResponse()],
  });

  await env.clickButton(card, 'mark-read');
  await flushMicrotasks();
  // 1 回目: unread → read
  const body1 = JSON.parse(env.fetchCalls[0].options.body);
  assert.deepEqual(body1, { status: 'read' });
  // data-current-status が read に更新されたことを確認（stale な unread を読まない前提）
  assert.equal(card.markReadBtn.getAttribute('data-current-status'), 'read');

  // 2 回目 click
  await env.clickButton(card, 'mark-read');
  await flushMicrotasks();
  const body2 = JSON.parse(env.fetchCalls[1].options.body);
  // stale unread を読むと {"status":"read"} を再送してしまう。
  // 正しく data-current-status を更新していれば {"status":"unread"} になる。
  assert.deepEqual(body2, { status: 'unread' });
});

test('Req 2.7: PATCH 失敗時に data-status / data-current-status / label / aria-label / badge を維持し toast.error', async () => {
  const card = new FakeCard({ itemId: 'item-9', status: 'unread' });
  const env = loadModule({
    cards: [card],
    fetchHandlers: [jsonFailResponse(500)],
  });

  await env.clickButton(card, 'mark-read');
  await flushMicrotasks();

  // 元状態維持（unread のまま）
  assert.equal(card.getAttribute('data-status'), 'unread', 'card.data-status は変わらない');
  assert.equal(card.markReadBtn.getAttribute('data-current-status'), 'unread', 'mark-read-toggle の data-current-status は変わらない');
  assert.equal(card.archiveBtn.getAttribute('data-current-status'), 'unread', 'archive-toggle の data-current-status も変わらない');
  assert.equal(card.markReadBtn.textContent, 'Mark read', 'label は変わらない');
  assert.equal(card.markReadBtn.getAttribute('aria-label'), '既読にする', 'aria-label は変わらない');
  assert.equal(card.badge.textContent, 'unread', 'badge text は変わらない');
  assert.equal(card.badge.getAttribute('data-status'), 'unread', 'badge data-status は変わらない');
  // toast.error が表示される（fallback として window.alert 経由で観測）
  assert.equal(env.getAlertCount(), 1, 'toast.error が 1 回呼ばれる');
  assert.ok(env.getAlertMessages()[0].includes('状態の更新に失敗'), 'toast メッセージは失敗通知');
  // disabled / is-status-updating は解除される
  assert.equal(card.markReadBtn.disabled, false, 'disabled は解除される');
  assert.ok(!card.classList.contains('is-status-updating'), 'is-status-updating は外れる');
});

test('Req 2.7 (network error): fetch が throw した場合も元状態維持 + toast.error', async () => {
  const card = new FakeCard({ itemId: 'item-10', status: 'read' });
  const env = loadModule({
    cards: [card],
    fetchHandlers: [() => { throw new Error('network down'); }],
  });

  await env.clickButton(card, 'archive');
  await flushMicrotasks();

  assert.equal(card.getAttribute('data-status'), 'read');
  assert.equal(card.archiveBtn.getAttribute('data-current-status'), 'read');
  assert.equal(env.getAlertCount(), 1, 'network error でも toast.error');
  assert.equal(card.archiveBtn.disabled, false);
  assert.ok(!card.classList.contains('is-status-updating'));
});

test('Req 2.8 (Unread タブ + unread→read): mark-read 成功で当該 card が DOM から fade-out 削除される', async () => {
  const card = new FakeCard({ itemId: 'item-11', status: 'unread' });
  const env = loadModule({
    cards: [card],
    tab: 'unread',
    fetchHandlers: [jsonOkResponse()],
  });

  await env.clickButton(card, 'mark-read');
  await flushMicrotasks();

  // fade-out クラスが付与され、setTimeout が積まれている
  assert.ok(card.classList.contains('fade-out'), 'fade-out クラスが付与される');
  assert.equal(env.timers.pending(), 1, 'setTimeout が 1 回登録されている');
  // setTimeout を発火させると DOM から削除される
  env.timers.flushAll();
  assert.equal(card.removed, true, 'card が DOM から remove される');
});

test('Req 2.8 (Archived タブ + archived→unread): archive 成功で当該 card が DOM から fade-out 削除される', async () => {
  const card = new FakeCard({ itemId: 'item-12', status: 'archived' });
  const env = loadModule({
    cards: [card],
    tab: 'archived',
    fetchHandlers: [jsonOkResponse()],
  });

  await env.clickButton(card, 'archive');
  await flushMicrotasks();

  assert.ok(card.classList.contains('fade-out'));
  env.timers.flushAll();
  assert.equal(card.removed, true);
});

test('Req 2.8 (All タブ + 一致状態): タブ条件に一致したままなら DOM 上に残る', async () => {
  // All タブで unread → read は archived 化ではないので DOM に残る
  const card = new FakeCard({ itemId: 'item-13', status: 'unread' });
  const env = loadModule({
    cards: [card],
    tab: 'all',
    fetchHandlers: [jsonOkResponse()],
  });

  await env.clickButton(card, 'mark-read');
  await flushMicrotasks();

  assert.ok(!card.classList.contains('fade-out'), 'All タブで read 化なら fade-out しない');
  assert.equal(env.timers.pending(), 0, 'remove 用 setTimeout は登録されない');
  assert.equal(card.removed, false, 'card は DOM に残る');
});

test('Req 2.8 (All タブ + archived 化): archive 成功で archived 化なら DOM から fade-out 削除される', async () => {
  // All タブは unread + read の和集合（archived 除外）。archived 化された item は消す。
  const card = new FakeCard({ itemId: 'item-14', status: 'unread' });
  const env = loadModule({
    cards: [card],
    tab: 'all',
    fetchHandlers: [jsonOkResponse()],
  });

  await env.clickButton(card, 'archive');
  await flushMicrotasks();

  assert.ok(card.classList.contains('fade-out'), 'All タブで archived 化なら fade-out');
  env.timers.flushAll();
  assert.equal(card.removed, true);
});

test('NFR 1.3 (同期 visual ack): click 直後 (fetch 応答前) にボタン disabled + card に is-status-updating が同期付与される', async () => {
  const card = new FakeCard({ itemId: 'item-15', status: 'unread' });
  // pending forever な fetch を仕込む
  const env = loadModule({
    cards: [card],
    fetchHandlers: [() => new Promise(() => { /* never resolve */ })],
  });

  // click を dispatch（dispatch は sync handler 実行後に return する）
  await env.clickButton(card, 'mark-read');
  // この時点で fetch は pending、PATCH 応答はまだ。
  // ただし click handler は同期的に disabled + is-status-updating を付与しているはず。
  assert.equal(card.markReadBtn.disabled, true, 'click 直後に同期的に disabled');
  assert.ok(card.classList.contains('is-status-updating'), 'click 直後に同期的に is-status-updating');
  // PATCH 応答前なので data-status / data-current-status は変わらないまま
  assert.equal(card.getAttribute('data-status'), 'unread');
  assert.equal(card.markReadBtn.getAttribute('data-current-status'), 'unread');
});

test('NFR 1.3 (応答成功時): 成功応答後に is-status-updating が外れ disabled も解除される', async () => {
  const card = new FakeCard({ itemId: 'item-16', status: 'unread' });
  const env = loadModule({
    cards: [card],
    tab: 'all',
    fetchHandlers: [jsonOkResponse()],
  });

  await env.clickButton(card, 'mark-read');
  await flushMicrotasks();

  assert.ok(!card.classList.contains('is-status-updating'), '成功後 is-status-updating は外れる');
  assert.equal(card.markReadBtn.disabled, false, '成功後 disabled は解除');
});

test('NFR 1.3 (応答失敗時): 失敗応答後にも is-status-updating が外れ disabled も解除される', async () => {
  const card = new FakeCard({ itemId: 'item-17', status: 'unread' });
  const env = loadModule({
    cards: [card],
    fetchHandlers: [jsonFailResponse(500)],
  });

  await env.clickButton(card, 'mark-read');
  await flushMicrotasks();

  assert.ok(!card.classList.contains('is-status-updating'), '失敗後 is-status-updating は外れる');
  assert.equal(card.markReadBtn.disabled, false, '失敗後 disabled も解除');
});

test('btn.disabled が true のときは click を二重発火させない', async () => {
  const card = new FakeCard({ itemId: 'item-18', status: 'unread' });
  // 1 回目は pending forever。2 回目クリックも fetch を呼ばない想定（disabled gate）。
  const env = loadModule({
    cards: [card],
    fetchHandlers: [() => new Promise(() => { /* never resolve */ })],
  });

  await env.clickButton(card, 'mark-read');
  assert.equal(card.markReadBtn.disabled, true);

  // 二重 click
  await env.clickButton(card, 'mark-read');
  await flushMicrotasks();

  // fetch は 1 回だけ
  assert.equal(env.fetchCalls.length, 1, 'disabled 状態の 2 回目 click では fetch されない');
});

// --- 詳細画面 (.detail-card) 回帰テスト (Reviewer 指摘 #1, items_status_actions.js:243) ----

test('Reviewer 指摘 #1 (detail-card / mark-read 成功): 詳細画面の mark-read-toggle 成功で同カード内 2 ボタンの data-current-status / label / aria-label が更新される', async () => {
  // templates/item_detail.html の `article.card.detail-card` 配下に置かれたボタン。
  // 修正前は btn.closest('.item-card') が null になるため、PATCH 自体は成功しても
  // data-current-status / label / aria-label / disabled が再リロードまで反映されず、
  // 同じ操作を再送可能なまま残っていた。修正後はカード内ボタンが新 status に追随する。
  const card = new FakeDetailCard({ itemId: 'detail-1', status: 'unread' });
  const env = loadModule({
    cards: [card],
    fetchHandlers: [jsonOkResponse()],
  });

  await env.clickButton(card, 'mark-read');
  await flushMicrotasks();

  // PATCH 自体は従来も飛んでいたが、回帰のため確認
  assert.equal(env.fetchCalls.length, 1);
  assert.equal(env.fetchCalls[0].url, '/v1/items/detail-1/status');
  assert.equal(env.fetchCalls[0].options.method, 'PATCH');
  // 修正後: 同一カード内 2 ボタンの data-current-status が更新される
  assert.equal(card.markReadBtn.getAttribute('data-current-status'), 'read');
  assert.equal(card.archiveBtn.getAttribute('data-current-status'), 'read');
  // label / aria-label も更新される
  assert.equal(card.markReadBtn.textContent, 'Mark unread');
  assert.equal(card.markReadBtn.getAttribute('aria-label'), '未読に戻す');
  assert.equal(card.archiveBtn.textContent, 'Archive');
  assert.equal(card.archiveBtn.getAttribute('aria-label'), 'アーカイブする');
  // disabled は応答後に解除される
  assert.equal(card.markReadBtn.disabled, false);
  assert.ok(!card.classList.contains('is-status-updating'));
});

test('Reviewer 指摘 #1 (detail-card / archive 連続): 詳細画面で archive-toggle 成功 → 再 click → 2 回目 body は {"status":"unread"}', async () => {
  // 詳細画面で archive を 2 回連続で押す回帰確認。
  // closest('.item-card') が null だと 1 回目成功後も data-current-status='unread' が
  // 残るため、2 回目 click も {"status":"archived"} を再送してしまう。
  // 修正後は 1 回目で data-current-status='archived' に更新され、2 回目は
  // archived → unread (Req 2.6) で {"status":"unread"} を送るのが正しい挙動。
  const card = new FakeDetailCard({ itemId: 'detail-2', status: 'unread' });
  const env = loadModule({
    cards: [card],
    fetchHandlers: [jsonOkResponse(), jsonOkResponse()],
  });

  await env.clickButton(card, 'archive');
  await flushMicrotasks();
  const body1 = JSON.parse(env.fetchCalls[0].options.body);
  assert.deepEqual(body1, { status: 'archived' });
  // data-current-status が archived に更新されている
  assert.equal(card.archiveBtn.getAttribute('data-current-status'), 'archived');

  await env.clickButton(card, 'archive');
  await flushMicrotasks();
  const body2 = JSON.parse(env.fetchCalls[1].options.body);
  assert.deepEqual(body2, { status: 'unread' }, 'archived から再 archive-toggle で unread に戻る');
});

test('Reviewer 指摘 #1 (detail-card / PATCH 失敗): 詳細画面で失敗時は元状態維持 + toast.error', async () => {
  // 詳細画面でも失敗時の DOM 維持 + toast.error が同じ規約で動作することを確認
  const card = new FakeDetailCard({ itemId: 'detail-3', status: 'read' });
  const env = loadModule({
    cards: [card],
    fetchHandlers: [jsonFailResponse(500)],
  });

  await env.clickButton(card, 'mark-read');
  await flushMicrotasks();

  // 元状態維持
  assert.equal(card.markReadBtn.getAttribute('data-current-status'), 'read');
  assert.equal(card.archiveBtn.getAttribute('data-current-status'), 'read');
  assert.equal(card.markReadBtn.textContent, 'Mark unread', 'label は変わらない');
  // toast.error
  assert.equal(env.getAlertCount(), 1);
  // 視覚 ack の解除
  assert.equal(card.markReadBtn.disabled, false);
  assert.ok(!card.classList.contains('is-status-updating'));
});

test('Reviewer 指摘 #1 (detail-card / 削除されない): 詳細画面には [data-items-region] が無いため fade-out 削除は発生しない', async () => {
  // detail page には status タブが存在しないため、いかなる状態遷移でも DOM 削除しない
  const card = new FakeDetailCard({ itemId: 'detail-4', status: 'unread' });
  const env = loadModule({
    cards: [card],
    // tab を null にして [data-items-region] を残しつつ data-current-status 未設定にする
    tab: null,
    fetchHandlers: [jsonOkResponse()],
  });

  await env.clickButton(card, 'archive');
  await flushMicrotasks();

  assert.ok(!card.classList.contains('fade-out'), 'detail-card は fade-out しない');
  assert.equal(env.timers.pending(), 0);
  assert.equal(card.removed, false);
});

test('button 以外の要素 click は no-op', async () => {
  const card = new FakeCard({ itemId: 'item-19', status: 'unread' });
  const env = loadModule({
    cards: [card],
    fetchHandlers: [],
  });

  // 別要素を click（例: badge）
  await env.document.dispatch('click', { target: card.badge });
  await flushMicrotasks();

  assert.equal(env.fetchCalls.length, 0, '無関係要素 click は fetch を呼ばない');
});

// --- Reviewer r4 指摘 #3 race-condition 回帰テスト ---------------------------

test('Reviewer r4 指摘 #3 (item-card): mark-read 進行中に同カードの archive-toggle を click しても 2 本目の PATCH が飛ばない', async () => {
  // race シナリオ:
  // 1. unread カードで mark-read-toggle を click → PATCH {status:'read'} pending
  // 2. PATCH 応答前に同カードの archive-toggle を click
  // 修正前: archive-toggle は stale data-current-status='unread' を読んで
  //         computeNext('archive', 'unread') = 'archived' を送ってしまう
  // 修正後: 同一カード内の status ボタン両方が disabled になり、2 本目の
  //         PATCH は発火しない（onClick の `if (btn.disabled) return` で阻止）
  const card = new FakeCard({ itemId: 'item-race-1', status: 'unread' });
  const env = loadModule({
    cards: [card],
    // 1 本目の fetch は never-resolve にして並行 click が disabled 越しに発火するかを検証
    fetchHandlers: [
      () => new Promise(() => { /* never resolve */ }),
      jsonOkResponse(),
    ],
  });

  await env.clickButton(card, 'mark-read');
  // 同期 visual ack で両ボタンが disabled
  assert.equal(card.markReadBtn.disabled, true, 'クリックされた mark-read-toggle は disabled');
  assert.equal(card.archiveBtn.disabled, true, '同カード内の archive-toggle も disabled (race 防止)');
  assert.ok(card.classList.contains('is-status-updating'));

  // 並行 archive click は fetch を呼ばないこと
  await env.clickButton(card, 'archive');
  await flushMicrotasks();
  assert.equal(env.fetchCalls.length, 1, '進行中の遷移をまたいで 2 本目の PATCH が発火しないこと');
});

test('Reviewer r4 指摘 #3 (detail-card): 詳細画面でも mark-read 進行中に archive-toggle click が遮断される', async () => {
  // 詳細画面は CSS の .item-card { pointer-events: none } の対象外だったため、
  // 修正前はカード CSS による race 防止が効かず、JS 側で 1 ボタンしか disabled
  // にしていなかったので race を素通りした。修正後は JS 側で両ボタン disabled +
  // .detail-card.is-status-updating の CSS でも pointer-events 抑止が二重に効く。
  const card = new FakeDetailCard({ itemId: 'detail-race-1', status: 'unread' });
  const env = loadModule({
    cards: [card],
    fetchHandlers: [
      () => new Promise(() => { /* never resolve */ }),
      jsonOkResponse(),
    ],
  });

  await env.clickButton(card, 'mark-read');
  assert.equal(card.markReadBtn.disabled, true);
  assert.equal(card.archiveBtn.disabled, true, '詳細画面でも同カードの archive-toggle が disabled');

  await env.clickButton(card, 'archive');
  await flushMicrotasks();
  assert.equal(env.fetchCalls.length, 1, '詳細画面でも 2 本目の PATCH が発火しない');
});

test('Reviewer r4 指摘 #3: PATCH 解決後は同カード内の両ボタン disabled が解除される', async () => {
  // 1 本目の PATCH が成功 / 失敗どちらでも、performTransition の finally で
  // 同一カード内のすべての status ボタンが re-enable される（成功で fade-out
  // remove されるケースを除く）。
  const card = new FakeCard({ itemId: 'item-race-2', status: 'unread' });
  const env = loadModule({
    cards: [card],
    fetchHandlers: [jsonFailResponse(500)],
  });

  await env.clickButton(card, 'mark-read');
  await flushMicrotasks();

  // 失敗後: 両ボタン共に disabled が解除されている
  assert.equal(card.markReadBtn.disabled, false, '失敗後 mark-read-toggle が再操作可能');
  assert.equal(card.archiveBtn.disabled, false, '失敗後 archive-toggle も再操作可能');
  assert.ok(!card.classList.contains('is-status-updating'));
});
