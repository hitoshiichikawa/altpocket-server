// items_active_filters.test.mjs
//
// /ui/items のアクティブフィルタチップ列 (Issue #115) を司る
// `static/items_active_filters.js` の単体テスト。
//
// `items_tags.test.mjs` と同じ規約で、実 DOM を持たない node:test 上で動作させる
// ため、本機能の AC が要求する範囲（click / preventDefault / fetch / history.* /
// location / document.querySelector* / element classList / aria-* / dataset /
// closest）に絞った最小 fake DOM を用意し、vm.createContext で
// items_active_filters.js を評価する。
//
// AC マッピング:
//  - 要件 2.1 / 2.2 / 2.6: チップクリックで URL から該当タグを削除 + pushState
//  - 要件 2.3 / 2.4: サイドバー checkbox / カード上タグの同期
//  - 要件 2.5 / 3.6 / 5.3: 最後の 1 件解除で URL から tag が消える
//  - 要件 3.2 / 3.3 / 3.4 / 3.5: 「すべてクリア」で全解除 + UI 同期
//  - 要件 4.4: popstate でフラグメント再取得
//  - 要件 5.1 / 5.2: 正準形式 ?tag= 維持、他クエリ保持
//  - 要件 6.2: <a> なので Enter は click にディスパッチされる前提
//  - NFR 1.1 / 1.2 / 1.3: AbortController で前段 fetch を破棄
//  - NFR 2.1: <a href> による SSR フォールバック動線は preventDefault しても残る

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
    this.form = null;
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
      if (selector === '[data-active-filter-chip]' &&
          node.attrs && node.attrs.has('data-active-filter-chip')) {
        return node;
      }
      if (selector === '[data-active-filter-clear-all]' &&
          node.attrs && node.attrs.has('data-active-filter-clear-all')) {
        return node;
      }
      node = node.parent;
    }
    return null;
  }
}

class FakeChip extends FakeElement {
  constructor(normalizedName, removeURL) {
    super('a', {
      'data-active-filter-chip': '',
      'data-tag-normalized': normalizedName,
      href: removeURL,
      role: 'button',
      'aria-label': `フィルタ解除: ${normalizedName}`,
    });
  }
}

class FakeClearAll extends FakeElement {
  constructor(clearURL) {
    super('a', {
      'data-active-filter-clear-all': '',
      href: clearURL,
      role: 'button',
      'aria-label': 'すべてのフィルタを解除',
    });
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

class FakeCheckbox extends FakeElement {
  constructor(value, { checked = false } = {}) {
    super('input', { type: 'checkbox', name: 'tag', value });
    this.type = 'checkbox';
    this.name = 'tag';
    this.value = value;
    this.checked = checked;
  }
}

class FakeRegion {
  constructor() {
    this.innerHTML = '';
    this._attr = new Map([['data-items-region', '']]);
  }
  getAttribute(name) { return this._attr.has(name) ? this._attr.get(name) : null; }
}

// --- Document/Window factories -----------------------------------------

function createFakeDocument({ chips, clearAll, region, cardButtons = [], checkboxes = [] }) {
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
      if (selector === '[data-active-filter-clear-all]') return clearAll;
      return null;
    },

    querySelectorAll(selector) {
      if (selector === '[data-active-filter-chip]') return chips.slice();
      if (selector === '[data-tag-filter-toggle]') return cardButtons.slice();
      if (selector === 'input[type="checkbox"][name="tag"]') return checkboxes.slice();
      return [];
    },
  };
}

