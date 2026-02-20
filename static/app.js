(() => {
  const accountMenu = document.querySelector('[data-account-menu]');
  const accountMenuTrigger = accountMenu?.querySelector('.account-menu-trigger');
  if (accountMenu && accountMenuTrigger) {
    const closeAccountMenu = () => {
      accountMenu.classList.remove('open');
      accountMenuTrigger.setAttribute('aria-expanded', 'false');
    };
    const openAccountMenu = () => {
      accountMenu.classList.add('open');
      accountMenuTrigger.setAttribute('aria-expanded', 'true');
    };

    accountMenuTrigger.addEventListener('click', (e) => {
      e.stopPropagation();
      if (accountMenu.classList.contains('open')) {
        closeAccountMenu();
        return;
      }
      openAccountMenu();
    });

    document.addEventListener('click', (e) => {
      if (!accountMenu.contains(e.target)) {
        closeAccountMenu();
      }
    });

    document.addEventListener('keydown', (e) => {
      if (e.key === 'Escape') {
        closeAccountMenu();
      }
    });
  }

  const csrf = document.querySelector('meta[name="csrf-token"]')?.content;
  if (!csrf) return;

  const headers = {
    'X-CSRF-Token': csrf,
  };

  const normalizeTagName = (value) => {
    const trimmed = (value || '').trim();
    if (!trimmed) return '';
    if (typeof trimmed.normalize === 'function') {
      return trimmed.normalize('NFKC').toLowerCase();
    }
    return trimmed.toLowerCase();
  };

  document.querySelectorAll('button.refetch').forEach((btn) => {
    btn.addEventListener('click', async () => {
      const id = btn.dataset.itemId;
      if (!id) return;
      const res = await fetch(`/v1/items/${id}/refetch`, { method: 'POST', headers });
      if (res.ok) {
        alert('Refetch queued');
      } else {
        alert('Failed to queue refetch');
      }
    });
  });

  document.querySelectorAll('button.delete').forEach((btn) => {
    btn.addEventListener('click', async () => {
      const id = btn.dataset.itemId;
      if (!id) return;
      if (!confirm('Delete this item?')) return;
      const res = await fetch(`/v1/items/${id}`, { method: 'DELETE', headers });
      if (res.ok) {
        window.location = '/ui/items';
      } else {
        alert('Failed to delete');
      }
    });
  });

  const form = document.querySelector('.search-form');
  if (form) {
    form.querySelectorAll('select').forEach((sel) => {
      sel.addEventListener('change', () => form.submit());
    });

    const tagCheckboxes = form.querySelectorAll('input[type="checkbox"][name="tag"]');
    if (tagCheckboxes.length > 0) {
      let autoSubmitTimer = null;
      let submitting = false;

      const submitForm = () => {
        if (submitting) return;
        submitting = true;
        if (typeof form.requestSubmit === 'function') {
          form.requestSubmit();
          return;
        }
        form.submit();
      };

      const scheduleSubmit = () => {
        if (submitting) return;
        if (autoSubmitTimer) {
          clearTimeout(autoSubmitTimer);
        }
        autoSubmitTimer = setTimeout(() => {
          autoSubmitTimer = null;
          submitForm();
        }, 350);
      };

      form.addEventListener('submit', () => {
        submitting = true;
      });

      tagCheckboxes.forEach((checkbox) => {
        checkbox.addEventListener('change', scheduleSubmit);
      });
    }
  }

  const detailEditTagsBtn = document.querySelector('button.edit-tags');
  const detailTagEditor = document.getElementById('detail-tag-editor');
  const detailTagChips = document.getElementById('detail-tag-chips');
  const detailTagInput = document.getElementById('detail-tag-input');
  const detailTagSuggestions = document.getElementById('detail-tag-suggestions');
  const detailTagActions = document.getElementById('detail-tag-actions');
  const detailTagSaveBtn = document.getElementById('detail-tag-save');
  const detailTagCancelBtn = document.getElementById('detail-tag-cancel');

  if (
    detailEditTagsBtn &&
    detailTagEditor &&
    detailTagChips &&
    detailTagInput &&
    detailTagSuggestions &&
    detailTagActions &&
    detailTagSaveBtn &&
    detailTagCancelBtn
  ) {
    const itemID = detailTagEditor.dataset.itemId || detailEditTagsBtn.dataset.itemId;
    const draftTags = [];
    const draftTagSet = new Set();
    let originalTags = []; // 編集開始前のタグ（Cancel用）
    let suggestions = [];
    let activeSuggestion = -1;

    const clearSuggestions = () => {
      suggestions = [];
      activeSuggestion = -1;
      detailTagSuggestions.innerHTML = '';
      detailTagSuggestions.hidden = true;
    };

    const renderSuggestions = () => {
      detailTagSuggestions.innerHTML = '';
      if (suggestions.length === 0) {
        detailTagSuggestions.hidden = true;
        return;
      }
      suggestions.forEach((name, idx) => {
        const option = document.createElement('li');
        option.textContent = name;
        option.setAttribute('role', 'option');
        option.setAttribute('aria-selected', String(idx === activeSuggestion));
        option.addEventListener('mousedown', (e) => e.preventDefault());
        option.addEventListener('click', () => {
          addDraftTag(name);
          detailTagInput.value = '';
          clearSuggestions();
          detailTagInput.focus();
        });
        detailTagSuggestions.appendChild(option);
      });
      detailTagSuggestions.hidden = false;
    };

    // チップエリアを再描画する。editing=trueなら×ボタン付き、falseなら×なし
    const renderChips = (tags, editing) => {
      detailTagChips.innerHTML = '';
      tags.forEach((tag, idx) => {
        const chip = document.createElement('span');
        chip.className = editing ? 'tag-chip' : 'tag';
        chip.textContent = tag;
        if (editing) {
          const remove = document.createElement('button');
          remove.type = 'button';
          remove.textContent = '×';
          remove.addEventListener('click', () => {
            draftTagSet.delete(tag);
            draftTags.splice(idx, 1);
            renderChips(draftTags, true);
            renderSuggestions();
          });
          chip.appendChild(remove);
        }
        detailTagChips.appendChild(chip);
      });
    };

    const addDraftTag = (value) => {
      const normalized = normalizeTagName(value);
      if (!normalized || draftTagSet.has(normalized)) return false;
      draftTagSet.add(normalized);
      draftTags.push(normalized);
      renderChips(draftTags, true);
      return true;
    };

    const loadSuggestions = async () => {
      const q = detailTagInput.value.trim();
      if (!q) { clearSuggestions(); return; }
      try {
        const res = await fetch(`/v1/tags?q=${encodeURIComponent(q)}`);
        if (!res.ok) { clearSuggestions(); return; }
        const data = await res.json();
        const next = [];
        const seen = new Set();
        if (Array.isArray(data)) {
          data.forEach((item) => {
            const normalized = normalizeTagName(item?.name);
            if (!normalized || draftTagSet.has(normalized) || seen.has(normalized)) return;
            seen.add(normalized);
            next.push(normalized);
          });
        }
        suggestions = next;
        activeSuggestion = -1;
        renderSuggestions();
      } catch {
        clearSuggestions();
      }
    };

    // 編集モード開始：チップに×を付け、テキストボックスとボタンを表示
    const openEditor = () => {
      // 現在表示中の×なしチップからタグ名を読み取る
      draftTags.length = 0;
      draftTagSet.clear();
      Array.from(detailTagChips.querySelectorAll('.tag')).forEach((el) => {
        const normalized = normalizeTagName(el.textContent);
        if (!normalized || draftTagSet.has(normalized)) return;
        draftTagSet.add(normalized);
        draftTags.push(normalized);
      });
      originalTags = draftTags.slice(); // Cancel用に保存
      renderChips(draftTags, true);
      detailTagInput.value = '';
      clearSuggestions();
      detailTagInput.hidden = false;
      detailTagActions.hidden = false;
      detailEditTagsBtn.disabled = true; // 編集中は再クリック不可
      // Refetch/Deleteも編集中は無効化
      document.querySelectorAll('.item-actions button.refetch, .item-actions button.delete').forEach((btn) => { btn.disabled = true; });
      detailTagInput.focus();
    };

    // 編集モード終了：×なしチップに戻し、テキストボックスとボタンを隠す
    const closeEditor = (savedTags) => {
      detailTagInput.hidden = true;
      detailTagActions.hidden = true;
      clearSuggestions();
      detailTagInput.value = '';
      detailEditTagsBtn.disabled = false; // 編集終了後に再び押せるように
      // Refetch/Deleteも再有効化
      document.querySelectorAll('.item-actions button.refetch, .item-actions button.delete').forEach((btn) => { btn.disabled = false; });
      renderChips(savedTags, false);
    };

    detailEditTagsBtn.addEventListener('click', () => {
      if (!itemID) return;
      openEditor();
    });

    detailTagCancelBtn.addEventListener('click', () => {
      // キャンセル：編集開始前のタグに戻す
      closeEditor(originalTags);
    });

    detailTagSaveBtn.addEventListener('click', async () => {
      if (!itemID) return;
      // 入力中のテキストがあれば追加
      addDraftTag(detailTagInput.value);
      detailTagInput.value = '';
      clearSuggestions();

      detailTagSaveBtn.disabled = true;
      try {
        const res = await fetch(`/v1/items/${encodeURIComponent(itemID)}/tags`, {
          method: 'PUT',
          headers: { ...headers, 'Content-Type': 'application/json' },
          body: JSON.stringify({ tags: draftTags }),
        });
        if (!res.ok) {
          alert('Failed to update tags');
          return;
        }
        const data = await res.json().catch(() => null);
        const updatedTags = [];
        const seen = new Set();
        if (Array.isArray(data?.tags)) {
          data.tags.forEach((item) => {
            const normalized = normalizeTagName(item?.name);
            if (!normalized || seen.has(normalized)) return;
            seen.add(normalized);
            updatedTags.push(normalized);
          });
        }
        // APIレスポンスにタグがなければドラフトをそのまま使う
        const finalTags = updatedTags.length > 0 ? updatedTags : draftTags.slice();
        closeEditor(finalTags);
      } finally {
        detailTagSaveBtn.disabled = false;
      }
    });

    detailTagInput.addEventListener('keydown', (e) => {
      if (e.key === 'Enter' || e.key === ',') {
        e.preventDefault();
        if (activeSuggestion >= 0 && suggestions[activeSuggestion]) {
          addDraftTag(suggestions[activeSuggestion]);
        } else {
          addDraftTag(detailTagInput.value.replace(',', ''));
        }
        detailTagInput.value = '';
        clearSuggestions();
        return;
      }
      if (e.key === 'Tab' && suggestions.length > 0) {
        e.preventDefault();
        if (suggestions.length === 1) {
          addDraftTag(suggestions[0]);
          detailTagInput.value = '';
          clearSuggestions();
          return;
        }
        activeSuggestion = (activeSuggestion + 1 + suggestions.length) % suggestions.length;
        detailTagInput.value = suggestions[activeSuggestion];
        renderSuggestions();
        return;
      }
      if (e.key === 'ArrowDown' && suggestions.length > 0) {
        e.preventDefault();
        activeSuggestion = (activeSuggestion + 1 + suggestions.length) % suggestions.length;
        detailTagInput.value = suggestions[activeSuggestion];
        renderSuggestions();
        return;
      }
      if (e.key === 'ArrowUp' && suggestions.length > 0) {
        e.preventDefault();
        activeSuggestion = (activeSuggestion - 1 + suggestions.length) % suggestions.length;
        detailTagInput.value = suggestions[activeSuggestion];
        renderSuggestions();
        return;
      }
      if (e.key === 'Escape') {
        clearSuggestions();
      }
    });

    detailTagInput.addEventListener('input', () => { loadSuggestions(); });

    detailTagInput.addEventListener('blur', () => {
      window.setTimeout(() => { clearSuggestions(); }, 100);
    });
  }

  const quickAddTagInput = document.getElementById('quick-add-tag-input');
  const quickAddTagsEl = document.getElementById('quick-add-tags');
  const quickAddSuggestionsEl = document.getElementById('quick-add-suggestions');
  const quickAddTagsValue = document.getElementById('quick-add-tags-value');

  if (quickAddTagInput && quickAddTagsEl && quickAddSuggestionsEl && quickAddTagsValue) {
    const tags = [];
    const tagSet = new Set();

    const addTag = (value) => {
      const normalized = normalizeTagName(value);
      if (!normalized || tagSet.has(normalized)) return;
      tagSet.add(normalized);
      tags.push(normalized);
      renderTags();
    };

    const renderTags = () => {
      quickAddTagsEl.innerHTML = '';
      tags.forEach((tag, idx) => {
        const chip = document.createElement('span');
        chip.className = 'tag-chip';
        chip.textContent = tag;
        const remove = document.createElement('button');
        remove.type = 'button';
        remove.textContent = '×';
        remove.addEventListener('click', () => {
          tagSet.delete(tag);
          tags.splice(idx, 1);
          renderTags();
        });
        chip.appendChild(remove);
        quickAddTagsEl.appendChild(chip);
      });
      quickAddTagsValue.value = tags.join(',');
    };

    const renderSuggestions = (list) => {
      quickAddSuggestionsEl.innerHTML = '';
      if (!list || list.length === 0) return;
      list.forEach((t) => {
        if (!t || typeof t.name !== 'string' || !t.name) return;
        const normalized = normalizeTagName(t.name);
        if (!normalized || tagSet.has(normalized)) return;
        const btn = document.createElement('button');
        btn.type = 'button';
        btn.textContent = normalized;
        btn.addEventListener('mousedown', (e) => e.preventDefault());
        btn.addEventListener('click', () => {
          addTag(normalized);
          quickAddSuggestionsEl.innerHTML = '';
          quickAddTagInput.value = '';
          quickAddTagInput.focus();
        });
        quickAddSuggestionsEl.appendChild(btn);
      });
    };

    const parseInitialTags = (value) => {
      if (!value) return;
      value
        .split(/[,\n;\r]/)
        .map((v) => v.trim())
        .filter((v) => v.length > 0)
        .forEach((v) => addTag(v));
    };

    parseInitialTags(quickAddTagsValue.value);

    quickAddTagInput.addEventListener('keydown', (e) => {
      if (e.key === 'Enter' || e.key === ',') {
        e.preventDefault();
        addTag(quickAddTagInput.value.replace(',', ''));
        quickAddTagInput.value = '';
        quickAddSuggestionsEl.innerHTML = '';
      }
    });

    quickAddTagInput.addEventListener('input', async () => {
      const q = quickAddTagInput.value.trim();
      if (q.length < 1) {
        quickAddSuggestionsEl.innerHTML = '';
        return;
      }
      try {
        const res = await fetch(`/v1/tags?q=${encodeURIComponent(q)}`);
        if (!res.ok) {
          quickAddSuggestionsEl.innerHTML = '';
          return;
        }
        const data = await res.json();
        renderSuggestions(Array.isArray(data) ? data : []);
      } catch {
        quickAddSuggestionsEl.innerHTML = '';
      }
    });

    quickAddTagInput.addEventListener('blur', () => {
      quickAddSuggestionsEl.innerHTML = '';
    });
  }
})();
