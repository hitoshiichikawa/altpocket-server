// items_active_filters.js
//
// /ui/items のアクティブフィルタチップ列 (Issue #115) を司る小さなモジュール。
//
// チップ列は `templates/items_list.html` で SSR される (Req 4.5 / NFR 2.1)。
// 各チップは `<a class="chip active-filter-chip" data-active-filter-chip
// data-tag-normalized="<norm>" href="<解除後 URL>">` 形式、「すべてクリア」は
// `<a data-active-filter-clear-all href="<タグなし URL>">`。
//
// JS が有効な場合、本モジュールは以下を担当する:
//
//   - チップ click を捕捉して preventDefault → 現在 URL から該当タグだけを
//     除いた URL を再構築 → history.pushState（戻るで解除前に戻れる / Req
//     2.6）→ サイドバー checkbox とカード上タグ button の即時同期（Req 2.3
//     / 2.4 / NFR 1.1）→ X-Requested-With: ItemsFragment 経由のフラグメント
//     取得で一覧を更新（Req 2.2 / NFR 1.2）
//   - 「すべてクリア」click を捕捉して同じ経路で全タグを解除（Req 3.2〜3.6）
//   - popstate（戻る/進む）で URL に応じてフラグメントを再取得（Req 4.4）
//
// 重要: チップ click / Space activate では SSR の `href` ではなく、`data-tag-
// normalized` で指定された 1 タグを「現在の `location.href`」から削除した URL
// を JS 側で再構築して pushState に使う。SSR の href はフラグメント取得が完了
// するまで前回 URL を反映した古い値のままなので、連続解除中に古い href を
// そのまま使うと「go 解除 → fetch 待ち → rust 解除」で rust チップの古い
// href (`?tag=go`) によって解除済みの go が復活する退行が起きる (Req 2.1 /
// 2.5 / NFR 1.3)。`location.href` は pushState 直後に新 URL を返すため、現在
// URL から再構築すれば連続解除でも最終状態が正しくなる。
//
// URL 再構築はサーバ側 (server.go buildTagRemovedURL / buildClearAllTagsURL)
// と同じ正準形式 (`?tag=` 繰り返し / 旧 `?tags=csv` は drop) に揃え、他クエリ
// (page / q / sort / per_page など) を保持する (Req 5.1 / 5.2 / 5.4)。
//
// JS 無効環境では本ファイルが評価されないため、`<a href>` のフルページ遷移が
// そのまま動く (NFR 2.1)。
//
// フラグメント取得の AbortController は Issue #117 / #114 と同様、
// `[data-items-region]` 要素上の `__itemsFragmentInflight` slot で共有する。
// これにより、検索 debounce / カード上タグクリック / アクティブフィルタ操作の
// いずれが新しい fetch を始めても、他の保留 fetch を確実に abort する
// (NFR 1.2 / 1.3)。

