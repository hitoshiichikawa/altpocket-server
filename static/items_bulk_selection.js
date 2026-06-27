// items_bulk_selection.js
//
// /ui/items の一括選択モジュール (Issue #118 task 6)。
//
// 責務:
//   - 一覧上の選択状態（内部 Set<itemID>）を管理する
//   - チェックボックス change / カード click（shift modifier 検出）/ ドキュメント
//     keydown `x` を delegated に捕捉して toggle / 範囲選択を発動する
//   - 選択件数 0 ⇄ 1+ の遷移ごとに `[data-items-region]` 上で
//     `bulkselection:changed` custom event を発火する（detail: {count, ids}）
//   - 上限 100 件超の選択操作を抑止し、`window.altpocketToast.error(...)` で
//     通知する（NFR 2.2）
//   - SSR で `disabled` 付き出力された `input.item-select` を init / fragment
//     差替後に enable する（NFR 3.5 Progressive Enhancement）
//   - fragment 差替 (状態タブ切替・タグフィルタチップ・検索クエリ・ソート・
//     ページ送り・popstate) を `[data-items-region]` 上の MutationObserver +
//     popstate ハンドラで検知して Set をリセットする (Req 7.1 / 7.2 / 7.3 /
//     7.4 / 7.5)
//   - actions モジュール (`items_bulk_actions.js`) からの per-item DOM 削除を
//     `beginActionMutation()` / `endActionMutation()` ブラケットで保護し、
//     部分失敗時の failed 選択を保持する (Req 4.8 / 5.8)
//
// 設計上の重要な不変:
//   - DOM 上の `.item-select[checked]` と内部 Set は常に同期する
//   - fragment 差替 / popstate / `clear()` 呼出時には `lastClickedID` も `null`
//     にリセットする（stale anchor を起点とする Shift+click 範囲選択を防止）
//
// inter-module API:
//   - selection モジュールは `init()` 末尾で同オブジェクトを
//     `window.altpocketBulkSelection` にも代入する。actions モジュールはこの
//     global を参照することで script 読み込み順への依存を排除できる
//     （既存 `window.altpocketToast` と同じ流儀）
//
// JS 無効環境では本ファイルが評価されないため、SSR の `disabled` 付き
// checkbox がそのまま Tab フォーカスからも click からも除外される
// （HTML 仕様の disabled 挙動 / NFR 3.5）。

