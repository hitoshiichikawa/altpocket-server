(() => {
  /* =============================================
     Utility: Toast Notification System
     ============================================= */
  const toast = {
    _container: null,
    _getContainer() {
      if (!this._container) {
        this._container = document.getElementById('toast-container');
      }
      return this._container;
    },
    show(message, type = 'info', duration = 4000) {
      const container = this._getContainer();
      if (!container) return;

      const icons = {
        success: '\u2713',
        danger: '\u2717',
        info: '\u2139',
      };

      const el = document.createElement('div');
      el.className = `toast toast-${type}`;
      el.setAttribute('role', 'alert');
      el.innerHTML = `
        <span class="toast-icon">${icons[type] || icons.info}</span>
        <span class="toast-message">${this._escape(message)}</span>
        <button type="button" class="toast-dismiss" aria-label="Dismiss">&times;</button>
      `;

      const dismiss = () => {
        el.classList.add('toast-exit');
        el.addEventListener('animationend', () => el.remove(), { once: true });
      };

      el.querySelector('.toast-dismiss').addEventListener('click', dismiss);

      container.appendChild(el);

      if (duration > 0) {
        setTimeout(dismiss, duration);
      }
    },
    success(msg, dur) { this.show(msg, 'success', dur); },
    error(msg, dur) { this.show(msg, 'danger', dur); },
    info(msg, dur) { this.show(msg, 'info', dur); },
    _escape(str) {
      const div = document.createElement('div');
      div.textContent = str;
      return div.innerHTML;
    },
  };

  /* =============================================
     Utility: Confirmation Dialog (replaces confirm())
     ============================================= */
  const confirm = (() => {
    const overlay = document.getElementById('confirm-overlay');
    const titleEl = document.getElementById('confirm-title');
    const descEl = document.getElementById('confirm-desc');
    const actionBtn = document.getElementById('confirm-action');
    const cancelBtn = document.getElementById('confirm-cancel');

    if (!overlay || !titleEl || !descEl || !actionBtn || !cancelBtn) {
      return { show: (_, __, cb) => { if (cb) cb(); } };
    }

    let currentResolve = null;

    const close = () => {
      overlay.classList.remove('open');
      overlay.setAttribute('aria-hidden', 'true');
      currentResolve = null;
    };

    cancelBtn.addEventListener('click', () => {
      close();
    });

    overlay.addEventListener('click', (e) => {
      if (e.target === overlay) close();
    });

    document.addEventListener('keydown', (e) => {
      if (e.key === 'Escape' && overlay.classList.contains('open')) {
        close();
      }
    });

    return {
      show(title, description, onConfirm, actionLabel = 'Delete', actionClass = 'btn-danger') {
        titleEl.textContent = title;
        descEl.textContent = description;
        actionBtn.textContent = actionLabel;
        actionBtn.className = actionClass;

        // Remove old listener
        const newBtn = actionBtn.cloneNode(true);
        actionBtn.parentNode.replaceChild(newBtn, actionBtn);

        newBtn.addEventListener('click', () => {
          close();
          if (onConfirm) onConfirm();
        });

        // Reassign for future calls
        Object.defineProperty(confirm, '_actionBtn', { value: newBtn, writable: true });

        overlay.classList.add('open');
        overlay.setAttribute('aria-hidden', 'false');
        newBtn.focus();
      },
    };
  })();

  /* =============================================
     Account Menu
     ============================================= */
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

  /* =============================================
     Action Menu (More ... on detail page)
     ============================================= */
  document.querySelectorAll('[data-action-menu]').forEach((menu) => {
    const trigger = menu.querySelector('[data-action-menu-trigger]');
    if (!trigger) return;

    const close = () => {
      menu.classList.remove('open');
      trigger.setAttribute('aria-expanded', 'false');
    };

    trigger.addEventListener('click', (e) => {
      e.stopPropagation();
      if (menu.classList.contains('open')) {
        close();
      } else {
        menu.classList.add('open');
        trigger.setAttribute('aria-expanded', 'true');
      }
    });

    document.addEventListener('click', (e) => {
      if (!menu.contains(e.target)) close();
    });

    document.addEventListener('keydown', (e) => {
      if (e.key === 'Escape') close();
    });
  });

  /* =============================================
     Mobile Slide-over Navigation
     ============================================= */
  const navOverlay = document.querySelector('[data-nav-overlay]');
  const navToggle = document.querySelector('[data-nav-toggle]');

  if (navOverlay && navToggle) {
    const openNav = () => {
      navOverlay.classList.add('open');
      navOverlay.setAttribute('aria-hidden', 'false');
      // Focus first link
      const firstLink = navOverlay.querySelector('.nav-drawer-links a');
      if (firstLink) firstLink.focus();
    };

    const closeNav = () => {
      navOverlay.classList.remove('open');
      navOverlay.setAttribute('aria-hidden', 'true');
      navToggle.focus();
    };

    navToggle.addEventListener('click', () => {
      if (navOverlay.classList.contains('open')) {
        closeNav();
      } else {
        openNav();
      }
    });

    // Close on overlay click (outside drawer)
    navOverlay.addEventListener('click', (e) => {
      if (e.target === navOverlay) closeNav();
    });

    document.addEventListener('keydown', (e) => {
      if (e.key === 'Escape' && navOverlay.classList.contains('open')) {
        closeNav();
      }
    });

    // Swipe to close
    let touchStartX = 0;
    navOverlay.addEventListener('touchstart', (e) => {
      touchStartX = e.touches[0].clientX;
    }, { passive: true });

    navOverlay.addEventListener('touchend', (e) => {
      const touchEndX = e.changedTouches[0].clientX;
      if (touchStartX - touchEndX > 60) {
        closeNav();
      }
    }, { passive: true });
  }

  /* =============================================
     Bottom Sheet (Mobile Filters)
     ============================================= */
  document.querySelectorAll('[data-sheet-toggle]').forEach((btn) => {
    const sheetId = btn.dataset.sheetToggle;
    const sheetOverlay = document.getElementById(sheetId);
    if (!sheetOverlay) return;

    const openSheet = () => {
      sheetOverlay.classList.add('open');
      sheetOverlay.setAttribute('aria-hidden', 'false');
    };

    const closeSheet = () => {
      sheetOverlay.classList.remove('open');
      sheetOverlay.setAttribute('aria-hidden', 'true');
    };

    btn.addEventListener('click', openSheet);

    sheetOverlay.addEventListener('click', (e) => {
      if (e.target === sheetOverlay) closeSheet();
    });

    document.addEventListener('keydown', (e) => {
      if (e.key === 'Escape' && sheetOverlay.classList.contains('open')) {
        closeSheet();
      }
    });

    // Drag handle - swipe down to close
    const handle = sheetOverlay.querySelector('.sheet-handle');
    if (handle) {
      let startY = 0;
      handle.addEventListener('touchstart', (e) => {
        startY = e.touches[0].clientY;
      }, { passive: true });

      handle.addEventListener('touchend', (e) => {
        const endY = e.changedTouches[0].clientY;
        if (endY - startY > 50) closeSheet();
      }, { passive: true });
    }
  });

  /* =============================================
     Theme Toggle
     ============================================= */
  const themeToggle = document.querySelector('[data-theme-toggle]');
  if (themeToggle) {
    themeToggle.querySelectorAll('input[name="theme"]').forEach((radio) => {
      radio.addEventListener('change', () => {
        const theme = radio.value;
        document.documentElement.setAttribute('data-theme', theme);
        localStorage.setItem('altpocket-theme', theme);
        // Update theme-color meta
        const metaDark = document.querySelector('meta[name="theme-color"][media*="dark"]');
        const metaLight = document.querySelector('meta[name="theme-color"][media*="light"]');
        if (metaDark) metaDark.content = theme === 'dark' ? '#0a0a0a' : '#f5f5f7';
        if (metaLight) metaLight.content = theme === 'light' ? '#f5f5f7' : '#0a0a0a';
      });
    });
  }

  // Restore theme from localStorage + sync settings radio
  const savedTheme = localStorage.getItem('altpocket-theme');
  if (savedTheme) {
    document.documentElement.setAttribute('data-theme', savedTheme);
    const radio = document.getElementById(`theme-${savedTheme}`);
    if (radio) radio.checked = true;
  }

  /* =============================================
     Shortcuts Dialog (Settings page)
     ============================================= */
  const shortcutsBtn = document.querySelector('[data-shortcuts-toggle]');
  const shortcutsOverlay = document.getElementById('shortcuts-overlay');
  if (shortcutsBtn && shortcutsOverlay) {
    shortcutsBtn.addEventListener('click', () => {
      shortcutsOverlay.classList.add('open');
      shortcutsOverlay.setAttribute('aria-hidden', 'false');
    });
    shortcutsOverlay.addEventListener('click', (e) => {
      if (e.target === shortcutsOverlay) {
        shortcutsOverlay.classList.remove('open');
        shortcutsOverlay.setAttribute('aria-hidden', 'true');
      }
    });
  }

  /* =============================================
     CSRF & API helpers
     ============================================= */
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

  /* =============================================
     Refetch Buttons (with toast)
     ============================================= */
  document.querySelectorAll('button.refetch').forEach((btn) => {
    btn.addEventListener('click', async () => {
      const id = btn.dataset.itemId;
      if (!id) return;
      btn.disabled = true;
      try {
        const res = await fetch(`/v1/items/${id}/refetch`, { method: 'POST', headers });
        if (res.ok) {
          toast.success('Refetch queued');
        } else {
          toast.error('Failed to queue refetch');
        }
      } catch {
        toast.error('Network error');
      } finally {
        btn.disabled = false;
      }
    });
  });

  /* =============================================
     Delete Buttons (with confirmation dialog)
     ============================================= */
  document.querySelectorAll('button.delete').forEach((btn) => {
    btn.addEventListener('click', () => {
      const id = btn.dataset.itemId;
      if (!id) return;

      confirm.show(
        'Delete article?',
        'This action cannot be undone.',
        async () => {
          try {
            const res = await fetch(`/v1/items/${id}`, { method: 'DELETE', headers });
            if (res.ok) {
              toast.success('Article deleted');
              setTimeout(() => { window.location = '/ui/items'; }, 500);
            } else {
              toast.error('Failed to delete');
            }
          } catch {
            toast.error('Network error');
          }
        },
        'Delete',
        'btn-danger'
      );
    });
  });

  /* =============================================
     Filter Form (auto-submit on change)
     ============================================= */
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

  /* =============================================
     Detail Page: Tag Editor
     ============================================= */
  const detailEditTagsBtn = document.querySelector('button.edit-tags');
  const detailTagEditor = document.getElementById('detail-tag-editor');
  const detailTagChips = document.getElementById('detail-tag-chips');
  const detailTagInput = document.getElementById('detail-tag-input');
  const detailTagSuggestions = document.getElementById('detail-tag-suggestions');
  const detailTagActions = document.getElementById('detail-tag-actions');
  const detailTagSaveBtn = document.getElementById('detail-tag-save');
  const detailTagCancelBtn = document.getElementById('detail-tag-cancel');
  const detailTitle = document.getElementById('detail-title');
  const detailTitleInput = document.getElementById('detail-title-input');

  if (
    detailEditTagsBtn &&
    detailTagEditor &&
    detailTagChips &&
    detailTagInput &&
    detailTagSuggestions &&
    detailTagActions &&
    detailTagSaveBtn &&
    detailTagCancelBtn &&
    detailTitle &&
    detailTitleInput
  ) {
    const itemID = detailTagEditor.dataset.itemId || detailEditTagsBtn.dataset.itemId;
    const draftTags = [];
    const draftTagSet = new Set();
    let originalTags = [];
    let originalTitle = '';
    let suggestions = [];
    let activeSuggestion = -1;

    const clearSuggestions = () => {
      suggestions = [];
      activeSuggestion = -1;
      detailTagSuggestions.innerHTML = '';
      detailTagSuggestions.hidden = true;
      detailTagInput.setAttribute('aria-expanded', 'false');
    };

    const renderSuggestions = () => {
      detailTagSuggestions.innerHTML = '';
      if (suggestions.length === 0) {
        detailTagSuggestions.hidden = true;
        detailTagInput.setAttribute('aria-expanded', 'false');
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
      detailTagInput.setAttribute('aria-expanded', 'true');
    };

    const renderChips = (tags, editing) => {
      detailTagChips.innerHTML = '';
      tags.forEach((tag, idx) => {
        const chip = document.createElement('span');
        chip.className = editing ? 'tag-chip' : 'tag';
        chip.setAttribute('role', 'listitem');
        chip.textContent = tag;
        if (editing) {
          const remove = document.createElement('button');
          remove.type = 'button';
          remove.textContent = '\u00D7';
          remove.setAttribute('aria-label', `Remove tag ${tag}`);
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

    const openEditor = () => {
      originalTitle = detailTitle.textContent;
      detailTitleInput.value = originalTitle;
      detailTitle.hidden = true;
      detailTitleInput.hidden = false;

      draftTags.length = 0;
      draftTagSet.clear();
      Array.from(detailTagChips.querySelectorAll('.tag')).forEach((el) => {
        const normalized = normalizeTagName(el.textContent);
        if (!normalized || draftTagSet.has(normalized)) return;
        draftTagSet.add(normalized);
        draftTags.push(normalized);
      });
      originalTags = draftTags.slice();
      renderChips(draftTags, true);
      detailTagInput.value = '';
      clearSuggestions();
      detailTagInput.hidden = false;
      detailTagActions.hidden = false;
      detailEditTagsBtn.disabled = true;
      // Hide the header actions and show edit mode actions
      const headerActions = document.getElementById('detail-header-actions');
      if (headerActions) headerActions.style.display = 'none';
      detailTitleInput.focus();
    };

    const closeEditor = (savedTags, savedTitle) => {
      detailTitle.textContent = savedTitle;
      detailTitle.hidden = false;
      detailTitleInput.hidden = true;
      detailTagInput.hidden = true;
      detailTagActions.hidden = true;
      clearSuggestions();
      detailTagInput.value = '';
      detailEditTagsBtn.disabled = false;
      const headerActions = document.getElementById('detail-header-actions');
      if (headerActions) headerActions.style.display = '';
      renderChips(savedTags, false);
    };

    detailEditTagsBtn.addEventListener('click', () => {
      if (!itemID) return;
      openEditor();
    });

    detailTagCancelBtn.addEventListener('click', () => {
      closeEditor(originalTags, originalTitle);
    });

    detailTagSaveBtn.addEventListener('click', async () => {
      if (!itemID) return;
      addDraftTag(detailTagInput.value);
      detailTagInput.value = '';
      clearSuggestions();

      const titleValue = detailTitleInput.value.trim();
      if (!titleValue) {
        toast.error('Title cannot be empty');
        detailTitleInput.focus();
        return;
      }

      detailTagSaveBtn.disabled = true;
      detailTagSaveBtn.classList.add('btn-loading');
      try {
        const res = await fetch(`/v1/items/${encodeURIComponent(itemID)}`, {
          method: 'PATCH',
          headers: { ...headers, 'Content-Type': 'application/json' },
          body: JSON.stringify({ title: titleValue, tags: draftTags }),
        });
        if (!res.ok) {
          toast.error('Failed to update');
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
        const finalTags = updatedTags.length > 0 ? updatedTags : draftTags.slice();
        const finalTitle = data?.title || titleValue;
        closeEditor(finalTags, finalTitle);
        document.title = finalTitle + ' | altpocket';
        toast.success('Changes saved');
      } finally {
        detailTagSaveBtn.disabled = false;
        detailTagSaveBtn.classList.remove('btn-loading');
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

  /* =============================================
     Quick Add: Tag Input
     ============================================= */
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
        chip.setAttribute('role', 'listitem');
        chip.textContent = tag;
        const remove = document.createElement('button');
        remove.type = 'button';
        remove.textContent = '\u00D7';
        remove.setAttribute('aria-label', `Remove tag ${tag}`);
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

  /* =============================================
     Keyboard Shortcuts
     ============================================= */
  document.addEventListener('keydown', (e) => {
    // Don't trigger shortcuts when typing in inputs
    const tag = e.target.tagName;
    if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || e.target.isContentEditable) {
      return;
    }

    // Don't trigger on modifier combos (except Cmd+Enter for forms)
    if (e.ctrlKey || e.altKey || e.metaKey) return;

    switch (e.key) {
      case '/': {
        e.preventDefault();
        const searchInput = document.querySelector('.search-form input[type="text"], .search-bar input');
        if (searchInput) searchInput.focus();
        break;
      }
      case 'n': {
        window.location.href = '/ui/quick-add';
        break;
      }
      case '?': {
        const overlay = document.getElementById('shortcuts-overlay');
        if (overlay) {
          overlay.classList.toggle('open');
          overlay.setAttribute('aria-hidden', overlay.classList.contains('open') ? 'false' : 'true');
        }
        break;
      }
      case 'j':
      case 'k': {
        const cards = Array.from(document.querySelectorAll('.item-card'));
        if (cards.length === 0) return;
        const focused = document.activeElement?.closest('.item-card');
        let idx = focused ? cards.indexOf(focused) : -1;
        if (e.key === 'j') {
          idx = Math.min(idx + 1, cards.length - 1);
        } else {
          idx = Math.max(idx - 1, 0);
        }
        const link = cards[idx]?.querySelector('.tile-link');
        if (link) link.focus();
        break;
      }
      case 'o':
      case 'Enter': {
        const focusedCard = document.activeElement?.closest('.item-card');
        if (focusedCard) {
          const link = focusedCard.querySelector('.tile-link');
          if (link) link.click();
        }
        break;
      }
      case 'e': {
        const editBtn = document.querySelector('button.edit-tags');
        if (editBtn && !editBtn.disabled) editBtn.click();
        break;
      }
    }
  });

  // Cmd+Enter to submit forms
  document.addEventListener('keydown', (e) => {
    if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') {
      const form = e.target.closest('form');
      if (form) {
        e.preventDefault();
        if (typeof form.requestSubmit === 'function') {
          form.requestSubmit();
        } else {
          form.submit();
        }
      }
    }
  });
})();