(function () {
  'use strict';

  function init(opts) {
    const doc = opts && opts.document ? opts.document : (typeof document !== 'undefined' ? document : null);
    const win = opts && opts.window ? opts.window : (typeof window !== 'undefined' ? window : null);
    if (!doc || !win) return null;

    const region = doc.querySelector('[data-items-region]');
    if (!region) return null;

    const fetchImpl = (opts && opts.fetch) || (typeof win.fetch === 'function' ? win.fetch.bind(win) : null);
    if (!fetchImpl) return null;
    const history = (opts && opts.history) || win.history;
    const location = (opts && opts.location) || win.location;

    // AbortController slot は items_tags.js / items_search.js と共有する
    // (Issue #117 で導入された規約)。region 要素上の slot に置くことで、
    // どのモジュールも同じ controller を参照する。
    if (!region.__itemsFragmentInflight) {
      region.__itemsFragmentInflight = { ctrl: null };
    }
    const coord = region.__itemsFragmentInflight;

    // サーバ側 (internal/tag/tag.go Normalize) と完全一致するタグ正規化。
    // Issue #117 と同じ実装。URL から読んだ tag 値の比較を正規化済みで
    // 揃えるために使う。本モジュールでは「URL から現在の tag を読み取って
    // サイドバー checkbox / カード button の同期に使う」ためにのみ必要。
    function normalizeTag(raw) {
      if (raw == null) return '';
      let s = String(raw).trim();
      if (s === '') return '';
      if (typeof s.normalize === 'function') {
        s = s.normalize('NFKC');
      }
      return s.toLowerCase();
    }

    // 指定 URL から現在の絞り込みタグ集合 (正規化済み) を読み取る。
    // ?tag= 繰り返し + ?tags= 複数形 (カンマ区切り) の両方をサーバ側
    // parseTagFilters と同じ規則で受理する (Req 5.4 既存 URL 互換)。
    //
    // SSR が出力するチップの href / clear-all の href は相対 URL
    // (`/ui/items?tag=...`) なので、`new URL` の第 2 引数に現在ページの
    // location.href を base として渡す。base を省略すると相対 URL の
    // パースが失敗し空配列を返してしまい、サイドバー checkbox 同期で
    // 「全解除状態」と誤判定される (Req 2.3 違反)。
    function readURLTags(targetURL) {
      try {
        const base = location && location.href ? location.href : undefined;
        const u = new URL(targetURL, base);
        const out = [];
        const seen = new Set();
        for (const raw of u.searchParams.getAll('tag')) {
          const norm = normalizeTag(raw);
          if (norm === '' || seen.has(norm)) continue;
          seen.add(norm);
          out.push(norm);
        }
        for (const csv of u.searchParams.getAll('tags')) {
          for (const part of String(csv).split(',')) {
            const norm = normalizeTag(part);
            if (norm === '' || seen.has(norm)) continue;
            seen.add(norm);
            out.push(norm);
          }
        }
        return out;
      } catch {
        return [];
      }
    }

    // カード上タグ button とサイドバー checkbox を、与えられた tag リストに
    // 合わせて同期する (Req 2.3 / 2.4 / 3.4 / 3.5)。
    // items_tags.js の syncControls と同じ規約だが、本モジュールは独立して
    // 動作する必要がある（items_tags.js が region.innerHTML 差し替えで再
    // 評価されないため、本モジュール内に同等関数を持つ）。
    function syncControls(selectedTags) {
      const selected = new Set(selectedTags);

      const buttons = doc.querySelectorAll('[data-tag-filter-toggle]');
      buttons.forEach((btn) => {
        const name = btn.getAttribute('data-tag-normalized') || '';
        const isOn = selected.has(name);
        btn.setAttribute('aria-pressed', isOn ? 'true' : 'false');
        if (isOn) {
          btn.classList.add('is-selected');
        } else {
          btn.classList.remove('is-selected');
        }
      });

      const checkboxes = doc.querySelectorAll('input[type="checkbox"][name="tag"]');
      checkboxes.forEach((cb) => {
        const want = selected.has(cb.value);
        if (cb.checked !== want) cb.checked = want;
      });
    }

    async function refreshFragment(targetURL) {
      // 前段の保留中リクエストを破棄して最新の絞り込みのみ反映する
      // (NFR 1.2 / 1.3)。coord は items_tags.js / items_search.js と
      // 共有しているので、cross-module race も同時に防げる。
      if (coord.ctrl) {
        try { coord.ctrl.abort(); } catch { /* noop */ }
      }
      const ctrl = (typeof win.AbortController === 'function') ? new win.AbortController() : null;
      coord.ctrl = ctrl;

      try {
        const res = await fetchImpl(targetURL, {
          method: 'GET',
          credentials: 'same-origin',
          headers: { 'X-Requested-With': 'ItemsFragment' },
          signal: ctrl ? ctrl.signal : undefined,
        });
        if (!res || !res.ok) return;
        const html = await res.text();
        // ちらつき防止のため、レスポンスが返ってから一気に差し替える
        // (NFR 1.2)。差し替え後はサーバ側 SSR がチップ列・サイドバー
        // checkbox 状態を URL と整合して返してくれるため、本モジュールが
        // 追加で同期する必要はない。
        region.innerHTML = html;
      } catch {
        // ネットワーク失敗時は前回結果を維持する (NFR 1.2)。
      } finally {
        if (coord.ctrl === ctrl) coord.ctrl = null;
      }
    }

    // 現在 URL (location.href) を base に、正準形式 ?tag= 繰り返しで `nextTags`
    // を再構築した相対 URL (path + search + hash) を返す。
    // 旧形式 ?tags=csv は一律 drop し、他クエリ (page / q / sort / per_page など)
    // は保持する (Req 5.1 / 5.2 / 5.4)。
    function buildTargetURL(nextTags) {
      const base = location && location.href ? location.href : null;
      let url;
      try {
        url = new URL(base || '/');
      } catch {
        return null;
      }
      url.searchParams.delete('tag');
      url.searchParams.delete('tags');
      for (const t of nextTags) {
        if (t === '' || t == null) continue;
        url.searchParams.append('tag', t);
      }
      return url.pathname + (url.search || '') + (url.hash || '');
    }

    // チップ個別解除のターゲット URL を、現在 URL から `normalized` 1 件だけ
    // 除いた形で再構築する。SSR の chip href は前回 URL を反映した古い値で
    // ありうるため、連続解除のために必ず現在 URL を基点にする (NFR 1.3)。
    function buildRemoveTagURL(normalized) {
      const base = location && location.href ? location.href : null;
      if (!base) return null;
      const remaining = readURLTags(base).filter((t) => t !== normalized);
      return buildTargetURL(remaining);
    }

    // 「すべてクリア」のターゲット URL は、現在 URL から tag/tags を全削除した
    // 相対 URL。
    function buildClearAllURL() {
      return buildTargetURL([]);
    }

    // チップ解除・「すべてクリア」共通のコミット処理。
    // ターゲット URL は呼び出し側で再構築済み。pushState → サイドバー /
    // カード button の即時同期 → フラグメント取得の順で実行する。
    function commit(targetHref, nextTags) {
      if (!targetHref) return;

      // pushState: 戻るで解除前の絞り込みに戻れる (Req 2.6 / 3.6 / OQ-(b))。
      try {
        history.pushState({ source: 'items_active_filters' }, '', targetHref);
      } catch {
        // history が使えない環境ではフェッチだけ行う。
      }

      // 即時に UI を更新（フェッチの完了を待たない）(NFR 1.1)。サイドバー
      // checkbox とカード上タグ button が新条件と一致する状態に瞬時に
      // 切り替わる (Req 2.3 / 2.4 / 3.4 / 3.5)。
      const tags = nextTags != null ? nextTags : readURLTags(targetHref);
      syncControls(tags);

      void refreshFragment(targetHref);
    }

    // チップ解除を実行する高レベル API。`data-tag-normalized` 属性から削除対象
    // タグを取得し、現在 URL から該当タグだけを除く形で commit する。古い SSR
    // href を経由しないため、フラグメント取得待ち中の連続解除でも最終状態が
    // 正しく収束する (Req 2.5 / 6.2 / 6.3 / NFR 1.3)。
    function commitRemoveTag(chip) {
      if (!chip) return;
      const normalized = chip.getAttribute('data-tag-normalized') || '';
      if (normalized === '') return;
      const base = location && location.href ? location.href : null;
      if (!base) return;
      const remaining = readURLTags(base).filter((t) => t !== normalized);
      const href = buildTargetURL(remaining);
      commit(href, remaining);
    }

    // 「すべてクリア」を実行する高レベル API。現在 URL から tag/tags を全削除
    // する。チップ解除と同じく古い SSR href を使わない (Req 3.2〜3.6 / NFR 1.3)。
    function commitClearAll() {
      const href = buildClearAllURL();
      commit(href, []);
    }

    // チップ click / 「すべてクリア」click を delegated でハンドリング
    // (フラグメント差し替えで要素が再生成されるため)。
    // <a> なので Enter は HTML 仕様上 click にディスパッチされる
    // (Req 6.2 / 6.3)。
    function onDocumentClick(e) {
      const target = e.target;
      if (!target || typeof target.closest !== 'function') return;

      // 修飾キー付きクリックは「新しいタブで開く」等のブラウザ既定動作を
      // 妨げないため intercept しない。
      if (e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
      if (e.defaultPrevented) return;

      const chip = target.closest('[data-active-filter-chip]');
      if (chip) {
        e.preventDefault();
        commitRemoveTag(chip);
        return;
      }
      const clearAll = target.closest('[data-active-filter-clear-all]');
      if (clearAll) {
        e.preventDefault();
        commitClearAll();
        return;
      }
    }

    // チップ / 「すべてクリア」は `<a role="button">` で描画されているため、
    // 支援技術には button として読み上げられる。ARIA Authoring Practices に
    // 従い button は Space キーでも activate できる必要があるが、`<a>` 要素は
    // ブラウザがネイティブに Space を click にディスパッチしないため、本ハンドラ
    // が Space を click 相当の commit に変換する (Req 6.2 / 6.3)。
    // Enter は `<a>` ネイティブで click にディスパッチされるため、ここでは
    // 扱わない (click ハンドラ側で拾われる)。
    function onDocumentKeydown(e) {
      // Space (`' '` または `'Spacebar'`) のみを対象。
      if (e.key !== ' ' && e.key !== 'Spacebar') return;
      const target = e.target;
      if (!target || typeof target.closest !== 'function') return;
      if (e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
      if (e.defaultPrevented) return;

      const chip = target.closest('[data-active-filter-chip]');
      if (chip) {
        // Space のページスクロール既定動作を抑止して click 相当に変換する。
        e.preventDefault();
        commitRemoveTag(chip);
        return;
      }
      const clearAll = target.closest('[data-active-filter-clear-all]');
      if (clearAll) {
        e.preventDefault();
        commitClearAll();
        return;
      }
    }

    // popstate (戻る/進む) で URL に応じてフラグメントとサイドバー状態を
    // 再同期する (Req 4.4)。フラグメント差し替え後にサーバ側 SSR が
    // チップ列を URL と整合させて返してくれるため、本モジュールは
    // フラグメント取得をトリガするだけでよい。
    async function onPopState() {
      const tags = readURLTags(location.href);
      syncControls(tags);
      await refreshFragment(location.href);
    }

    doc.addEventListener('click', onDocumentClick);
    doc.addEventListener('keydown', onDocumentKeydown);
    win.addEventListener('popstate', onPopState);

    return {
      _debug: {
        getInflight: () => coord.ctrl,
        commit,
        commitRemoveTag,
        commitClearAll,
        buildTargetURL,
        buildRemoveTagURL,
        buildClearAllURL,
        syncControls,
        readURLTags,
      },
    };
  }

  if (typeof document !== 'undefined' && typeof window !== 'undefined') {
    init();
  }
})();
