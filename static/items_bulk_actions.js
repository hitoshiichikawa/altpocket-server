// items_bulk_actions.js
//
// /ui/items の一括操作 actions モジュール (Issue #118 task 7)。
//
// 責務:
//   - `[data-bulk-toolbar]` の表示 / 非表示 / 件数テキストを
//     `bulkselection:changed` event で同期する (Req 3.1 / 3.2 / 3.6)
//   - ツールバー delegated click を捕捉:
//     - `button.bulk-delete` → 確認ダイアログ → POST /v1/items/bulk-delete
//     - `button.bulk-tag`    → `<dialog data-bulk-tag-dialog>` open → POST /v1/items/bulk-tag
//     - `button.bulk-clear`  → selection.clear()
//   - 部分失敗 / 全件失敗の通知ダイアログ `[data-bulk-failure-dialog]` を populate
//   - 一括削除成功時は対象 article を fade-out + 同期 remove (begin/endActionMutation で
//     保護) し、 selection から `removeFromSelection(snapshot)` する (Req 4.5 / 4.8)
//   - 一括タグ付け成功時は succeeded[].tags を chip 列に再構築する (Req 5.5 / NFR 3.2 / 3.3)
//
// 設計上の不変:
//   - **リクエスト ID スナップショット規約** (round 6 review feedback): click ハンドラ冒頭で
//     `Array.from(selection.getSelectedIDs())` を snapshot し、以降のレスポンス処理 /
//     dialog 表示 / DOM 収集 / removeFromSelection で **live selection は参照しない**。
//     fetch 中にユーザーが新規選択 / 解除した変更が反映されない条件下で結果反映を一意化する。
//   - **selection.removeFromSelection(snapshot)** を全成功 / 部分失敗の選択解除で使い、
//     `selection.clear()` は呼ばない。これにより fetch 中の新規選択 C が誤って消えるのを防ぐ。
//   - **busy 状態**: ツールバーに `is-busy` class + 全 button を `disabled` 属性で setAttribute。
//     CSS の pointer-events だけではキーボード起動を止められない (NFR 1.2)。
//   - **chip rebuild は SSR contract と完全一致**: `button.tag.tag-filter-toggle` +
//     `data-tag-filter-toggle` + `data-tag-normalized` + `aria-label` + `textContent` +
//     `is-selected` class + `aria-pressed` で組み立て、innerHTML を使わない (NFR 5.1)。
//   - **chip 反映時の active filter 判定**: URL の `?tag=X` (canonical) と `?tags=a,b` (legacy)
//     の両形式を Set にマージし、`tag.normalized_name` が含まれるかで is-selected 判定する。
//     これにより SSR 側 `parseTagFilters` の両形式受理規約を JS 側でミラーする (NFR 3.3)。

