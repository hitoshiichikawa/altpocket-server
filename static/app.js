(() => {
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
  }

  const detailEditTagsBtn = document.querySelector('button.edit-tags');
  const detailTagsDisplay = document.getElementById('detail-tags');
  const detailTagEditor = document.getElementById('detail-tag-editor');
  const detailTagChips = document.getElementById('detail-tag-chips');
  const detailTagInput = document.getElementById('detail-tag-input');
  const detailTagSuggestions = document.getElementById('detail-tag-suggestions');
  const detailTagSaveBtn = document.getElementById('detail-tag-save');
  const detailTagCancelBtn = document.getElementById('detail-tag-cancel');

  if (
    detailEditTagsBtn &&
    detailTagsDisplay &&
    detailTagEditor &&
    detailTagChips &&
    detailTagInput &&
    detailTagSuggestions &&
    detailTagSaveBtn &&
    detailTagCancelBtn
  ) {
    const itemID = detailTagEditor.dataset.itemId || detailEditTagsBtn.dataset.itemId;
    const draftTags = [];
    const draftTagSet = new Set();
    let suggestions = [];
    let activeSuggestion = -1;

    const readDisplayTags = () =>
      Array.from(detailTagsDisplay.querySelectorAll('.chip'))
        .map((chip) => normalizeTagName(chip.textContent))
        .filter((tag) => tag.length > 0);

    const renderDisplayTags = (tags) => {
      detailTagsDisplay.innerHTML = '';
      tags.forEach((tag) => {
        const chip = document.createElement('span');
        chip.className = 'chip';
        chip.textContent = tag;
        detailTagsDisplay.appendChild(chip);
      });
    };

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

    const renderDraftTags = () => {
      detailTagChips.innerHTML = '';
      draftTags.forEach((tag, idx) => {
        const chip = document.createElement('span');
        chip.className = 'tag-chip';
        chip.textContent = tag;
        const remove = document.createElement('button');
        remove.type = 'button';
        remove.textContent = '×';
        remove.addEventListener('click', () => {
          draftTagSet.delete(tag);
          draftTags.splice(idx, 1);
          renderDraftTags();
          renderSuggestions();
        });
        chip.appendChild(remove);
        detailTagChips.appendChild(chip);
      });
    };

    const resetDraftTags = (values) => {
      draftTags.length = 0;
      draftTagSet.clear();
      values.forEach((value) => {
        const normalized = normalizeTagName(value);
        if (!normalized || draftTagSet.has(normalized)) return;
        draftTagSet.add(normalized);
        draftTags.push(normalized);
      });
      renderDraftTags();
    };

    const addDraftTag = (value) => {
      const normalized = normalizeTagName(value);
      if (!normalized || draftTagSet.has(normalized)) return false;
      draftTagSet.add(normalized);
      draftTags.push(normalized);
      renderDraftTags();
      return true;
    };

    const loadSuggestions = async () => {
      const q = detailTagInput.value.trim();
      if (!q) {
        clearSuggestions();
        return;
      }

      try {
        const res = await fetch(`/v1/tags?q=${encodeURIComponent(q)}`);
        if (!res.ok) {
          clearSuggestions();
          return;
        }

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

    const openEditor = () => {
      resetDraftTags(readDisplayTags());
      detailTagInput.value = '';
      clearSuggestions();
      detailTagsDisplay.hidden = true;
      detailTagEditor.hidden = false;
      detailEditTagsBtn.hidden = true;
      detailTagInput.focus();
    };

    const closeEditor = () => {
      detailTagInput.value = '';
      clearSuggestions();
      detailTagEditor.hidden = true;
      detailTagsDisplay.hidden = false;
      detailEditTagsBtn.hidden = false;
    };

    detailEditTagsBtn.addEventListener('click', () => {
      if (!itemID) return;
      openEditor();
    });

    detailTagCancelBtn.addEventListener('click', () => {
      closeEditor();
    });

    detailTagSaveBtn.addEventListener('click', async () => {
      if (!itemID) return;
      addDraftTag(detailTagInput.value);
      detailTagInput.value = '';
      clearSuggestions();

      detailTagSaveBtn.disabled = true;
      try {
        const res = await fetch(`/v1/items/${encodeURIComponent(itemID)}/tags`, {
          method: 'PUT',
          headers: {
            ...headers,
            'Content-Type': 'application/json',
          },
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
        if (updatedTags.length === 0 && draftTags.length > 0) {
          draftTags.forEach((tag) => {
            if (seen.has(tag)) return;
            seen.add(tag);
            updatedTags.push(tag);
          });
        }

        renderDisplayTags(updatedTags);
        closeEditor();
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

    detailTagInput.addEventListener('input', () => {
      loadSuggestions();
    });

    detailTagInput.addEventListener('blur', () => {
      window.setTimeout(() => {
        clearSuggestions();
      }, 100);
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
      if (quickAddTagInput.value.trim()) {
        addTag(quickAddTagInput.value);
        quickAddTagInput.value = '';
      }
      quickAddSuggestionsEl.innerHTML = '';
    });
  }
})();
