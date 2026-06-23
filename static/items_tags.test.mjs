// items_tags.test.mjs
//
// /ui/items のカード上タグ (button.tag-filter-toggle) を入口にした絞り込み
// トグル (Issue #117) を司る `static/items_tags.js` の単体テスト。
//
// 実 DOM を持たない node:test 上で動作させるため、Issue #117 の AC が
// 要求する範囲（click / change / popstate / fetch / history.* / location /
// document.querySelectorAll / element classList / aria-* / dataset）に絞った
// 最小 fake DOM を用意し、vm.createContext で items_tags.js を評価する。
//
// AC マッピング:
//  - 要件 2.1 / 2.2 / 2.4 / 2.5: click でタグ URL の toggle + pushState
//  - 要件 2.3 / 5.2: ボタン UI とサイドバーチェックボックスの双方向同期
//  - 要件 3.1 / 3.2 / 3.3: 既存 URL クエリ形式 (?tag=<normalized>) 遵守
//  - 要件 3.4: popstate でフラグメント再取得 + UI 復元
//  - 要件 4.2: keyboard アクティブ化（<button> なのでブラウザの Enter/Space
//    は click にディスパッチされる前提を、click ハンドラ呼び出しで担保）
//  - NFR 1.2: 連続クリック時、前段リクエストを AbortController で破棄

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
    this.form = null; // input.form の擬似サポート（FakeForm から差し戻し）
    for (const [k, v] of Object.entries(attrs)) {
      if (k === 'class') continue;
      this.setAttribute(k, v);
    }
  }

  setAttribute(name, value) {
    this.attrs.set(name, String(value));
    if (name.startsWith('data-')) {
      // data-tag-normalized -> tagNormalized
      const key = name.slice(5).replace(/-([a-z])/g, (_, c) => c.toUpperCase());
      this.dataset[key] = String(value);
    }
  }

  getAttribute(name) {
    if (name === 'class') return this.classList.toString();
    return this.attrs.has(name) ? this.attrs.get(name) : null;
  }

  // 自分の祖先（自身含む）から最初に selector に一致するものを返す。
  // ここでは「[data-tag-filter-toggle]」と「[data-items-region]」のみ対応。
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

class FakeCheckbox extends FakeElement {
  constructor(value, { checked = false } = {}) {
    super('input', { type: 'checkbox', name: 'tag', value });
    this.type = 'checkbox';
    this.name = 'tag';
    this.value = value;
    this.checked = checked;
  }
}

