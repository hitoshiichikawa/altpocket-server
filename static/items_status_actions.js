// items_status_actions.js
//
// /ui/items および /ui/items/<id> の状態切替ボタン (Issue #119 task 8) を司る
// 小さなモジュール。
//
// SSR で各 item-card / detail-card に追加された以下の 2 ボタンの delegated
// click を扱う:
//   - <button class="mark-read-toggle" data-item-id data-current-status>
//   - <button class="archive-toggle"   data-item-id data-current-status>
//
// カードコンテナのセレクタは一覧画面 (`.item-card`) と詳細画面 (`.detail-card`)
// の両方を対象にする（item_detail.html の状態操作ボタンも同じ遷移ロジックを
// 共有するため）。
//
// 対応 AC:
//   - Req 2.3 / 2.4 / 2.6: mark-read-toggle で unread ⇄ read / archived → unread
//   - Req 2.5 / 2.6: archive-toggle で unread/read → archived / archived → unread
//   - Req 2.7: PATCH 失敗時は data-status / data-current-status / label / aria-label /
//     badge を一切書き換えず toast.error で通知
//   - Req 2.8: 成功時に現在の status タブ条件で表示すべきでなくなった article は
//     fade-out で DOM 削除（タブ条件は [data-items-region].dataset.currentStatus）
//   - NFR 1.3: click 直後（fetch 応答前）に同期的にボタン disabled + card に
//     is-status-updating クラスを付与する視覚フィードバック
//
// 設計判断:
//   - 既存 static/app.js の delegated click handler パターン（refetch / delete）と
//     同じ構造だが、テスト戦略上 app.js を vm.createContext で評価するのは依存の
//     多さで現実的でないため、独立モジュールとして切り出す。app.js の確立済み
//     パターン（document.addEventListener('click', ...) + closest）を踏襲する。
//   - task 9 で作られる予定の static/items_status.js（タブ切替担当）と命名衝突を
//     避けるため、本ファイル名は items_status_actions.js とした（spec はファイル名を
//     明示していないため、命名は実装者裁量）。
//   - headers は最小限の Content-Type のみ + CSRF token（meta タグから取得）で十分。
//     PATCH /v1/items/<id>/status は session cookie ベース認証 + CSRF 検証で済む
//     ため app.js と同じ headers 構造を独立に持てばよい（cross-module で headers を
//     共有する仕組みは存在しない）。

