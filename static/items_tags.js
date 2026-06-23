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
//   この AbortController は `[data-items-region]` 要素上の
//   `__itemsFragmentInflight` に置き、`items_search.js` と **共有** する。
//   タグクリックは検索 debounce 中の保留 fetch を、検索 fetch はタグクリック
//   起源の保留 fetch を、それぞれ相互に abort する。これにより、後着の
//   レスポンスが先着の絞り込み結果を上書きする race（URL とボタン状態は
//   タグ済みなのに一覧だけ古い検索結果に戻る）を防ぐ (要件 2.1 / 5.3)。
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

    // フラグメント取得の AbortController は items_search.js と共有する
    // ため、region 要素上の slot に置く。両モジュールが同じ slot を
    // 参照することで、片方が新しい fetch を開始したときに他方の保留
    // fetch も abort される。
    if (!region.__itemsFragmentInflight) {
      region.__itemsFragmentInflight = { ctrl: null };
    }
    const coord = region.__itemsFragmentInflight;

    // サーバ側 (internal/tag/tag.go の Normalize) と完全一致するタグ正規化。
    // server: strings.ToLower(NFKC.String(strings.TrimSpace(name)))。
    // サーバが ?tag=Go を内部で go として絞り込む一方、JS が raw 値 "Go" の
    // ままトグル判定すると、選択中タグの解除が「再追加」になってしまう
    // (?tag=Go&tag=go)。URL から読んだ tag・カードボタン・チェックボックスの
    // value (テンプレートが既に NormalizedName を出力) と JS のトグル比較を
    // すべて正規化済み値で揃えるための関数 (#117 codex 指摘 1)。
    function normalizeTag(raw) {
      if (raw == null) return '';
      let s = String(raw).trim();
      if (s === '') return '';
      // String.prototype.normalize('NFKC') は Go の norm.NFKC.String と等価。
      if (typeof s.normalize === 'function') {
        s = s.normalize('NFKC');
      }
      return s.toLowerCase();
    }

    // URL の ?tag= 値を「サーバ側の正規化規則で正規化し、空・重複を畳んだ」
    // リストとして返す。これにより set.has() / トグル判定 / カード表示が
    // 正規化済み値で一貫する (#117 codex 指摘 1)。
    function readURLTags() {
      try {
        const u = new URL(location.href);
        const out = [];
        const seen = new Set();
        for (const raw of u.searchParams.getAll('tag')) {
          const norm = normalizeTag(raw);
          if (norm === '' || seen.has(norm)) continue;
          seen.add(norm);
          out.push(norm);
        }
        return out;
      } catch {
        return [];
      }
    }

    // 当該 URL の tag リストを toggle した URL を返す。tag 以外のクエリは
    // 触らない (要件 3.2)。tag が空配列になったら "tag" パラメータ自体を
    // 落とす (要件 3.3 / 2.5)。
    //
    // 既存 URL の ?tag= は正規化済み値に畳んでから Set 化する。これにより、
    // サーバが ?tag=Go を go として選択中にしている状態でカードの go ボタン
    // (data-tag-normalized="go") をクリックしても、set.has("go") が true に
    // なって正しく「解除」される (#117 codex 指摘 1)。正規化前は raw "Go" と
    // "go" が別物扱いされ、解除ではなく ?tag=Go&tag=go の追加になっていた。
    function buildToggledURL(normalizedName) {
      const u = new URL(location.href);
      const set = new Set(readURLTags());
      if (set.has(normalizedName)) {
        set.delete(normalizedName);
      } else {
        set.add(normalizedName);
      }
      // tag を全部消してから入れ直す（順序を含めて URLSearchParams の
      // 既存値ごと洗い替える）。入れ直す値は正規化済み (set の中身)。
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
      // (NFR 1.2, OQ-(c))。coord は items_search.js と共有しているので、
      // 検索 debounce 由来の保留 fetch も同時に abort される (要件 2.1 /
      // 5.3 の race 対策)。
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
        if (coord.ctrl === ctrl) coord.ctrl = null;
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

    // デスクトップのサイドバーのチェックボックス操作 (既存フロー) でも、
    // カード上の同名タグの選択中スタイルを更新する (要件 5.1 / 5.2)。
    // サイドバーは change で即時に app.js が form を auto-submit し全体
    // リロードする経路なので、カード表示を即時に追従させても URL・一覧と
    // ズレない。フラグメント取得は呼ばない (app.js の auto-submit と二重
    // 発火しないように、ここでは UI 反映だけ行う)。
    //
    // モバイルのボトムシート (templates/items.html の #filter-sheet 内 form)
    // は change では絞り込みが変わらず、Apply ボタンを押して初めて submit
    // される。Apply 前にカードの aria-pressed / is-selected を変えてしまうと、
    // URL・一覧は未変更なのにカードだけ選択中表示になり、要件 1.4 / 5.2 が
    // 言う「現在の絞り込み条件」とズレる。そのため、auto-submit 対象である
    // デスクトップのサイドバー form (#filter-form) の change にだけ追従し、
    // ボトムシートの Apply 前の change ではカード表示を更新しない
    // (#117 codex 指摘 2)。
    function isDesktopSidebarForm(form) {
      if (!form) return false;
      // テンプレート上、auto-submit されるデスクトップ form は
      // id="filter-form"。モバイルのボトムシート form は id を持たない。
      const id = (typeof form.getAttribute === 'function') ? form.getAttribute('id') : form.id;
      return id === 'filter-form';
    }

    function onSidebarTagChange(e) {
      const target = e.target;
      if (!target || target.tagName !== 'INPUT') return;
      if (target.type !== 'checkbox') return;
      if (target.name !== 'tag') return;
      // 現在の form 状態から「これから飛ぶ URL に含まれるタグ」を再構築
      // して、ボタン側 UI に反映する。form 自体は既存 app.js が submit する。
      const form = target.form;
      if (!form) return;
      // 即時に絞り込みが適用されない (Apply 待ち) モバイルのボトムシートの
      // change ではカード表示を更新しない (#117 codex 指摘 2)。
      if (!isDesktopSidebarForm(form)) return;
      const checked = Array.from(form.querySelectorAll('input[type="checkbox"][name="tag"]:checked'))
        .map((el) => normalizeTag(el.value))
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
        getInflight: () => coord.ctrl,
        commitToggle,
        syncControls,
        readURLTags,
        buildToggledURL,
        normalizeTag,
        isDesktopSidebarForm,
      },
    };
  }

  if (typeof document !== 'undefined' && typeof window !== 'undefined') {
    init();
  }
})();