class FakeForm extends FakeElement {
  constructor(attrs = {}) {
    super('form', attrs);
    this._checkboxes = [];
  }
  addCheckbox(cb) {
    cb.form = this;
    this._checkboxes.push(cb);
  }
  querySelectorAll(selector) {
    if (selector === 'input[type="checkbox"][name="tag"]:checked') {
      return this._checkboxes.filter((c) => c.checked);
    }
    if (selector === 'input[type="checkbox"][name="tag"]') {
      return this._checkboxes.slice();
    }
    return [];
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

function createFakeDocument({ buttons, region, sidebarForm, mobileForm }) {
  const docListeners = new Map();
  const allButtons = buttons.slice();
  const allCheckboxes = [
    ...(sidebarForm ? sidebarForm._checkboxes : []),
    ...(mobileForm ? mobileForm._checkboxes : []),
  ];

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
      if (selector === '[data-tag-filter-toggle]') return allButtons.slice();
      if (selector === 'input[type="checkbox"][name="tag"]') return allCheckboxes.slice();
      return [];
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
    back() { if (index > 0) index -= 1; },
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

function loadModule({ initialURL, fetchHandlers = [], buttonTags = [], selected = new Set(), sidebarTags = null, mobileTags = null }) {
  const buttons = buttonTags.map((name) => new FakeButton(name, { selected: selected.has(name) }));
  const region = new FakeRegion();

  // デスクトップのサイドバーは指定があれば form + checkbox を作る。
  // 実テンプレート (templates/items.html) では auto-submit 対象の
  // デスクトップ form だけが id="filter-form" を持つ。
  let sidebarForm = null;
  if (sidebarTags) {
    sidebarForm = new FakeForm({ id: 'filter-form' });
    for (const name of sidebarTags) {
      sidebarForm.addCheckbox(new FakeCheckbox(name, { checked: selected.has(name) }));
    }
  }

  // モバイルのボトムシート form は id を持たない (Apply ボタンで初めて
  // submit される。change では絞り込みが変わらない)。
  let mobileForm = null;
  if (mobileTags) {
    mobileForm = new FakeForm();
    for (const name of mobileTags) {
      mobileForm.addCheckbox(new FakeCheckbox(name, { checked: selected.has(name) }));
    }
  }

  const document = createFakeDocument({ buttons, region, sidebarForm, mobileForm });
  const history = createHistory(initialURL);
  const location = createLocation(history);
  const { fetch, calls } = createFetchQueue(fetchHandlers);

  // window 側 listener (popstate) を捕捉
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

  const source = readFileSync(resolve(process.cwd(), 'static/items_tags.js'), 'utf8');
  new vm.Script(source, { filename: 'static/items_tags.js' }).runInContext(context);

  // 評価結果 (init() の戻り値) を捕捉する。items_tags.js は IIFE で
  // init() を呼ぶが戻り値を公開しないため、_debug は context 越しには
  // 直接取れない。テストでは DOM 経由の観測を主とし、必要なら別途検証する。
  return {
    buttons, region, history, sidebarForm, mobileForm,
    fetchCalls: calls,
    clickButton: async (name) => {
      const btn = buttons.find((b) => b.dataset.tagNormalized === name);
      if (!btn) throw new Error(`no button for tag=${name}`);
      // <button> なので keyboard activation はブラウザが click にディスパッチする。
      // 同等の経路（click イベントを target=btn で投げる）を直接駆動する。
      return document.dispatch('click', { target: btn });
    },
    toggleCheckbox: async (name) => {
      if (!sidebarForm) throw new Error('no sidebar form configured');
      const cb = sidebarForm._checkboxes.find((c) => c.value === name);
      if (!cb) throw new Error(`no checkbox for tag=${name}`);
      cb.checked = !cb.checked;
      return document.dispatch('change', { target: cb });
    },
    toggleMobileCheckbox: async (name) => {
      if (!mobileForm) throw new Error('no mobile form configured');
      const cb = mobileForm._checkboxes.find((c) => c.value === name);
      if (!cb) throw new Error(`no mobile checkbox for tag=${name}`);
      cb.checked = !cb.checked;
      return document.dispatch('change', { target: cb });
    },
    dispatchPopstate: async () => {
      const handlers = winListeners.get('popstate') || [];
      for (const fn of handlers) await fn({ type: 'popstate' });
    },
  };
}

// --- Tests --------------------------------------------------------------

test('要件 2.1 / 2.4 / 3.1: 未選択タグをクリックすると tag が追加され pushState される', async () => {
  const env = loadModule({
    initialURL: 'http://test.invalid/ui/items',
    fetchHandlers: [fragmentResponse('<x>after</x>')],
    buttonTags: ['go'],
    selected: new Set(),
  });

  await env.clickButton('go');
  await flushMicrotasks();

  // pushState が 1 回、replaceState は 0 回。
  const pushes = env.history.calls.filter((c) => c.kind === 'push');
  const replaces = env.history.calls.filter((c) => c.kind === 'replace');
  assert.equal(pushes.length, 1, 'pushState は 1 回だけ呼ばれる');
  assert.equal(replaces.length, 0, 'replaceState は呼ばれない');
  const u = new URL(pushes[0].url);
  assert.deepEqual(u.searchParams.getAll('tag'), ['go']);

  // ボタン側 UI が即時更新されている (NFR 1.1)。
  assert.equal(env.buttons[0].getAttribute('aria-pressed'), 'true');
  assert.ok(env.buttons[0].classList.contains('is-selected'));

  // フラグメントが取得され、X-Requested-With ヘッダが付いている。
  assert.equal(env.fetchCalls.length, 1);
  assert.equal(env.fetchCalls[0].options.headers['X-Requested-With'], 'ItemsFragment');
  assert.equal(env.region.innerHTML, '<x>after</x>');
});

test('要件 2.2 / 2.5 / 3.3: 選択中タグをクリックすると除外され tag パラメータが空なら URL から消える', async () => {
  const env = loadModule({
    initialURL: 'http://test.invalid/ui/items?tag=go',
    fetchHandlers: [fragmentResponse('<x/>')],
    buttonTags: ['go'],
    selected: new Set(['go']),
  });

  await env.clickButton('go');
  await flushMicrotasks();

  const pushes = env.history.calls.filter((c) => c.kind === 'push');
  assert.equal(pushes.length, 1);
  const u = new URL(pushes[0].url);
  assert.equal(u.searchParams.get('tag'), null, '最後の 1 件が外れたら tag は URL から消える');

  // 選択状態が解除されている。
  assert.equal(env.buttons[0].getAttribute('aria-pressed'), 'false');
  assert.ok(!env.buttons[0].classList.contains('is-selected'));
});

test('要件 2.1 (b 解釈: 複数選択時の追加トグル): 既選択 tag がある状態で別タグを click すると追加される', async () => {
  const env = loadModule({
    initialURL: 'http://test.invalid/ui/items?tag=go',
    fetchHandlers: [fragmentResponse('<x/>')],
    buttonTags: ['go', 'rust'],
    selected: new Set(['go']),
  });

  await env.clickButton('rust');
  await flushMicrotasks();

  const pushes = env.history.calls.filter((c) => c.kind === 'push');
  const u = new URL(pushes[0].url);
  assert.deepEqual(
    u.searchParams.getAll('tag').sort(),
    ['go', 'rust'].sort(),
    '既存タグを残しつつ追加される（単独置換ではない）',
  );
});

test('要件 3.2: タグ以外のクエリ (q / sort / per_page / page) は保持される', async () => {
  const env = loadModule({
    initialURL: 'http://test.invalid/ui/items?q=hello&sort=relevance&per_page=20&page=3',
    fetchHandlers: [fragmentResponse('<x/>')],
    buttonTags: ['go'],
    selected: new Set(),
  });

  await env.clickButton('go');
  await flushMicrotasks();

  const u = new URL(env.history.calls.filter((c) => c.kind === 'push')[0].url);
  assert.equal(u.searchParams.get('q'), 'hello');
  assert.equal(u.searchParams.get('sort'), 'relevance');
  assert.equal(u.searchParams.get('per_page'), '20');
  assert.equal(u.searchParams.get('page'), '3');
  assert.deepEqual(u.searchParams.getAll('tag'), ['go']);
});

test('要件 2.3: タグクリックでサイドバーの同名チェックボックスも追従する', async () => {
  const env = loadModule({
    initialURL: 'http://test.invalid/ui/items',
    fetchHandlers: [fragmentResponse('<x/>')],
    buttonTags: ['go', 'rust'],
    selected: new Set(),
    sidebarTags: ['go', 'rust', 'news'],
  });

  await env.clickButton('go');
  await flushMicrotasks();

  const goCb = env.sidebarForm._checkboxes.find((c) => c.value === 'go');
  const rustCb = env.sidebarForm._checkboxes.find((c) => c.value === 'rust');
  assert.equal(goCb.checked, true, 'クリックされたタグの checkbox は ON になる');
  assert.equal(rustCb.checked, false, '他のタグはそのまま');
});

test('要件 5.2: サイドバーのチェックボックス変更で同名タグボタンの aria-pressed / is-selected が更新される', async () => {
  const env = loadModule({
    initialURL: 'http://test.invalid/ui/items',
    fetchHandlers: [],
    buttonTags: ['go', 'rust'],
    selected: new Set(),
    sidebarTags: ['go', 'rust'],
  });

  // 「go」を ON にする (form submit は走らない fake DOM 環境)。
  await env.toggleCheckbox('go');
  await flushMicrotasks();

  const goBtn = env.buttons.find((b) => b.dataset.tagNormalized === 'go');
  const rustBtn = env.buttons.find((b) => b.dataset.tagNormalized === 'rust');
  assert.equal(goBtn.getAttribute('aria-pressed'), 'true');
  assert.ok(goBtn.classList.contains('is-selected'));
  assert.equal(rustBtn.getAttribute('aria-pressed'), 'false');
  assert.ok(!rustBtn.classList.contains('is-selected'));
});

test('要件 3.4: popstate で URL の tag に UI とフラグメントを揃え直す', async () => {
  const env = loadModule({
    initialURL: 'http://test.invalid/ui/items?tag=go',
    fetchHandlers: [fragmentResponse('<x>after-popstate</x>')],
    buttonTags: ['go', 'rust'],
    // 最初の DOM は go=ON, rust=OFF を反映している前提。
    selected: new Set(['go']),
  });

  // ユーザが「戻る」した結果として URL が ?tag=rust に変わった、と擬似的に表現。
  env.history.replaceState({}, '', 'http://test.invalid/ui/items?tag=rust');
  await env.dispatchPopstate();
  await flushMicrotasks();

  const goBtn = env.buttons.find((b) => b.dataset.tagNormalized === 'go');
  const rustBtn = env.buttons.find((b) => b.dataset.tagNormalized === 'rust');
  assert.equal(goBtn.getAttribute('aria-pressed'), 'false', 'go は新 URL に居ないので OFF');
  assert.equal(rustBtn.getAttribute('aria-pressed'), 'true', 'rust は新 URL に居るので ON');
  assert.equal(env.region.innerHTML, '<x>after-popstate</x>');
});

test('NFR 1.2 / OQ-(c): 連続クリック時、前段の保留中 fetch が AbortController で破棄される', async () => {
  let firstSignal = null;
  const firstHandler = (_url, options) => {
    firstSignal = options.signal;
    // never resolves until next assertion (test ends sooner via second fetch).
    return new Promise(() => { /* pending forever */ });
  };
  const env = loadModule({
    initialURL: 'http://test.invalid/ui/items',
    fetchHandlers: [firstHandler, fragmentResponse('<x>second</x>')],
    buttonTags: ['go', 'rust'],
    selected: new Set(),
  });

  // 1 件目: pending のまま
  await env.clickButton('go');
  await flushMicrotasks();
  assert.equal(env.fetchCalls.length, 1);
  assert.ok(firstSignal, 'signal が渡されている');
  assert.equal(firstSignal.aborted, false, '1 件目はまだ aborted ではない');

  // 2 件目: これにより 1 件目が abort される
  await env.clickButton('rust');
  await flushMicrotasks();
  assert.equal(env.fetchCalls.length, 2);
  assert.equal(firstSignal.aborted, true, '1 件目の signal が aborted=true になっている');
});

test('要件 1.4 / 4.3: 初期 SSR レンダリングで選択中タグに aria-pressed=true / is-selected が付いている', async () => {
  // この AC はサーバ側テンプレート (templates/items_list.html) と initial DOM の責務。
  // ここでは「JS が後から壊さないこと」を回帰として検証する。
  const env = loadModule({
    initialURL: 'http://test.invalid/ui/items?tag=go',
    fetchHandlers: [],
    buttonTags: ['go', 'rust'],
    selected: new Set(['go']),
  });

  // JS 初期化直後、ボタンの初期状態は維持されている (touch しない)。
  const goBtn = env.buttons.find((b) => b.dataset.tagNormalized === 'go');
  const rustBtn = env.buttons.find((b) => b.dataset.tagNormalized === 'rust');
  assert.equal(goBtn.getAttribute('aria-pressed'), 'true');
  assert.ok(goBtn.classList.contains('is-selected'));
  assert.equal(rustBtn.getAttribute('aria-pressed'), 'false');
  assert.ok(!rustBtn.classList.contains('is-selected'));
});

test('要件 2.5: 複数選択中で 1 つだけ残った状態から click すると tag は URL から消える (空配列の削除)', async () => {
  const env = loadModule({
    initialURL: 'http://test.invalid/ui/items?tag=go&q=keep',
    fetchHandlers: [fragmentResponse('<x/>')],
    buttonTags: ['go'],
    selected: new Set(['go']),
  });

  await env.clickButton('go');
  await flushMicrotasks();

  const u = new URL(env.history.calls.filter((c) => c.kind === 'push')[0].url);
  assert.equal(u.searchParams.get('tag'), null, 'tag は完全に消える');
  assert.equal(u.searchParams.get('q'), 'keep', 'tag 以外のクエリは保持');
});

test('要件 2.4: pushState の URL が getAll("tag") で複数値を維持できる形 (?tag=a&tag=b)', async () => {
  const env = loadModule({
    initialURL: 'http://test.invalid/ui/items?tag=go',
    fetchHandlers: [fragmentResponse('<x/>')],
    buttonTags: ['go', 'rust'],
    selected: new Set(['go']),
  });

  await env.clickButton('rust');
  await flushMicrotasks();

  const pushed = env.history.calls.filter((c) => c.kind === 'push')[0];
  const u = new URL(pushed.url);
  const tags = u.searchParams.getAll('tag').sort();
  assert.deepEqual(tags, ['go', 'rust']);
  // 既存サーバ実装 (parseTagFilters in internal/server/server.go) は
  // url.Values["tag"] を読むので、上記書式と互換。
});

// --- codex 指摘 1: タグ正規化の不一致 (URL 互換バグ) -------------------

test('codex#1 / 要件 2.2 / 3.3: URL の ?tag=Go (大文字) で正規化済みボタン go をクリックすると「解除」になる (?tag=Go&tag=go の追加にならない)', async () => {
  // サーバ側 (internal/tag/tag.go Normalize) は ?tag=Go を go として
  // 絞り込んでおり、カードボタンは data-tag-normalized="go" を持つ。
  // 正規化しないと set.has("go") が false になり、解除のはずが
  // ?tag=Go&tag=go の追加になる退行を回帰検証する。
  const env = loadModule({
    initialURL: 'http://test.invalid/ui/items?tag=Go',
    fetchHandlers: [fragmentResponse('<x/>')],
    buttonTags: ['go'],
    selected: new Set(['go']),
  });

  await env.clickButton('go');
  await flushMicrotasks();

  const pushes = env.history.calls.filter((c) => c.kind === 'push');
  assert.equal(pushes.length, 1);
  const u = new URL(pushes[0].url);
  // 解除なので tag は完全に消える。?tag=Go&tag=go の二重追加にはならない。
  assert.deepEqual(u.searchParams.getAll('tag'), [], 'tag は解除されて消える');
  // カード表示も解除される。
  assert.equal(env.buttons[0].getAttribute('aria-pressed'), 'false');
  assert.ok(!env.buttons[0].classList.contains('is-selected'));
});

test('codex#1: URL に大文字混在 (?tag=Go&tag=RUST) があっても正規化済み値で同期され、新規タグ追加は正規化済み値のみを増やす', async () => {
  const env = loadModule({
    initialURL: 'http://test.invalid/ui/items?tag=Go&tag=RUST',
    fetchHandlers: [fragmentResponse('<x/>')],
    buttonTags: ['go', 'rust', 'news'],
    // SSR では go / rust が選択中として描画される想定。
    selected: new Set(['go', 'rust']),
  });

  await env.clickButton('news');
  await flushMicrotasks();

  const u = new URL(env.history.calls.filter((c) => c.kind === 'push')[0].url);
  const tags = u.searchParams.getAll('tag').sort();
  // 既存 Go / RUST は正規化されて go / rust になり、news が追加される。
  // 大文字の重複 (Go と go など) は生まれない。
  assert.deepEqual(tags, ['go', 'news', 'rust']);
});

test('codex#1: 同一タグが大小文字で重複 (?tag=Go&tag=go) していても 1 つに畳まれて選択中表示される', async () => {
  const env = loadModule({
    initialURL: 'http://test.invalid/ui/items?tag=Go&tag=go',
    fetchHandlers: [fragmentResponse('<x>after</x>')],
    buttonTags: ['go', 'rust'],
    selected: new Set(),
  });

  // popstate 経由で syncControls を駆動し、正規化・重複畳み込みを観測する。
  await env.dispatchPopstate();
  await flushMicrotasks();

  const goBtn = env.buttons.find((b) => b.dataset.tagNormalized === 'go');
  const rustBtn = env.buttons.find((b) => b.dataset.tagNormalized === 'rust');
  assert.equal(goBtn.getAttribute('aria-pressed'), 'true', 'Go/go は go として選択中');
  assert.equal(rustBtn.getAttribute('aria-pressed'), 'false');
});

// --- codex 指摘 3: ?tags= 複数形 URL のタグトグル互換 ------------------
//
// サーバ (internal/server/server.go parseTagFilters) は ?tag= 繰り返しと
// ?tags= 複数形 (カンマ区切り) の両形式を受理する。現実装が ?tag= しか
// 読まないと、既存の ?tags=go URL で選択中の go をクリックしても
// ?tags=go&tag=go になり、外しても ?tags=go が残ってサーバ側では絞り込みが
// 継続する退行が起きる (NFR 2.2 既存絞り込み URL 互換に反する)。

test('codex#3 / NFR 2.2: ?tags=go (複数形) で選択中の go をクリックすると完全に解除される (?tags=go が残らない)', async () => {
  const env = loadModule({
    initialURL: 'http://test.invalid/ui/items?tags=go',
    fetchHandlers: [fragmentResponse('<x/>')],
    buttonTags: ['go'],
    // SSR ではサーバが ?tags=go を go として絞り込み、go が選択中で描画される。
    selected: new Set(['go']),
  });

  await env.clickButton('go');
  await flushMicrotasks();

  const pushes = env.history.calls.filter((c) => c.kind === 'push');
  assert.equal(pushes.length, 1);
  const u = new URL(pushes[0].url);
  // 解除されたので tag / tags のどちらも残ってはいけない。
  assert.deepEqual(u.searchParams.getAll('tag'), [], 'tag は残らない');
  assert.deepEqual(u.searchParams.getAll('tags'), [], 'tags も残らない (退行防止)');
  // 二重付与 (?tags=go&tag=go) になっていないこと。
  assert.equal(u.search.includes('tag'), false, '絞り込みパラメータが完全に消える');
  // カード表示も解除される。
  assert.equal(env.buttons[0].getAttribute('aria-pressed'), 'false');
  assert.ok(!env.buttons[0].classList.contains('is-selected'));
});

test('codex#3 / NFR 2.2: ?tags=go,rust (複数形カンマ区切り) で選択中の rust をクリックすると go のみ残り、正準形式 ?tag= に揃う', async () => {
  const env = loadModule({
    initialURL: 'http://test.invalid/ui/items?tags=go,rust',
    fetchHandlers: [fragmentResponse('<x/>')],
    buttonTags: ['go', 'rust'],
    selected: new Set(['go', 'rust']),
  });

  await env.clickButton('rust');
  await flushMicrotasks();

  const u = new URL(env.history.calls.filter((c) => c.kind === 'push')[0].url);
  // 残った go は正準形式 ?tag= (繰り返し) で出力され、?tags= は消える。
  assert.deepEqual(u.searchParams.getAll('tag'), ['go'], 'go が ?tag= 形式で残る');
  assert.deepEqual(u.searchParams.getAll('tags'), [], '旧形式 ?tags= は残らない');
});

test('codex#3: ?tag= と ?tags= が混在 (?tag=go&tags=rust) していても両方読み取り、新規追加で重複なくマージされる', async () => {
  const env = loadModule({
    initialURL: 'http://test.invalid/ui/items?tag=go&tags=rust',
    fetchHandlers: [fragmentResponse('<x/>')],
    buttonTags: ['go', 'rust', 'news'],
    selected: new Set(['go', 'rust']),
  });

  await env.clickButton('news');
  await flushMicrotasks();

  const u = new URL(env.history.calls.filter((c) => c.kind === 'push')[0].url);
  const tags = u.searchParams.getAll('tag').sort();
  // go (tag=) + rust (tags=) + news (新規) がマージされ、正準 ?tag= に揃う。
  assert.deepEqual(tags, ['go', 'news', 'rust']);
  assert.deepEqual(u.searchParams.getAll('tags'), [], '旧形式 ?tags= は残らない');
});

test('codex#3: ?tags=Go (大文字・複数形) で正規化済みボタン go をクリックすると解除になる (?tags=Go&tag=go の追加にならない)', async () => {
  const env = loadModule({
    initialURL: 'http://test.invalid/ui/items?tags=Go',
    fetchHandlers: [fragmentResponse('<x/>')],
    buttonTags: ['go'],
    selected: new Set(['go']),
  });

  await env.clickButton('go');
  await flushMicrotasks();

  const u = new URL(env.history.calls.filter((c) => c.kind === 'push')[0].url);
  // ?tags=Go を go として正規化したうえで解除するので、tag / tags とも残らない。
  assert.deepEqual(u.searchParams.getAll('tag'), [], 'tag は残らない');
  assert.deepEqual(u.searchParams.getAll('tags'), [], 'tags も残らない');
  assert.equal(env.buttons[0].getAttribute('aria-pressed'), 'false');
});

test('codex#3: ?tags=go,Go (複数形内で大小重複) でも 1 つに畳まれ、popstate で go が選択中表示される', async () => {
  const env = loadModule({
    initialURL: 'http://test.invalid/ui/items?tags=go,Go',
    fetchHandlers: [fragmentResponse('<x/>')],
    buttonTags: ['go', 'rust'],
    selected: new Set(),
  });

  await env.dispatchPopstate();
  await flushMicrotasks();

  const goBtn = env.buttons.find((b) => b.dataset.tagNormalized === 'go');
  const rustBtn = env.buttons.find((b) => b.dataset.tagNormalized === 'rust');
  assert.equal(goBtn.getAttribute('aria-pressed'), 'true', 'go,Go は go として畳まれ選択中');
  assert.equal(rustBtn.getAttribute('aria-pressed'), 'false');
});

// --- codex 指摘 2: モバイル ボトムシートの選択中表示ズレ --------------

test('codex#2 / 要件 1.4 / 5.2: モバイル ボトムシートの checkbox 変更 (Apply 前) ではカードの選択中表示を更新しない', async () => {
  const env = loadModule({
    initialURL: 'http://test.invalid/ui/items',
    fetchHandlers: [],
    buttonTags: ['go', 'rust'],
    selected: new Set(),
    // モバイルのボトムシート form (id なし = Apply 待ち)。
    mobileTags: ['go', 'rust'],
  });

  // ボトムシート内の go を ON にする (Apply はまだ押していない)。
  await env.toggleMobileCheckbox('go');
  await flushMicrotasks();

  // URL も一覧も未変更なので、カードの選択中表示は変わってはいけない。
  const goBtn = env.buttons.find((b) => b.dataset.tagNormalized === 'go');
  const rustBtn = env.buttons.find((b) => b.dataset.tagNormalized === 'rust');
  assert.equal(goBtn.getAttribute('aria-pressed'), 'false', 'Apply 前はカードを変えない');
  assert.ok(!goBtn.classList.contains('is-selected'));
  assert.equal(rustBtn.getAttribute('aria-pressed'), 'false');
  assert.ok(!rustBtn.classList.contains('is-selected'));
});

test('codex#2 / 要件 5.1: デスクトップのサイドバー (#filter-form) の checkbox 変更ではカードを即時に追従させる (回帰: 既存挙動を壊さない)', async () => {
  const env = loadModule({
    initialURL: 'http://test.invalid/ui/items',
    fetchHandlers: [],
    buttonTags: ['go', 'rust'],
    selected: new Set(),
    sidebarTags: ['go', 'rust'],
  });

  await env.toggleCheckbox('go');
  await flushMicrotasks();

  const goBtn = env.buttons.find((b) => b.dataset.tagNormalized === 'go');
  const rustBtn = env.buttons.find((b) => b.dataset.tagNormalized === 'rust');
  assert.equal(goBtn.getAttribute('aria-pressed'), 'true', 'デスクトップは即時反映 (Req 5.1)');
  assert.ok(goBtn.classList.contains('is-selected'));
  assert.equal(rustBtn.getAttribute('aria-pressed'), 'false');
});
