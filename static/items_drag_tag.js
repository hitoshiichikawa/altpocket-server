// items_drag_tag.js
//
// /ui/items のカードからサイドバー / ボトムシートのタグ要素へのドラッグ&ドロップで
// タグを付与するモジュール (Issue #120)。
//
// 責務:
//   - カード (`[data-item-card]`) を起点にした HTML5 drag&drop で、ドロップ先タグ要素
//     (`[data-tag-drop-target]`) が表すタグを当該アイテムに付与する (Req 1.1〜1.5)。
//   - 付与は既存の `POST /v1/items/bulk-tag` を単一 item_id で呼ぶ。bulk-tag は
//     additive かつ冪等で、server 側で所有権チェック / タグ正規化を行うため、
//     既存タグ集合を読まずに Req 2.3 / 2.4（重複なし・再ドロップ成功）と
//     Req 5.3 / 5.4（所有アイテム限定・セッション失効時の非実行）を自然に満たす。
//     新規 API は追加しない (Req 2.5 / NFR 1.1)。
//   - 成功時は response の `succeeded[0].tags`（付与後の全タグ集合）でカードの chip 列を
//     再構築する (Req 1.4 / 2.2 リロード保持 / NFR 3.2)。chip 描画は
//     items_bulk_actions.js の SSR contract と完全一致させる (innerHTML 不使用)。
//   - 通信失敗 / 4xx / 5xx / response の `failed[]` 非空時は、カードの chip 表示を
//     成功状態に変えず、toast で失敗を通知する (Req 5.1 / 5.2 / 5.5)。
//   - ドラッグ中 / ドロップ先候補の視覚状態を class 付与で提示し、dragend / dragleave
//     で解除する (Req 3.1 / 3.2 / 3.3)。色のみに依存しない（テキストラベルは SSR の
//     `<span>` がそのまま担う / NFR 4.2）。
//   - タッチ環境（pointer: coarse）向け代替手段: カードの `[data-card-tag-add]` トリガを
//     表示し、tap で「tagging モード」に入って、続けてタグ要素を tap すると同一の
//     assign 関数で付与する (Req 4.1 / 4.2 / 4.3)。ドロップとロジックを共有する。
//
// 設計上の不変:
//   - **既存挙動非回帰**: 本モジュールは dragstart / dragover / dragleave / drop /
//     dragend と「tagging モード中のタグ tap」だけを扱う。通常 click（タグ絞り込み
//     トグル #117 / 一括選択 #118 / 状態タブ #119 / アクティブフィルタ #115）は
//     intercept しない (NFR 2.2〜2.4)。
//   - **イベントは document delegated**: フラグメント取得（X-Requested-With:
//     ItemsFragment）で region.innerHTML が差し替わってもカードの drag が動くよう、
//     dragstart / drop 等は document レベルで delegated に張る（items_tags.js /
//     items_status.js と同じ規約）。
//   - **プログレッシブエンハンスメント**: JS 無効環境では本ファイルが評価されず、
//     既存の単一アイテム編集経路によるタグ付与が維持される (NFR 2.1 / NFR 4.1)。
//
// drag dataTransfer のキー: text/plain に item-id を載せる（一部ブラウザは
// カスタム MIME を制限するため、最も互換性の高い text/plain を使う）。

