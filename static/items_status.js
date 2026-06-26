// items_status.js
//
// /ui/items の状態タブ（Unread / All / Archived）の click 切替と URL 同期を司る
// 小さなモジュール (Issue #119 task 9)。
//
// SSR (templates/items.html) で描画される markup:
//
//   <nav class="status-tabs" role="tablist" aria-label="アイテム状態">
//     <a role="tab" class="status-tab[ is-active]" aria-selected="..."
//        href="/ui/items?status=unread&...">Unread</a>
//     <a role="tab" class="status-tab[ is-active]" aria-selected="..."
//        href="/ui/items?status=all&...">All</a>
//     <a role="tab" class="status-tab[ is-active]" aria-selected="..."
//        href="/ui/items?status=archived&...">Archived</a>
//   </nav>
//
// JS が有効な環境では、本モジュールが以下を担当する:
//
//   - status-tabs の <a role="tab"> click を delegated 捕捉し preventDefault →
//     `?status=` を書き換えた相対 URL を「現在の location.href」を base に再構築
//     → history.pushState（戻る/進むで前タブに戻れる / Req 3.8） →
//     X-Requested-With: ItemsFragment 経由のフラグメント取得で一覧を更新（Req 3.2）
//   - タブ active 状態の手動同期: status-tabs は items.html 側に SSR されており
//     fragment 取得で置換される items_list.html には含まれないため、click 直後 /
//     fragment 取得直後 / popstate 直後に nav.status-tabs a[role="tab"] を走査し、
//     新 ?status= 値に一致するタブにだけ aria-selected="true" / is-active を付与
//     する（他のタブからは外す。Req 3.7 の常時可視化 / Req 3.8 の描画維持）
//   - popstate（戻る/進む）で URL の `?status=` を読み取って fragment 取得 +
//     タブ active 状態同期（Req 3.8 の URL クエリ永続を戻る/進むに追従）
//   - 修飾キー付き click（Cmd/Ctrl/Shift/Alt）は intercept せず、ブラウザ既定の
//     「新しいタブで開く」等の挙動を維持する
//
// URL 再構築は items_active_filters.js と同じく「現在の location.href を base
// に new URL し、searchParams.set('status', next) で他クエリ（q / tag / sort /
// per_page / page）を保持」する規約。サーバ側 buildStatusTabURLs
// (internal/server/items_status.go) も同じ「他クエリ全保持 + ?status= 上書き」
// を行うため、JS 側 / SSR href のどちらでも同形状の URL を生む（Req 3.6 の併用）。
//
// データ region の dataset.currentStatus 同期: items_status_actions.js (task 8)
// が `[data-items-region].dataset.currentStatus` を読んで「タブ条件で消すべき
// card」を判定するため、本モジュールはタブ切替時に region の data-current-status
// 属性も新 status 値に同期更新する（SSR templates/items.html はこの属性を
// 出力しないため、JS 側で init / click / popstate のタイミングで補填する）。
//
// JS 無効環境では本ファイルが評価されないため、`<a href>` のフルページ遷移が
// そのまま動く（SSR fallback / NFR 4.1 アクセシビリティ）。
//
// AbortController slot: items_active_filters.js / items_tags.js / items_search.js
// と同じ `[data-items-region]` 上の __itemsFragmentInflight slot を共有し、新規
// fetch 開始時に前段 controller を abort する（NFR 1.1 / 1.2 の連続切替時 race
// 防止）。事前注入された slot は再作成しない（参照を維持し、他モジュールと共有）。