function createHistory(initialURL) {
  const stack = [{ state: null, url: initialURL }];
  let index = 0;
  const calls = [];
  // 実ブラウザの pushState / replaceState は引数の URL を「現在の URL を base」
  // として絶対 URL に解決した上で履歴に積み、その後 location.href も絶対 URL を
  // 返す。fake history が相対 URL をそのまま保持してしまうと、本モジュールの
  // `new URL(rel, location.href)` の base が相対のままになり、SSR の相対 chip
  // href を扱う回帰テスト (要件 2.3 回帰) が再現できないので、ここで実ブラウザ
  // 挙動に合わせて絶対化する。
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
  chipTags = [], // [{ normalized, removeURL }]
  clearAllURL = null,
  cardTags = [], // [{ normalized, selected }]
  checkboxTags = [], // [{ normalized, checked }]
  preInjectInflight = null, // region に事前注入する __itemsFragmentInflight slot
}) {
  const chips = chipTags.map(({ normalized, removeURL }) => new FakeChip(normalized, removeURL));
  const clearAll = clearAllURL ? new FakeClearAll(clearAllURL) : null;
  const region = new FakeRegion();
  if (preInjectInflight) {
    region.__itemsFragmentInflight = preInjectInflight;
  }
  const cardButtons = cardTags.map(({ normalized, selected = false }) => new FakeButton(normalized, { selected }));
  const checkboxes = checkboxTags.map(({ normalized, checked = false }) => new FakeCheckbox(normalized, { checked }));

  const document = createFakeDocument({ chips, clearAll, region, cardButtons, checkboxes });
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

  const source = readFileSync(resolve(process.cwd(), 'static/items_active_filters.js'), 'utf8');
  new vm.Script(source, { filename: 'static/items_active_filters.js' }).runInContext(context);

  return {
    chips, clearAll, region, history, cardButtons, checkboxes,
    fetchCalls: calls,
    clickChip: async (normalized) => {
      const chip = chips.find((c) => c.dataset.tagNormalized === normalized);
      if (!chip) throw new Error(`no chip for tag=${normalized}`);
      return document.dispatch('click', { target: chip });
    },
    clickClearAll: async () => {
      if (!clearAll) throw new Error('no clear-all configured');
      return document.dispatch('click', { target: clearAll });
    },
    keydownChip: async (normalized, key) => {
      const chip = chips.find((c) => c.dataset.tagNormalized === normalized);
      if (!chip) throw new Error(`no chip for tag=${normalized}`);
      return document.dispatch('keydown', { target: chip, key });
    },
    keydownClearAll: async (key) => {
      if (!clearAll) throw new Error('no clear-all configured');
      return document.dispatch('keydown', { target: clearAll, key });
    },
    dispatchPopstate: async () => {
      const handlers = winListeners.get('popstate') || [];
      for (const fn of handlers) await fn({ type: 'popstate' });
    },
  };
}

// --- Tests --------------------------------------------------------------

test('要件 2.1 / 2.2 / 2.6: チップクリックで該当タグが URL から削除され pushState される', async () => {
  const env = loadModule({
    initialURL: 'http://test.invalid/ui/items?tag=go&tag=rust',
    fetchHandlers: [fragmentResponse('<x>after-remove</x>')],
    chipTags: [
      { normalized: 'go', removeURL: 'http://test.invalid/ui/items?tag=rust' },
      { normalized: 'rust', removeURL: 'http://test.invalid/ui/items?tag=go' },
    ],
  });

  await env.clickChip('go');
  await flushMicrotasks();

  const pushes = env.history.calls.filter((c) => c.kind === 'push');
  assert.equal(pushes.length, 1, 'pushState は 1 回だけ呼ばれる');
  const u = new URL(pushes[0].url);
  assert.deepEqual(u.searchParams.getAll('tag'), ['rust'], 'go が消えて rust だけが残る');

  // フラグメントが取得されている
  assert.equal(env.fetchCalls.length, 1, 'フラグメント fetch が 1 回発火');
  assert.equal(env.fetchCalls[0].options.headers['X-Requested-With'], 'ItemsFragment');
  assert.equal(env.region.innerHTML, '<x>after-remove</x>');
});

test('要件 2.5 / 3.6 / 5.3: 最後の 1 件解除で URL から tag パラメータが消える', async () => {
  const env = loadModule({
    initialURL: 'http://test.invalid/ui/items?tag=go&q=keep',
    fetchHandlers: [fragmentResponse('<x/>')],
    chipTags: [
      { normalized: 'go', removeURL: 'http://test.invalid/ui/items?q=keep' },
    ],
  });

  await env.clickChip('go');
  await flushMicrotasks();

  const pushes = env.history.calls.filter((c) => c.kind === 'push');
  const u = new URL(pushes[0].url);
  assert.equal(u.searchParams.get('tag'), null, 'tag は URL から完全に消える');
  assert.equal(u.searchParams.get('q'), 'keep', '他クエリは保持される (Req 5.2)');
});

