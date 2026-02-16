async function enableOpenPanelOnActionClick() {
  if (!chrome.sidePanel || typeof chrome.sidePanel.setPanelBehavior !== 'function') {
    return;
  }
  try {
    await chrome.sidePanel.setPanelBehavior({ openPanelOnActionClick: true });
  } catch {
    // Ignore unsupported Chrome versions or transient runtime errors.
  }
}

chrome.runtime.onInstalled.addListener(() => {
  void enableOpenPanelOnActionClick();
});

chrome.runtime.onStartup.addListener(() => {
  void enableOpenPanelOnActionClick();
});

void enableOpenPanelOnActionClick();
