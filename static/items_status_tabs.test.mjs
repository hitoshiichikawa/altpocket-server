// items_status_tabs.test.mjs
//
// /ui/items の状態タブ (Issue #119 task 9) を司る `static/items_status.js` の
// 単体テスト。
//
// `items_active_filters.test.mjs` と同じ規約で、実 DOM を持たない node:test 上で
// 動作させるため、本機能の AC が要求する範囲（click / preventDefault / fetch /
// history.* / location / document.querySelector* / element classList / aria-* /
// dataset / closest）に絞った最小 fake DOM を用意し、vm.createContext で
// items_status.js を評価する。
//
// AC マッピング:
//   - Req 3.2: タブ click で URL の `?status=` が新値に切り替わり、fragment 取得が
//     X-Requested-With: ItemsFragment を含む
//   - Req 3.6: タブ click 時に他クエリ（q / tag / sort / per_page / page）が
//     保持される（往復ケースの双方を assert）
//   - Req 3.7: タブ click 直後 / fragment fetch 完了後に、新 ?status= 値に
//     一致するタブだけが aria-selected="true" / is-active を持ち、他は外れる
//   - Req 3.8: ?status= 未指定 popstate で Unread タブが active になる
//              popstate で fragment 再取得 + タブ active 同期が起きる
//   - NFR 1.1 / 1.2: 連続切替時に AbortController で前段が abort される
//                    （cross-module slot 共有規約も併せて回帰固定）

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

  // closest は本テスト範囲で必要なセレクタのみサポート。
  closest(selector) {
    let node = this;
    while (node) {
      if (selector === 'nav.status-tabs a[role="tab"]') {
        // tab 自身（<a role="tab"> 直下） + 親階層に nav.status-tabs がある場合のみ true
        if (node.tagName === 'A' &&
            node.attrs && node.attrs.get('role') === 'tab' &&
            node._inStatusTabs === true) {
          return node;
        }
      }
      node = node.parent;
    }
    return null;
  }
}

// status-tabs の <a role="tab"> を表す。サーバ側 buildStatusTabURLs が出力する
// `href` を保持し、closest('nav.status-tabs a[role="tab"]') にマッチするよう
// `_inStatusTabs=true` フラグを内部的に持つ。
class FakeTab extends FakeElement {
  constructor({ tabValue, href, active }) {
    super('a', {
      role: 'tab',
      class: active ? 'status-tab is-active' : 'status-tab',
      'aria-selected': active ? 'true' : 'false',
      href,
    });
    this.tabValue = tabValue;
    this._inStatusTabs = true;
  }
}

class FakeRegion {
  constructor() {
    this.innerHTML = '';
    this._attr = new Map([['data-items-region', '']]);
    this.dataset = {};
  }
  setAttribute(name, value) {
    this._attr.set(name, String(value));
    if (name.startsWith('data-')) {
      const key = name.slice(5).replace(/-([a-z])/g, (_, c) => c.toUpperCase());
      this.dataset[key] = String(value);
    }
  }
  getAttribute(name) { return this._attr.has(name) ? this._attr.get(name) : null; }
}

// --- Document/Window factories -----------------------------------------

function createFakeDocument({ tabs, region }) {
  const docListeners = new Map();

  return {
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
        metaKey: false, ctrlKey: false, shiftKey: false, altKey: false,
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
      if (selector === 'nav.status-tabs a[role="tab"]') return tabs.slice();
      return [];
    },
  };
}

