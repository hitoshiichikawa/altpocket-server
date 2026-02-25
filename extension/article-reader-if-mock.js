const mockItems = [
  {
    id: "item-1",
    title: "Go 1.22でのHTTPハンドラ構成を整理する",
    content: "Go 1.22でAPIとUIを同居させる場合、ハンドラは薄く保ち、認証・入力検証・レスポンスの責務を明確に分けると保守性が上がる。",
    tags: ["go", "backend", "chi"],
    url: "https://example.com/go-http-structure"
  },
  {
    id: "item-2",
    title: "Pocket代替サービス比較: 個人運用の観点",
    content: "個人運用では保存の速さと再訪のしやすさが最重要。タグ整理より先に検索導線を最適化した方が日常利用では体感がよい。",
    tags: ["productivity", "self-hosted"],
    url: "https://example.com/read-it-later-self-hosted"
  },
  {
    id: "item-3",
    title: "フロントエンドで情報密度を下げるUI原則",
    content: "表示要素を減らして認知負荷を抑えるための設計原則と実践例。",
    tags: ["ui", "design", "extension"],
    url: "https://example.com/ui-density-principles"
  },
  {
    id: "item-4",
    title: "PostgreSQLのtrigram検索を実運用で使う",
    content: "部分一致検索は title / content_search / tags を同時対象にすると使い勝手が高い。",
    tags: ["postgresql", "search", "tags"],
    url: "https://example.com/postgresql-trigram"
  },
  {
    id: "item-5",
    title: "失敗時の復帰導線を先に設計する",
    content: "認証切れや通信失敗時の復帰導線を最初に定義することで利用者の離脱を防ぎやすい。",
    tags: ["ux", "error-handling"],
    url: "https://example.com/failure-recovery-flow"
  }
];

const state = {
  query: ""
};

const searchInput = document.getElementById("searchInput");
const itemList = document.getElementById("itemList");
const resultMeta = document.getElementById("resultMeta");
const logoutBtn = document.getElementById("logoutBtn");
const utilityStatus = document.getElementById("utilityStatus");

function normalize(text) {
  return String(text || "").toLowerCase();
}

function matchesQuery(item, query) {
  if (!query) {
    return true;
  }
  const haystack = normalize([
    item.title,
    item.content,
    item.tags.join(" ")
  ].join(" "));
  return haystack.includes(query);
}

function getFilteredItems() {
  const q = normalize(state.query.trim());
  return mockItems.filter((item) => matchesQuery(item, q));
}

function escapeHTML(text) {
  return String(text)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll("\"", "&quot;")
    .replaceAll("'", "&#39;");
}

function renderTagList(tags) {
  if (!Array.isArray(tags) || tags.length === 0) {
    return "<span class=\"tag\">(no tags)</span>";
  }
  return tags.map((tag) => `<span class="tag">${escapeHTML(tag)}</span>`).join("");
}

function setUtilityStatus(message, level = "default") {
  if (!utilityStatus) {
    return;
  }
  utilityStatus.textContent = message;
  utilityStatus.classList.remove("is-success", "is-error");
  if (level === "success") {
    utilityStatus.classList.add("is-success");
  }
  if (level === "error") {
    utilityStatus.classList.add("is-error");
  }
}

async function clearExtensionSessionToken() {
  const c = globalThis.chrome;
  if (!c || !c.storage || !c.storage.local) {
    return false;
  }
  if (typeof c.storage.local.remove === "function") {
    await c.storage.local.remove("token");
    return true;
  }
  if (typeof c.storage.local.set === "function") {
    await c.storage.local.set({ token: "" });
    return true;
  }
  return false;
}

async function clearGoogleAuthCache() {
  const c = globalThis.chrome;
  if (!c || !c.identity || typeof c.identity.clearAllCachedAuthTokens !== "function") {
    return false;
  }
  await c.identity.clearAllCachedAuthTokens();
  return true;
}

async function logout() {
  setUtilityStatus("Signing out...");
  if (logoutBtn) {
    logoutBtn.disabled = true;
  }

  try {
    const tokenCleared = await clearExtensionSessionToken();
    const authCacheCleared = await clearGoogleAuthCache();

    if (tokenCleared || authCacheCleared) {
      setUtilityStatus("Signed out", "success");
      return;
    }
    setUtilityStatus("No session", "default");
  } catch {
    setUtilityStatus("Sign out failed", "error");
  } finally {
    if (logoutBtn) {
      logoutBtn.disabled = false;
    }
  }
}

function renderList() {
  const filtered = getFilteredItems();
  resultMeta.textContent = filtered.length + "件";

  if (filtered.length === 0) {
    itemList.innerHTML = "<li><p class=\"empty\">条件に一致する記事がありません。</p></li>";
    return;
  }

  itemList.innerHTML = "";
  filtered.forEach((item) => {
    const li = document.createElement("li");
    li.innerHTML = `
      <article class="item-card">
        <div class="item-head">
          <h3 class="item-title">${escapeHTML(item.title)}</h3>
        </div>
        <div class="item-meta">${renderTagList(item.tags)}</div>
        <div class="item-actions">
          <a class="show-original" href="${escapeHTML(item.url)}" target="_blank" rel="noopener noreferrer">Show original</a>
        </div>
      </article>
    `;
    itemList.appendChild(li);
  });
}

function render() {
  renderList();
}

searchInput.addEventListener("input", (event) => {
  state.query = event.target.value;
  render();
});

searchInput.addEventListener("keydown", (event) => {
  if (event.key !== "Escape") {
    return;
  }
  if (searchInput.value === "") {
    return;
  }
  searchInput.value = "";
  state.query = "";
  render();
});

if (logoutBtn) {
  logoutBtn.addEventListener("click", () => {
    void logout();
  });
}

render();