(function () {
  'use strict';

  // 一括操作の上限件数（NFR 2.1 / 2.2）。
  const MAX_SELECTION = 100;

  // 上限超過時の toast 文言。
  const TOAST_SINGLE_OVER_LIMIT = '一括操作は最大 100 件までです';
  const TOAST_RANGE_OVER_LIMIT = '範囲選択により上限を超えるため処理されませんでした';

  // input.type が文字入力扱いとなる種別。これら以外（checkbox / radio /
  // button / submit / reset / image / hidden）の input は `x` キーボード
  // 操作の guard を通過させる（Req 6.1 / 6.3）。
  const TEXT_INPUT_TYPES = new Set([
    'text', 'search', 'email', 'url', 'tel', 'password',
    'number', 'date', 'time', 'datetime-local', 'month',
    'week', 'color', 'file',
  ]);

  function init(opts) {
    const o = opts || {};
    const doc = o.document || (typeof document !== 'undefined' ? document : null);
    const win = o.window || (typeof window !== 'undefined' ? window : null);
    if (!doc || !win) return null;

    const region = doc.querySelector ? doc.querySelector('[data-items-region]') : null;
    if (!region) return null;

    // 内部状態: 選択中の itemID 集合と Shift+click 範囲選択の anchor。
    const selectionSet = new Set();
    let lastClickedID = null;

    // 部分失敗時の per-item article.remove() 中は MutationObserver に
    // よる reset を抑止する reference counter。actions 側が
    // beginActionMutation() / endActionMutation() で囲む。
    let actionMutationDepth = 0;

    // SSR で `disabled` 付き出力された `input.item-select` を enable する
    // 共通 helper。init 直後と fragment 差替後の reset callback の双方から
    // 呼び出す（NFR 3.5 Progressive Enhancement 規約）。
    function enableSelectionCheckboxes(root) {
      if (!root || !root.querySelectorAll) return;
      const disabled = root.querySelectorAll('input.item-select[disabled]');
      for (let i = 0; i < disabled.length; i += 1) {
        const el = disabled[i];
        if (el.removeAttribute) el.removeAttribute('disabled');
        // FakeDOM や一部ブラウザ向けに property も明示的に false にする
        if ('disabled' in el) {
          try { el.disabled = false; } catch { /* noop */ }
        }
      }
    }

    // `bulkselection:changed` event を `[data-items-region]` 上で発火する。
    // actions モジュール / ツールバーがこの event を listen して件数表示と
    // hidden 切替を行う（Req 3.1 / 3.2 / 3.6）。
    function dispatchChanged() {
      const ids = Array.from(selectionSet);
      let event;
      if (typeof CustomEvent === 'function') {
        event = new CustomEvent('bulkselection:changed', {
          detail: { count: ids.length, ids: ids },
        });
      } else if (win && typeof win.CustomEvent === 'function') {
        event = new win.CustomEvent('bulkselection:changed', {
          detail: { count: ids.length, ids: ids },
        });
      } else {
        // 環境に CustomEvent が無い場合は最小限の event-like オブジェクトで
        // 代替する（テスト用 fake DOM 経路）。
        event = { type: 'bulkselection:changed', detail: { count: ids.length, ids: ids } };
      }
      if (region.dispatchEvent) region.dispatchEvent(event);
    }

    // 内部 helper: id が region 配下にある article 要素を返す（無ければ null）。
    function findCard(id) {
      if (id == null) return null;
      // 属性値に空白等が含まれない前提（SSR 側で uuid を出力するため安全）。
      const sel = 'article[data-item-id="' + String(id) + '"]';
      return region.querySelector ? region.querySelector(sel) : null;
    }

    // 単一カードの選択状態を id 集合に追加する。`.is-selected` / checkbox
    // checked / Set の 3 系列を同期する。
    function addToSet(id, card) {
      selectionSet.add(id);
      if (card) {
        if (card.classList) card.classList.add('is-selected');
        const cb = card.querySelector ? card.querySelector('input.item-select') : null;
        if (cb && cb.checked === false) cb.checked = true;
      }
    }

    function removeFromSet(id, card) {
      selectionSet.delete(id);
      if (card) {
        if (card.classList) card.classList.remove('is-selected');
        const cb = card.querySelector ? card.querySelector('input.item-select') : null;
        if (cb && cb.checked === true) cb.checked = false;
      }
    }

    // toast.error を安全に呼ぶ helper。altpocketToast が無い環境（テスト fixture
    // など）でも throw しない。
    function toastError(msg) {
      const t = win && win.altpocketToast;
      if (t && typeof t.error === 'function') {
        try { t.error(msg); } catch { /* noop */ }
      }
    }

    // 上限 100 件超を弾く: 単一 toggle 経路から呼ばれる。toggle 試行が許容
    // されるなら true、抑止する場合は false を返す（同時に toast.error）。
    function ensureCanAddOne() {
      if (selectionSet.size < MAX_SELECTION) return true;
      toastError(TOAST_SINGLE_OVER_LIMIT);
      return false;
    }

    // 範囲選択経路: 既に Set にある id を除いた追加分（newIDs）が「合算 > 100」に
    // ならないかを判定する。範囲全体を all-or-nothing で扱うため、はみ出るなら
    // 全件 reject + toast.error し false を返す。
    function ensureCanAddRange(newIDs) {
      let added = 0;
      for (const id of newIDs) {
        if (!selectionSet.has(id)) added += 1;
      }
      if (selectionSet.size + added > MAX_SELECTION) {
        toastError(TOAST_RANGE_OVER_LIMIT);
        return false;
      }
      return true;
    }

    // change ハンドラ (delegated)。`input.item-select` の change で
    // toggle 処理を行う（Req 1.1 / 1.2 / 1.3 / 1.4 / 3.6）。
    function onChange(e) {
      const t = e.target;
      if (!t || !t.matches || !t.matches('input.item-select')) return;
      const card = t.closest ? t.closest('.item-card') : null;
      const id = card && card.dataset ? card.dataset.itemId : null;
      if (!id) return;

      const willCheck = !!t.checked;
      if (willCheck) {
        if (selectionSet.has(id)) {
          // 既に選択済み（範囲選択経路の programmatic check 等）→ no-op
          return;
        }
        if (!ensureCanAddOne()) {
          // 上限到達 → 取り消す
          t.checked = false;
          return;
        }
        addToSet(id, card);
      } else {
        if (!selectionSet.has(id)) return;
        removeFromSet(id, card);
      }
      dispatchChanged();
    }

    // 範囲算出: region 配下の `.item-card` の DOM 順で、anchorID と currentID の
    // 間（両端含む）の itemID リストを返す。anchor / current のいずれかが見つから
    // ない場合は空配列を返す。
    function computeRange(anchorID, currentID) {
      if (!region.querySelectorAll) return [];
      const cards = region.querySelectorAll('.item-card');
      let aIdx = -1;
      let cIdx = -1;
      for (let i = 0; i < cards.length; i += 1) {
        const cid = cards[i].dataset ? cards[i].dataset.itemId : null;
        if (cid === anchorID) aIdx = i;
        if (cid === currentID) cIdx = i;
      }
      if (aIdx === -1 || cIdx === -1) return [];
      const lo = Math.min(aIdx, cIdx);
      const hi = Math.max(aIdx, cIdx);
      const out = [];
      for (let i = lo; i <= hi; i += 1) {
        const cid = cards[i].dataset ? cards[i].dataset.itemId : null;
        if (cid) out.push(cid);
      }
      return out;
    }

    // click ハンドラ (delegated)。shift+click で範囲選択を発動する。
    // 通常 click は change ハンドラに委ねる（preventDefault しない）。
    function onClick(e) {
      const t = e.target;
      if (!t || !t.matches || !t.matches('input.item-select')) return;
      const card = t.closest ? t.closest('.item-card') : null;
      const id = card && card.dataset ? card.dataset.itemId : null;
      if (!id) return;

      // Shift+click で範囲選択を発動する 3 条件:
      //  (a) selectionSet.size > 0
      //  (b) lastClickedID !== null
      //  (c) anchor article が region 配下に存在
      // のすべてを満たす場合のみ範囲選択。1 つでも欠ければ通常 toggle に降格。
      const shiftRange =
        e.shiftKey &&
        selectionSet.size > 0 &&
        lastClickedID !== null &&
        findCard(lastClickedID) !== null;

      if (shiftRange) {
        // ネイティブの checkbox toggle を抑止する。既選択端を Shift+click した
        // 場合、ブラウザがその checkbox を unchecked にしてしまうのを防ぐ。
        if (typeof e.preventDefault === 'function') e.preventDefault();

        const rangeIDs = computeRange(lastClickedID, id);
        if (rangeIDs.length === 0) {
          // 範囲算出に失敗（DOM 順序解決できない）→ fallback として lastClickedID
          // を更新するだけで処理を終える。
          lastClickedID = id;
          return;
        }
        if (!ensureCanAddRange(rangeIDs)) {
          // 上限超過 → 範囲全体を reject（既存選択も触らない）
          // lastClickedID は更新しない（次の Shift+click の anchor を維持）
          return;
        }
        let added = false;
        for (const rid of rangeIDs) {
          if (selectionSet.has(rid)) continue;
          const c = findCard(rid);
          addToSet(rid, c);
          added = true;
        }
        lastClickedID = id;
        if (added) dispatchChanged();
        return;
      }

      // 通常 click / fallback shift+click → lastClickedID を更新するだけ。
      // 実際の toggle は change ハンドラ経路で処理される。
      lastClickedID = id;
    }

    // keydown ハンドラ。Req 6.1 〜 6.3 のガードを適用する。
    function onKeydown(e) {
      // (1) modifier present → return（既存 app.js 規約と整合 / Req 6.2）
      if (e.ctrlKey || e.altKey || e.metaKey) return;

      const t = e.target;
      if (t) {
        const tag = t.tagName;
        // (2) TEXTAREA / SELECT / contenteditable → return（Req 6.3）
        if (tag === 'TEXTAREA' || tag === 'SELECT' || t.isContentEditable === true) {
          return;
        }
        // (3) INPUT の場合は type で分岐
        if (tag === 'INPUT') {
          // item-select / checkbox / radio / button / submit は通過させる
          const isItemSelect = t.matches && t.matches('input.item-select');
          const inputType = (t.type || '').toLowerCase();
          if (!isItemSelect) {
            if (TEXT_INPUT_TYPES.has(inputType)) return;
            // 'checkbox' / 'radio' / 'button' / 'submit' / 'reset' / 'image' /
            // 'hidden' / '' などは通過
          }
        }
      }

      if (e.key !== 'x') return;

      // activeElement を起点にカードを解決する。Tab で checkbox にフォーカス
      // がある場合でも closest('.item-card') で対応する <article> に到達する。
      const active = doc.activeElement;
      if (!active || !active.closest) return;
      const card = active.closest('.item-card');
      if (!card || !card.dataset) return;
      const id = card.dataset.itemId;
      if (!id) return;

      // toggle: 既選択なら解除、未選択なら追加（上限ガード）。
      if (selectionSet.has(id)) {
        removeFromSet(id, card);
        dispatchChanged();
      } else {
        if (!ensureCanAddOne()) return;
        addToSet(id, card);
        dispatchChanged();
      }
    }

    // 共通 reset 処理 (fragment 差替 / popstate / clear() で利用)。
    function resetSelectionState() {
      const wasNonEmpty = selectionSet.size > 0;
      selectionSet.clear();
      lastClickedID = null;
      if (wasNonEmpty) dispatchChanged();
      else dispatchChanged(); // 連続 reset でも count=0 イベントを 1 回出して
                              // 件数表示の整合を保つ（テストは最後の event を読む）
    }

    // 単一 MutationRecord を per-record で判定して reset / discard を分ける。
    // 戻り値:
    //   - 'fragment-swap': reset を発火した
    //   - 'per-item-suppressed': bracket 中の per-item 削除を抑止した
    //   - 'per-item-reset': bracket 外の per-item 削除で保守的 reset を発火した
    //   - null: 無視
    function classifyAndApply(record) {
      if (!record) return null;
      const added = record.addedNodes;
      const addedLen = added ? (added.length || 0) : 0;
      if (addedLen > 0) {
        // fragment 差替 → bracket 状態に関係なく reset
        selectionSet.clear();
        lastClickedID = null;
        dispatchChanged();
        enableSelectionCheckboxes(region);
        return 'fragment-swap';
      }
      // addedNodes.length === 0 → per-item 削除
      if (actionMutationDepth > 0) {
        return 'per-item-suppressed';
      }
      // 保守的 reset
      selectionSet.clear();
      lastClickedID = null;
      dispatchChanged();
      return 'per-item-reset';
    }

    // MutationObserver callback: 受信した records を per-record 判定する。
    function onMutations(records) {
      for (let i = 0; i < records.length; i += 1) {
        classifyAndApply(records[i]);
      }
    }

    // MutationObserver を起動。fragment 差替・per-item 削除を観測する。
    const ObserverCtor =
      (typeof MutationObserver === 'function') ? MutationObserver :
      ((win && typeof win.MutationObserver === 'function') ? win.MutationObserver : null);
    let observer = null;
    if (ObserverCtor) {
      observer = new ObserverCtor(onMutations);
      try {
        observer.observe(region, { childList: true });
      } catch { /* noop */ }
    }

    // popstate ハンドラ (Req 7.3 / 7.4)。新 pageload では Set が空から始まる
    // ため、popstate でも明示的にリセットする。
    function onPopState() {
      selectionSet.clear();
      lastClickedID = null;
      dispatchChanged();
    }
    if (win.addEventListener) win.addEventListener('popstate', onPopState);

    // 公開 API.
    function getSelectedIDs() {
      return Array.from(selectionSet);
    }

    function clear() {
      const wasEmpty = selectionSet.size === 0;
      // 全 checkbox / .is-selected を解除
      const cards = region.querySelectorAll ? region.querySelectorAll('.item-card') : [];
      for (let i = 0; i < cards.length; i += 1) {
        const card = cards[i];
        if (card.classList) card.classList.remove('is-selected');
        const cb = card.querySelector ? card.querySelector('input.item-select') : null;
        if (cb && cb.checked === true) cb.checked = false;
      }
      selectionSet.clear();
      lastClickedID = null;
      // count=0 を必ず 1 回発火する（既に空でも refresh のため発火）
      dispatchChanged();
      // 未使用変数 warning 回避（明示的に noop）
      void wasEmpty;
    }

    function removeFromSelection(ids) {
      if (!ids || !ids.length) return;
      let mutated = false;
      for (const id of ids) {
        if (selectionSet.has(id)) {
          selectionSet.delete(id);
          mutated = true;
        }
        // anchor stale 防止: 削除対象が lastClickedID と一致する場合は null に倒す
        if (lastClickedID === id) lastClickedID = null;
      }
      if (mutated) dispatchChanged();
    }

    function beginActionMutation() {
      actionMutationDepth += 1;
    }

    function endActionMutation() {
      // bracket 中に貯まった records を取り出して per-record 判定で処理する。
      // - fragment 差替 record (addedNodes.length > 0) は bracket 状態に関係なく
      //   reset を発火する
      // - per-item 削除 record (addedNodes.length === 0) は actionMutationDepth > 0
      //   の間 discard する
      if (observer && typeof observer.takeRecords === 'function') {
        const queued = observer.takeRecords();
        for (let i = 0; i < queued.length; i += 1) {
          classifyAndApply(queued[i]);
        }
      }
      if (actionMutationDepth > 0) actionMutationDepth -= 1;
    }

    // Progressive Enhancement: SSR で `disabled` 付き出力された checkbox を
    // 操作可能にする（NFR 3.5）。change / click / keydown ハンドラを register
    // する前に実行する。
    enableSelectionCheckboxes(region);

    // delegated event handler を document に登録。
    // change / click はバブリングで document まで到達するため、`[data-items-region]`
    // 配下の対象のみハンドラ側で `target.closest('[data-items-region]')` で絞り込む。
    // 既存 `items_status.js` / `items_active_filters.js` と同じ delegated 規約。
    function isInRegion(t) {
      if (!t) return false;
      if (!t.closest) return false;
      return t.closest('[data-items-region]') === region;
    }
    function onDocChange(e) {
      if (!isInRegion(e.target)) return;
      onChange(e);
    }
    function onDocClick(e) {
      if (!isInRegion(e.target)) return;
      onClick(e);
    }
    if (doc.addEventListener) {
      doc.addEventListener('change', onDocChange);
      doc.addEventListener('click', onDocClick);
      doc.addEventListener('keydown', onKeydown);
    }

    const api = {
      getSelectedIDs,
      clear,
      removeFromSelection,
      beginActionMutation,
      endActionMutation,
    };

    // window.altpocketBulkSelection に公開する（actions モジュール / テストが参照）
    if (win) win.altpocketBulkSelection = api;

    return api;
  }

  if (typeof document !== 'undefined' && typeof window !== 'undefined') {
    init();
  }
})();