(function () {
  'use strict';

  // mark-read-toggle / archive-toggle の next status 算出。
  // tasks.md task 8 / design.md の二分岐式と一致:
  //   mark-read-toggle: next = currentStatus === 'unread' ? 'read' : 'unread'
  //   archive-toggle:   next = currentStatus === 'archived' ? 'unread' : 'archived'
  function computeNext(kind, currentStatus) {
    if (kind === 'mark-read') {
      return currentStatus === 'unread' ? 'read' : 'unread';
    }
    if (kind === 'archive') {
      return currentStatus === 'archived' ? 'unread' : 'archived';
    }
    return null;
  }

  // mark-read-toggle / archive-toggle のボタン label / aria-label を、
  // 新 status に基づいて算出する。SSR テンプレート (templates/items_list.html
  // / item_detail.html) の三項分岐と完全一致させる:
  //   - mark-read-toggle:
  //       label: status === 'unread' ? 'Mark read' : 'Mark unread'
  //       aria : status === 'unread' ? '既読にする' : '未読に戻す'
  //   - archive-toggle:
  //       label: status === 'archived' ? 'Unarchive' : 'Archive'
  //       aria : status === 'archived' ? 'アーカイブ解除' : 'アーカイブする'
  function labelsFor(kind, status) {
    if (kind === 'mark-read') {
      if (status === 'unread') return { label: 'Mark read', aria: '既読にする' };
      return { label: 'Mark unread', aria: '未読に戻す' };
    }
    // archive
    if (status === 'archived') return { label: 'Unarchive', aria: 'アーカイブ解除' };
    return { label: 'Archive', aria: 'アーカイブする' };
  }

  function init(opts) {
    const o = opts || {};
    const doc = o.document || (typeof document !== 'undefined' ? document : null);
    const win = o.window || (typeof window !== 'undefined' ? window : null);
    if (!doc || !win) return null;

    const fetchImpl = o.fetch || (typeof win.fetch === 'function' ? win.fetch.bind(win) : null);
    if (!fetchImpl) return null;

    // toast 解決順序（reviewer #2 round 2 指摘 #3 反映 — 本番自動初期化で
    // window.alert に降格する notification UI 退行を回避）:
    //   1. opts.toast（テストから注入された stub）
    //   2. win.altpocketToast（app.js が window.altpocketToast = toast で公開する
    //      本番 toast UI。show / success / error / info を持つ）
    //   3. window.alert への fallback（最後の防波堤 / app.js 未ロード時のみ）
    // 2 と 3 のいずれも、本モジュール側のロード順序が app.js より前でも
    // 後でも安全に動作する（参照時点で window.altpocketToast を解決する）。
    const toast = o.toast || (function () {
      const resolve = () => (win && win.altpocketToast) || null;
      return {
        error(msg) {
          const t = resolve();
          if (t && typeof t.error === 'function') {
            t.error(msg);
            return;
          }
          if (typeof win.alert === 'function') win.alert(msg);
        },
        success(msg) {
          const t = resolve();
          if (t && typeof t.success === 'function') t.success(msg);
        },
      };
    })();

    // テストから fake setTimeout を注入できるようにする（fade-out 削除タイミング
    // を test 上で同期的に進めるため）。
    const setTimeoutImpl = o.setTimeout || (typeof win.setTimeout === 'function' ? win.setTimeout.bind(win) : null);

    // CSRF token は app.js と同じく meta タグから読む。テストでは headers
    // 経由で擬似注入可能にする。
    const headersBase = o.headers || (function () {
      const meta = doc.querySelector ? doc.querySelector('meta[name="csrf-token"]') : null;
      const csrf = meta ? (meta.getAttribute ? meta.getAttribute('content') : meta.content) : null;
      const h = { 'Content-Type': 'application/json' };
      if (csrf) h['X-CSRF-Token'] = csrf;
      return h;
    })();

    // タブ条件取得: <section data-items-region data-current-status="unread|all|archived">
    // 値が unread / archived のとき、status が一致しない item は DOM から fade-out 削除する。
    // 値が 'all' / 空 / 不在 のときは全 status を許容する（削除しない）。
    function shouldRemoveAfterTransition(card, nextStatus) {
      const region = doc.querySelector ? doc.querySelector('[data-items-region]') : null;
      if (!region) return false;
      const tab = (region.dataset ? region.dataset.currentStatus : null) || '';
      if (tab === 'unread' && nextStatus !== 'unread') return true;
      if (tab === 'archived' && nextStatus !== 'archived') return true;
      // 'all' は unread + read の和集合（archived 除外）。archived 化された item は消す。
      if (tab === 'all' && nextStatus === 'archived') return true;
      return false;
    }

    // fade-out して DOM から削除する。アニメーション時間は CSS 側 (task 10) で
    // 制御することを想定し、本モジュールは setTimeout 経由で remove を呼ぶ。
    function fadeOutAndRemove(article) {
      if (!article) return;
      if (article.classList && typeof article.classList.add === 'function') {
        article.classList.add('fade-out');
      }
      const doRemove = () => {
        if (typeof article.remove === 'function') {
          article.remove();
        } else if (article.parentNode && typeof article.parentNode.removeChild === 'function') {
          article.parentNode.removeChild(article);
        }
      };
      if (setTimeoutImpl) {
        setTimeoutImpl(doRemove, 300);
      } else {
        doRemove();
      }
    }

    // card 内の対象 2 ボタンを取得して新 status に合わせて
    // data-current-status / label / aria-label を更新する。
    // 「同一カード内の 2 ボタン両方の data-current-status を更新」は Reviewer
    // 指摘 #1 への必須対応 — stale data-current-status を残置すると次回 click で
    // 誤った遷移を送る。
    function updateCardButtonsAndBadge(card, nextStatus) {
      if (!card) return;
      // card 自体の data-status を更新
      if (typeof card.setAttribute === 'function') {
        card.setAttribute('data-status', nextStatus);
      }
      // 同一カード内の data-current-status を持つ全ボタンを更新
      // （mark-read-toggle と archive-toggle の 2 ボタン両方）
      const sameCardButtons = card.querySelectorAll ? card.querySelectorAll('[data-current-status]') : [];
      for (let i = 0; i < sameCardButtons.length; i += 1) {
        const b = sameCardButtons[i];
        if (typeof b.setAttribute === 'function') {
          b.setAttribute('data-current-status', nextStatus);
        }
        // label / aria-label を更新（mark-read-toggle / archive-toggle で分岐）
        const isMarkRead = b.classList && typeof b.classList.contains === 'function' && b.classList.contains('mark-read-toggle');
        const isArchive = b.classList && typeof b.classList.contains === 'function' && b.classList.contains('archive-toggle');
        if (isMarkRead) {
          const labels = labelsFor('mark-read', nextStatus);
          if ('textContent' in b) b.textContent = labels.label;
          if (typeof b.setAttribute === 'function') b.setAttribute('aria-label', labels.aria);
        } else if (isArchive) {
          const labels = labelsFor('archive', nextStatus);
          if ('textContent' in b) b.textContent = labels.label;
          if (typeof b.setAttribute === 'function') b.setAttribute('aria-label', labels.aria);
        }
      }
      // badge を更新（item-status-badge は 1 件想定）
      const badge = card.querySelector ? card.querySelector('.item-status-badge') : null;
      if (badge) {
        if (typeof badge.setAttribute === 'function') {
          badge.setAttribute('data-status', nextStatus);
          badge.setAttribute('aria-label', '状態: ' + nextStatus);
        }
        if ('textContent' in badge) badge.textContent = nextStatus;
      }
    }

    // PATCH を投げて成功 / 失敗を分岐する。
    // 成功時: updateCardButtonsAndBadge → タブ条件チェック → fade-out 削除
    // 失敗時: data-status / data-current-status / label / aria-label / badge を
    //         一切触らず toast.error のみ
    // いずれの場合も disabled と is-status-updating は解除する（NFR 1.3）。
    async function performTransition(btn, kind, id, nextStatus, card) {
      try {
        const res = await fetchImpl('/v1/items/' + encodeURIComponent(id) + '/status', {
          method: 'PATCH',
          headers: headersBase,
          credentials: 'same-origin',
          body: JSON.stringify({ status: nextStatus }),
        });
        if (res && res.ok) {
          // 成功時のみ DOM を更新
          updateCardButtonsAndBadge(card, nextStatus);
          // タブ条件で非表示にすべきなら fade-out して remove
          if (shouldRemoveAfterTransition(card, nextStatus)) {
            fadeOutAndRemove(card);
          }
        } else {
          toast.error('状態の更新に失敗しました');
        }
      } catch {
        toast.error('状態の更新に失敗しました');
      } finally {
        // disabled / is-status-updating の解除は成功・失敗共通
        if (btn && 'disabled' in btn) btn.disabled = false;
        if (card && card.classList && typeof card.classList.remove === 'function') {
          card.classList.remove('is-status-updating');
        }
      }
    }

    // delegated click handler。mark-read-toggle / archive-toggle のみを担当する。
    // 同期処理として（fetch を呼ぶ前に）ボタン disabled + card に
    // is-status-updating を即時付与する（NFR 1.3: synchronous visual ack）。
    function onClick(e) {
      const target = e.target;
      if (!target || typeof target.closest !== 'function') return;

      let kind = null;
      let btn = target.closest('button.mark-read-toggle');
      if (btn) {
        kind = 'mark-read';
      } else {
        btn = target.closest('button.archive-toggle');
        if (btn) kind = 'archive';
      }
      if (!btn || !kind) return;

      if (btn.disabled) return;
      const id = btn.dataset ? btn.dataset.itemId : null;
      if (!id) return;
      const currentStatus = btn.dataset ? btn.dataset.currentStatus : null;
      if (!currentStatus) return;

      const nextStatus = computeNext(kind, currentStatus);
      if (!nextStatus) return;

      // 同期 visual ack（NFR 1.3）— PATCH 応答前に DOM を更新する
      btn.disabled = true;
      // 一覧画面は `.item-card`、詳細画面は `.detail-card` をカードコンテナとして扱う。
      // 詳細画面ボタン (templates/item_detail.html) で `.item-card` のみを見ると
      // card が null になり、成功後に data-current-status / label / aria-label /
      // badge が更新されず同じ遷移を再送できてしまうため、両方をマッチさせる。
      const card = typeof btn.closest === 'function'
        ? btn.closest('.item-card, .detail-card')
        : null;
      if (card && card.classList && typeof card.classList.add === 'function') {
        card.classList.add('is-status-updating');
      }

      // 非同期 PATCH をトリガ（呼び出し側は await しない / 内部で finally）
      void performTransition(btn, kind, id, nextStatus, card);
    }

    doc.addEventListener('click', onClick);

    return {
      _debug: {
        computeNext,
        labelsFor,
        updateCardButtonsAndBadge,
        shouldRemoveAfterTransition,
        fadeOutAndRemove,
      },
    };
  }

  if (typeof document !== 'undefined' && typeof window !== 'undefined') {
    init();
  }
})();