(function () {
  'use strict';

  // タグの空判定で使う共通の fallback normalize（window.altpocketNormalizeTagName
  // が無い fallback 用途）。NFKC + lowercase + trim の sequence は app.js / server 側
  // tag.Normalize と一致する。
  function fallbackNormalize(value) {
    const trimmed = (value || '').trim();
    if (!trimmed) return '';
    if (typeof trimmed.normalize === 'function') {
      return trimmed.normalize('NFKC').toLowerCase();
    }
    return trimmed.toLowerCase();
  }

  function init(opts) {
    const o = opts || {};
    const doc = o.document || (typeof document !== 'undefined' ? document : null);
    const win = o.window || (typeof window !== 'undefined' ? window : null);
    if (!doc || !win) return null;

    const fetchImpl = o.fetch || (typeof win.fetch === 'function' ? win.fetch.bind(win) : null);
    if (!fetchImpl) return null;

    const region = doc.querySelector ? doc.querySelector('[data-items-region]') : null;
    if (!region) return null;

    // toast 解決順序:
    //   1. opts.toast（テストから注入された stub）
    //   2. win.altpocketToast（app.js が公開する本番 UI）
    //   3. win.alert への fallback（toast UI 未ロード時の最終防波堤）
    const toast = o.toast || (function () {
      const resolve = () => (win && win.altpocketToast) || null;
      return {
        error(msg) {
          const t = resolve();
          if (t && typeof t.error === 'function') { t.error(msg); return; }
          if (typeof win.alert === 'function') win.alert(msg);
        },
        success(msg) {
          const t = resolve();
          if (t && typeof t.success === 'function') t.success(msg);
        },
        info(msg) {
          const t = resolve();
          if (t && typeof t.info === 'function') t.info(msg);
        },
      };
    })();

    // setTimeout（fadeOutAndRemove 用）。テストから fake を注入できるよう opts でも受け取る。
    const setTimeoutImpl = o.setTimeout
      || (typeof win.setTimeout === 'function' ? win.setTimeout.bind(win) : null);

    // CSRF token を meta タグから取得（app.js / items_status_actions.js と同じパターン）。
    function getCSRFHeaders() {
      const meta = doc.querySelector ? doc.querySelector('meta[name="csrf-token"]') : null;
      const csrf = meta ? (meta.getAttribute ? meta.getAttribute('content') : meta.content) : null;
      const h = { 'Content-Type': 'application/json' };
      if (csrf) h['X-CSRF-Token'] = csrf;
      return h;
    }

    // selection モジュール API を取得する helper。bulkselection:changed の都度
    // 取得することで、selection モジュールが actions より遅れて load されても OK。
    function getSelection() {
      return (win && win.altpocketBulkSelection) || null;
    }

    // ツールバー / dialog の DOM 参照（init 時に解決）。SSR で必ず存在する前提。
    const toolbar = doc.querySelector ? doc.querySelector('[data-bulk-toolbar]') : null;
    const countEl = toolbar && toolbar.querySelector ? toolbar.querySelector('[data-bulk-count]') : null;

    const tagDialog = doc.querySelector ? doc.querySelector('[data-bulk-tag-dialog]') : null;
    const tagForm = tagDialog && tagDialog.querySelector ? tagDialog.querySelector('[data-bulk-tag-form]') : null;
    const tagInput = tagDialog && tagDialog.querySelector ? tagDialog.querySelector('[data-bulk-tag-input]') : null;
    const tagCancel = tagDialog && tagDialog.querySelector ? tagDialog.querySelector('[data-bulk-tag-cancel]') : null;
    const tagConfirm = tagDialog && tagDialog.querySelector ? tagDialog.querySelector('[data-bulk-tag-confirm]') : null;

    const failureDialog = doc.querySelector ? doc.querySelector('[data-bulk-failure-dialog]') : null;
    const failureTitle = failureDialog && failureDialog.querySelector ? failureDialog.querySelector('[data-bulk-failure-title]') : null;
    const failureList = failureDialog && failureDialog.querySelector ? failureDialog.querySelector('[data-bulk-failure-list]') : null;
    const failureClose = failureDialog && failureDialog.querySelector ? failureDialog.querySelector('[data-bulk-failure-close]') : null;

    // --- ヘルパー: ツールバー件数 / busy / disabled 状態 -------------------

    function setCountText(n) {
      if (countEl && 'textContent' in countEl) countEl.textContent = String(n);
    }

    function showToolbar(count) {
      if (!toolbar) return;
      if (typeof toolbar.removeAttribute === 'function') toolbar.removeAttribute('hidden');
      if ('hidden' in toolbar) {
        try { toolbar.hidden = false; } catch { /* noop */ }
      }
      setCountText(count);
    }

    function hideToolbar() {
      if (!toolbar) return;
      if (typeof toolbar.setAttribute === 'function') toolbar.setAttribute('hidden', '');
      if ('hidden' in toolbar) {
        try { toolbar.hidden = true; } catch { /* noop */ }
      }
      setCountText(0);
    }

    // ツールバー / dialog 内のボタンを busy 状態にする。`is-busy` class と全 button
    // に disabled 属性を付与する。pointer-events のみだとキーボード起動を止められない
    // ため、button の `disabled` 属性で二重送信を確実に抑止する (NFR 1.2)。
    function busyButtons() {
      const out = [];
      if (toolbar && toolbar.querySelectorAll) {
        const a = toolbar.querySelectorAll('button.bulk-delete, button.bulk-tag, button.bulk-clear');
        for (let i = 0; i < a.length; i += 1) out.push(a[i]);
      }
      if (tagDialog && tagDialog.querySelectorAll) {
        const b = tagDialog.querySelectorAll('button[data-bulk-tag-cancel], button[data-bulk-tag-confirm]');
        for (let i = 0; i < b.length; i += 1) out.push(b[i]);
      }
      return out;
    }

    function setBusy(busy) {
      if (toolbar && toolbar.classList) {
        if (busy) toolbar.classList.add('is-busy');
        else toolbar.classList.remove('is-busy');
      }
      const btns = busyButtons();
      for (let i = 0; i < btns.length; i += 1) {
        const b = btns[i];
        if (!b) continue;
        if (busy) {
          if (typeof b.setAttribute === 'function') b.setAttribute('disabled', '');
          if ('disabled' in b) {
            try { b.disabled = true; } catch { /* noop */ }
          }
        } else {
          if (typeof b.removeAttribute === 'function') b.removeAttribute('disabled');
          if ('disabled' in b) {
            try { b.disabled = false; } catch { /* noop */ }
          }
        }
      }
    }

    // --- ヘルパー: article 走査と失敗詳細収集 -----------------------------

    function findCardByID(id) {
      if (id == null) return null;
      const sel = 'article[data-item-id="' + String(id) + '"]';
      return region.querySelector ? region.querySelector(sel) : null;
    }

    // article から「失敗 dialog の `<li>` に出すための {id, title, url}」を抽出する。
    // querySelector が null なら `{id, title: null, url: null}` を返す（後段で id-only
    // fallback として 1 行は提示される / snapshot 規約 5）。
    function collectFailureItem(id) {
      const card = findCardByID(id);
      if (!card) return { id: id, title: null, url: null };
      const titleEl = card.querySelector ? card.querySelector('h3[id^="item-title-"]') : null;
      const title = titleEl && 'textContent' in titleEl ? (titleEl.textContent || '').trim() : '';
      // URL は `<article data-original-url>` から取得する（`.tile-link[href]` は内部詳細
      // ページ URL なので元記事 URL fallback には使えない）。
      let url = '';
      if (card.dataset && card.dataset.originalUrl) url = card.dataset.originalUrl;
      else if (card.getAttribute) url = card.getAttribute('data-original-url') || '';
      return { id: id, title: title || null, url: url || null };
    }

    // --- ヘルパー: 失敗 dialog の populate + showModal -----------------

    function showBulkFailureDialog(args) {
      const verb = args && args.verb ? String(args.verb) : '';
      const items = (args && args.items) ? args.items : [];
      // タイトル更新
      if (failureTitle && 'textContent' in failureTitle) {
        failureTitle.textContent = items.length + ' 件の' + verb + 'に失敗しました';
      }
      // <li> populate（既存子要素を全消去 → textContent で順次 append。XSS 防御）
      if (failureList) {
        if (typeof failureList.replaceChildren === 'function') {
          failureList.replaceChildren();
        } else {
          // fallback: remove all children manually
          while (failureList.firstChild) failureList.removeChild(failureList.firstChild);
        }
        for (let i = 0; i < items.length; i += 1) {
          const it = items[i] || {};
          const li = doc.createElement ? doc.createElement('li') : null;
          if (!li) continue;
          let text = '';
          if (it.title) text = it.title;
          else if (it.url) text = it.url;
          else text = it.id || '';
          // textContent のみを使う（innerHTML / insertAdjacentHTML は禁止 / XSS 防御）。
          if ('textContent' in li) li.textContent = text;
          if (typeof failureList.appendChild === 'function') failureList.appendChild(li);
        }
      }
      // dialog を showModal で開く（fallback として open 属性 / show() も試す）。
      if (failureDialog) {
        if (typeof failureDialog.showModal === 'function') {
          try { failureDialog.showModal(); } catch { /* noop */ }
        } else if (typeof failureDialog.setAttribute === 'function') {
          failureDialog.setAttribute('open', '');
        }
      }
      // トースト併用（件数の reminder）。
      toast.error(items.length + ' 件の' + verb + 'に失敗しました（詳細を開く）');
    }

    // --- ヘルパー: confirm dialog を呼ぶ (object 形式 + fallback) ---------

    // tasks.md 規約: `window.altpocketConfirm.show(title, description, onConfirm,
    // actionLabel, actionClass)` の **object メソッド呼び出し**。fallback として
    // `window.confirm(message)` 標準に降格。`window.altpocketConfirm` が関数として
    // 呼ばれることはない（過去レビューで指摘されたシグネチャ規約の固定）。
    function showConfirm(title, description, onConfirm, actionLabel, actionClass) {
      const c = win && win.altpocketConfirm;
      if (c && typeof c.show === 'function') {
        try {
          c.show(title, description, onConfirm, actionLabel || 'Delete', actionClass || 'btn-danger');
          return;
        } catch { /* fall through to native */ }
      }
      // fallback: ブラウザ標準 confirm。OK なら onConfirm を発火、Cancel なら no-op。
      if (typeof win.confirm === 'function') {
        if (win.confirm(description || title)) {
          if (onConfirm) onConfirm();
        }
      }
    }

    // --- ヘルパー: active タグフィルタ Set を URL から算出 -----------------

    // canonical `?tag=` repetition + legacy `?tags=csv` の両形式を見て、active な
    // タグの normalized name の Set を返す。chip rebuild 時に1回だけ算出する。
    function computeActiveNormalizedNames() {
      const set = new Set();
      const href = (win && win.location && win.location.href) ? win.location.href : '';
      let params;
      try {
        params = new URL(href).searchParams;
      } catch {
        params = null;
      }
      if (!params) return set;
      const tagAll = (typeof params.getAll === 'function') ? params.getAll('tag') : [];
      const tagsCSV = (typeof params.get === 'function') ? (params.get('tags') || '') : '';
      const csvParts = tagsCSV ? tagsCSV.split(',') : [];
      const raw = tagAll.concat(csvParts);
      const norm = (win && win.altpocketNormalizeTagName) || fallbackNormalize;
      for (let i = 0; i < raw.length; i += 1) {
        const n = norm(raw[i]);
        if (n) set.add(n);
      }
      return set;
    }

    // --- ヘルパー: chip 列の再構築 ----------------------------------------

    // succeeded item の `.tags` chip 列を tag 配列で全置換する。SSR contract
    // （items_list.html line 65-78）と完全一致させる。
    function rebuildChipsForCard(card, tags, activeNormalizedNames) {
      if (!card) return;
      let tagsContainer = card.querySelector ? card.querySelector('.tags') : null;
      if (!tagsContainer) {
        // 既存 chip 列が無い card → `<div class="tags">` を新規作成して append する
        if (!doc.createElement) return;
        tagsContainer = doc.createElement('div');
        if (typeof tagsContainer.setAttribute === 'function') {
          tagsContainer.setAttribute('class', 'tags');
        } else if ('className' in tagsContainer) {
          tagsContainer.className = 'tags';
        }
        if (typeof card.appendChild === 'function') card.appendChild(tagsContainer);
      }
      const newBtns = [];
      for (let i = 0; i < tags.length; i += 1) {
        const t = tags[i] || {};
        if (!doc.createElement) continue;
        const btn = doc.createElement('button');
        const isActive = activeNormalizedNames.has(t.normalized_name);
        if (typeof btn.setAttribute === 'function') {
          btn.setAttribute('type', 'button');
          btn.setAttribute('class', isActive
            ? 'tag tag-filter-toggle is-selected'
            : 'tag tag-filter-toggle');
          btn.setAttribute('data-tag-filter-toggle', '');
          btn.setAttribute('data-tag-normalized', String(t.normalized_name || ''));
          btn.setAttribute('aria-label', 'タグで絞り込み: ' + (t.name || ''));
          btn.setAttribute('aria-pressed', isActive ? 'true' : 'false');
        }
        if ('textContent' in btn) btn.textContent = (t.name || '');
        newBtns.push(btn);
      }
      if (typeof tagsContainer.replaceChildren === 'function') {
        tagsContainer.replaceChildren.apply(tagsContainer, newBtns);
      } else {
        while (tagsContainer.firstChild) tagsContainer.removeChild(tagsContainer.firstChild);
        for (let i = 0; i < newBtns.length; i += 1) tagsContainer.appendChild(newBtns[i]);
      }
    }

    // --- ヘルパー: fade-out + remove (beginActionMutation ブラケット) ----

    // 削除対象 article を fade-out し、setTimeout 内で remove と endActionMutation を
    // 同時に発火する。begin は呼び出し側で先に呼んでおく前提（方式 A / ブラケット規約）。
    function fadeOutAndRemove(article, selection) {
      if (!article) {
        // article が null の場合でも、すでに begin されている分は end する必要がある。
        if (selection && typeof selection.endActionMutation === 'function') {
          selection.endActionMutation();
        }
        return;
      }
      if (article.classList && typeof article.classList.add === 'function') {
        article.classList.add('fade-out');
      }
      const doRemove = () => {
        try {
          if (typeof article.remove === 'function') article.remove();
          else if (article.parentNode && typeof article.parentNode.removeChild === 'function') {
            article.parentNode.removeChild(article);
          }
        } finally {
          // 同 microtask 内で end を続けて発火（reference counted bracket カウンタ）
          if (selection && typeof selection.endActionMutation === 'function') {
            selection.endActionMutation();
          }
        }
      };
      if (setTimeoutImpl) {
        setTimeoutImpl(doRemove, 300);
      } else {
        doRemove();
      }
    }

    // --- API call: bulk-delete -------------------------------------------

    async function performBulkDelete(requestIds) {
      const selection = getSelection();
      // busy 状態の確立は同期的に
      setBusy(true);

      let res;
      try {
        res = await fetchImpl('/v1/items/bulk-delete', {
          method: 'POST',
          headers: getCSRFHeaders(),
          credentials: 'same-origin',
          body: JSON.stringify({ item_ids: requestIds }),
        });
      } catch {
        // network 失敗 → 全件失敗扱い
        const items = requestIds.map(collectFailureItem);
        showBulkFailureDialog({ verb: '削除', items: items });
        setBusy(false);
        return;
      }

      const status = res ? res.status : 0;

      // 200 OK 経路
      if (res && res.ok && status === 200) {
        let body = null;
        try {
          body = await res.json();
        } catch { body = null; }
        const succeeded = (body && Array.isArray(body.succeeded)) ? body.succeeded : [];
        const failed = (body && Array.isArray(body.failed)) ? body.failed : [];

        // 部分失敗時は **DOM 削除前に failed 詳細を収集** する（順序依存防止）
        let failureItems = null;
        if (failed.length > 0) {
          failureItems = [];
          for (let i = 0; i < failed.length; i += 1) {
            const fid = failed[i] && failed[i].item_id;
            failureItems.push(collectFailureItem(fid));
          }
        }

        // succeeded の DOM を fade-out + remove（ブラケット内で）
        for (let i = 0; i < succeeded.length; i += 1) {
          const sid = succeeded[i];
          const article = findCardByID(sid);
          if (!article) {
            // null fallback: fade-out skip、selection からは削除のみ実施 (snapshot 規約 4)
            continue;
          }
          if (selection && typeof selection.beginActionMutation === 'function') {
            selection.beginActionMutation();
            fadeOutAndRemove(article, selection);
          } else {
            // selection 未取得 fallback: 直接 remove
            if (article.classList && typeof article.classList.add === 'function') {
              article.classList.add('fade-out');
            }
            if (setTimeoutImpl) {
              setTimeoutImpl(() => {
                if (typeof article.remove === 'function') article.remove();
              }, 300);
            } else if (typeof article.remove === 'function') {
              article.remove();
            }
          }
        }

        // selection から succeeded を解除（snapshot 規約 2: clear() ではなく
        // removeFromSelection で行う）。
        if (selection && typeof selection.removeFromSelection === 'function') {
          if (succeeded.length > 0) selection.removeFromSelection(succeeded);
        }

        if (failed.length === 0) {
          // 全成功
          toast.success(succeeded.length + ' 件削除しました');
        } else {
          // 部分失敗: failed は selection に残置（Req 4.8）
          showBulkFailureDialog({ verb: '削除', items: failureItems });
        }
        setBusy(false);
        return;
      }

      // 4xx / 5xx エラー分岐
      let errBody = null;
      try { errBody = await res.json(); } catch { errBody = null; }
      const errCode = errBody && errBody.error ? String(errBody.error) : '';

      if (status === 400 && errCode === 'invalid_request') {
        toast.error('一括削除のリクエストが不正です');
        setBusy(false);
        return;
      }
      if (status === 400 && errCode === 'payload_too_large') {
        toast.error('100 件を超える選択はできません');
        setBusy(false);
        return;
      }

      // 401 / 403 / 429 / 500 / その他の 4xx 5xx → 全件失敗 dialog
      const items = requestIds.map(collectFailureItem);
      showBulkFailureDialog({ verb: '削除', items: items });
      setBusy(false);
    }

    // --- API call: bulk-tag ----------------------------------------------

    async function performBulkTag(requestIds, tagValue) {
      const selection = getSelection();
      setBusy(true);

      let res;
      try {
        res = await fetchImpl('/v1/items/bulk-tag', {
          method: 'POST',
          headers: getCSRFHeaders(),
          credentials: 'same-origin',
          body: JSON.stringify({ item_ids: requestIds, tag: tagValue }),
        });
      } catch {
        // network 失敗 → 全件失敗 dialog
        const items = requestIds.map(collectFailureItem);
        showBulkFailureDialog({ verb: 'タグ付け', items: items });
        setBusy(false);
        return;
      }

      const status = res ? res.status : 0;

      // 200 OK 経路
      if (res && res.ok && status === 200) {
        let body = null;
        try { body = await res.json(); } catch { body = null; }
        const succeeded = (body && Array.isArray(body.succeeded)) ? body.succeeded : [];
        const failed = (body && Array.isArray(body.failed)) ? body.failed : [];

        // active tag filter Set を 1 回だけ算出
        const activeSet = computeActiveNormalizedNames();

        // succeeded の chip を再構築
        for (let i = 0; i < succeeded.length; i += 1) {
          const s = succeeded[i] || {};
          const card = findCardByID(s.item_id);
          if (!card) continue;
          const tags = Array.isArray(s.tags) ? s.tags : [];
          rebuildChipsForCard(card, tags, activeSet);
        }

        // selection 解除（snapshot 規約 2 / Req 5.6 / 5.8）
        if (selection && typeof selection.removeFromSelection === 'function') {
          if (failed.length === 0) {
            // 全成功 → snapshot 全件を解除
            if (requestIds.length > 0) selection.removeFromSelection(requestIds);
          } else {
            // 部分失敗 → succeeded の item_id のみ解除
            const ids = succeeded.map((x) => x.item_id);
            if (ids.length > 0) selection.removeFromSelection(ids);
          }
        }

        // dialog 閉鎖
        if (tagDialog && typeof tagDialog.close === 'function') {
          try { tagDialog.close(); } catch { /* noop */ }
        }

        if (failed.length === 0) {
          // 全成功
          toast.success(succeeded.length + ' 件にタグを付与しました');
        } else {
          // 部分失敗 → 失敗 dialog
          const failureItems = [];
          for (let i = 0; i < failed.length; i += 1) {
            const fid = failed[i] && failed[i].item_id;
            failureItems.push(collectFailureItem(fid));
          }
          showBulkFailureDialog({ verb: 'タグ付け', items: failureItems });
        }
        setBusy(false);
        return;
      }

      // 4xx / 5xx 経路
      let errBody = null;
      try { errBody = await res.json(); } catch { errBody = null; }
      const errCode = errBody && errBody.error ? String(errBody.error) : '';

      if (status === 400 && errCode === 'invalid_tag') {
        // dialog 開いたまま + input focus 戻し + toast.error
        toast.error('タグ名を入力してください');
        if (tagInput && typeof tagInput.focus === 'function') {
          try { tagInput.focus(); } catch { /* noop */ }
        }
        setBusy(false);
        return;
      }
      if (status === 400 && errCode === 'invalid_request') {
        toast.error('一括タグ付けのリクエストが不正です');
        setBusy(false);
        return;
      }
      if (status === 400 && errCode === 'payload_too_large') {
        toast.error('一括タグ付けの対象が多すぎます（最大 100 件）');
        setBusy(false);
        return;
      }

      // 401 / 403 / 429 / 5xx → 全件失敗 dialog
      const items = requestIds.map(collectFailureItem);
      showBulkFailureDialog({ verb: 'タグ付け', items: items });
      setBusy(false);
    }

    // --- bulkselection:changed event listen -----------------------------

    function onBulkSelectionChanged(e) {
      const detail = e && e.detail ? e.detail : { count: 0, ids: [] };
      const n = (typeof detail.count === 'number') ? detail.count : 0;
      if (n > 0) showToolbar(n);
      else hideToolbar();
    }
    if (region.addEventListener) {
      region.addEventListener('bulkselection:changed', onBulkSelectionChanged);
    }

    // --- ツールバー delegated click ---------------------------------------

    function onToolbarClick(e) {
      const target = e && e.target;
      if (!target || typeof target.closest !== 'function') return;

      // bulk-clear
      const clearBtn = target.closest('button.bulk-clear');
      if (clearBtn) {
        if (clearBtn.disabled) return;
        const selection = getSelection();
        if (selection && typeof selection.clear === 'function') selection.clear();
        return;
      }

      // bulk-delete
      const delBtn = target.closest('button.bulk-delete');
      if (delBtn) {
        if (delBtn.disabled) return;
        const selection = getSelection();
        if (!selection || typeof selection.getSelectedIDs !== 'function') return;
        // **リクエスト ID スナップショット規約** (round 6): live ではなく defensive copy。
        const requestIds = Array.from(selection.getSelectedIDs());
        if (requestIds.length === 0) return;

        // confirm dialog（object.show()）。approve callback で fetch を起動する。
        showConfirm(
          '一括削除',
          requestIds.length + ' 件を削除しますか？',
          () => {
            void performBulkDelete(requestIds);
          },
          'Delete',
          'btn-danger'
        );
        return;
      }

      // bulk-tag
      const tagBtn = target.closest('button.bulk-tag');
      if (tagBtn) {
        if (tagBtn.disabled) return;
        const selection = getSelection();
        if (!selection || typeof selection.getSelectedIDs !== 'function') return;
        const requestIds = Array.from(selection.getSelectedIDs());
        if (requestIds.length === 0) return;

        // dialog の input をクリア + showModal
        if (tagInput) {
          if ('value' in tagInput) tagInput.value = '';
        }
        // 現在の選択 snapshot を click 単位の closure で保持する。submit ハンドラから
        // 参照される。
        currentTagRequestIds = requestIds;
        if (tagDialog) {
          if (typeof tagDialog.showModal === 'function') {
            try { tagDialog.showModal(); } catch { /* noop */ }
          } else if (typeof tagDialog.setAttribute === 'function') {
            tagDialog.setAttribute('open', '');
          }
        }
        // input に focus（autofocus は browser によって発火タイミングが不安定なため明示）
        if (tagInput && typeof tagInput.focus === 'function') {
          try { tagInput.focus(); } catch { /* noop */ }
        }
        return;
      }
    }

    // bulk-tag dialog の click closure で参照するための snapshot（最後に bulk-tag を
    // 押下した時点の selection 内容）。submit / cancel ハンドラから参照する。
    let currentTagRequestIds = [];

    // bulk-tag dialog: form submit ハンドラ
    function onTagFormSubmit(e) {
      // **`event.preventDefault()` 必須**（method="dialog" の自動 close 抑止）
      if (e && typeof e.preventDefault === 'function') e.preventDefault();

      const raw = (tagInput && 'value' in tagInput) ? (tagInput.value || '') : '';
      // 空判定だけのために normalize を呼ぶ。送信時は **原文字列** を送る。
      const norm = (win && win.altpocketNormalizeTagName) || fallbackNormalize;
      const normalized = norm(raw);
      if (!normalized) {
        // 空入力 → no-op + input に focus 戻し（Req 5.9）
        if (tagInput && typeof tagInput.focus === 'function') {
          try { tagInput.focus(); } catch { /* noop */ }
        }
        return;
      }
      // 原文字列で送信する（NFKC + lowercase は server 側で適用される）。
      const ids = currentTagRequestIds.slice();
      if (ids.length === 0) return;
      void performBulkTag(ids, raw);
    }

    // bulk-tag dialog: cancel ボタン
    function onTagCancelClick() {
      if (tagDialog && typeof tagDialog.close === 'function') {
        try { tagDialog.close(); } catch { /* noop */ }
      }
    }

    // failure dialog: close ボタン
    function onFailureCloseClick() {
      if (failureDialog && typeof failureDialog.close === 'function') {
        try { failureDialog.close(); } catch { /* noop */ }
      } else if (failureDialog && typeof failureDialog.removeAttribute === 'function') {
        failureDialog.removeAttribute('open');
      }
    }

    // delegated click を toolbar に register（toolbar 自身の addEventListener）。
    // toolbar 外の click は無関係なので document delegated にしない（範囲を狭くする）。
    if (toolbar && toolbar.addEventListener) {
      toolbar.addEventListener('click', onToolbarClick);
    }
    if (tagForm && tagForm.addEventListener) {
      tagForm.addEventListener('submit', onTagFormSubmit);
    }
    if (tagCancel && tagCancel.addEventListener) {
      tagCancel.addEventListener('click', onTagCancelClick);
    }
    if (failureClose && failureClose.addEventListener) {
      failureClose.addEventListener('click', onFailureCloseClick);
    }

    // 起動時、selection モジュールがすでに初期化済みなら現在件数で同期する。
    // selection が後から init される場合は bulkselection:changed event で同期される
    // ため、ここでは存在する場合のみ初期同期する。
    const initialSelection = getSelection();
    if (initialSelection && typeof initialSelection.getSelectedIDs === 'function') {
      const ids = initialSelection.getSelectedIDs();
      if (ids && ids.length > 0) showToolbar(ids.length);
      else hideToolbar();
    } else {
      // selection 未取得の場合は隠した状態を保証する（SSR の `hidden` 初期値を尊重）。
      hideToolbar();
    }

    return {
      _debug: {
        showBulkFailureDialog,
        rebuildChipsForCard,
        computeActiveNormalizedNames,
        collectFailureItem,
        performBulkDelete,
        performBulkTag,
        setBusy,
      },
    };
  }

  // テスト経路から init を呼べるよう、グローバルでも公開する（既存
  // items_bulk_selection.js の流儀と一致 — テストでは vm.createContext で
  // source を評価したのち、_init() を直接呼び出す）。
  if (typeof window !== 'undefined') {
    window.altpocketBulkActionsInit = init;
  }

  // 自動 init は本番経路でのみ実施する。テストは `window.__altpocketBulkActionsSkipAutoInit
  // = true` をセットしておき、明示的に init({ ... }) を呼ぶ（auto-init による handler
  // 重複登録を防ぐ）。
  if (typeof document !== 'undefined' && typeof window !== 'undefined') {
    if (!window.__altpocketBulkActionsSkipAutoInit) {
      init();
    }
  }
})();