test('要件 5.1 / 5.2: チップの RemoveURL がそのまま pushState に使われる (サーバ側で正準形式が確定済み)', async () => {
  // RemoveURL は SSR 側 (buildTagRemovedURL in server.go) で正準 ?tag= 形式に
  // 整えてあるので、JS はそれを尊重して pushState すれば足りる。
  const env = loadModule({
    initialURL: 'http://test.invalid/ui/items?tags=go,rust',
    fetchHandlers: [fragmentResponse('<x/>')],
    chipTags: [
      // サーバが正準 ?tag= に整えた URL
      { normalized: 'go', removeURL: 'http://test.invalid/ui/items?tag=rust' },
    ],
  });

  await env.clickChip('go');
  await flushMicrotasks();

  const pushed = env.history.calls.filter((c) => c.kind === 'push')[0];
  const u = new URL(pushed.url);
  assert.deepEqual(u.searchParams.getAll('tag'), ['rust'], '正準 ?tag= 形式に揃う');
  assert.deepEqual(u.searchParams.getAll('tags'), [], '旧 ?tags= 形式は残らない');
});

test('要件 3.2 / 3.3 / 3.6: 「すべてクリア」で全タグが解除されフラグメント取得が走る', async () => {
  const env = loadModule({
    initialURL: 'http://test.invalid/ui/items?tag=go&tag=rust&q=keep',
    fetchHandlers: [fragmentResponse('<x>after-clear</x>')],
    chipTags: [
      { normalized: 'go', removeURL: 'http://test.invalid/ui/items?tag=rust&q=keep' },
      { normalized: 'rust', removeURL: 'http://test.invalid/ui/items?tag=go&q=keep' },
    ],
    clearAllURL: 'http://test.invalid/ui/items?q=keep',
  });

  await env.clickClearAll();
  await flushMicrotasks();

  const pushes = env.history.calls.filter((c) => c.kind === 'push');
  assert.equal(pushes.length, 1);
  const u = new URL(pushes[0].url);
  assert.equal(u.searchParams.get('tag'), null, 'tag は URL から完全に消える (Req 3.6)');
  assert.equal(u.searchParams.get('tags'), null, '旧 ?tags= も残らない');
  assert.equal(u.searchParams.get('q'), 'keep', '他クエリは保持 (Req 5.2)');
  assert.equal(env.region.innerHTML, '<x>after-clear</x>');
});

test('要件 2.3 / 3.4: チップクリックでサイドバー checkbox が新条件と一致するよう更新される', async () => {
  const env = loadModule({
    initialURL: 'http://test.invalid/ui/items?tag=go&tag=rust',
    fetchHandlers: [fragmentResponse('<x/>')],
    chipTags: [
      { normalized: 'go', removeURL: 'http://test.invalid/ui/items?tag=rust' },
    ],
    checkboxTags: [
      { normalized: 'go', checked: true },
      { normalized: 'rust', checked: true },
      { normalized: 'news', checked: false },
    ],
  });

  await env.clickChip('go');
  await flushMicrotasks();

  const goCb = env.checkboxes.find((c) => c.value === 'go');
  const rustCb = env.checkboxes.find((c) => c.value === 'rust');
  const newsCb = env.checkboxes.find((c) => c.value === 'news');
  assert.equal(goCb.checked, false, 'go の checkbox は OFF になる');
  assert.equal(rustCb.checked, true, 'rust の checkbox は ON のまま');
  assert.equal(newsCb.checked, false, 'news の checkbox はそのまま OFF');
});

test('要件 2.4 / 3.5: チップクリックでカード上タグ button の aria-pressed / is-selected が更新される', async () => {
  const env = loadModule({
    initialURL: 'http://test.invalid/ui/items?tag=go&tag=rust',
    fetchHandlers: [fragmentResponse('<x/>')],
    chipTags: [
      { normalized: 'go', removeURL: 'http://test.invalid/ui/items?tag=rust' },
    ],
    cardTags: [
      { normalized: 'go', selected: true },
      { normalized: 'rust', selected: true },
    ],
  });

  await env.clickChip('go');
  await flushMicrotasks();

  const goBtn = env.cardButtons.find((b) => b.dataset.tagNormalized === 'go');
  const rustBtn = env.cardButtons.find((b) => b.dataset.tagNormalized === 'rust');
  assert.equal(goBtn.getAttribute('aria-pressed'), 'false', 'go は OFF');
  assert.ok(!goBtn.classList.contains('is-selected'));
  assert.equal(rustBtn.getAttribute('aria-pressed'), 'true', 'rust は ON のまま');
  assert.ok(rustBtn.classList.contains('is-selected'));
});

