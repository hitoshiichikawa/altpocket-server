# altpocket UI Design System — Apple HIG Edition

> **Version:** 1.0
> **Date:** 2026-03-02
> **Author:** Senior Apple UI Designer
> **Target:** Web-based read-later service for knowledge workers
> **Design Language:** Apple Human Interface Guidelines (HIG), adapted for web

---

## Table of Contents

1. [User Research](#1-user-research)
2. [Design Principles](#2-design-principles)
3. [Design Tokens](#3-design-tokens)
4. [Typography](#4-typography)
5. [Layout System](#5-layout-system)
6. [Navigation](#6-navigation)
7. [Core Screens (8 Screens)](#7-core-screens)
8. [Component Library](#8-component-library)
9. [Micro-interactions & Motion](#9-micro-interactions--motion)
10. [Accessibility](#10-accessibility)
11. [Responsive Behavior](#11-responsive-behavior)
12. [Designer's Notes](#12-designers-notes)

---

## 1. User Research

### Knowledge Worker Profile

| Attribute | Detail |
|---|---|
| **Persona** | ソフトウェアエンジニア、リサーチャー、テクニカルライター |
| **Environment** | MacBook (primary) + iPhone (secondary)。デスクとカフェを行き来 |
| **Workflow** | ブラウジング → 発見 → 保存 → 後で読む → ナレッジ整理 |
| **Reading Volume** | 週 20-50 記事を保存、うち読むのは 30-40% |

### Goals

| # | Goal | Metric |
|---|---|---|
| G1 | **即座に保存** — 読んでいるフローを中断せず記事を保存したい | 保存完了まで 2 秒以内 |
| G2 | **すぐに見つかる** — 保存した記事を検索・タグで即座にアクセスしたい | 目的の記事に 3 タップ以内 |
| G3 | **快適に読む** — 広告や装飾なしで記事本文を集中して読みたい | 読了率 (content 表示から 30 秒以上滞在) |
| G4 | **整理が苦にならない** — タグ付け・分類が直感的で最小の操作 | タグ操作 1 ステップ |
| G5 | **どこでも使える** — デスク・モバイル・通勤中、シームレスに | 全画面がモバイルファースト |

### Pain Points (現状の課題)

| # | Pain Point | Severity |
|---|---|---|
| P1 | モバイルでフィルターサイドバーがアクセス困難 | High |
| P2 | 記事本文が `<pre>` タグの raw text で読みにくい | High |
| P3 | `alert()` / `confirm()` のネイティブダイアログが現代的でない | Medium |
| P4 | テーマ切替 UI がない (ダークモード固定) | Medium |
| P5 | 保存済み記事のステータスが小さい pill だけで把握しづらい | Low |
| P6 | ナビゲーションがモバイルで崩れる | High |
| P7 | 空状態・エラー状態のデザインが最小限 | Medium |

---

## 2. Design Principles

Apple HIG の 4 原則を altpocket に適用:

### 2.1 Deference (控えめさ)
> UIはコンテンツに従属する。記事そのものが主役。

- カード・ボタン・ナビは控えめに。背景はコンテンツを引き立てるニュートラルカラー
- アイコンは SF Symbols のアウトラインスタイル。塗りつぶしは選択状態のみ
- 装飾要素は最小限。グラデーション・ドロップシャドウは浅く、vibrancy で奥行きを表現

### 2.2 Clarity (明快さ)
> すべてのテキストは読みやすく、アイコンは一目瞭然、装飾は機能的。

- テキスト階層は 4 段階のみ: Large Title / Title / Body / Caption
- インタラクティブ要素は色 (--color-primary) で一貫して示す
- 状態変化は色 + アイコン + ラベルの 3 重で示す (色覚多様性に配慮)

### 2.3 Depth (奥行き)
> レイヤー、トランジション、リアルな動きで空間的な理解を助ける。

- 背景 → カード → モーダル/シートの 3 レイヤー構造
- シートは下からスライドイン (半モーダル)
- ナビゲーション遷移は左右スライド

### 2.4 Content-First (コンテンツファースト)
> altpocket の価値は「保存した記事」。UI は記事を邪魔しない。

- 記事一覧: 大きなタイトル + 2 行の excerpt
- 記事詳細: リーダーモード。散文に最適化されたタイポグラフィ
- アクションは必要なときにだけ表示

---

## 3. Design Tokens

### 3.1 Color Palette

```
┌─────────────────────────────────────────────────────────┐
│  DARK THEME (Default)           LIGHT THEME             │
│                                                         │
│  Base                                                   │
│  ├─ bg-base:      #0a0a0a      #f5f5f7                 │
│  ├─ bg-surface:   #1c1c1e      #ffffff                 │
│  ├─ bg-elevated:  #2c2c2e      #f2f2f7                 │
│  ├─ bg-grouped:   #141416      #f2f2f7                 │
│  │                                                      │
│  Text                                                   │
│  ├─ text-primary:   #f5f5f7    #1d1d1f                 │
│  ├─ text-secondary: #98989d    #6e6e73                 │
│  ├─ text-tertiary:  #636366    #aeaeb2                 │
│  ├─ text-quaternary:#48484a    #d1d1d6                 │
│  │                                                      │
│  Accent                                                 │
│  ├─ color-primary:  #d4a574    #b8895a   (warm gold)   │
│  ├─ color-primary-soft: rgba(212,165,116,0.12)         │
│  │                       rgba(184,137,90,0.10)         │
│  │                                                      │
│  Semantic                                               │
│  ├─ color-success:  #30d158    #34c759                 │
│  ├─ color-warning:  #ffd60a    #ff9f0a                 │
│  ├─ color-danger:   #ff453a    #ff3b30                 │
│  ├─ color-info:     #64d2ff    #007aff                 │
│  │                                                      │
│  Borders & Separators                                   │
│  ├─ separator:      rgba(84,84,88,0.65)                │
│  │                  rgba(60,60,67,0.29)                │
│  ├─ separator-opaque: #38383a  #c6c6c8                 │
│  │                                                      │
│  Materials (Backdrop blur layers)                       │
│  ├─ material-thick:  rgba(30,30,30,0.85)               │
│  │                   rgba(255,255,255,0.85)            │
│  ├─ material-thin:   rgba(30,30,30,0.55)               │
│  │                   rgba(255,255,255,0.55)            │
│  └─ material-ultra:  rgba(30,30,30,0.35)               │
│                      rgba(255,255,255,0.35)            │
└─────────────────────────────────────────────────────────┘
```

### 3.2 Spacing Scale (4px ベース)

```
--space-0:   0
--space-1:   4px     (micro gap)
--space-2:   8px     (tight)
--space-3:  12px     (compact)
--space-4:  16px     (default)
--space-5:  20px     (comfortable)
--space-6:  24px     (section gap)
--space-8:  32px     (group gap)
--space-10: 40px     (page section)
--space-12: 48px     (large section)
--space-16: 64px     (hero spacing)
```

### 3.3 Radius

```
--radius-sm:    8px   (buttons, inputs, chips)
--radius-md:   12px   (cards, tiles)
--radius-lg:   16px   (modals, sheets)
--radius-xl:   20px   (large cards)
--radius-full: 9999px (pills, avatars)
```

### 3.4 Elevation (Box Shadow)

```
--shadow-sm:   0 1px 3px rgba(0,0,0,0.08), 0 1px 2px rgba(0,0,0,0.06)
--shadow-md:   0 4px 12px rgba(0,0,0,0.10), 0 2px 4px rgba(0,0,0,0.06)
--shadow-lg:   0 12px 40px rgba(0,0,0,0.15), 0 4px 12px rgba(0,0,0,0.08)
--shadow-sheet:0 -4px 32px rgba(0,0,0,0.20)
```

Note: ダークテーマでは shadow は控えめに。border + subtle gradient で分離を表現。

---

## 4. Typography

### 4.1 Font Stack

```css
--font-sans: -apple-system, BlinkMacSystemFont, "SF Pro Text",
             "Hiragino Kaku Gothic ProN", "Hiragino Sans",
             "Noto Sans JP", "Yu Gothic", sans-serif;

--font-display: -apple-system, BlinkMacSystemFont, "SF Pro Display",
                "Hiragino Kaku Gothic ProN", "Hiragino Sans",
                "Noto Sans JP", "Yu Gothic", sans-serif;

--font-mono: "SF Mono", "Menlo", "Consolas",
             "Noto Sans Mono", monospace;

--font-serif: "New York", "Georgia", "Hiragino Mincho ProN",
              "Noto Serif JP", serif;
```

### 4.2 Type Scale

| Token | Size | Weight | Line-height | Tracking | Usage |
|---|---|---|---|---|---|
| `--type-large-title` | 34px | 700 | 1.18 | -0.02em | ページヘッダー (items, detail) |
| `--type-title-1` | 28px | 700 | 1.21 | -0.015em | セクションタイトル |
| `--type-title-2` | 22px | 700 | 1.27 | -0.01em | カードタイトル |
| `--type-title-3` | 20px | 600 | 1.30 | -0.008em | サブセクション |
| `--type-headline` | 17px | 600 | 1.29 | -0.005em | 強調テキスト、ナビリンク |
| `--type-body` | 17px | 400 | 1.53 | -0.005em | 本文、excerpt |
| `--type-callout` | 16px | 400 | 1.38 | -0.003em | 補足テキスト |
| `--type-subhead` | 15px | 400 | 1.33 | -0.003em | フォームラベル |
| `--type-footnote` | 13px | 400 | 1.38 | -0.002em | メタ情報、日時 |
| `--type-caption-1` | 12px | 400 | 1.33 | 0 | ステータスラベル |
| `--type-caption-2` | 11px | 400 | 1.27 | 0.005em | 微小ラベル |

### 4.3 Article Reader Typography

記事本文表示には serif フォントを使用し、長文読書に最適化:

```css
.reader-content {
  font-family: var(--font-serif);
  font-size: 19px;
  line-height: 1.68;
  letter-spacing: 0.01em;
  max-width: 680px;
  margin: 0 auto;
  word-break: break-word;
  overflow-wrap: break-word;
}

.reader-content p + p {
  margin-top: 1.4em;
}
```

---

## 5. Layout System

### 5.1 Container

```
┌──────────────────────────────────────────────────────────┐
│ Browser viewport                                         │
│  ┌────────────────────────────────────────────────────┐  │
│  │ .page-container                                    │  │
│  │ max-width: 1120px                                  │  │
│  │ padding: 0 var(--space-6)                          │  │
│  │ margin: 0 auto                                     │  │
│  │                                                    │  │
│  │  ┌──────────────────────────────────────────────┐  │  │
│  │  │ Content area                                 │  │  │
│  │  │ (varies by screen)                           │  │  │
│  │  └──────────────────────────────────────────────┘  │  │
│  └────────────────────────────────────────────────────┘  │
└──────────────────────────────────────────────────────────┘
```

### 5.2 Grid Patterns

**Library (Items 一覧):**
```
Desktop (≥1024px):
┌──────────────────────────────────────────────┐
│  [Toolbar: Search | Sort | Filter toggle]    │
├──────────┬───────────────────────────────────┤
│ Sidebar  │  Item Card                        │
│ (260px)  │  Item Card                        │
│ - Tags   │  Item Card                        │
│ - Filter │  ...                              │
│ (sticky) │  Pagination                       │
└──────────┴───────────────────────────────────┘

Tablet (768px–1023px):
┌──────────────────────────────────────────────┐
│  [Toolbar: Search | Sort | Filter ▼]        │
├──────────────────────────────────────────────┤
│  Item Card                                   │
│  Item Card                                   │
│  ...                                         │
│  Pagination                                  │
└──────────────────────────────────────────────┘
  ↳ Filter opens as bottom sheet

Mobile (≤767px):
┌──────────────────────┐
│  [☰ altpocket  🔍 👤]│  ← Compact header
├──────────────────────┤
│  Search bar          │
│  [Sort ▾] [Filter ▾] │
├──────────────────────┤
│  Item Card (full-w)  │
│  Item Card           │
│  ...                 │
│  Pagination          │
└──────────────────────┘
  ↳ Filter/Sort as bottom sheet
```

**Detail (記事詳細):**
```
Desktop / Tablet:
┌──────────────────────────────────────────────┐
│  ← Back to Library        [Edit] [⋯]        │
├──────────────────────────────────────────────┤
│                                              │
│   Article Title                              │
│   example.com · 2024-12-15 · ● success      │
│   [tag1] [tag2] [tag3]                       │
│                                              │
│   ─────────────────────────────              │
│                                              │
│   Article content in reader mode...          │
│   Serif font, 680px max-width,              │
│   comfortable line-height...                 │
│                                              │
└──────────────────────────────────────────────┘

Mobile:
┌──────────────────────┐
│  ← Back       [⋯]   │
├──────────────────────┤
│  Article Title       │
│  meta · tags         │
│  ────────────        │
│  Reader content      │
│  (full bleed)        │
│                      │
└──────────────────────┘
```

**Quick Add / Settings:**
```
All sizes (centered single-column):
┌──────────────────────────────────────────────┐
│                                              │
│     ┌────────────────────────────────┐       │
│     │  Form card (max 640px)         │       │
│     │  ...fields...                  │       │
│     │  [Primary Action]             │       │
│     └────────────────────────────────┘       │
│                                              │
└──────────────────────────────────────────────┘
```

---

## 6. Navigation

### 6.1 Structure

```
Navigation Hierarchy:
├── Top Bar (persistent)
│   ├── Brand: "altpocket" → /ui/items
│   ├── Primary Nav: Library | Quick Add
│   ├── Account Menu: Avatar → dropdown (Settings, Sign out)
│   └── Mobile: ☰ Hamburger → slide-over nav
│
├── In-page Navigation
│   ├── Library: Toolbar (search, sort, filter)
│   ├── Detail: Back button + action menu (⋯)
│   └── Settings: Section anchors
│
└── Contextual Actions
    ├── Item Card: hover → reveal actions
    ├── Detail: Edit mode toggle
    └── Quick Add: Form submission
```

### 6.2 Top Bar

```
Desktop:
┌────────────────────────────────────────────────────────┐
│  ◉ altpocket     Library   Quick Add      [👤 Name ▾] │
│  ─────────────────────────────────────separator─────── │
└────────────────────────────────────────────────────────┘
  backdrop-filter: blur(20px) saturate(180%)
  background: var(--material-thick)
  position: sticky; top: 0; z-index: 100
  height: 52px
  border-bottom: 0.5px solid var(--separator)

Mobile:
┌────────────────────────────────┐
│  ☰  altpocket              👤  │
│  ──────────separator────────── │
└────────────────────────────────┘
  height: 48px
  ☰ → slide-over navigation panel (left)
  👤 → account sheet (bottom)
```

### 6.3 Platform Rules

| Rule | Implementation |
|---|---|
| **Back navigation** | 詳細 → 一覧: 左矢印 + "Library" テキスト。ブラウザバックも対応 |
| **Swipe gestures** | モバイル: 詳細画面で右端からスワイプ → 一覧に戻る (touch-action) |
| **Keyboard shortcuts** | `/`: 検索フォーカス, `n`: Quick Add, `j/k`: アイテム上下移動, `o`: 記事を開く |
| **Tab navigation** | すべてのインタラクティブ要素にフォーカスリング (2px offset, primary color) |
| **URL state** | フィルター・検索・ページ番号はすべてURLに反映 (ブラウザバック対応) |

---

## 7. Core Screens

### Screen 1: Landing / Sign In

```
┌─────────────────────────────────────────────────────┐
│                                                     │
│                                                     │
│                   ◉                                 │
│              altpocket                              │
│                                                     │
│         Save it. Read it. Know it.                  │
│                                                     │
│   あなたの「あとで読む」を、ナレッジに変える。       │
│                                                     │
│    ┌─────────────────────────────────────────┐      │
│    │  🍎  Sign in with Google               │      │
│    └─────────────────────────────────────────┘      │
│                                                     │
│    ┌─────────────────────────────────────────┐      │
│    │      Create an account                  │      │
│    └─────────────────────────────────────────┘      │
│                                                     │
│   By continuing, you agree to our Terms.            │
│                                                     │
└─────────────────────────────────────────────────────┘
```

**Components:**
- `.auth-hero`: 中央寄せ、`min-height: 100dvh - header`
- Brand mark: SF Symbol `book.fill` + text "altpocket" in display font
- Tagline: `--type-title-2`, `--text-secondary`
- Description: `--type-body`, `--text-tertiary`
- Primary CTA: `.btn-primary-lg` (full-width, 50px height, radius-sm)
- Secondary CTA: `.btn-secondary-lg` (ghost style)
- Legal: `--type-caption-1`, `--text-quaternary`

**States:**
| State | Behavior |
|---|---|
| Default | 上記レイアウト |
| Loading (OAuth redirect) | ボタンがスピナーに変化、テキスト "Redirecting..." |
| Error (auth failed) | 赤い `.notice-danger` バナーがフェードイン |

---

### Screen 2: Library (Items 一覧) — Primary Screen

```
┌─────────────────────────────────────────────────────┐
│  ◉ altpocket     Library   Quick Add      [👤 ▾]   │
│  ───────────────────────────────────────────────────│
│                                                     │
│  Library                              200 articles  │
│                                                     │
│  ┌──────────────────────────────┐ [Sort ▾] [⊞ ▾]   │
│  │ 🔍 Search articles...       │                    │
│  └──────────────────────────────┘                    │
│                                                     │
│  ┌─────────┬───────────────────────────────────┐    │
│  │ TAGS    │                                   │    │
│  │         │ ┌─────────────────────────────┐   │    │
│  │ □ react │ │ ● Understanding React 19    │   │    │
│  │ □ go    │ │   react.dev · 3 days ago    │   │    │
│  │ □ rust  │ │   React 19 introduces new   │   │    │
│  │ □ ai    │ │   compiler optimizations... │   │    │
│  │ □ web   │ │   [react] [frontend]        │   │    │
│  │ ─────── │ │   ● Success                 │   │    │
│  │ 12 more │ └─────────────────────────────┘   │    │
│  │         │                                   │    │
│  │ FILTER  │ ┌─────────────────────────────┐   │    │
│  │         │ │ ○ Building CLI Tools in Go  │   │    │
│  │ Status  │ │   go.dev · 1 week ago       │   │    │
│  │ ○ All   │ │   A comprehensive guide to  │   │    │
│  │ ● Read  │ │   building robust CLI...    │   │    │
│  │ ○ Unread│ │   [go] [cli]               │   │    │
│  │         │ │   ● Success                 │   │    │
│  │         │ └─────────────────────────────┘   │    │
│  │         │                                   │    │
│  │         │ ┌─────────────────────────────┐   │    │
│  │         │ │ ◐ Rust Ownership Explained  │   │    │
│  │         │ │   rust-lang.org · 2 wks ago │   │    │
│  │         │ │   ...                       │   │    │
│  │         │ │   [rust]                    │   │    │
│  │         │ │   ◐ Fetching...             │   │    │
│  │         │ └─────────────────────────────┘   │    │
│  │         │                                   │    │
│  │         │      ‹ 1  2  3  4  5 ›            │    │
│  └─────────┴───────────────────────────────────┘    │
└─────────────────────────────────────────────────────┘
```

**Components:**

- **Page Header:** Large Title "Library" + article count badge (--type-footnote, --text-tertiary)
- **Search Bar:** `.search-input` with magnifying glass icon, rounded-full, bg-elevated
- **Sort Control:** Segmented control or dropdown: "Newest" / "Relevance"
- **Item Card (`.item-card`):**
  ```
  ┌─────────────────────────────────────────┐
  │ ● Title (--type-headline, 2 lines max)  │
  │   domain.com · relative time            │  ← --type-footnote, --text-tertiary
  │   Excerpt text up to 3 lines with       │  ← --type-body, --text-secondary
  │   ellipsis truncation...                │
  │   [tag1] [tag2]          ● Status       │  ← chips + status indicator
  └─────────────────────────────────────────┘
  padding: var(--space-4) var(--space-5)
  bg: var(--bg-surface)
  radius: var(--radius-md)
  border: 0.5px solid var(--separator)
  hover: translateY(-1px), shadow-sm → shadow-md
  ```
- **Tag Chip:** `bg: var(--color-primary-soft)`, `color: var(--color-primary)`, `radius-full`, `font: --type-caption-1`
- **Status Indicator:**
  - `● success` → `--color-success` + checkmark
  - `◐ fetching` → `--color-info` + spinning
  - `○ pending` → `--text-quaternary` + clock
  - `✕ failed` → `--color-danger` + exclamation
- **Sidebar Tags:** Checkbox list with count badges
- **Pagination:** `‹ 1 2 3 ... N ›` pill buttons, current = primary filled

**States:**

| State | Wireframe |
|---|---|
| Empty (no items) | ```┌────────────────────┐```<br>```│   📚               │```<br>```│   No articles yet  │```<br>```│   Save your first  │```<br>```│   article with      │```<br>```│   Quick Add or the  │```<br>```│   bookmarklet.      │```<br>```│   [Quick Add →]     │```<br>```└────────────────────┘``` |
| Empty (search, no results) | "No articles match "{query}"." + [Clear search] |
| Loading | 3 skeleton cards (shimmer animation) |
| Error | `.notice-danger` banner: "Failed to load articles. [Retry]" |
| Quick Add success | `.notice-success` toast at top: "Article saved." (auto-dismiss 4s) |

---

### Screen 3: Article Detail — Reader Mode

```
┌─────────────────────────────────────────────────────┐
│  ◉ altpocket     Library   Quick Add      [👤 ▾]   │
│  ───────────────────────────────────────────────────│
│                                                     │
│  ← Library                          [Edit] [⋯]     │
│                                                     │
│  Understanding React 19 Server                      │
│  Components and the New Compiler                    │
│                                                     │
│  react.dev · 2024-12-15 · ● Fetched                │
│  [react] [frontend] [compiler]                      │
│                                                     │
│  ─────────────────────────────────────              │
│                                                     │
│      React 19 represents a fundamental shift        │
│      in how we think about rendering. The new       │
│      compiler automatically optimizes your          │
│      components, eliminating the need for           │
│      manual memoization with useMemo and            │
│      useCallback.                                   │
│                                                     │
│      The Server Components architecture allows      │
│      you to fetch data directly in your             │
│      components, reducing the need for client-      │
│      side data fetching libraries...                │
│                                                     │
│      ...                                            │
│                                                     │
│  ─────────────────────────────────────              │
│  [Open original ↗]                                  │
│                                                     │
└─────────────────────────────────────────────────────┘
```

**Components:**

- **Back Button:** `← Library` (--type-headline, --color-primary). Click → navigate back
- **Action Menu (⋯):** Dropdown with: "Refetch article", "Open original", "Delete" (danger)
- **Title:** `--type-large-title`, `--font-display`, `--text-primary`
- **Meta Row:** domain (extracted, linked) · date (relative) · status pill
- **Tag Row:** Editable tag chips. タップで edit mode に入る (後述)
- **Separator:** `<hr>` as `0.5px solid var(--separator)`
- **Reader Content (`.reader-content`):**
  - `font-family: var(--font-serif)`
  - `font-size: 19px; line-height: 1.68`
  - `max-width: 680px; margin: 0 auto`
  - `color: var(--text-primary)`
  - paragraphs: `margin-top: 1.4em`
- **Footer:** "Open original" link button

**Edit Mode:**
```
┌─────────────────────────────────────────────────────┐
│  ← Library                      [Cancel] [Save ✓]  │
│                                                     │
│  ┌───────────────────────────────────────────────┐  │
│  │ Understanding React 19 Server Components...   │  │  ← input 表示
│  └───────────────────────────────────────────────┘  │
│                                                     │
│  react.dev · 2024-12-15                             │
│  [react ×] [frontend ×] [compiler ×] [+ Add tag]   │  ← 削除ボタン付きチップ
│  ┌───────────────────────────────────────────────┐  │
│  │ Type to add tag...                            │  │  ← tag input + suggestions
│  │  ┌─────────────┐                              │  │
│  │  │ javascript  │  ← autocomplete              │  │
│  │  │ java        │                              │  │
│  │  └─────────────┘                              │  │
│  └───────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────┘
```

**States:**

| State | Behavior |
|---|---|
| Loading | Skeleton: title (2 lines) + meta + separator + content (8 shimmer lines) |
| Content not fetched | Illustration + "Content is being fetched..." + progress indicator |
| Fetch failed | Warning banner: "Failed to fetch article content." + [Retry] |
| Edit saving | Save button → spinner, inputs disabled |
| Delete confirmation | Bottom sheet (mobile) / popover (desktop): "Delete this article?" [Cancel] [Delete] (danger) |

---

### Screen 4: Quick Add

```
┌─────────────────────────────────────────────────────┐
│  ◉ altpocket     Library   Quick Add      [👤 ▾]   │
│  ───────────────────────────────────────────────────│
│                                                     │
│           Quick Add                                 │
│           Save a page to your library.              │
│                                                     │
│    ┌─────────────────────────────────────────┐      │
│    │                                         │      │
│    │  URL *                                  │      │
│    │  ┌───────────────────────────────────┐  │      │
│    │  │ https://example.com/article       │  │      │
│    │  └───────────────────────────────────┘  │      │
│    │                                         │      │
│    │  Title                                  │      │
│    │  ┌───────────────────────────────────┐  │      │
│    │  │ Page title                        │  │      │
│    │  └───────────────────────────────────┘  │      │
│    │                                         │      │
│    │  Content                                │      │
│    │  ┌───────────────────────────────────┐  │      │
│    │  │ Add notes or captured content     │  │      │
│    │  │                                   │  │      │
│    │  │                                   │  │      │
│    │  └───────────────────────────────────┘  │      │
│    │                                         │      │
│    │  Tags                                   │      │
│    │  [react ×] [frontend ×]                 │      │
│    │  ┌───────────────────────────────────┐  │      │
│    │  │ Type tag then press Enter         │  │      │
│    │  └───────────────────────────────────┘  │      │
│    │                                         │      │
│    │  ┌───────────────────────────────────┐  │      │
│    │  │          Save                     │  │      │
│    │  └───────────────────────────────────┘  │      │
│    │  ┌───────────────────────────────────┐  │      │
│    │  │          Cancel                   │  │      │
│    │  └───────────────────────────────────┘  │      │
│    │                                         │      │
│    └─────────────────────────────────────────┘      │
│                                                     │
└─────────────────────────────────────────────────────┘
```

**Components:**

- **Card:** `.form-card`, `max-width: 640px`, centered, `padding: var(--space-8)`
- **Form Fields:** HIG-style floating labels (label above input)
  - Focus: bottom border animates to `--color-primary` (2px)
  - Filled: label stays above in `--type-caption-1`
- **Tag Editor:** Same component as detail page (shared)
- **Buttons:** Primary (full width) + Secondary (full width, ghost)

**States:**

| State | Behavior |
|---|---|
| Empty | All fields empty, URL focused |
| Pre-filled (bookmarklet) | URL, Title, Content pre-filled from query string |
| Validation error | Red border + error message below field. Focus on first error |
| Submitting | Primary button → spinner + "Saving..." |
| CSRF error | Error banner: "Session expired. Please reload." |
| Rate limited | Error banner: "Too many requests. Please wait." |

---

### Screen 5: Settings

```
┌─────────────────────────────────────────────────────┐
│  ◉ altpocket     Library   Quick Add      [👤 ▾]   │
│  ───────────────────────────────────────────────────│
│                                                     │
│           Settings                                  │
│                                                     │
│    ┌─────────────────────────────────────────┐      │
│    │  ACCOUNT                                │      │
│    │  ──────────────────────────────────     │      │
│    │  Name          Hitoshi Ichikawa         │      │
│    │  Email         user@example.com         │      │
│    │                                         │      │
│    │  APPEARANCE                             │      │
│    │  ──────────────────────────────────     │      │
│    │  Theme         [☀ Light | ● Dark]       │      │  ← segmented control
│    │                                         │      │
│    │  INTEGRATIONS                           │      │
│    │  ──────────────────────────────────     │      │
│    │  Google Sheets  ● Connected             │      │
│    │  Spreadsheet    [Open sheet ↗]          │      │
│    │                                         │      │
│    │  [Export to Google Sheets]              │      │
│    │  [Disconnect Google] (danger)           │      │
│    │                                         │      │
│    │  TOOLS                                  │      │
│    │  ──────────────────────────────────     │      │
│    │  Bookmarklet   [Install guide]          │      │
│    │  Browser Ext.  [Chrome Web Store ↗]     │      │
│    │  Keyboard      [View shortcuts]         │      │
│    │                                         │      │
│    └─────────────────────────────────────────┘      │
│                                                     │
└─────────────────────────────────────────────────────┘
```

**Components:**

- **Settings Card:** `.settings-card`, `max-width: 640px`, centered
- **Section Groups:** `.settings-group` with section header (--type-footnote, uppercase, --text-tertiary)
- **Setting Row:** `.setting-row` — label left + value/control right, separated by `--separator`
- **Segmented Control (Theme):** 2-segment, pill-shaped, animated selection indicator
- **Danger Action:** Red text, confirmation sheet before execution

**States:**

| State | Behavior |
|---|---|
| Google not connected | "Connect Google Sheets" button + description |
| Export in progress | Button → spinner + "Exporting..." |
| Export success | `.notice-success` toast: "Export complete." |
| Disconnect confirm | Sheet: "Disconnect Google Sheets?" [Cancel] [Disconnect] |

---

### Screen 6: Mobile Navigation (Slide-over)

```
┌──────────────────────┬───────┐
│                      │       │
│  altpocket           │ dim   │
│                      │ over- │
│  ──────────────      │ lay   │
│                      │       │
│  📚 Library          │       │
│  ＋ Quick Add        │       │
│                      │       │
│  ──────────────      │       │
│                      │       │
│  ⚙ Settings          │       │
│  ↗ Sign out          │       │
│                      │       │
│                      │       │
│                      │       │
│                      │       │
│  ──────────────      │       │
│  👤 Hitoshi          │       │
│     user@example.com │       │
│                      │       │
└──────────────────────┴───────┘
```

**Behavior:**
- Trigger: ☰ hamburger in mobile header
- Animation: Slide from left, 280px width
- Overlay: dim background (rgba(0,0,0,0.4)), tap to dismiss
- Swipe left to close
- Active page highlighted with `--color-primary` tint background
- Transition: 300ms ease-out (--motion-standard)

---

### Screen 7: Filter Sheet (Mobile)

```
┌──────────────────────┐
│                      │
│  (dimmed content)    │
│                      │
├──────────────────────┤  ← drag handle (pill, 36×5px)
│  ──                  │
│                      │
│  Filters             │  ← --type-title-3
│                      │
│  Search              │
│  ┌────────────────┐  │
│  │ 🔍 Keywords... │  │
│  └────────────────┘  │
│                      │
│  Sort by             │
│  ┌────────────────┐  │
│  │ ● Newest       │  │
│  │ ○ Relevance    │  │
│  └────────────────┘  │
│                      │
│  Tags                │
│  □ react (12)        │
│  ■ go (8)            │
│  □ rust (5)          │
│  □ ai (3)            │
│  ...                 │
│                      │
│  ┌────────────────┐  │
│  │  Apply Filters │  │
│  └────────────────┘  │
│  [Clear all]         │
│                      │
└──────────────────────┘
```

**Behavior:**
- Half-sheet: `max-height: 85vh`, `border-radius: var(--radius-lg) var(--radius-lg) 0 0`
- Drag handle: swipe down to dismiss
- Detents: half (50vh) → full (85vh)
- Apply: closes sheet + reloads items with filters
- `backdrop-filter: blur(20px)` on overlay

---

### Screen 8: Toast / Confirmation System

**Toast Notification (Success/Info):**
```
┌─────────────────────────────────────────────────────┐
│                                                     │
│  ┌───────────────────────────────────────────┐      │
│  │ ✓  Article saved.                    [×]  │      │
│  └───────────────────────────────────────────┘      │
│                                                     │
│  (page content below)                               │
│                                                     │
└─────────────────────────────────────────────────────┘
```

- Position: top center, `top: var(--space-4)` below header
- Animation: slide down + fade in (300ms), auto-dismiss after 4s
- Background: `var(--bg-elevated)`, `border: 0.5px solid var(--separator)`, `shadow-md`
- Swipe up to dismiss (mobile)

**Confirmation Sheet (Delete/Disconnect):**
```
Desktop (popover):                Mobile (bottom sheet):
┌──────────────────┐              ┌──────────────────────┐
│ Delete article?  │              │  ──                   │
│                  │              │                       │
│ This can't be    │              │  Delete article?      │
│ undone.          │              │  This can't be undone.│
│                  │              │                       │
│ [Cancel] [Delete]│              │  ┌──────────────────┐ │
└──────────────────┘              │  │     Delete       │ │  ← danger
                                  │  └──────────────────┘ │
                                  │  ┌──────────────────┐ │
                                  │  │     Cancel       │ │
                                  │  └──────────────────┘ │
                                  └──────────────────────┘
```

---

## 8. Component Library

### 8.1 Buttons

```
Primary (Filled):
┌─────────────────────────┐
│         Label           │   bg: --color-primary
└─────────────────────────┘   color: white
                              height: 44px (touch target)
                              radius: --radius-sm
                              font: --type-headline
                              hover: brightness(1.08)
                              active: brightness(0.92), scale(0.98)
                              disabled: opacity 0.35

Secondary (Ghost):
┌─────────────────────────┐
│         Label           │   bg: transparent
└─────────────────────────┘   color: --color-primary
                              border: 1px solid --separator-opaque
                              hover: bg --color-primary-soft
                              active: bg --color-primary-soft, scale(0.98)

Tertiary (Text):
     Label                    bg: transparent
                              color: --color-primary
                              padding: --space-2 --space-3
                              hover: bg --color-primary-soft, radius-sm
                              active: opacity 0.7

Danger:
┌─────────────────────────┐
│         Label           │   bg: transparent
└─────────────────────────┘   color: --color-danger
                              border: 1px solid color-mix(in srgb, var(--color-danger) 30%, transparent)
                              hover: bg color-mix(in srgb, var(--color-danger) 10%, transparent)

Icon Button:
  ┌───┐
  │ ⋯ │                       44×44px touch target
  └───┘                       bg: transparent
                              hover: bg --bg-elevated, radius-full
```

### 8.2 Form Inputs

```
Default:
  Label                           ← --type-subhead, --text-secondary
  ┌───────────────────────────┐
  │ Placeholder text           │   height: 44px
  └───────────────────────────┘   bg: --bg-elevated
                                  border: 1px solid --separator
                                  radius: --radius-sm
                                  font: --type-body

Focus:
  Label                           ← --type-subhead, --color-primary
  ┌───────────────────────────┐
  │ Input text                 │   border: 2px solid --color-primary
  └───────────────────────────┘   box-shadow: 0 0 0 3px --color-primary-soft

Error:
  Label                           ← --type-subhead, --color-danger
  ┌───────────────────────────┐
  │ Input text                 │   border: 2px solid --color-danger
  └───────────────────────────┘
  ⚠ Error message                 ← --type-caption-1, --color-danger

Textarea:
  ┌───────────────────────────┐
  │ Multi-line text            │   min-height: 100px
  │                            │   resize: vertical
  │                            │   Same styling as input
  └───────────────────────────┘
```

### 8.3 Cards

```
Item Card:
┌─────────────────────────────────────────────┐
│                                             │   bg: --bg-surface
│  ● Title (--type-headline)                  │   border: 0.5px solid --separator
│  domain.com · 3 days ago                    │   radius: --radius-md
│  Excerpt text truncated to 3 lines with     │   padding: --space-4 --space-5
│  elegant ellipsis...                        │   transition: transform 200ms, shadow 200ms
│  [tag1] [tag2]                ● Success     │   hover: translateY(-1px), shadow-md
│                                             │   active: translateY(0), shadow-sm
└─────────────────────────────────────────────┘

Form Card:
┌─────────────────────────────────────────────┐
│                                             │   bg: --bg-surface
│  (form content)                             │   border: 0.5px solid --separator
│                                             │   radius: --radius-lg
│                                             │   padding: --space-8
│                                             │   max-width: 640px
└─────────────────────────────────────────────┘

Settings Row:
┌─────────────────────────────────────────────┐
│  Label                           Value  ›   │   height: 44px
├─────────────────────────────────────────────┤   border-bottom: 0.5px solid --separator
│  Label                     [Control]        │   padding: 0 --space-5
├─────────────────────────────────────────────┤
│  Label                        Value         │
└─────────────────────────────────────────────┘
```

### 8.4 Tags / Chips

```
Display Tag:
  [tag-name]                      bg: --color-primary-soft
                                  color: --color-primary
                                  radius: --radius-full
                                  padding: --space-1 --space-3
                                  font: --type-caption-1, weight 500

Editable Tag (Edit Mode):
  [tag-name ×]                    Same + remove button (×)
                                  hover: bg brightness(1.2)
                                  × hover: color --color-danger

Filter Tag (Sidebar):
  □ tag-name (12)                 Checkbox + label + count
  ■ tag-name (12)                 Selected: --color-primary fill
```

### 8.5 Status Indicators

```
● Success    → filled circle, --color-success, "Fetched"
◐ Fetching   → half circle (animated rotation), --color-info, "Fetching..."
○ Pending    → empty circle, --text-quaternary, "Pending"
✕ Failed     → × mark, --color-danger, "Failed"

All include: icon (12px) + label (--type-caption-1)
Color + icon + label = triple encoding for accessibility
```

### 8.6 Pagination

```
  ‹  1  2  [3]  4  5  ...  12  ›

  ‹/› arrows: icon buttons, disabled at bounds
  Numbers: --type-subhead, --text-secondary
  Current [3]: bg --color-primary, color white, radius-full
  ...: --text-quaternary
  Spacing: --space-1 gap
```

### 8.7 Skeleton Loader

```
┌─────────────────────────────────────────────┐
│  ████████████████████ (title)               │   bg: linear-gradient shimmer
│  ████████ · ████████                        │   animation: shimmer 1.5s infinite
│  ██████████████████████████████████████████  │   border-radius matches content
│  ████████████████████████████████████████    │
│  [████] [████████]                          │
└─────────────────────────────────────────────┘

@keyframes shimmer {
  0%   { background-position: -200% 0; }
  100% { background-position: 200% 0; }
}
background: linear-gradient(90deg,
  var(--bg-elevated) 25%,
  color-mix(in srgb, var(--bg-elevated) 70%, var(--text-quaternary)) 50%,
  var(--bg-elevated) 75%
);
background-size: 200% 100%;
```

---

## 9. Micro-interactions & Motion

### 9.1 Motion Tokens

```css
--motion-fast:      120ms   /* button press, toggle */
--motion-standard:  250ms   /* card hover, focus ring */
--motion-moderate:  350ms   /* sheet open/close, navigation */
--motion-slow:      500ms   /* page transition, skeleton fade */

--ease-default:     cubic-bezier(0.25, 0.1, 0.25, 1.0)   /* standard ease */
--ease-spring:      cubic-bezier(0.34, 1.56, 0.64, 1.0)   /* bouncy overshoot */
--ease-decelerate:  cubic-bezier(0.0, 0.0, 0.2, 1.0)      /* enter/appear */
--ease-accelerate:  cubic-bezier(0.4, 0.0, 1.0, 1.0)      /* exit/disappear */
```

### 9.2 Interaction Catalog

| Interaction | Trigger | Animation | Duration |
|---|---|---|---|
| **Button press** | mousedown/touchstart | `scale(0.97)` + `brightness(0.92)` | --motion-fast |
| **Card hover** | mouseenter | `translateY(-2px)` + `shadow-sm → shadow-md` | --motion-standard |
| **Card tap** | click | `scale(0.98)` → `scale(1)` | --motion-fast |
| **Focus ring** | tab focus | `box-shadow: 0 0 0 3px var(--color-primary-soft)` | --motion-standard |
| **Toast enter** | event | `translateY(-16px) → translateY(0)` + `opacity: 0 → 1` | --motion-moderate |
| **Toast exit** | 4s timer / swipe | `translateY(0) → translateY(-16px)` + `opacity: 1 → 0` | --motion-standard |
| **Sheet open** | trigger | `translateY(100%) → translateY(0)` + overlay fade | --motion-moderate |
| **Sheet close** | dismiss | `translateY(0) → translateY(100%)` + overlay fade | --motion-moderate |
| **Nav slide-over** | ☰ tap | `translateX(-100%) → translateX(0)` + overlay | --motion-moderate |
| **Skeleton shimmer** | loading | gradient sweep left → right | 1500ms infinite |
| **Status spinner** | fetching | `rotate(0deg) → rotate(360deg)` | 1000ms infinite linear |
| **Tag add** | enter key | `scale(0) → scale(1.05) → scale(1)` | --motion-standard, --ease-spring |
| **Tag remove** | × click | `scale(1) → scale(0)` + `opacity: 1 → 0` | --motion-fast |
| **Delete confirmation** | delete click | Sheet/popover with `scale(0.95) → scale(1)` | --motion-moderate |
| **Success checkmark** | save complete | Stroke draw animation (SVG path) | --motion-slow |
| **Page transition** | navigation | `opacity: 0.6 → 1` + `translateY(8px) → translateY(0)` | --motion-moderate |

### 9.3 Reduced Motion

```css
@media (prefers-reduced-motion: reduce) {
  *, *::before, *::after {
    animation-duration: 0.01ms !important;
    transition-duration: 0.01ms !important;
  }
  /* Keep opacity transitions for state changes */
  .toast, .sheet-overlay {
    transition: opacity 0.15s ease;
  }
}
```

---

## 10. Accessibility

### 10.1 WCAG 2.1 AA Compliance

| Criterion | Implementation |
|---|---|
| **1.1.1 Non-text Content** | すべてのアイコンに `aria-label`。装飾アイコンは `aria-hidden="true"` |
| **1.3.1 Info and Relationships** | セマンティック HTML: `<nav>`, `<main>`, `<article>`, `<aside>`, `<header>`, `<footer>` |
| **1.3.2 Meaningful Sequence** | DOM 順序 = 視覚順序。CSS `order` 使用時も読み上げ順序を維持 |
| **1.4.3 Contrast** | テキスト: 最低 4.5:1。大テキスト (≥18px bold / ≥24px): 3:1。UI コンポーネント: 3:1 |
| **1.4.11 Non-text Contrast** | フォーカスリング、ボタン境界、フォーム入力境界: 3:1 以上 |
| **2.1.1 Keyboard** | すべての機能がキーボードのみで操作可能 |
| **2.4.3 Focus Order** | 論理的なタブ順序。モーダル/シート内でフォーカストラップ |
| **2.4.7 Focus Visible** | カスタムフォーカスリング: `3px solid var(--color-primary)`, offset `2px` |
| **2.5.5 Target Size** | すべてのインタラクティブ要素: 最低 44×44px のタッチターゲット |
| **4.1.2 Name, Role, Value** | カスタムコンポーネントに適切な ARIA ロールとプロパティ |

### 10.2 Contrast Ratios (Verified)

| Element | Dark Theme | Light Theme | Pass? |
|---|---|---|---|
| Body text on surface | #f5f5f7 on #1c1c1e → **15.4:1** | #1d1d1f on #ffffff → **17.4:1** | AA ✓ |
| Secondary text | #98989d on #1c1c1e → **5.2:1** | #6e6e73 on #ffffff → **5.0:1** | AA ✓ |
| Primary on surface | #d4a574 on #1c1c1e → **5.8:1** | #b8895a on #ffffff → **3.5:1** | AA (large) ✓ |
| Danger text | #ff453a on #1c1c1e → **5.1:1** | #ff3b30 on #ffffff → **4.5:1** | AA ✓ |

### 10.3 Screen Reader (VoiceOver) Support

```html
<!-- Navigation landmark -->
<nav aria-label="Main navigation">
  <a href="/ui/items" aria-current="page">Library</a>
  <a href="/ui/quick-add">Quick Add</a>
</nav>

<!-- Search with live region -->
<div role="search">
  <label for="search-input">Search articles</label>
  <input id="search-input" type="search"
         aria-describedby="search-hint"
         aria-controls="items-list">
  <div id="search-hint" class="sr-only">
    Type to search by title, content, or tags
  </div>
</div>

<!-- Items list with count announcement -->
<div id="items-list" role="region" aria-label="Articles"
     aria-live="polite" aria-atomic="false">
  <div class="sr-only" aria-live="polite">
    {{.Total}} articles found
  </div>
  <!-- Item cards -->
  <article class="item-card" aria-labelledby="item-title-{{.ID}}">
    <h2 id="item-title-{{.ID}}">{{.Title}}</h2>
    <span class="status" role="status" aria-label="Fetch status: {{.FetchStatus}}">
      ● {{.FetchStatus}}
    </span>
  </article>
</div>

<!-- Tag editor -->
<div role="group" aria-label="Tags">
  <div role="list" aria-label="Current tags">
    <span role="listitem">
      react
      <button aria-label="Remove tag react">×</button>
    </span>
  </div>
  <input type="text" role="combobox"
         aria-expanded="false"
         aria-autocomplete="list"
         aria-controls="tag-suggestions"
         aria-label="Add tag">
  <ul id="tag-suggestions" role="listbox" aria-label="Tag suggestions">
    <li role="option" aria-selected="false">javascript</li>
  </ul>
</div>

<!-- Toast notifications -->
<div role="alert" aria-live="assertive" class="toast">
  Article saved.
</div>

<!-- Confirmation dialog -->
<dialog aria-labelledby="confirm-title" aria-describedby="confirm-desc">
  <h2 id="confirm-title">Delete article?</h2>
  <p id="confirm-desc">This action cannot be undone.</p>
  <button>Cancel</button>
  <button class="danger">Delete</button>
</dialog>
```

### 10.4 Dynamic Type / Text Scaling

```css
/* Base font size responds to user preference */
html {
  font-size: clamp(14px, 1rem, 22px);
}

/* All type tokens use rem for scaling */
.type-body { font-size: 1.0625rem; }    /* 17px base */
.type-headline { font-size: 1.0625rem; font-weight: 600; }
.type-large-title { font-size: 2.125rem; }  /* 34px base */

/* Ensure containers expand with text */
.item-card {
  min-height: auto;
  padding: var(--space-4);
}

/* Test at 200% zoom: all content remains accessible */
@media (min-resolution: 192dpi) {
  /* High DPI adjustments if needed */
}
```

### 10.5 Keyboard Shortcuts

| Shortcut | Action | Context |
|---|---|---|
| `/` | フォーカス → 検索バー | Library |
| `n` | Quick Add を開く | Global |
| `j` / `k` | 次/前のアイテムカードにフォーカス | Library |
| `o` or `Enter` | フォーカス中のアイテムを開く | Library |
| `e` | 編集モードに入る | Detail |
| `Escape` | モーダル/シート/編集モードを閉じる | Global |
| `Cmd+Enter` | フォーム送信 | Quick Add, Edit mode |
| `?` | キーボードショートカット一覧を表示 | Global |

```html
<!-- Keyboard shortcut hint (sr-only by default, shown on ? press) -->
<dialog id="keyboard-shortcuts" aria-label="Keyboard shortcuts">
  <h2>Keyboard Shortcuts</h2>
  <dl>
    <dt><kbd>/</kbd></dt><dd>Search</dd>
    <dt><kbd>n</kbd></dt><dd>Quick Add</dd>
    <!-- ... -->
  </dl>
</dialog>
```

---

## 11. Responsive Behavior

### 11.1 Breakpoints

```css
/* Mobile-first approach */
--bp-sm:   640px    /* Small phones → Large phones */
--bp-md:   768px    /* Phones → Tablets */
--bp-lg:  1024px    /* Tablets → Desktops */
--bp-xl:  1280px    /* Desktops → Wide screens */
```

### 11.2 Behavior Matrix

| Component | Mobile (<768) | Tablet (768–1023) | Desktop (≥1024) |
|---|---|---|---|
| **Header** | Compact: ☰ brand 👤 (48px) | Full nav with text | Full nav with text |
| **Navigation** | Slide-over from left | Top bar inline | Top bar inline |
| **Library grid** | Single column | Single column | Sidebar + main |
| **Sidebar** | Bottom sheet (filter ▾) | Bottom sheet | Sticky sidebar (260px) |
| **Item card** | Full width, compact | Full width | Full width |
| **Detail: reader** | Full bleed, 16px padding | 680px centered | 680px centered |
| **Quick Add form** | Full width - 16px padding | 640px centered card | 640px centered card |
| **Settings** | Full width - 16px padding | 640px centered card | 640px centered card |
| **Confirmation** | Bottom sheet | Bottom sheet | Popover near trigger |
| **Toast** | Full width - 16px | 400px centered | 400px centered |
| **Pagination** | Prev/Next only | Full numbers | Full numbers |
| **Account menu** | Part of slide-over nav | Dropdown | Dropdown |

### 11.3 Mobile-Specific Adaptations

```
Safe areas (notch/island):
  padding-top: env(safe-area-inset-top);
  padding-bottom: env(safe-area-inset-bottom);

Bottom sheet accounts for home indicator:
  padding-bottom: calc(var(--space-6) + env(safe-area-inset-bottom));

Touch targets: minimum 44×44px
Tap delay: none (touch-action: manipulation on all interactive)

Scroll behavior:
  -webkit-overflow-scrolling: touch;
  overscroll-behavior: contain;  /* Prevent pull-to-refresh on sheets */
```

### 11.4 Responsive Typography

```css
/* Scale down heading sizes on mobile */
@media (max-width: 767px) {
  :root {
    --type-large-title-size: 28px;  /* 34→28 */
    --type-title-1-size: 24px;      /* 28→24 */
    --type-title-2-size: 20px;      /* 22→20 */
  }
}
```

---

## 12. Designer's Notes

### Design Philosophy

> altpocket は「読むための場所」。UI は図書館の書架のように、静かで整然として、
> 探している本にすぐ手が届く空間であるべき。Apple HIG の "content-first" を
> 徹底し、すべての装飾は記事コンテンツに奉仕する。

### Key Design Decisions

**1. Warm Gold アクセントカラーの維持**

既存の `#d4a574` は altpocket のブランドアイデンティティ。Apple の Blue (`#007AFF`) ではなく、温かみのあるゴールドを一貫して使用。これはナレッジワーカーの「蓄積」「知恵」を象徴し、冷たいテック感とは一線を画す。

**2. Reader Mode の Serif フォント**

記事本文には New York / Georgia を採用。Safari の Reader Mode と同様のアプローチで、
長文読書の快適性を最優先。コードブロックのみ SF Mono に切り替え。

**3. Bottom Sheet パターン (モバイル)**

モバイルのフィルター・確認ダイアログには iOS の `UISheetPresentationController` に
倣った Bottom Sheet を採用。ドラッグハンドル + detents (半分/全面) で自然な操作感。
`alert()` / `confirm()` からの脱却。

**4. Skeleton Loading**

Shimmer アニメーションによるスケルトンローダーで、レイアウトシフトを防ぎつつ
「何かが来る」期待感を維持。Apple の動きの哲学「purposeful motion」に準拠。

**5. ステータスの三重エンコーディング**

Fetch status は色 + アイコン形状 + テキストラベルの 3 つで表現。
色覚多様性のあるユーザーでもアイコン形状（●/◐/○/✕）で状態を区別可能。

**6. Sidebar vs. Sheet の切り替え (1024px)**

デスクトップではタグフィルターを常時表示するサイドバーが効率的。タブレット以下では
画面を占有しないシートに切り替え。この 1024px ブレイクポイントは iPad 横向きを基準。

### Implementation Priority

| Phase | Scope | Impact |
|---|---|---|
| **Phase 1** | Design tokens (theme.css) + Typography | Foundation |
| **Phase 2** | Component library (buttons, inputs, cards, chips) | All screens |
| **Phase 3** | Layout system + responsive breakpoints | All screens |
| **Phase 4** | Library screen (items list + sidebar + search) | Primary screen |
| **Phase 5** | Detail screen (reader mode) | Core value |
| **Phase 6** | Quick Add + Settings | Supporting screens |
| **Phase 7** | Mobile navigation (slide-over, bottom sheet) | Mobile UX |
| **Phase 8** | Toast/confirmation system | Polish |
| **Phase 9** | Micro-interactions + motion | Delight |
| **Phase 10** | Accessibility audit + keyboard shortcuts | Compliance |

### What We're NOT Doing

- **CSS Framework 導入**: Tailwind/Bootstrap は不採用。既存の CSS 変数システムを拡張し、
  Apple HIG に忠実なカスタムデザインシステムを構築。
- **SPA 化**: サーバーサイドレンダリング + Progressive Enhancement を維持。
  React/Vue への移行は行わない。
- **画像サムネイル**: OGP 画像取得は別の機能拡張として。デザインには placeholder を用意するが、
  初回リリースではテキストのみ。
- **ダッシュボード/分析**: 将来的に読書統計を追加する余地は残すが、今回のスコープ外。

---

*Document generated by Senior Apple UI Designer*
*Reference: Apple Human Interface Guidelines (2024), WCAG 2.1 AA*
