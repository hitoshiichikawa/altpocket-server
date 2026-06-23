// items_tags.js
//
// /ui/items のカード上タグ (button.tag-filter-toggle) を入口にした絞り込み
// トグルと URL 同期を担当する小さなモジュール (Issue #117)。
//
// - クリック / Enter / Space で当該タグの URL ?tag= を toggle する
//   (要件 2.1 / 2.2 / 4.2)。
// - 履歴は明示的なコミット操作として history.pushState で残す (OQ-(a) の
//   採用: 戻るで前の絞り込みに戻れる)。
// - 既存サイドバーのチェックボックスとカード上の同名タグボタンを双方向に
//   同期する (要件 2.3 / 5.2)。
// - フラグメント取得 (items_search.js と同じ X-Requested-With: ItemsFragment
//   経路) で、再描画後にカード上タグの状態も新しい URL に揃え直す
//   (要件 3.4 / 5.3 / NFR 1.2)。
// - 連続クリック / debounce 中のレース対策として AbortController で前段の
//   保留リクエストを破棄する (OQ-(c)、items_search.js と同じ規約)。
//
// JS 無効環境では本ファイルが評価されないだけで、サイドバーの form 送信に
// よる従来の絞り込みはそのまま動く (NFR 2.1)。<button type="button"> なので
// 暗黙の form submit には繋がらない。

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

    let inflight = null; // AbortController for the active fragment fetch

    function readURLTags() {
      try {
        const u = new URL(location.href);
        return u.searchParams.getAll('tag');
      } catch {
        return [];
      }
    }

    // 当該 URL の tag リストを toggle した URL を返す。tag 以外のクエリは
    // 触らない (要件 3.2)。tag が空配列になったら "tag" パラメータ自体を
    // 落とす (要件 3.3 / 2.5)。
    function buildToggledURL(normalizedName) {
      const u = new URL(location.href);
      const current = u.searchParams.getAll('tag');
      const set = new Set(current);
      if (set.has(normalizedName)) {
        set.delete(normalizedName);
      } else {
        set.add(normalizedName);
      }
      // tag を全部消してから入れ直す（順序を含めて URLSearchParams の
      // 既存値ごと洗い替える）。
      u.searchParams.delete('tag');
      for (const t of set) {
        u.searchParams.append('tag', t);
      }
      return u;
    }

    // タグボタン群とサイドバーのチェックボックス群を、与えられた URL の
    // tag 状態に揃える (要件 2.3 / 5.2 / 5.3)。
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
      // (NFR 1.2, OQ-(c))。
      if (inflight) {
        try { inflight.abort(); } catch { /* noop */ }
      }
      const ctrl = (typeof win.AbortController === 'function') ? new win.AbortController() : null;
      inflight = ctrl;

      try {
        const res = await fetchImpl(targetURL, {
          method: 'GET',
          credentials: 'same-origin',
          headers: { 'X-Requested-With': 'ItemsFragment' },
          signal: ctrl ? ctrl.signal : undefined,
        });
        if (!res || !res.ok) return;
        const html = await res.text();
        // 一気に差し替え (ちらつき防止 NFR 1.2)。差し替え後、新しい
        // フラグメント中のタグボタンも URL と整合させる。
        region.innerHTML = html;
        // 差し替え直後はサーバ側がレンダリング時点で aria-pressed を付けて
        // くるが、フェッチ前後で URL とのズレが無いことを念押しする
        // (将来サーバ側で状態の付与漏れがあっても JS で補正する)。
        const tags = readURLTags();
        syncControls(tags);
      } catch {
        // ネットワーク失敗時は前回結果を維持する (NFR 1.2)。
      } finally {
        if (inflight === ctrl) inflight = null;
      }
    }

    function commitToggle(normalizedName) {
      if (!normalizedName) return;
      const newURL = buildToggledURL(normalizedName);
      const tags = newURL.searchParams.getAll('tag');

      // pushState: 戻るで前の絞り込みに戻れる (OQ-(a))。
      try {
        history.pushState({ tag: tags, source: 'items_tags' }, '', newURL.toString());
      } catch {
        // history が使えない環境ではフェッチだけ行う。
      }

      // 即時に UI を更新（フェッチの完了を待たない）して、ユーザに
      // 「絞り込みを開始した」ことを 300ms 以内に提示する (NFR 1.1)。
      syncControls(tags);

      void refreshFragment(newURL.toString());
    }

    // タグボタンの click を delegated でハンドリング (フラグメント差し替えで
    // 要素が再生成されるため)。<button type="button"> なので Enter/Space は
    // ブラウザが click にディスパッチしてくれる (要件 4.1 / 4.2)。
    function onDocumentClick(e) {
      const target = e.target;
      if (!target || typeof target.closest !== 'function') return;
      const btn = target.closest('[data-tag-filter-toggle]');
      if (!btn) return;
      // 修飾キー付きクリックは「タブで開く」等のブラウザ既定動作を妨げない。
      // ただし <button> なのでデフォルト動作はもともと submit のみ。念のため。
      if (e.defaultPrevented) return;
      const name = btn.getAttribute('data-tag-normalized') || '';
      if (!name) return;
      e.preventDefault();
      commitToggle(name);
    }

    // サイドバーのチェックボックス操作 (既存フロー) でも、カード上の
    // 同名タグの選択中スタイルを更新する (要件 5.2)。サイドバーは form 送信で
    // 全体リロードする経路なのでフラグメント取得は呼ばない (既存 app.js
    // の auto-submit と二重発火しないように、ここでは UI 反映だけ行う)。
    function onSidebarTagChange(e) {
      const target = e.target;
      if (!target || target.tagName !== 'INPUT') return;
      if (target.type !== 'checkbox') return;
      if (target.name !== 'tag') return;
      // 現在の form 状態から「これから飛ぶ URL に含まれるタグ」を再構築
      // して、ボタン側 UI に反映する。form 自体は既存 app.js が submit する。
      const form = target.form;
      if (!form) return;
      const checked = Array.from(form.querySelectorAll('input[type="checkbox"][name="tag"]:checked'))
        .map((el) => el.value)
        .filter((v) => v && v.length > 0);
      // ボタンの aria-pressed / クラスだけを更新（チェックボックス側は
      // ユーザ操作の結果として既に正しい状態）。
      const buttons = doc.querySelectorAll('[data-tag-filter-toggle]');
      const selected = new Set(checked);
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
    }

    // popstate (戻る/進む) で URL の tag 状態に UI とリストを揃え直す
    // (要件 3.4)。
    async function onPopState() {
      const tags = readURLTags();
      syncControls(tags);
      await refreshFragment(location.href);
    }

    doc.addEventListener('click', onDocumentClick);
    doc.addEventListener('change', onSidebarTagChange);
    win.addEventListener('popstate', onPopState);

    return {
      _debug: {
        getInflight: () => inflight,
        commitToggle,
        syncControls,
        readURLTags,
        buildToggledURL,
      },
    };
  }

  if (typeof document !== 'undefined' && typeof window !== 'undefined') {
    init();
  }
})();