test('要件 3.4 / 3.5: 「すべてクリア」でサイドバー checkbox とカード上タグの全選択が解除される', async () => {
  const env = loadModule({
    initialURL: 'http://test.invalid/ui/items?tag=go&tag=rust',
    fetchHandlers: [fragmentResponse('<x/>')],
    clearAllURL: 'http://test.invalid/ui/items',
    cardTags: [
      { normalized: 'go', selected: true },
      { normalized: 'rust', selected: true },
    ],
    checkboxTags: [
      { normalized: 'go', checked: true },
      { normalized: 'rust', checked: true },
    ],
  });

  await env.clickClearAll();
  await flushMicrotasks();

  for (const btn of env.cardButtons) {
    assert.equal(btn.getAttribute('aria-pressed'), 'false', `${btn.dataset.tagNormalized} は OFF`);
    assert.ok(!btn.classList.contains('is-selected'));
  }
  for (const cb of env.checkboxes) {
    assert.equal(cb.checked, false, `${cb.value} の checkbox は OFF`);
  }
});

test('要件 4.4: popstate で新しい URL に応じたフラグメント取得が走る', async () => {
  const env = loadModule({
    initialURL: 'http://test.invalid/ui/items?tag=go',
    fetchHandlers: [fragmentResponse('<x>after-popstate</x>')],
    chipTags: [
      { normalized: 'go', removeURL: 'http://test.invalid/ui/items' },
    ],
  });

  // 「戻る」操作で URL が変わったことを擬似する。
  env.history.replaceState({}, '', 'http://test.invalid/ui/items?tag=rust');
  await env.dispatchPopstate();
  await flushMicrotasks();

  assert.equal(env.region.innerHTML, '<x>after-popstate</x>');
  assert.equal(env.fetchCalls.length, 1);
  assert.equal(env.fetchCalls[0].options.headers['X-Requested-With'], 'ItemsFragment');
});

test('要件 6.2: <a> なので Enter は click にディスパッチされる (JS は preventDefault でフルページ遷移を抑止)', async () => {
  // 本テストでは、click イベントが <a> の defaultPrevented を有効化することを確認する。
  // ブラウザは <a href> の click の preventDefault を見てフルページ遷移を抑止する。
  const env = loadModule({
    initialURL: 'http://test.invalid/ui/items?tag=go',
    fetchHandlers: [fragmentResponse('<x/>')],
    chipTags: [
      { normalized: 'go', removeURL: 'http://test.invalid/ui/items' },
    ],
  });

  const ev = await env.clickChip('go');
  await flushMicrotasks();
  assert.equal(ev.defaultPrevented, true, '<a> の click は preventDefault されてフルページ遷移を抑止');
});

test('要件 6.2 (回帰): role="button" のチップは Space キーでも click 相当に activate される', async () => {
  // <a role="button"> は ARIA 上 button として読まれるため、Space キーで
  // activate できる必要がある。`<a>` 要素はブラウザがネイティブに Space を
  // click にディスパッチしないので、JS 側で keydown を click 相当に変換する。
  const env = loadModule({
    initialURL: 'http://test.invalid/ui/items?tag=go&tag=rust',
    fetchHandlers: [fragmentResponse('<x>after-space</x>')],
    chipTags: [
      { normalized: 'go', removeURL: 'http://test.invalid/ui/items?tag=rust' },
      { normalized: 'rust', removeURL: 'http://test.invalid/ui/items?tag=go' },
    ],
    checkboxTags: [
      { normalized: 'go', checked: true },
      { normalized: 'rust', checked: true },
    ],
  });

  const ev = await env.keydownChip('go', ' ');
  await flushMicrotasks();

  assert.equal(ev.defaultPrevented, true, 'Space の既定動作 (ページスクロール) は抑止');
  const pushes = env.history.calls.filter((c) => c.kind === 'push');
  assert.equal(pushes.length, 1, 'Space 押下で pushState が click と同じく 1 回呼ばれる');
  const u = new URL(pushes[0].url);
  assert.deepEqual(u.searchParams.getAll('tag'), ['rust'], 'go が解除されて rust のみ残る');
  assert.equal(env.region.innerHTML, '<x>after-space</x>', 'フラグメント取得も click 相当に発火');
  const goCb = env.checkboxes.find((c) => c.value === 'go');
  assert.equal(goCb.checked, false, 'サイドバー checkbox も即時更新される');
});