(function () {
  'use strict';

  // canonical な status タブ識別子。サーバ側 internal/server/items_status.go の
  // statusTabUnread / statusTabAll / statusTabArchived と完全に対応する。
  const TAB_VALUES = ['unread', 'all', 'archived'];
  const DEFAULT_TAB = 'unread';

  // 与えられた URL 文字列から `?status=` 値を読み取り、canonical な tab 識別子
  // ('unread' / 'all' / 'archived') に解決する。未指定 / 不明値は既定の Unread
  // にフォールバックする（サーバ側 resolveStatusTab と同等の挙動）。
  function resolveStatusFromURL(urlStr, base) {
    let value = '';
    try {
      const u = new URL(urlStr, base || undefined);
      value = (u.searchParams.get('status') || '').trim().toLowerCase();
    } catch {
      value = '';
    }
    if (value === 'all') return 'all';
    if (value === 'archived') return 'archived';
    // 'unread' / '' / 不明値はすべて Unread 既定に collapse する。
    return DEFAULT_TAB;
  }

  function init(opts) {
    const o = opts || {};
    const doc = o.document || (typeof document !== 'undefined' ? document : null);
    const win = o.window || (typeof window !== 'undefined' ? window : null);
    if (!doc || !win) return null;

    const region = doc.querySelector ? doc.querySelector('[data-items-region]') : null;
    if (!region) return null;

    const fetchImpl = o.fetch || (typeof win.fetch === 'function' ? win.fetch.bind(win) : null);
    if (!fetchImpl) return null;
    const history = o.history || win.history;
    const location = o.location || win.location;

    // AbortController slot を items_active_filters.js / items_tags.js /
    // items_search.js と共有する (#117 で導入された規約)。既存の slot が
    // 事前注入されていれば再利用し、無ければ作成する（参照を維持することで
    // 他モジュールから見て同じ slot として扱える）。
    if (!region.__itemsFragmentInflight) {
      region.__itemsFragmentInflight = { ctrl: null };
    }
    const coord = region.__itemsFragmentInflight;

    // タブ active 状態の手動同期。
    // status-tabs は items.html 側にあり fragment 取得で置換される items_list.html
    // には含まれないため、fragment 取得後も markup は再描画されない。
    // 本関数は nav.status-tabs a[role="tab"] を走査して `href` の `?status=` 値と
    // 新 `?status=` 値を比較し、一致するタブに aria-selected="true" / is-active を
    // 付与し、他のタブからは外す（Req 3.7 / 3.8）。
    function syncTabActive(nextTab) {
      const tabs = doc.querySelectorAll
        ? doc.querySelectorAll('nav.status-tabs a[role="tab"]')
        : [];
      const base = location && location.href ? location.href : undefined;
      for (let i = 0; i < tabs.length; i += 1) {
        const tab = tabs[i];
        const href = tab.getAttribute ? (tab.getAttribute('href') || '') : '';
        // tab の href には canonical な `?status=` が必ず含まれている前提
        // (サーバ側 buildStatusTabURLs の出力)。href から `?status=` を読んで
        // canonical 識別子を取得し、新 tab と一致するかで active 判定する。
        const tabValue = resolveStatusFromURL(href, base);
        const isActive = tabValue === nextTab;
        if (tab.setAttribute) {
          tab.setAttribute('aria-selected', isActive ? 'true' : 'false');
        }
        if (tab.classList) {
          if (isActive) {
            tab.classList.add('is-active');
          } else {
            tab.classList.remove('is-active');
          }
        }
      }
    }

    // `[data-items-region]` の dataset.currentStatus を新 tab 値に同期する。
    // items_status_actions.js (task 8) がタブ条件 DOM 削除判定で本属性を参照する
    // ため、タブ切替直後に必ず最新値へ更新する必要がある。SSR templates は本属性を
    // 出力しないので、JS 側で init / click / popstate のタイミングで補填する。
    function syncRegionDataset(nextTab) {
      if (region.setAttribute) {
        region.setAttribute('data-current-status', nextTab);
      }
      // dataset の同期は setAttribute で十分（DOM 仕様上 data-* は dataset と
      // 連動する）。フォールバックとして直接 dataset プロパティへも書き込んで
      // おく（fake DOM 環境で setAttribute と dataset が独立して実装される
      // 可能性があるため。templates/items_active_filters.js 等の既存実装と整合）。
      if (region.dataset) {
        region.dataset.currentStatus = nextTab;
      }
    }

    async function refreshFragment(targetURL) {
      // 前段の保留中リクエストを破棄して最新の絞り込みのみ反映する
      // (NFR 1.1 / 1.2)。coord は items_tags.js / items_search.js /
      // items_active_filters.js と共有しているので、cross-module race も同時に防ぐ。
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
        // (NFR 1.2)。差し替え後は items.html 側の status-tabs markup は維持される
        // が、active 状態の同期は本モジュールが syncTabActive で個別に行う。
        region.innerHTML = html;
      } catch {
        // ネットワーク失敗時は前回結果を維持する (NFR 1.2)。
      } finally {
        if (coord.ctrl === ctrl) coord.ctrl = null;
      }
    }

    // 現在 URL (location.href) を base に、`?status=<next>` を上書きした相対 URL
    // (path + search + hash) を返す。他クエリ（q / tag / sort / per_page / page
    // など）は保持する（Req 3.6 の併用）。サーバ側 buildStatusTabURL と同形状の
    // URL を生む規約。
    function buildTargetURL(nextTab) {
      const base = location && location.href ? location.href : null;
      let url;
      try {
        url = new URL(base || '/');
      } catch {
        return null;
      }
      url.searchParams.set('status', nextTab);
      return url.pathname + (url.search || '') + (url.hash || '');
    }

    // タブ click → URL 書換 + pushState + fragment 取得 + active 同期。
    // 古い SSR href（fragment 取得待ち中は前回 URL を反映した古い値でありうる）
    // ではなく現在の location.href を base に再構築するため、連続切替でも最終
    // 状態が正しい URL に収束する（items_active_filters.js 同様の規約）。
    function commit(nextTab) {
      const targetHref = buildTargetURL(nextTab);
      if (!targetHref) return;

      // pushState: 戻る/進むで前タブに戻れる（Req 3.8）。
      try {
        history.pushState({ source: 'items_status' }, '', targetHref);
      } catch {
        // history が使えない環境では fetch だけ行う。
      }

      // 即時に UI を更新（fetch の完了を待たない / Req 3.7 の常時可視化を
      // synchronous DOM 反映で満たす）。
      syncTabActive(nextTab);
      syncRegionDataset(nextTab);

      void refreshFragment(targetHref);
    }

    // タブ click を delegated でハンドリング（fragment 差し替えで items_list 側の
    // 要素が再生成されるが、status-tabs 自体は items.html 側に常駐するため、
    // 厳密には click handler が再生成される心配は無い。それでも document
    // レベルの delegated handler パターンで統一する）。
    function onDocumentClick(e) {
      const target = e.target;
      if (!target || typeof target.closest !== 'function') return;

      // 修飾キー付き click は「新しいタブで開く」等のブラウザ既定動作を妨げない
      // ため intercept しない（items_active_filters.js と同じ規約）。
      if (e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
      if (e.defaultPrevented) return;

      // status-tabs 配下の <a role="tab"> のみを対象にする。
      const tab = target.closest('nav.status-tabs a[role="tab"]');
      if (!tab) return;

      const href = tab.getAttribute ? (tab.getAttribute('href') || '') : '';
      if (!href) return;

      const base = location && location.href ? location.href : undefined;
      const nextTab = resolveStatusFromURL(href, base);
      if (!nextTab || TAB_VALUES.indexOf(nextTab) === -1) return;

      e.preventDefault();
      commit(nextTab);
    }

    // popstate (戻る/進む) で URL の `?status=` を読み取って fragment 取得 +
    // タブ active 同期を行う（Req 3.8）。
    async function onPopState() {
      const base = location && location.href ? location.href : undefined;
      const nextTab = resolveStatusFromURL(base || '', base);
      syncTabActive(nextTab);
      syncRegionDataset(nextTab);
      await refreshFragment(base || '');
    }

    // 初回 init 時にも region.dataset.currentStatus と tab active 状態を URL に
    // 揃える。SSR は items_status.go の resolveStatusTab で `aria-selected`
    // / `is-active` を SSR 側で確定済みだが、region の data-current-status は
    // SSR が出力しないため、JS 評価時にここで補填する（items_status_actions.js
    // が依存する属性なので、必須同期）。
    const initialTab = resolveStatusFromURL(location && location.href ? location.href : '', undefined);
    syncRegionDataset(initialTab);

    doc.addEventListener('click', onDocumentClick);
    win.addEventListener('popstate', onPopState);

    return {
      _debug: {
        getInflight: () => coord.ctrl,
        commit,
        buildTargetURL,
        syncTabActive,
        syncRegionDataset,
        resolveStatusFromURL,
      },
    };
  }

  if (typeof document !== 'undefined' && typeof window !== 'undefined') {
    init();
  }
})();