(function () {
  'use strict';

  // ドラッグ中に dataTransfer 経由で item-id を運ぶ MIME type。
  const DT_KEY = 'text/plain';

  function init(opts) {
    const o = opts || {};
    const doc = o.document || (typeof document !== 'undefined' ? document : null);
    const win = o.window || (typeof window !== 'undefined' ? window : null);
    if (!doc || !win) return null;

    const region = doc.querySelector ? doc.querySelector('[data-items-region]') : null;
    if (!region) return null;

    const fetchImpl = o.fetch || (typeof win.fetch === 'function' ? win.fetch.bind(win) : null);
    if (!fetchImpl) return null;

    // toast 解決順序（items_bulk_actions.js と同じ規約）:
    //   1. opts.toast（テスト注入）
    //   2. win.altpocketToast（app.js の本番 UI）
    //   3. win.alert への fallback
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

    // CSRF token を meta タグから取得（items_bulk_actions.js と同じパターン）。
    function getCSRFHeaders() {
      const meta = doc.querySelector ? doc.querySelector('meta[name="csrf-token"]') : null;
      const csrf = meta ? (meta.getAttribute ? meta.getAttribute('content') : meta.content) : null;
      const h = { 'Content-Type': 'application/json' };
      if (csrf) h['X-CSRF-Token'] = csrf;
      return h;
    }

    // --- active タグフィルタ Set の算出（#117 絞り込み状態の非回帰 / NFR 2.2） ----

    // タグの空判定 / 比較で使う fallback normalize（items_bulk_actions.js と同一規約）。
    // window.altpocketNormalizeTagName が無い場合の fallback。NFKC + lowercase + trim の
    // sequence は app.js / server 側 tag.Normalize と一致する。
    function fallbackNormalize(value) {
      const trimmed = (value || '').trim();
      if (!trimmed) return '';
      if (typeof trimmed.normalize === 'function') {
        return trimmed.normalize('NFKC').toLowerCase();
      }
      return trimmed.toLowerCase();
    }

    // canonical `?tag=` repetition + legacy `?tags=csv` の両形式を見て、active な
    // タグの normalized name の Set を返す（items_bulk_actions.js と同一規約）。chip
    // 再構築時に 1 回だけ算出し、絞り込み中タグへ再ドロップしても SSR と同じ
    // is-selected / aria-pressed 状態を維持する（#117 非回帰 / NFR 2.2）。
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

    // --- chip 再構築（items_bulk_actions.js の rebuildChipsForCard と同一規約） ----

    // succeeded item の `.tags` chip 列を tag 配列で全置換する。SSR contract
    // （items_list.html line 67-79）と完全一致させ、innerHTML を使わない (NFR 5.1)。
    // activeNormalizedNames に含まれるタグは SSR と同じく is-selected / aria-pressed=true
    // で描画し、絞り込み中タグへの再ドロップで選択状態が落ちるのを防ぐ（#117 非回帰）。
    function rebuildChipsForCard(card, tags, activeNormalizedNames) {
      const activeSet = activeNormalizedNames || new Set();
      if (!card) return;
      let tagsContainer = card.querySelector ? card.querySelector('.tags') : null;
      if (!tagsContainer) {
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
        const isActive = activeSet.has(t.normalized_name);
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

    // region 内の実在カードを data-item-id 一致で線形探索する。動的な属性セレクタを
    // 組み立てないことで、外部ドラッグの text/plain（引用符等を含む任意文字列）が
    // CSS セレクタ文字列に混入して querySelector が SyntaxError を投げ、未処理の
    // rejected promise になるのを防ぐ。未知 id（外部テキスト drop 等）には null を返す。
    function findCardByID(id) {
      if (id == null || id === '') return null;
      const wanted = String(id);
      const cards = region.querySelectorAll ? region.querySelectorAll('[data-item-card]') : [];
      for (let i = 0; i < cards.length; i += 1) {
        const c = cards[i];
        const cid = c.dataset ? c.dataset.itemId
          : (c.getAttribute ? c.getAttribute('data-item-id') : '');
        if (String(cid) === wanted) return c;
      }
      return null;
    }

    // --- core: assignTag ---------------------------------------------------

    // 付与処理中のカードに busy 視覚状態を付与/解除する。fetch 開始前に同期的に
    // 付けることで、遅い通信でも「処理を開始した」フィードバックを 300ms 以内に
    // 提示する (NFR 3.1)。色のみに依存しない（aria-busy + opacity / cursor）。
    function setCardBusy(card, busy) {
      if (!card) return;
      if (card.classList) {
        if (busy) card.classList.add('is-tagging');
        else card.classList.remove('is-tagging');
      }
      if (busy) {
        if (typeof card.setAttribute === 'function') card.setAttribute('aria-busy', 'true');
      } else if (typeof card.removeAttribute === 'function') {
        card.removeAttribute('aria-busy');
      }
    }

    // item ごとの「最新付与世代」。同一カードへ複数タグを短時間に連続ドロップ /
    // タップすると複数の assignTag が同時に in-flight になる。bulk-tag は additive
    // なので新しい付与のレスポンスほど多くのタグ集合を返すが、古いレスポンスが後着
    // すると stale な部分集合で chip 列を巻き戻し、既に永続化済みの別タグを UI 上から
    // 消してしまう。各 assignTag に単調増加の世代番号を割り当て、最新世代のレスポンス
    // でのみ chip 再構築 / busy 解除を行うことで競合時の上書きを防ぐ (Req 1.4 / NFR 3.2)。
    const tagAssignGenerations = new Map();

    // 単一アイテムに単一タグを付与する。ドロップ経路 / タッチ代替経路の双方が
    // 共有する（Req 4.2 の挙動同一性）。tagName は display 名（SSR の data-tag-name）
    // を送る。bulk-tag は受け取った文字列を display 名として保持しつつ server 側で
    // NFKC + lowercase + trim による正規化を dedup/空判定に適用するため、正規化値
    // ではなく display 名を送らないと既存タグの表示名が劣化する（Req 2.6 / #115 の
    // display 名保持契約）。
    async function assignTag(itemId, tagName) {
      if (!itemId || !tagName) return;

      // この付与の世代を確定し、自分が最新世代か判定するクロージャを作る。
      const generation = (tagAssignGenerations.get(itemId) || 0) + 1;
      tagAssignGenerations.set(itemId, generation);
      const isLatestAssign = () => tagAssignGenerations.get(itemId) === generation;

      // fetch 前に同期的に busy 状態を付与する (NFR 3.1)。chip 再描画時に
      // detail.item_id 経由で再解決するが、busy は drag 元カードに付ければ十分。
      const busyCard = findCardByID(itemId);
      setCardBusy(busyCard, true);

      let res;
      try {
        res = await fetchImpl('/v1/items/bulk-tag', {
          method: 'POST',
          headers: getCSRFHeaders(),
          credentials: 'same-origin',
          body: JSON.stringify({ item_ids: [itemId], tag: tagName }),
        });
      } catch {
        // network 失敗 → カード表示を変えず通知 (Req 5.1 / 5.2)。busy 解除は最新
        // 世代でのみ行い、後続のより新しい付与が in-flight なら busy を残す。
        if (isLatestAssign()) setCardBusy(busyCard, false);
        toast.error('タグの付与に失敗しました');
        return;
      }

      const status = res ? res.status : 0;

      if (res && res.ok && status === 200) {
        let body = null;
        try { body = await res.json(); } catch { body = null; }
        const succeeded = (body && Array.isArray(body.succeeded)) ? body.succeeded : [];
        const failed = (body && Array.isArray(body.failed)) ? body.failed : [];

        // server が当該アイテムを failed に入れた（所有していない / セッション失効 /
        // 存在しない等）場合は、カード表示を変えず通知する (Req 5.5 / 5.3 / 5.4)。
        if (failed.length > 0 || succeeded.length === 0) {
          if (isLatestAssign()) setCardBusy(busyCard, false);
          toast.error('タグの付与に失敗しました');
          return;
        }

        // succeeded[0] の付与後タグ集合で chip を再構築する (Req 1.4 / 2.2)。
        // 絞り込み中タグの選択状態は URL から算出して維持する（#117 非回帰）。
        // ただし stale なレスポンス（既により新しい付与が走った）では chip を
        // 上書きしない。古い部分集合で最新付与のタグを消さないため (NFR 3.2)。
        if (isLatestAssign()) {
          const detail = succeeded[0] || {};
          const card = findCardByID(detail.item_id || itemId);
          const tags = Array.isArray(detail.tags) ? detail.tags : [];
          rebuildChipsForCard(card, tags, computeActiveNormalizedNames());
          setCardBusy(card || busyCard, false);
        }
        toast.success('タグを付与しました');
        return;
      }

      // 4xx / 5xx（401/403 セッション失効・認可エラー / 500 等）→ カード表示を
      // 変えず通知 (Req 5.1 / 5.2 / 5.3)。invalid_tag（空タグ等）は SSR の
      // data-tag-name 経由では起き得ないが、念のため失敗として通知する。
      if (isLatestAssign()) setCardBusy(busyCard, false);
      toast.error('タグの付与に失敗しました');
    }

    // --- 視覚状態の解除（Req 3.3） -----------------------------------------

    function clearDragVisuals() {
      // ドラッグ中カードの is-dragging を全解除。
      const dragging = doc.querySelectorAll ? doc.querySelectorAll('.is-dragging') : [];
      for (let i = 0; i < dragging.length; i += 1) {
        const el = dragging[i];
        if (el.classList) el.classList.remove('is-dragging');
      }
      // ドロップ先候補ハイライトを全解除。
      const over = doc.querySelectorAll ? doc.querySelectorAll('.is-drop-target') : [];
      for (let i = 0; i < over.length; i += 1) {
        const el = over[i];
        if (el.classList) el.classList.remove('is-drop-target');
      }
    }

    // --- drag&drop handlers（document delegated） --------------------------

    // dragstart: カード起点。dataTransfer に item-id を載せ、is-dragging を付与する。
    function onDragStart(e) {
      const target = e && e.target;
      if (!target || typeof target.closest !== 'function') return;
      const card = target.closest('[data-item-card]');
      if (!card) return;
      const itemId = card.dataset ? card.dataset.itemId
        : (card.getAttribute ? card.getAttribute('data-item-id') : '');
      if (!itemId) return;
      const dt = e.dataTransfer;
      if (dt && typeof dt.setData === 'function') {
        try { dt.setData(DT_KEY, itemId); } catch { /* noop */ }
        try { dt.effectAllowed = 'copy'; } catch { /* noop */ }
      }
      if (card.classList) card.classList.add('is-dragging');
    }

    // dragover: ドロップ先タグ要素の上では preventDefault して drop を許可し、
    // 候補ハイライトを付与する (Req 3.2)。
    function onDragOver(e) {
      const target = e && e.target;
      if (!target || typeof target.closest !== 'function') return;
      const dropTarget = target.closest('[data-tag-drop-target]');
      if (!dropTarget) return;
      // preventDefault しないと drop イベントが発火しない（HTML5 DnD 仕様）。
      if (typeof e.preventDefault === 'function') e.preventDefault();
      const dt = e.dataTransfer;
      if (dt) { try { dt.dropEffect = 'copy'; } catch { /* noop */ } }
      if (dropTarget.classList) dropTarget.classList.add('is-drop-target');
    }

    // dragleave: 当該ドロップ先候補のハイライトを外す (Req 3.3)。
    function onDragLeave(e) {
      const target = e && e.target;
      if (!target || typeof target.closest !== 'function') return;
      const dropTarget = target.closest('[data-tag-drop-target]');
      if (!dropTarget) return;
      if (dropTarget.classList) dropTarget.classList.remove('is-drop-target');
    }

    // drop: ドロップ先タグ要素上でのみ付与する。タグ要素以外では何もしない
    // (Req 1.5)。dataTransfer から item-id を取り、当該タグの正規化値で assignTag。
    function onDrop(e) {
      const target = e && e.target;
      if (!target || typeof target.closest !== 'function') return;
      const dropTarget = target.closest('[data-tag-drop-target]');
      if (!dropTarget) return;
      if (typeof e.preventDefault === 'function') e.preventDefault();

      const dt = e.dataTransfer;
      let itemId = '';
      if (dt && typeof dt.getData === 'function') {
        try { itemId = dt.getData(DT_KEY) || ''; } catch { itemId = ''; }
      }
      // display 名（data-tag-name）を送る。正規化値を送ると bulk-tag が display 名を
      // 入力文字列で上書きし、既存タグ表示名が劣化するため (Req 2.6 / #115 契約)。
      const tagName = dropTarget.dataset ? dropTarget.dataset.tagName
        : (dropTarget.getAttribute ? dropTarget.getAttribute('data-tag-name') : '');

      clearDragVisuals();
      if (!itemId || !tagName) return;
      // dataTransfer の text/plain は外部アプリ由来のドラッグでも任意文字列が入りうる。
      // region 内の実在カードに紐づく id のときだけ付与に進み、カード以外の外部
      // テキストを誤って bulk-tag へ送らない (Req 1.5 の対象外ドロップ no-op の延長)。
      if (!findCardByID(itemId)) return;
      void assignTag(itemId, tagName);
    }

    // dragend: ドラッグ操作が（成功/中断問わず）終わったら全視覚状態を解除する
    // (Req 3.3)。
    function onDragEnd() {
      clearDragVisuals();
    }

    // --- タッチ代替手段（Req 4.1 / 4.2 / 4.3） ----------------------------

    // tagging モード中の対象 item-id。null ならモード外。タッチ環境で
    // [data-card-tag-add] を tap すると set され、次のタグ tap で消費される。
    let pendingTouchItemId = null;

    // タッチ環境判定: pointer が coarse（指）なら touch 代替手段を表示する。
    function isTouchEnvironment() {
      if (typeof win.matchMedia === 'function') {
        try {
          const mq = win.matchMedia('(pointer: coarse)');
          if (mq && mq.matches) return true;
        } catch { /* noop */ }
      }
      // matchMedia 非対応環境の fallback。
      return ('ontouchstart' in win);
    }

    // タッチ環境では各カードの [data-card-tag-add] トリガを表示する。
    // 非タッチ環境では hidden のまま（ドラッグ&ドロップで十分なため）。
    function revealTouchTriggers() {
      const triggers = doc.querySelectorAll ? doc.querySelectorAll('[data-card-tag-add]') : [];
      for (let i = 0; i < triggers.length; i += 1) {
        const t = triggers[i];
        if (typeof t.removeAttribute === 'function') t.removeAttribute('hidden');
        if ('hidden' in t) {
          try { t.hidden = false; } catch { /* noop */ }
        }
      }
    }

    // tagging モードに入る。対象カードを視覚的にマークし、ユーザに次操作を促す。
    function enterTaggingMode(itemId) {
      pendingTouchItemId = itemId;
      const card = findCardByID(itemId);
      if (card && card.classList) card.classList.add('is-tag-target');
      toast.info('タグをタップして付与します');
    }

    // tagging モードを抜ける。視覚マークを外す。
    function exitTaggingMode() {
      pendingTouchItemId = null;
      const marked = doc.querySelectorAll ? doc.querySelectorAll('.is-tag-target') : [];
      for (let i = 0; i < marked.length; i += 1) {
        const el = marked[i];
        if (el.classList) el.classList.remove('is-tag-target');
      }
    }

    // tagging モード中にタグ以外を tap したとき、モードを維持すべき対象か判定する。
    // モバイルではタグ一覧がボトムシート内にあり、ユーザーは trigger tap のあと
    // Filters ボタン (`[data-sheet-toggle]`) を tap してシートを開いてからタグを tap
    // する。この中間 tap でモードが解除されると、続くタグ tap が付与に至らず
    // Req 4.1 / 4.2 を満たせない。シート開閉トグル・シート/サイドバーのフィルタ UI
    // 内の tap ではモードを維持し、それ以外の無関係 tap でのみ解除する（誤付与防止）。
    function shouldPreserveTaggingMode(el) {
      if (!el || typeof el.closest !== 'function') return false;
      return !!(
        el.closest('[data-sheet-toggle]') ||
        el.closest('.sheet-overlay') ||
        el.closest('.sidebar') ||
        el.closest('.tag-list')
      );
    }

    // click delegated handler。タッチ代替手段の 2 段階操作だけを扱い、通常 click
    // （絞り込みトグル等）は触らない (NFR 2.2〜2.4)。
    function onClick(e) {
      const target = e && e.target;
      if (!target || typeof target.closest !== 'function') return;

      // 1) tagging モードトリガの tap → モードに入る。
      const trigger = target.closest('[data-card-tag-add]');
      if (trigger) {
        const itemId = trigger.dataset ? trigger.dataset.itemId
          : (trigger.getAttribute ? trigger.getAttribute('data-item-id') : '');
        if (!itemId) return;
        if (typeof e.preventDefault === 'function') e.preventDefault();
        if (pendingTouchItemId === itemId) {
          // 同じトリガ再 tap → モード解除（トグル）。
          exitTaggingMode();
        } else {
          exitTaggingMode();
          enterTaggingMode(itemId);
        }
        return;
      }

      // 2) tagging モード中のタグ要素 tap → 付与（ドロップと同一 assign）。
      if (pendingTouchItemId) {
        const dropTarget = target.closest('[data-tag-drop-target]');
        if (dropTarget) {
          if (typeof e.preventDefault === 'function') e.preventDefault();
          // ドロップ経路と同一に display 名を送る (Req 4.2 の挙動同一性 / #115 契約)。
          const tagName = dropTarget.dataset ? dropTarget.dataset.tagName
            : (dropTarget.getAttribute ? dropTarget.getAttribute('data-tag-name') : '');
          const itemId = pendingTouchItemId;
          exitTaggingMode();
          if (itemId && tagName) void assignTag(itemId, tagName);
          return;
        }
        // モード中にタグ以外をタップ → 原則モード解除（誤付与防止）。ただし、
        // モバイルでタグ一覧に到達するための中間 tap（Filters ボタンでシートを開く /
        // シート・サイドバーのフィルタ UI 内の操作）ではモードを維持する (Req 4.1/4.2)。
        // トリガ自身は上で処理済みなのでここには来ない。
        if (!shouldPreserveTaggingMode(target)) exitTaggingMode();
      }
    }

    // --- register（document delegated） -----------------------------------

    if (doc.addEventListener) {
      doc.addEventListener('dragstart', onDragStart);
      doc.addEventListener('dragover', onDragOver);
      doc.addEventListener('dragleave', onDragLeave);
      doc.addEventListener('drop', onDrop);
      doc.addEventListener('dragend', onDragEnd);
      doc.addEventListener('click', onClick);
    }

    // タッチ環境なら touch trigger を表示する。非タッチ環境では SSR の hidden を尊重。
    // 加えて、検索 / タグ絞り込み / 状態タブ / ページ送りは region.innerHTML を差し替えて
    // カードを再描画するため、新カードの [data-card-tag-add] は初期 hidden に戻る。
    // MutationObserver で fragment 差し替えを観測し、再描画のたびに trigger を再表示する
    // （items_bulk_selection.js と同じ region 監視規約 / Req 4.1 / NFR 2.1）。
    let touchObserver = null;
    if (isTouchEnvironment()) {
      revealTouchTriggers();
      const ObserverCtor =
        (typeof MutationObserver === 'function') ? MutationObserver :
        ((win && typeof win.MutationObserver === 'function') ? win.MutationObserver : null);
      if (ObserverCtor && region) {
        touchObserver = new ObserverCtor(() => { revealTouchTriggers(); });
        try {
          touchObserver.observe(region, { childList: true });
        } catch { /* noop */ }
      }
    }

    return {
      _debug: {
        assignTag,
        rebuildChipsForCard,
        clearDragVisuals,
        findCardByID,
        isTouchEnvironment,
        revealTouchTriggers,
        enterTaggingMode,
        exitTaggingMode,
        getPendingTouchItemId: () => pendingTouchItemId,
      },
    };
  }

  // テスト経路から init を呼べるよう、グローバルでも公開する。
  if (typeof window !== 'undefined') {
    window.altpocketDragTagInit = init;
  }

  // 自動 init は本番経路でのみ実施する。テストは
  // `window.__altpocketDragTagSkipAutoInit = true` をセットしておき、明示的に
  // init({ ... }) を呼ぶ（auto-init による handler 重複登録を防ぐ）。
  if (typeof document !== 'undefined' && typeof window !== 'undefined') {
    if (!window.__altpocketDragTagSkipAutoInit) {
      init();
    }
  }
})();