test('要件 6.3 (回帰): role="button" の「すべてクリア」も Space キーで activate される', async () => {
  const env = loadModule({
    initialURL: 'http://test.invalid/ui/items?tag=go&tag=rust',
    fetchHandlers: [fragmentResponse('<x>after-space-clear</x>')],
    clearAllURL: 'http://test.invalid/ui/items',
    checkboxTags: [
      { normalized: 'go', checked: true },
      { normalized: 'rust', checked: true },
    ],
  });

  const ev = await env.keydownClearAll(' ');
  await flushMicrotasks();

  assert.equal(ev.defaultPrevented, true, 'Space の既定動作は抑止');
  const pushes = env.history.calls.filter((c) => c.kind === 'push');
  assert.equal(pushes.length, 1, 'Space 押下で pushState が呼ばれる');
  const u = new URL(pushes[0].url);
  assert.equal(u.searchParams.get('tag'), null, '全タグ解除で tag 消失');
  assert.equal(env.region.innerHTML, '<x>after-space-clear</x>');
  for (const cb of env.checkboxes) {
    assert.equal(cb.checked, false, `${cb.value} の checkbox も解除`);
  }
});

test('要件 6.2 / 6.3 (回帰): Space 以外のキーは chip / clear-all で発火しない', async () => {
  // 矢印キー・Tab・A〜Z などのキーが意図せず activate にならないこと。
  const env = loadModule({
    initialURL: 'http://test.invalid/ui/items?tag=go',
    fetchHandlers: [],
    chipTags: [
      { normalized: 'go', removeURL: 'http://test.invalid/ui/items' },
    ],
    clearAllURL: 'http://test.invalid/ui/items',
  });

  // 'a' / 'ArrowDown' / 'Tab' のいずれも no-op であることを確認。
  for (const k of ['a', 'ArrowDown', 'Tab']) {
    const ev1 = await env.keydownChip('go', k);
    const ev2 = await env.keydownClearAll(k);
    assert.equal(ev1.defaultPrevented, false, `chip: ${k} で preventDefault されない`);
    assert.equal(ev2.defaultPrevented, false, `clear-all: ${k} で preventDefault されない`);
  }
  await flushMicrotasks();
  assert.equal(env.history.calls.length, 0, 'pushState は 1 度も呼ばれない');
  assert.equal(env.fetchCalls.length, 0, 'fetch は 1 度も呼ばれない');
});

test('NFR 1.2 / 1.3: 連続チップクリックで前段の保留 fetch が AbortController で破棄される', async () => {
  let firstSignal = null;
  const firstHandler = (_url, options) => {
    firstSignal = options.signal;
    return new Promise(() => { /* pending forever */ });
  };
  const env = loadModule({
    initialURL: 'http://test.invalid/ui/items?tag=go&tag=rust',
    fetchHandlers: [firstHandler, fragmentResponse('<x>second</x>')],
    chipTags: [
      { normalized: 'go', removeURL: 'http://test.invalid/ui/items?tag=rust' },
      { normalized: 'rust', removeURL: 'http://test.invalid/ui/items?tag=go' },
    ],
  });

  await env.clickChip('go');
  await flushMicrotasks();
  assert.equal(env.fetchCalls.length, 1);
  assert.ok(firstSignal, 'signal が渡されている');
  assert.equal(firstSignal.aborted, false, '1 件目はまだ aborted ではない');

  await env.clickChip('rust');
  await flushMicrotasks();
  assert.equal(env.fetchCalls.length, 2);
  assert.equal(firstSignal.aborted, true, '1 件目の signal が aborted=true になっている');
});