function createHistory(initialURL) {
  const stack = [{ state: null, url: initialURL }];
  let index = 0;
  const calls = [];

  // 実ブラウザ挙動に合わせて相対 URL を絶対化する。
  function resolveURL(url) {
    try {
      return new URL(url, stack[index].url).toString();
    } catch {
      return url;
    }
  }

  return {
    pushState(state, _title, url) {
      const resolved = resolveURL(url);
      calls.push({ kind: 'push', state, url: resolved });
      stack.length = index + 1;
      stack.push({ state, url: resolved });
      index = stack.length - 1;
    },
    replaceState(state, _title, url) {
      const resolved = resolveURL(url);
      calls.push({ kind: 'replace', state, url: resolved });
      stack[index] = { state, url: resolved };
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
    if (queue.length === 0) {
      throw new Error(`unexpected fetch: ${url}`);
    }
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

function loadModule({
  initialURL,
  fetchHandlers = [],
  // tabs に渡される初期 active 値。サーバが SSR した state を擬似する。
  initialActiveTab = 'unread',
  preInjectInflight = null,
}) {
  // status-tabs の 3 タブを生成。href は initialURL を base に `?status=<value>`
  // へ書き換えた絶対 URL で SSR されている前提（サーバ buildStatusTabURLs と整合）。
  function tabHref(value) {
    const u = new URL(initialURL);
    u.searchParams.set('status', value);
    return u.toString();
  }
  const tabs = [
    new FakeTab({ tabValue: 'unread', href: tabHref('unread'), active: initialActiveTab === 'unread' }),
    new FakeTab({ tabValue: 'all', href: tabHref('all'), active: initialActiveTab === 'all' }),
    new FakeTab({ tabValue: 'archived', href: tabHref('archived'), active: initialActiveTab === 'archived' }),
  ];
  const region = new FakeRegion();
  if (preInjectInflight) {
    region.__itemsFragmentInflight = preInjectInflight;
  }

  const document = createFakeDocument({ tabs, region });
  const history = createHistory(initialURL);
  const location = createLocation(history);
  const { fetch, calls } = createFetchQueue(fetchHandlers);

  const winListeners = new Map();
  const window = {
    document,
    history,
    location,
    fetch,
    addEventListener(type, fn) {
      if (!winListeners.has(type)) winListeners.set(type, []);
      winListeners.get(type).push(fn);
    },
    AbortController,
  };

  const context = vm.createContext({
    document, window, history, location, fetch,
    URL, URLSearchParams, AbortController, console,
    globalThis: {},
  });

  const source = readFileSync(resolve(process.cwd(), 'static/items_status.js'), 'utf8');
  new vm.Script(source, { filename: 'static/items_status.js' }).runInContext(context);

  return {
    tabs, region, history, document,
    fetchCalls: calls,
    clickTab: async (tabValue) => {
      const tab = tabs.find((t) => t.tabValue === tabValue);
      if (!tab) throw new Error(`no tab for value=${tabValue}`);
      return document.dispatch('click', { target: tab });
    },
    clickTabWithModifier: async (tabValue, modifier) => {
      const tab = tabs.find((t) => t.tabValue === tabValue);
      if (!tab) throw new Error(`no tab for value=${tabValue}`);
      const init = { target: tab };
      init[modifier] = true; // 'metaKey' / 'ctrlKey' / 'shiftKey' / 'altKey'
      return document.dispatch('click', init);
    },
    dispatchPopstate: async () => {
      const handlers = winListeners.get('popstate') || [];
      for (const fn of handlers) await fn({ type: 'popstate' });
    },
  };
}

// --- Tests --------------------------------------------------------------

test('Req 3.2: タブ click で URL の ?status= が新値に切り替わり pushState される', async () => {
  const env = loadModule({
    initialURL: 'http://test.invalid/ui/items?status=unread',
    initialActiveTab: 'unread',
    fetchHandlers: [fragmentResponse('<x>archived</x>')],
  });

  await env.clickTab('archived');
  await flushMicrotasks();

  const pushes = env.history.calls.filter((c) => c.kind === 'push');
  assert.equal(pushes.length, 1, 'pushState は 1 回だけ呼ばれる');
  const u = new URL(pushes[0].url);
  assert.equal(u.searchParams.get('status'), 'archived', 'URL の ?status= が archived に切り替わる');
});

test('Req 3.2: fragment fetch が X-Requested-With: ItemsFragment を含む', async () => {
  const env = loadModule({
    initialURL: 'http://test.invalid/ui/items?status=unread',
    initialActiveTab: 'unread',
    fetchHandlers: [fragmentResponse('<x>all</x>')],
  });

  await env.clickTab('all');
  await flushMicrotasks();

  assert.equal(env.fetchCalls.length, 1, 'fragment fetch が 1 回発火');
  assert.equal(env.fetchCalls[0].options.headers['X-Requested-With'], 'ItemsFragment');
  assert.equal(env.fetchCalls[0].options.method, 'GET');
  assert.equal(env.region.innerHTML, '<x>all</x>', 'region が fragment 応答で置換される');
});

test('Req 3.7: タブ click 直後に新 ?status= 値に一致するタブだけが aria-selected="true" / is-active を持つ', async () => {
  const env = loadModule({
    initialURL: 'http://test.invalid/ui/items?status=unread',
    initialActiveTab: 'unread',
    fetchHandlers: [fragmentResponse('<x/>')],
  });

  await env.clickTab('archived');
  await flushMicrotasks();

  const unread = env.tabs.find((t) => t.tabValue === 'unread');
  const all = env.tabs.find((t) => t.tabValue === 'all');
  const archived = env.tabs.find((t) => t.tabValue === 'archived');

  assert.equal(archived.getAttribute('aria-selected'), 'true', 'archived タブが active');
  assert.ok(archived.classList.contains('is-active'), 'archived タブに is-active');
  assert.equal(unread.getAttribute('aria-selected'), 'false', 'unread タブから aria-selected="true" は外れる');
  assert.ok(!unread.classList.contains('is-active'), 'unread タブから is-active が外れる');
  assert.equal(all.getAttribute('aria-selected'), 'false');
  assert.ok(!all.classList.contains('is-active'));
});

test('Req 3.7: タブ click 直後の active 同期は fetch 完了を待たず synchronous に行われる', async () => {
  // pending forever な fetch を仕込んで、click handler の同期パスで active 同期が
  // 完了することを確認する（Req 3.7 の常時可視化を synchronous DOM 反映で満たす）。
  const env = loadModule({
    initialURL: 'http://test.invalid/ui/items?status=unread',
    initialActiveTab: 'unread',
    fetchHandlers: [() => new Promise(() => { /* pending forever */ })],
  });

  await env.clickTab('all');
  await flushMicrotasks();

  // fetch は pending のままだが、タブ active 同期は完了している。
  const all = env.tabs.find((t) => t.tabValue === 'all');
  const unread = env.tabs.find((t) => t.tabValue === 'unread');
  assert.equal(all.getAttribute('aria-selected'), 'true', 'all タブが即時 active');
  assert.equal(unread.getAttribute('aria-selected'), 'false', 'unread タブから即時に外れる');
});

test('Req 3.8: popstate で ?status= 未指定 URL に戻ったとき Unread タブが active になる', async () => {
  const env = loadModule({
    initialURL: 'http://test.invalid/ui/items?status=archived',
    initialActiveTab: 'archived',
    fetchHandlers: [fragmentResponse('<x>unread-default</x>')],
  });

  // popstate を擬似発火する前に、history の current URL を ?status= 未指定の
  // 状態に巻き戻す（実ブラウザの「戻る」操作と同等）。
  env.history.replaceState({}, '', 'http://test.invalid/ui/items');
  await env.dispatchPopstate();
  await flushMicrotasks();

  // ?status= 未指定 → 既定 Unread として active 化される
  const unread = env.tabs.find((t) => t.tabValue === 'unread');
  const archived = env.tabs.find((t) => t.tabValue === 'archived');
  assert.equal(unread.getAttribute('aria-selected'), 'true', 'Unread タブが既定として active');
  assert.ok(unread.classList.contains('is-active'));
  assert.equal(archived.getAttribute('aria-selected'), 'false', 'archived タブから外れる');
  assert.ok(!archived.classList.contains('is-active'));
});

test('Req 3.8: popstate で fragment 再取得が走り、タブ active 状態も新 URL に追従する', async () => {
  const env = loadModule({
    initialURL: 'http://test.invalid/ui/items?status=unread',
    initialActiveTab: 'unread',
    fetchHandlers: [fragmentResponse('<x>after-popstate</x>')],
  });

  // 「戻る」操作で URL が ?status=all に変わったことを擬似する。
  env.history.replaceState({}, '', 'http://test.invalid/ui/items?status=all');
  await env.dispatchPopstate();
  await flushMicrotasks();

  assert.equal(env.region.innerHTML, '<x>after-popstate</x>', 'popstate で fragment 再取得が走る');
  assert.equal(env.fetchCalls.length, 1);
  assert.equal(env.fetchCalls[0].options.headers['X-Requested-With'], 'ItemsFragment');

  const all = env.tabs.find((t) => t.tabValue === 'all');
  assert.equal(all.getAttribute('aria-selected'), 'true', 'popstate 後に all タブが active');
  assert.ok(all.classList.contains('is-active'));
});

test('NFR 1.1 / 1.2: 連続切替時に AbortController で前段の保留 fetch が abort される', async () => {
  let firstSignal = null;
  const firstHandler = (_url, options) => {
    firstSignal = options.signal;
    return new Promise(() => { /* pending forever */ });
  };
  const env = loadModule({
    initialURL: 'http://test.invalid/ui/items?status=unread',
    initialActiveTab: 'unread',
    fetchHandlers: [firstHandler, fragmentResponse('<x>second</x>')],
  });

  await env.clickTab('all');
  await flushMicrotasks();
  assert.equal(env.fetchCalls.length, 1);
  assert.ok(firstSignal, 'signal が渡されている');
  assert.equal(firstSignal.aborted, false, '1 件目はまだ aborted ではない');

  await env.clickTab('archived');
  await flushMicrotasks();
  assert.equal(env.fetchCalls.length, 2);
  assert.equal(firstSignal.aborted, true, '1 件目の signal が aborted=true になる');
});

test('Req 3.6: 既存クエリ（q / tag / sort / per_page / page）保持: Unread → Archived 切替で他クエリが落ちない', async () => {
  const env = loadModule({
    initialURL: 'http://test.invalid/ui/items?q=foo&tag=bar&sort=created_at&per_page=30&page=2',
    initialActiveTab: 'unread',
    fetchHandlers: [fragmentResponse('<x/>')],
  });

  await env.clickTab('archived');
  await flushMicrotasks();

  const pushes = env.history.calls.filter((c) => c.kind === 'push');
  assert.equal(pushes.length, 1);
  const u = new URL(pushes[0].url);
  assert.equal(u.searchParams.get('status'), 'archived', 'status が archived に切替');
  assert.equal(u.searchParams.get('q'), 'foo', 'q が保持される');
  assert.equal(u.searchParams.get('tag'), 'bar', 'tag が保持される');
  assert.equal(u.searchParams.get('sort'), 'created_at', 'sort が保持される');
  assert.equal(u.searchParams.get('per_page'), '30', 'per_page が保持される');
  assert.equal(u.searchParams.get('page'), '2', 'page が保持される');
});

test('Req 3.6 (逆方向): Archived → Unread 切替でも他クエリが落ちない', async () => {
  const env = loadModule({
    initialURL: 'http://test.invalid/ui/items?status=archived&q=foo&tag=bar&sort=created_at&per_page=30&page=2',
    initialActiveTab: 'archived',
    fetchHandlers: [fragmentResponse('<x/>')],
  });

  await env.clickTab('unread');
  await flushMicrotasks();

  const pushes = env.history.calls.filter((c) => c.kind === 'push');
  assert.equal(pushes.length, 1);
  const u = new URL(pushes[0].url);
  assert.equal(u.searchParams.get('status'), 'unread', 'status が unread に切替');
  assert.equal(u.searchParams.get('q'), 'foo', 'q が保持される');
  assert.equal(u.searchParams.get('tag'), 'bar', 'tag が保持される');
  assert.equal(u.searchParams.get('sort'), 'created_at', 'sort が保持される');
  assert.equal(u.searchParams.get('per_page'), '30', 'per_page が保持される');
  assert.equal(u.searchParams.get('page'), '2', 'page が保持される');
});

test('修飾キー付き click は intercept せず既定動作を維持する (Cmd / Ctrl / Shift / Alt)', async () => {
  for (const mod of ['metaKey', 'ctrlKey', 'shiftKey', 'altKey']) {
    const env = loadModule({
      initialURL: 'http://test.invalid/ui/items?status=unread',
      initialActiveTab: 'unread',
      fetchHandlers: [], // fetch を呼ばないはず
    });

    const ev = await env.clickTabWithModifier('archived', mod);
    await flushMicrotasks();

    assert.equal(ev.defaultPrevented, false, `${mod} 付き click では preventDefault されない`);
    assert.equal(env.history.calls.length, 0, `${mod} 付き click では pushState されない`);
    assert.equal(env.fetchCalls.length, 0, `${mod} 付き click では fetch しない`);
  }
});

test('AbortController slot 共有規約: 事前注入された slot は本モジュールで再作成されず再利用される', async () => {
  // items_active_filters.js / items_tags.js / items_search.js / items_status_actions.js
  // と同じ region.__itemsFragmentInflight slot を共有する規約を守らないと、
  // 検索 debounce / カードタグ操作 / アクティブフィルタ操作との cross-module race を
  // 防げない。事前注入した slot がそのまま使われることを assert する。
  const sharedCtrl = new AbortController();
  const sharedSlot = { ctrl: sharedCtrl };

  const env = loadModule({
    initialURL: 'http://test.invalid/ui/items?status=unread',
    initialActiveTab: 'unread',
    fetchHandlers: [fragmentResponse('<x/>')],
    preInjectInflight: sharedSlot,
  });

  await env.clickTab('all');
  await flushMicrotasks();

  // 新規 fetch 開始時に事前注入された controller が abort される。
  assert.equal(sharedCtrl.signal.aborted, true,
    '他モジュール由来の保留 controller が同じ slot 経由で abort される');
  // slot 自体は同一参照のまま保持される（本モジュールが上書き再作成しない）。
  assert.strictEqual(env.region.__itemsFragmentInflight, sharedSlot,
    '事前注入された slot は本モジュールに上書きされず再利用される');
});

test('init 時に region.dataset.currentStatus が URL の ?status= に同期される', async () => {
  // items_status_actions.js (task 8) が region.dataset.currentStatus を読んで
  // タブ条件 DOM 削除判定を行うため、SSR templates/items.html が data-current-status
  // を出力しない代わりに、本モジュールが init 時に補填する規約。
  const env = loadModule({
    initialURL: 'http://test.invalid/ui/items?status=archived',
    initialActiveTab: 'archived',
    fetchHandlers: [],
  });

  assert.equal(env.region.dataset.currentStatus, 'archived',
    'init で region.dataset.currentStatus が ?status= 値に同期される');
});

test('init 時に ?status= 未指定なら region.dataset.currentStatus は unread (既定)', async () => {
  const env = loadModule({
    initialURL: 'http://test.invalid/ui/items',
    initialActiveTab: 'unread',
    fetchHandlers: [],
  });

  assert.equal(env.region.dataset.currentStatus, 'unread',
    'init で ?status= 未指定 → unread (既定) が反映される');
});

test('タブ click 後に region.dataset.currentStatus が新 status に同期される', async () => {
  const env = loadModule({
    initialURL: 'http://test.invalid/ui/items?status=unread',
    initialActiveTab: 'unread',
    fetchHandlers: [fragmentResponse('<x/>')],
  });

  await env.clickTab('archived');
  await flushMicrotasks();

  assert.equal(env.region.dataset.currentStatus, 'archived',
    'click 後に region.dataset.currentStatus が archived に同期される');
});

test('popstate 後に region.dataset.currentStatus が新 URL の ?status= に同期される', async () => {
  const env = loadModule({
    initialURL: 'http://test.invalid/ui/items?status=unread',
    initialActiveTab: 'unread',
    fetchHandlers: [fragmentResponse('<x/>')],
  });

  env.history.replaceState({}, '', 'http://test.invalid/ui/items?status=all');
  await env.dispatchPopstate();
  await flushMicrotasks();

  assert.equal(env.region.dataset.currentStatus, 'all',
    'popstate 後に region.dataset.currentStatus が all に同期される');
});

test('Req 3.7 (回帰): SSR が出力する相対 URL の tab href でも resolveStatusFromURL が canonical 値に解決する', async () => {
  // サーバ側 buildStatusTabURLs は url.URL.String() を返すため、href は
  // 絶対 URL になる pattern が canonical だが、将来 SSR が相対 URL を返した場合
  // でも resolveStatusFromURL が location.href を base に正しく解決することを
  // 担保する（items_active_filters.test.mjs の同等回帰）。
  const env = loadModule({
    initialURL: 'http://test.invalid/ui/items?status=unread',
    initialActiveTab: 'unread',
    fetchHandlers: [fragmentResponse('<x/>')],
  });

  // tabs の href を相対 URL に擬似的に書き換える。
  const archivedTab = env.tabs.find((t) => t.tabValue === 'archived');
  archivedTab.setAttribute('href', '/ui/items?status=archived');

  await env.clickTab('archived');
  await flushMicrotasks();

  const pushes = env.history.calls.filter((c) => c.kind === 'push');
  assert.equal(pushes.length, 1);
  const u = new URL(pushes[0].url);
  assert.equal(u.searchParams.get('status'), 'archived',
    '相対 URL の href からも archived が正しく解決される');
});
