// items_search.test.mjs
//
// /ui/items の検索 debounce 即時反映と URL 同期 (Issue #114) を司る
// `static/items_search.js` の単体テスト。
//
// 実 DOM を持たない node:test 上で動作させるため、Issue #114 の AC が
// 要求する範囲（input / keydown / compositionstart / compositionend /
// popstate / fetch / history.* / location）に絞った最小 fake DOM を用意し、
// vm.createContext で items_search.js を評価する。

import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import test from 'node:test';
import vm from 'node:vm';

class FakeInput {
  constructor() {
    this.value = '';
    this.attrs = new Map();
    this.listeners = new Map();
    this.tagName = 'INPUT';
  }

  addEventListener(type, fn) {
    if (!this.listeners.has(type)) this.listeners.set(type, []);
    this.listeners.get(type).push(fn);
  }

  setAttribute(name, value) {
    this.attrs.set(name, String(value));
  }

  getAttribute(name) {
    return this.attrs.has(name) ? this.attrs.get(name) : null;
  }

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
    this.attrs = new Map([['data-items-region', '']]);
  }
  getAttribute(name) {
    return this.attrs.has(name) ? this.attrs.get(name) : null;
  }
}

function createFakeDocument({ inputs, region, activeElement = null }) {
  return {
    activeElement,
    querySelector(selector) {
      if (selector === '[data-items-region]') return region;
      return null;
    },
    querySelectorAll(selector) {
      if (selector === 'input[name="q"]') return inputs;
      return [];
    },
  };
}