test('NFR 1.3 (回帰): 連続チップ解除では古い SSR href を経由せず現在 URL から再構築するため、解除済みタグが復活しない', async () => {
  // 初期 URL: ?tag=go&tag=rust。SSR は go チップに href=?tag=rust、rust チップに
  // href=?tag=go を出力する（解除後 URL）。go チップを click した直後、
  // フラグメント取得が完了する前に rust チップを click する。
  // 旧実装は古い rust チップ href (?tag=go) をそのまま pushState していたため、
  // 解除済みの go が復活して最終 URL が ?tag=go になってしまった (Req 2.5 /
  // NFR 1.3 違反)。本テストは、JS が現在の location.href から rust 1 件だけを
  // 除く形で URL を再構築するため、最終 URL に tag が残らないことを確認する。
  let firstSignal = null;
  const firstPending = (_url, options) => {
    firstSignal = options.signal;
    return new Promise(() => { /* pending forever */ });
  };
  const env = loadModule({
    initialURL: 'http://test.invalid/ui/items?tag=go&tag=rust',
    fetchHandlers: [firstPending, fragmentResponse('<x>final</x>')],
    chipTags: [
      // SSR が出力したまま（fragment 取得待ち中は古い）の href。
      { normalized: 'go', removeURL: 'http://test.invalid/ui/items?tag=rust' },
      { normalized: 'rust', removeURL: 'http://test.invalid/ui/items?tag=go' },
    ],
    checkboxTags: [
      { normalized: 'go', checked: true },
      { normalized: 'rust', checked: true },
    ],
  });

  await env.clickChip('go');
  await flushMicrotasks();
  // 1 回目クリック直後: URL は ?tag=rust、go の checkbox は OFF。
  const afterFirst = env.history.calls.filter((c) => c.kind === 'push');
  assert.equal(afterFirst.length, 1);
  assert.deepEqual(new URL(afterFirst[0].url).searchParams.getAll('tag'), ['rust']);
  assert.ok(firstSignal && !firstSignal.aborted, '1 件目 fetch はまだ pending');

  await env.clickChip('rust');
  await flushMicrotasks();
  // 2 回目クリック後: 古い rust チップ href (?tag=go) を使わず、現在 URL
  // (?tag=rust) から rust を除いた形で URL を再構築するため、最終 URL に tag は残らない。
  const pushes = env.history.calls.filter((c) => c.kind === 'push');
  assert.equal(pushes.length, 2, 'pushState は 2 回 (1 件目 / 2 件目)');
  const finalURL = new URL(pushes[1].url);
  assert.deepEqual(finalURL.searchParams.getAll('tag'), [], '最終 URL に tag は残らない (go が復活しない)');
  // 1 件目の fetch は 2 件目 click で破棄され、2 件目 fetch が新条件で発火する。
  assert.equal(firstSignal.aborted, true, '1 件目 fetch signal は aborted');
  assert.equal(env.fetchCalls.length, 2);
  // サイドバー checkbox も最終状態と一致 (Req 2.3)。
  const goCb = env.checkboxes.find((c) => c.value === 'go');
  const rustCb = env.checkboxes.find((c) => c.value === 'rust');
  assert.equal(goCb.checked, false, 'go の checkbox は OFF のまま');
  assert.equal(rustCb.checked, false, 'rust の checkbox も OFF (復活しない)');
});

test('NFR 1.3 (回帰): Space キー連続解除でも古い SSR href を経由せず最終状態が正しく収束する', async () => {
  // Req 6.2 はマウスクリックと同等の挙動を Space キーに要求しているため、
  // 同じ古い href 経由の退行が Space 経路でも発生しないことを確認する。
  let firstSignal = null;
  const firstPending = (_url, options) => {
    firstSignal = options.signal;
    return new Promise(() => { /* pending forever */ });
  };
  const env = loadModule({
    initialURL: 'http://test.invalid/ui/items?tag=go&tag=rust',
    fetchHandlers: [firstPending, fragmentResponse('<x>final</x>')],
    chipTags: [
      { normalized: 'go', removeURL: 'http://test.invalid/ui/items?tag=rust' },
      { normalized: 'rust', removeURL: 'http://test.invalid/ui/items?tag=go' },
    ],
  });

  await env.keydownChip('go', ' ');
  await flushMicrotasks();
  await env.keydownChip('rust', ' ');
  await flushMicrotasks();

  const pushes = env.history.calls.filter((c) => c.kind === 'push');
  assert.equal(pushes.length, 2);
  const finalURL = new URL(pushes[1].url);
  assert.deepEqual(finalURL.searchParams.getAll('tag'), [], 'Space 連続解除でも tag は残らない');
  assert.equal(firstSignal.aborted, true);
});

