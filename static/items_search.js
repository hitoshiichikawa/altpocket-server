// items_search.js
//
// /ui/items の検索 debounce 即時反映と URL 同期を担当する小さなモジュール。
//
// - 入力停止後 300ms で結果一覧領域 (#items-list / data-items-region) を
//   非同期に差し替える (Issue #114 R1)
// - URL の ?q= は debounce 時 history.replaceState、Enter 押下時
//   history.pushState で同期する (R2 / R3 / R4 / OQ-1)
// - JS が無効な環境ではスクリプトが読み込まれないだけで、フォーム送信に
//   よる従来の挙動はそのまま動く (R6 AC-3)
// - IME 変換中 (compositionstart..compositionend) は debounce タイマーを
//   発火させず、変換確定後に再開する (OQ-4)
// - フェッチ経路はサーバ側ハンドラに `X-Requested-With: ItemsFragment`
//   ヘッダを付けて呼ぶ。サーバはこれを見て items_list 部分テンプレートだけ
//   を返す
//
// 本モジュールは items 画面でのみ読み込まれる前提だが、念のため
// data-items-region が存在しないページでは何もしない。

(function () {
  'use strict';

  const DEBOUNCE_MS = 300;

  // For test environments we expose the module factory instead of binding to
  // window.document directly. In the browser, we self-execute against the
  // real document.
  function init(opts) {
    const doc = opts && opts.document ? opts.document : (typeof document !== 'undefined' ? document : null);
    const win = opts && opts.window ? opts.window : (typeof window !== 'undefined' ? window : null);
    if (!doc || !win) return null;

    const region = doc.querySelector('[data-items-region]');
    if (!region) return null;

    // 全ての検索入力欄（デスクトップ・モバイル上部・ボトムシート内）。
    const inputs = Array.from(doc.querySelectorAll('input[name="q"]'));
    if (inputs.length === 0) return null;

    const fetchImpl = (opts && opts.fetch) || win.fetch.bind(win);
    const setTimeoutImpl = (opts && opts.setTimeout) || win.setTimeout.bind(win);
    const clearTimeoutImpl = (opts && opts.clearTimeout) || win.clearTimeout.bind(win);
    const history = (opts && opts.history) || win.history;
    const location = (opts && opts.location) || win.location;

    // モジュール内部状態。タイマー id は最新のもの 1 つだけ保持し、再入力で
    // 上書き破棄する (NFR 1.2)。
    let timerID = null;
    let composing = false;
    let lastSyncedQuery = readURLQuery() || '';
    let inflight = null; // AbortController for the active fetch.

    function readURLQuery() {
      try {
        return new URL(location.href).searchParams.get('q') || '';
      } catch {
        return '';
      }
    }

    // 空白のみは未指定として扱う (R5 AC-2: 既存の検索処理規約に揃える)。
    function isEmpty(value) {
      return value == null || String(value).trim() === '';
    }

    // URL を作る: 現在の URL の q を新しい値で置き換え（または空なら削除）し、
    // 他のクエリ（sort / per_page / tag / page など）は触らない (R2 AC-3)。
    function buildSyncURL(value) {
      const u = new URL(location.href);
      if (isEmpty(value)) {
        u.searchParams.delete('q');
      } else {
        u.searchParams.set('q', String(value).trim());
      }
      return u;
    }

    function syncInputs(value) {
      // 全 q 入力欄に同じ値を反映する (R6 AC-1)。フォーカス中の入力欄は
      // ユーザの編集中の状態なので value は触らない（カーソル位置と
      // composition を壊さないため）。
      const active = doc.activeElement;
      for (const el of inputs) {
        if (el === active) continue;
        if (el.value !== value) el.value = value;
      }
    }

    async function refresh(value, options) {
      const mode = (options && options.mode) || 'replace';
      const url = buildSyncURL(value);

      // 履歴粒度は OQ-1 の方針に従い、debounce 時 replaceState、
      // Enter 確定時 pushState (R3 AC-3)。
      try {
        if (mode === 'push') {
          history.pushState({ q: value, source: 'items_search' }, '', url.toString());
        } else {
          history.replaceState({ q: value, source: 'items_search' }, '', url.toString());
        }
      } catch {
        // history が使えない環境（テスト・古いブラウザ）では URL 同期は
        // ベストエフォート。fetch まで進む。
      }

      // 直前の保留中リクエストを破棄して最新の入力値だけ反映する
      // (NFR 1.2)。
      if (inflight) {
        try { inflight.abort(); } catch { /* noop */ }
      }
      const ctrl = (typeof win.AbortController === 'function') ? new win.AbortController() : null;
      inflight = ctrl;

      lastSyncedQuery = isEmpty(value) ? '' : String(value).trim();

      // クエリ文字列だけ買い替えた fetch URL を作る。location 自体は
      // 既に history.* で更新済みだが、テスト環境ではこれを使う。
      const fetchURL = url.toString();
      try {
        const res = await fetchImpl(fetchURL, {
          method: 'GET',
          credentials: 'same-origin',
          headers: { 'X-Requested-With': 'ItemsFragment' },
          signal: ctrl ? ctrl.signal : undefined,
        });
        if (!res || !res.ok) return;
        const html = await res.text();
        // ちらつき防止のため、レスポンスが返ってから一気に差し替える
        // (NFR 3.2)。
        region.innerHTML = html;
      } catch {
        // ネットワーク失敗時は前回結果を維持する (NFR 3.2)。
      } finally {
        if (inflight === ctrl) inflight = null;
      }
    }

    function scheduleDebounced(value) {
      if (timerID != null) {
        clearTimeoutImpl(timerID);
        timerID = null;
      }
      timerID = setTimeoutImpl(() => {
        timerID = null;
        // composition 中は発火しない (OQ-4)。
        if (composing) return;
        // 同じ値で連続トリガーされた場合は冪等にスキップ。
        const trimmed = isEmpty(value) ? '' : String(value).trim();
        if (trimmed === lastSyncedQuery) {
          // URL 同期だけは一応行う（既に同期済みなので no-op）。
          return;
        }
        void refresh(value, { mode: 'replace' });
      }, DEBOUNCE_MS);
    }

    function commitImmediate(value) {
      // Enter 押下時: 保留中 debounce をキャンセルして即時実行 (R4 AC-2)。
      if (timerID != null) {
        clearTimeoutImpl(timerID);
        timerID = null;
      }
      void refresh(value, { mode: 'push' });
    }

    function attachInput(input) {
      input.addEventListener('compositionstart', () => {
        composing = true;
      });
      input.addEventListener('compositionend', () => {
        composing = false;
        // 変換確定の文字列で改めて debounce を仕込む (OQ-4)。
        syncInputs(input.value);
        scheduleDebounced(input.value);
      });
      input.addEventListener('input', () => {
        if (composing) return;
        syncInputs(input.value);
        scheduleDebounced(input.value);
      });
      input.addEventListener('keydown', (e) => {
        if (e.key !== 'Enter') return;
        // フォーム送信を抑止して即時 fetch + pushState に切り替える
        // (R4 AC-1, AC-3)。フォーム自体は残しているため JS 無効環境では
        // submit が従来通り動く (R6 AC-3)。
        e.preventDefault();
        if (composing) return; // IME 確定の Enter ではコミットしない
        syncInputs(input.value);
        commitImmediate(input.value);
      });
    }

    inputs.forEach(attachInput);

    // ブラウザ戻る/進むで URL が変わったら入力欄と結果一覧を URL に合わせる
    // (R3 AC-1, AC-2)。pushState/replaceState 自体は popstate を発火しない
    // ので、本ハンドラはユーザ操作（戻る/進む）のみで動作する。
    win.addEventListener('popstate', () => {
      if (timerID != null) {
        clearTimeoutImpl(timerID);
        timerID = null;
      }
      const q = readURLQuery();
      // 全入力欄を URL の値に揃える（フォーカス有無に関わらず、戻る/進むは
      // 明示的な状態復元なので上書きする）。
      for (const el of inputs) {
        if (el.value !== q) el.value = q;
      }
      lastSyncedQuery = q;
      // popstate ではフラグメントのみ更新（URL は既に変わっている）。
      // 既に同期済みの値であっても、ユーザーが戻る/進む操作で表示を期待
      // しているため明示的に refresh を行う。
      void refreshFromCurrentURL();
    });

    async function refreshFromCurrentURL() {
      if (inflight) {
        try { inflight.abort(); } catch { /* noop */ }
      }
      const ctrl = (typeof win.AbortController === 'function') ? new win.AbortController() : null;
      inflight = ctrl;
      try {
        const res = await fetchImpl(location.href, {
          method: 'GET',
          credentials: 'same-origin',
          headers: { 'X-Requested-With': 'ItemsFragment' },
          signal: ctrl ? ctrl.signal : undefined,
        });
        if (!res || !res.ok) return;
        const html = await res.text();
        region.innerHTML = html;
      } catch {
        /* noop */
      } finally {
        if (inflight === ctrl) inflight = null;
      }
    }

    return {
      // テスト用に現在の状態を覗くフック。本番コードは触らない。
      _debug: {
        getLastSyncedQuery: () => lastSyncedQuery,
        isComposing: () => composing,
        hasPendingTimer: () => timerID != null,
      },
    };
  }

  // 自動初期化（ブラウザ実行時およびテスト時 vm.context 上）。
  if (typeof document !== 'undefined' && typeof window !== 'undefined') {
    init();
  }
})();