function createTimer() {
  let nextID = 1;
  const queue = [];
  return {
    setTimeout(fn, ms) {
      const t = { id: nextID++, fn, ms, cancelled: false };
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
    pending() {
      return queue.filter((t) => !t.cancelled).length;
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
    get currentURL() {
      return stack[index].url;
    },
    get calls() {
      return calls;
    },
    back() {
      if (index > 0) {
        index -= 1;
      }
    },
  };
}

function createLocation(initialURL, history) {
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
  return {
    ok: true,
    status: 200,
    async text() { return html; },
  };
}

async function flushMicrotasks(rounds = 24) {
  for (let i = 0; i < rounds; i += 1) {
    await Promise.resolve();
  }
}

function loadModule({ initialURL, fetchHandlers, inputs = 1, activeIndex = -1 }) {
  const inputEls = Array.from({ length: inputs }, () => new FakeInput());
  const region = new FakeRegion();
  const document = createFakeDocument({
    inputs: inputEls,
    region,
    activeElement: activeIndex >= 0 ? inputEls[activeIndex] : null,
  });
  const timer = createTimer();
  const history = createHistory(initialURL);
  const location = createLocation(initialURL, history);
  const { fetch, calls } = createFetchQueue(fetchHandlers || []);

  // window.addEventListener('popstate') を捕捉できるように最小実装。
  const winListeners = new Map();
  const window = {
    document,
    history,
    location,
    fetch,
    setTimeout: (...args) => timer.setTimeout(...args),
    clearTimeout: (...args) => timer.clearTimeout(...args),
    addEventListener(type, fn) {
      if (!winListeners.has(type)) winListeners.set(type, []);
      winListeners.get(type).push(fn);
    },
    AbortController,
  };

  const context = vm.createContext({
    document,
    window,
    history,
    location,
    fetch,
    setTimeout: window.setTimeout,
    clearTimeout: window.clearTimeout,
    URL,
    URLSearchParams,
    AbortController,
    console,
    globalThis: {},
  });

  const source = readFileSync(resolve(process.cwd(), 'static/items_search.js'), 'utf8');
  new vm.Script(source, { filename: 'static/items_search.js' }).runInContext(context);

  return {
    inputs: inputEls,
    region,
    timer,
    history,
    fetchCalls: calls,
    dispatchPopstate: async () => {
      const handlers = winListeners.get('popstate') || [];
      for (const fn of handlers) await fn({ type: 'popstate' });
    },
    activeIndex,
  };
}

test('R1 AC-1: input then 300ms idle triggers a single fragment fetch with q', async () => {
  const env = loadModule({
    initialURL: 'http://test.invalid/ui/items',
    fetchHandlers: [fragmentResponse('<article>filtered</article>')],
  });

  env.inputs[0].value = 'rust';
  await env.inputs[0].dispatch('input');
  // Before the timer fires, no fetch yet.
  assert.equal(env.fetchCalls.length, 0);

  env.timer.runAll();
  await flushMicrotasks();

  assert.equal(env.fetchCalls.length, 1);
  const u = new URL(env.fetchCalls[0].url);
  assert.equal(u.searchParams.get('q'), 'rust');
  assert.equal(env.fetchCalls[0].options.headers['X-Requested-With'], 'ItemsFragment');
  assert.equal(env.region.innerHTML, '<article>filtered</article>');
});

test('R1 AC-2 / NFR 1.2: rapid edits within debounce window collapse to one fetch with last value', async () => {
  const env = loadModule({
    initialURL: 'http://test.invalid/ui/items',
    fetchHandlers: [fragmentResponse('<article>final</article>')],
  });

  // Three rapid changes; only the last one should be fetched.
  env.inputs[0].value = 'r';
  await env.inputs[0].dispatch('input');
  env.inputs[0].value = 'ru';
  await env.inputs[0].dispatch('input');
  env.inputs[0].value = 'rust';
  await env.inputs[0].dispatch('input');

  // Pending timer count must be 1 (the older ones cleared).
  assert.equal(env.timer.pending(), 1);

  env.timer.runAll();
  await flushMicrotasks();

  assert.equal(env.fetchCalls.length, 1);
  assert.equal(new URL(env.fetchCalls[0].url).searchParams.get('q'), 'rust');
});

test('R2 AC-1: debounce-driven sync uses history.replaceState (not pushState)', async () => {
  const env = loadModule({
    initialURL: 'http://test.invalid/ui/items',
    fetchHandlers: [fragmentResponse('<x/>')],
  });
  env.inputs[0].value = 'go';
  await env.inputs[0].dispatch('input');
  env.timer.runAll();
  await flushMicrotasks();

  const replaces = env.history.calls.filter((c) => c.kind === 'replace');
  const pushes = env.history.calls.filter((c) => c.kind === 'push');
  assert.equal(replaces.length, 1);
  assert.equal(pushes.length, 0);
  const u = new URL(replaces[0].url);
  assert.equal(u.searchParams.get('q'), 'go');
});

test('R2 AC-2 / R5 AC-3: clearing the input drops q from the URL', async () => {
  const env = loadModule({
    initialURL: 'http://test.invalid/ui/items?q=rust&sort=relevance',
    fetchHandlers: [fragmentResponse('<x/>')],
  });
  env.inputs[0].value = '';
  await env.inputs[0].dispatch('input');
  env.timer.runAll();
  await flushMicrotasks();

  const replaces = env.history.calls.filter((c) => c.kind === 'replace');
  assert.equal(replaces.length, 1);
  const u = new URL(replaces[0].url);
  assert.equal(u.searchParams.get('q'), null, 'q must be removed');
  assert.equal(u.searchParams.get('sort'), 'relevance', 'other params must be kept');
});

test('R5 AC-2: whitespace-only input is treated as empty (q removed)', async () => {
  const env = loadModule({
    initialURL: 'http://test.invalid/ui/items?q=rust',
    fetchHandlers: [fragmentResponse('<x/>')],
  });
  env.inputs[0].value = '   ';
  await env.inputs[0].dispatch('input');
  env.timer.runAll();
  await flushMicrotasks();

  const replaces = env.history.calls.filter((c) => c.kind === 'replace');
  assert.equal(replaces.length, 1);
  assert.equal(new URL(replaces[0].url).searchParams.get('q'), null);
});

test('R2 AC-3: other query params (sort / per_page / tag / page) are preserved on q sync', async () => {
  const env = loadModule({
    initialURL: 'http://test.invalid/ui/items?sort=relevance&per_page=20&tag=go&tag=news&page=3',
    fetchHandlers: [fragmentResponse('<x/>')],
  });
  env.inputs[0].value = 'rust';
  await env.inputs[0].dispatch('input');
  env.timer.runAll();
  await flushMicrotasks();

  const replaces = env.history.calls.filter((c) => c.kind === 'replace');
  assert.equal(replaces.length, 1);
  const u = new URL(replaces[0].url);
  assert.equal(u.searchParams.get('q'), 'rust');
  assert.equal(u.searchParams.get('sort'), 'relevance');
  assert.equal(u.searchParams.get('per_page'), '20');
  assert.deepEqual(u.searchParams.getAll('tag'), ['go', 'news']);
  assert.equal(u.searchParams.get('page'), '3');
});

test('R4 AC-1 / AC-2: Enter triggers immediate fetch, cancels pending debounce, uses pushState', async () => {
  const env = loadModule({
    initialURL: 'http://test.invalid/ui/items',
    fetchHandlers: [fragmentResponse('<x/>')],
  });

  env.inputs[0].value = 'rust';
  await env.inputs[0].dispatch('input');
  // Pending debounce exists.
  assert.equal(env.timer.pending(), 1);

  // Enter immediately commits.
  const ev = await env.inputs[0].dispatch('keydown', { key: 'Enter' });
  assert.equal(ev.defaultPrevented, true, 'Enter must preventDefault to suppress form submit');
  // Pending timer must have been cleared.
  assert.equal(env.timer.pending(), 0);

  await flushMicrotasks();

  // Even if the orphaned timer were to fire later, no second fetch must happen.
  env.timer.runAll();
  await flushMicrotasks();

  assert.equal(env.fetchCalls.length, 1, 'Enter immediate fetch must not double-fire with debounce');
  const pushes = env.history.calls.filter((c) => c.kind === 'push');
  assert.equal(pushes.length, 1);
  assert.equal(new URL(pushes[0].url).searchParams.get('q'), 'rust');
});

test('R3 AC-1 / AC-2: popstate refreshes input value and refetches fragment from new URL', async () => {
  const env = loadModule({
    initialURL: 'http://test.invalid/ui/items?q=initial',
    fetchHandlers: [fragmentResponse('<x>after-popstate</x>')],
  });

  // Simulate that the user navigated away and the URL changed back.
  // We mutate the history's currentURL by performing a back-like operation:
  // here we just set up a synthetic state and re-emit popstate.
  env.history.replaceState({}, '', 'http://test.invalid/ui/items?q=back');
  await env.dispatchPopstate();
  await flushMicrotasks();

  assert.equal(env.inputs[0].value, 'back');
  assert.equal(env.region.innerHTML, '<x>after-popstate</x>');
});

test('OQ-4: composition (IME) suppresses debounce until compositionend', async () => {
  const env = loadModule({
    initialURL: 'http://test.invalid/ui/items',
    fetchHandlers: [fragmentResponse('<x/>')],
  });

  await env.inputs[0].dispatch('compositionstart');
  env.inputs[0].value = 'にほん';
  await env.inputs[0].dispatch('input'); // input during composition

  // No fetch should be scheduled yet.
  assert.equal(env.timer.pending(), 0);
  assert.equal(env.fetchCalls.length, 0);

  // End composition; debounce now starts.
  env.inputs[0].value = '日本';
  await env.inputs[0].dispatch('compositionend');
  assert.equal(env.timer.pending(), 1);

  env.timer.runAll();
  await flushMicrotasks();

  assert.equal(env.fetchCalls.length, 1);
  assert.equal(new URL(env.fetchCalls[0].url).searchParams.get('q'), '日本');
});

test('OQ-4: Enter during composition does not commit (IME confirm Enter is ignored)', async () => {
  const env = loadModule({
    initialURL: 'http://test.invalid/ui/items',
    fetchHandlers: [fragmentResponse('<x/>')],
  });
  await env.inputs[0].dispatch('compositionstart');
  env.inputs[0].value = 'にほん';
  await env.inputs[0].dispatch('input');
  // While composing, Enter must not fire a fetch.
  await env.inputs[0].dispatch('keydown', { key: 'Enter' });
  await flushMicrotasks();
  assert.equal(env.fetchCalls.length, 0);
});

test('R6 AC-1: typing in one search input syncs the value to the other inputs', async () => {
  const env = loadModule({
    initialURL: 'http://test.invalid/ui/items',
    fetchHandlers: [fragmentResponse('<x/>')],
    inputs: 3,
    activeIndex: 0, // user is typing in input[0]
  });

  env.inputs[0].value = 'rust';
  await env.inputs[0].dispatch('input');
  // Other (non-active) inputs are kept in sync.
  assert.equal(env.inputs[1].value, 'rust');
  assert.equal(env.inputs[2].value, 'rust');
});

test('R1 AC-3: focused (active) input is not overwritten by syncInputs (caret preservation)', async () => {
  const env = loadModule({
    initialURL: 'http://test.invalid/ui/items',
    fetchHandlers: [fragmentResponse('<x/>')],
    inputs: 2,
    activeIndex: 0,
  });

  env.inputs[0].value = 'rust';
  await env.inputs[0].dispatch('input');
  // The active input keeps its raw value (not stripped/normalized).
  assert.equal(env.inputs[0].value, 'rust');
});

test('R1 AC-4 / NFR 2.1: fragment fetch uses the same /ui/items path with all existing query params', async () => {
  const env = loadModule({
    initialURL: 'http://test.invalid/ui/items?sort=relevance&per_page=20&tag=go',
    fetchHandlers: [fragmentResponse('<x/>')],
  });

  env.inputs[0].value = 'rust';
  await env.inputs[0].dispatch('input');
  env.timer.runAll();
  await flushMicrotasks();

  assert.equal(env.fetchCalls.length, 1);
  const u = new URL(env.fetchCalls[0].url);
  assert.equal(u.pathname, '/ui/items');
  assert.equal(u.searchParams.get('q'), 'rust');
  assert.equal(u.searchParams.get('sort'), 'relevance');
  assert.equal(u.searchParams.get('per_page'), '20');
  assert.equal(u.searchParams.get('tag'), 'go');
});

test('NFR 3.2: failed fetch leaves previous innerHTML intact (no flicker)', async () => {
  const env = loadModule({
    initialURL: 'http://test.invalid/ui/items',
    fetchHandlers: [{ ok: false, status: 500, async text() { return 'err'; } }],
  });
  env.region.innerHTML = '<article>previous</article>';

  env.inputs[0].value = 'rust';
  await env.inputs[0].dispatch('input');
  env.timer.runAll();
  await flushMicrotasks();

  assert.equal(env.region.innerHTML, '<article>previous</article>');
});

test('R6 AC-2: same idempotent input does not re-fetch after debounce (no spurious double-submit)', async () => {
  const env = loadModule({
    initialURL: 'http://test.invalid/ui/items?q=rust',
    fetchHandlers: [], // expect zero fetches
  });

  // Same value as URL; debounce should be a no-op.
  env.inputs[0].value = 'rust';
  await env.inputs[0].dispatch('input');
  env.timer.runAll();
  await flushMicrotasks();

  assert.equal(env.fetchCalls.length, 0);
});