test('NFR 1.3 (回帰): チップ解除は他クエリ (page / q / sort / per_page) を保持する', async () => {
  // 現在 URL から target URL を再構築するロジックが、tag/tags 以外のクエリを
  // 落とさないこと (Req 5.2)。
  const env = loadModule({
    initialURL: 'http://test.invalid/ui/items?tag=go&tag=rust&q=react&page=3&sort=relevance&per_page=50',
    fetchHandlers: [fragmentResponse('<x/>')],
    chipTags: [
      { normalized: 'go', removeURL: 'http://test.invalid/ui/items?tag=rust&q=react&page=3&sort=relevance&per_page=50' },
      { normalized: 'rust', removeURL: 'http://test.invalid/ui/items?tag=go&q=react&page=3&sort=relevance&per_page=50' },
    ],
    clearAllURL: 'http://test.invalid/ui/items?q=react&page=3&sort=relevance&per_page=50',
  });

  await env.clickChip('go');
  await flushMicrotasks();
  const pushed = new URL(env.history.calls.filter((c) => c.kind === 'push')[0].url);
  assert.deepEqual(pushed.searchParams.getAll('tag'), ['rust']);
  assert.equal(pushed.searchParams.get('q'), 'react');
  assert.equal(pushed.searchParams.get('page'), '3');
  assert.equal(pushed.searchParams.get('sort'), 'relevance');
  assert.equal(pushed.searchParams.get('per_page'), '50');
});

test('NFR 1.1: チップクリックで pushState + UI 同期が fetch 完了を待たずに即時実行される', async () => {
  // pending 状態の fetch でも、pushState とサイドバー checkbox / カード button の
  // 即時更新が完了することを確認する (NFR 1.1: 300ms 以内の視覚反応)。
  const env = loadModule({
    initialURL: 'http://test.invalid/ui/items?tag=go',
    fetchHandlers: [() => new Promise(() => { /* pending */ })],
    chipTags: [
      { normalized: 'go', removeURL: 'http://test.invalid/ui/items' },
    ],
    cardTags: [{ normalized: 'go', selected: true }],
    checkboxTags: [{ normalized: 'go', checked: true }],
  });

  await env.clickChip('go');
  await flushMicrotasks();

  // fetch は pending のままだが、pushState と DOM 同期は完了している。
  const pushes = env.history.calls.filter((c) => c.kind === 'push');
  assert.equal(pushes.length, 1, 'pushState は fetch 完了を待たずに即時実行');
  assert.equal(env.cardButtons[0].getAttribute('aria-pressed'), 'false', 'カード button は即時更新');
  assert.equal(env.checkboxes[0].checked, false, 'サイドバー checkbox は即時更新');
});

test('要件 2.6 (履歴粒度): チップクリックは pushState を使い、replaceState を使わない', async () => {
  // PM Open Questions (b) で確認: 「戻る」で解除前の絞り込みに戻れる挙動を採用。
  const env = loadModule({
    initialURL: 'http://test.invalid/ui/items?tag=go',
    fetchHandlers: [fragmentResponse('<x/>')],
    chipTags: [
      { normalized: 'go', removeURL: 'http://test.invalid/ui/items' },
    ],
  });

  await env.clickChip('go');
  await flushMicrotasks();

  const pushes = env.history.calls.filter((c) => c.kind === 'push');
  const replaces = env.history.calls.filter((c) => c.kind === 'replace');
  assert.equal(pushes.length, 1);
  assert.equal(replaces.length, 0, 'replaceState は呼ばれない (戻るで前の絞り込みに戻れる)');
});

test('要件 3.6 (履歴粒度): 「すべてクリア」も pushState を使い、戻るで絞り込み状態に戻れる', async () => {
  const env = loadModule({
    initialURL: 'http://test.invalid/ui/items?tag=go&tag=rust',
    fetchHandlers: [fragmentResponse('<x/>')],
    clearAllURL: 'http://test.invalid/ui/items',
  });

  await env.clickClearAll();
  await flushMicrotasks();

  const pushes = env.history.calls.filter((c) => c.kind === 'push');
  const replaces = env.history.calls.filter((c) => c.kind === 'replace');
  assert.equal(pushes.length, 1);
  assert.equal(replaces.length, 0);
});

