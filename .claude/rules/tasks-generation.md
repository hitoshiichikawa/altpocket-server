<!-- SPDX-License-Identifier: MIT -->
<!-- Adapted from cc-sdd (https://github.com/gotalab/cc-sdd), MIT License, Copyright (c) gotalab -->

# tasks.md 生成ルール

Architect が出力する `tasks.md` は、Developer が迷わず実装を進められる粒度と、
トレーサビリティを持つアノテーションを持たせます。

## 基本フォーマット

### 単純タスクのみの場合

```markdown
- [ ] 1. <タスクの要約>
  - <詳細項目（必要な場合のみ）>
  - _Requirements: 1.1, 2.3_
```

### 親タスクと子タスクの構造を取る場合

```markdown
- [ ] 1. <親タスクの要約>
- [ ] 1.1 <子タスクの記述> (P)
  - <詳細項目 1>
  - <詳細項目 2>
  - _Requirements: 1.1, 1.2_
  - _Boundary: UserService, AuthController_
  - _Depends: 2.1_
```

## アノテーション

| キー | 必須? | 用途 |
|---|---|---|
| `_Requirements:_` | **必須** | 対応する requirement ID を列挙（numeric のみ、例: `1.1, 2.3`）。説明や括弧書きは付けない |
| `_Boundary:_` | 並列可タスク `(P)` でのみ必須 | 担当するコンポーネント名を列挙（design.md の Components 名と一致） |
| `_Depends:_` | 非自明な cross-boundary 依存のみ | 先行するタスク ID を列挙。自明な順序依存は省略 |

## 並列マーカー `(P)`

- **並列実行可能**なタスクのみ末尾に ` (P)` を付ける
- 並列実行できないタスク（順序依存のあるタスク）には付けない（デフォルト=直列）
- `(P)` を付けるなら `_Boundary:_` を必須とする（並列時の競合境界を明示するため）

## ID 規則

- **numeric 階層 ID** のみ使用: `1`, `1.1`, `1.2`, `2`, `2.1` ...
- `T-01` や `FR-01` 形式の英字 ID は使わない（requirements.md の numeric ID と揃えるため）

## Optional なテストタスク

deferrable なテスト追加タスクは checkbox を `- [ ]*`（アスタリスク付き）と記述し、詳細項目で
対応する AC を説明します:

```markdown
- [ ]* 1.3 統合テスト追加
  - 対応する受入基準のうち、現時点でカバレッジが不足する項目を補完
  - _Requirements: 1.1, 1.2_
```

## ガイドライン

- 各タスクは **1 commit 単位**で独立に完了可能な粒度にする
- 合計タスク数は **3〜10 件を目安**（多すぎる場合は design の File Structure Plan が大きすぎる可能性）
- 対応する `_Requirements:_` を必ず明示（トレーサビリティ確保）
- 親タスクに対する子タスクは、実装順序に沿って並べる

## 参考

- [cc-sdd `tasks.md` テンプレート](https://github.com/gotalab/cc-sdd/blob/main/.kiro/settings/templates/specs/tasks.md)
