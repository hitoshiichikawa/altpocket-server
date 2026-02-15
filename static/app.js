(() => {
  const csrf = document.querySelector('meta[name="csrf-token"]')?.content;
  if (!csrf) return;

  const headers = {
    'X-CSRF-Token': csrf,
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

  const quickAddTagInput = document.getElementById('quick-add-tag-input');
  const quickAddTagsEl = document.getElementById('quick-add-tags');
  const quickAddSuggestionsEl = document.getElementById('quick-add-suggestions');
  const quickAddTagsValue = document.getElementById('quick-add-tags-value');

  if (quickAddTagInput && quickAddTagsEl && quickAddSuggestionsEl && quickAddTagsValue) {
    const tags = [];

    const addTag = (value) => {
      const t = value.trim();
      if (!t) return;
      if (tags.includes(t)) return;
      tags.push(t);
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
        const btn = document.createElement('button');
        btn.type = 'button';
        btn.textContent = t.name;
        btn.addEventListener('mousedown', (e) => e.preventDefault());
        btn.addEventListener('click', () => {
          addTag(t.name);
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