test('NFR 2.1: チップが存在しない (フィルタなし) 環境では init が no-op で済む', async () => {
  // 0 件のフィルタが SSR された画面では <div class="active-filters"> 自体がない。
  // それでもスクリプトが評価されること、副作用がないことを確認する。
  const env = loadModule({
    initialURL: 'http://test.invalid/ui/items',
    fetchHandlers: [],
    chipTags: [],
  });
  // チップが 0 個でも例外なく評価できればよい。
  assert.equal(env.chips.length, 0);
  assert.equal(env.fetchCalls.length, 0);
});

// --- AbortController slot 共有規約の回帰テスト -------------------------
//
// Issue #117 と同じ `[data-items-region]` 上の __itemsFragmentInflight slot を共有する
// 規約を守らないと、検索 debounce やカード上タグクリック由来の保留 fetch との race
// を防げない。本テストは slot を事前に注入し、後続クリックでそれが abort されるか
// 確認する。

test('要件 2.3 (回帰): SSR が出力する相対 URL の chip href でもサイドバー checkbox が正しく同期される', async () => {
  // SSR (internal/server/server.go buildTagRemovedURL) は `url.URL{Path: "/ui/items", RawQuery: ...}`
  // の String() を返すため、チップ href は `/ui/items?tag=rust` のような相対 URL になる。
  // `new URL(relative)` は base なしでは TypeError を投げて空配列を返してしまい、
  // 複数タグから 1 つだけ解除した直後にサイドバー checkbox が全解除状態になる退行を生む。
  // 本テストは相対 href でも syncControls が新条件 (rust のみ残る) と一致させることを担保する。
  const env = loadModule({
    initialURL: 'http://test.invalid/ui/items?tag=go&tag=rust',
    fetchHandlers: [fragmentResponse('<x/>')],
    chipTags: [
      { normalized: 'go', removeURL: '/ui/items?tag=rust' },
      { normalized: 'rust', removeURL: '/ui/items?tag=go' },
    ],
    checkboxTags: [
      { normalized: 'go', checked: true },
      { normalized: 'rust', checked: true },
    ],
  });

  await env.clickChip('go');
  await flushMicrotasks();

  const goCb = env.checkboxes.find((c) => c.value === 'go');
  const rustCb = env.checkboxes.find((c) => c.value === 'rust');
  assert.equal(goCb.checked, false, 'go の checkbox は OFF になる');
  assert.equal(rustCb.checked, true, 'rust の checkbox は ON のまま残る (relative URL 退行ガード)');
});

test('AbortController slot を items_tags.js / items_search.js と共有する', async () => {
  // 規約: region 要素上の __itemsFragmentInflight slot に各モジュールが
  // controller を置き、新しい fetch を始めるときに前段を abort する
  // (Issue #117 で導入された規約。Issue #115 でも同じ slot を共有することで
  //  検索 / カードタグクリック / アクティブフィルタ操作の cross-module race
  //  を防ぐ)。
  //
  // 「slot を事前注入してその controller が abort される」ことを検証する。
  // これは items_search.js / items_tags.js のいずれかが先に評価されて
  // slot を作った後で本モジュールが評価された場合の挙動を擬似する。
  const sharedCtrl = new AbortController();
  const sharedSlot = { ctrl: sharedCtrl };

  const env = loadModule({
    initialURL: 'http://test.invalid/ui/items?tag=go',
    fetchHandlers: [fragmentResponse('<x/>')],
    chipTags: [
      { normalized: 'go', removeURL: 'http://test.invalid/ui/items' },
    ],
    preInjectInflight: sharedSlot,
  });

  await env.clickChip('go');
  await flushMicrotasks();

  // チップクリックで新規 fetch を始めるとき、本モジュールは事前注入された
  // slot 経由で前段の controller を abort する。
  assert.equal(sharedCtrl.signal.aborted, true,
    '他モジュール由来の保留 controller が同じ slot 経由で abort される');
  // slot 自体は同一参照のまま保持される (本モジュールが上書き再作成しない)。
  assert.strictEqual(env.region.__itemsFragmentInflight, sharedSlot,
    '事前注入された slot は本モジュールに上書きされず再利用される');
});
