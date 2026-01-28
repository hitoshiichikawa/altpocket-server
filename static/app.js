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
})();
