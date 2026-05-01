<!-- SPDX-License-Identifier: MIT -->
<!-- Adapted from cc-sdd (https://github.com/gotalab/cc-sdd), MIT License, Copyright (c) gotalab -->

# 要件定義レビューゲート（PM 自己レビュー）

Product Manager が `requirements.md` を書き終える前に、このゲートに従ってドラフトをレビューし、
問題があれば修正、問題なければ確定します。

## スコープ・カバレッジレビュー

- 主要なユーザー動線、スコープ境界、主要なエラーケース、ユーザー／運用者から見える edge condition
  をカバーしているか
- 業務・ドメインルール、コンプライアンス制約、セキュリティ／プライバシー要件、運用制約のうち、
  ユーザー可視の挙動に影響するものが明示されているか
- カバー不足が draft の不完全さなら → ドラフトを修正して再レビュー
- カバー不足が Issue 記述・既存ドキュメントの曖昧さが原因なら → 推測せず `確認事項` に
  列挙して、Issue コメントでの人間エスカレーションを提案

## EARS・テスト可能性レビュー

- すべての AC が [`ears-format.md`](./ears-format.md) に準拠しているか
- すべての要件が testable / observable / specific か
- 実装詳細が紛れ込んでいないか（→ `design.md` の領分なので requirements から除外）
- 要件見出しが **numeric ID のみ** であること（`Requirement 1`, `1.1` など。英字 ID `Requirement A` は不可）

## 構造・品質レビュー

- 関連する挙動をまとまった要件エリアにグルーピングし、同じ義務を複数箇所に重複記述していないか
- スコープの包含・除外境界が誤読されない程度に明確か
- 非機能要件が user-observable または operator-observable な粒度か
  （技術選定・内部構造は design に委ねる）
- "fast" "robust" "secure" 等の曖昧語を具体化しているか（[`ears-format.md`](./ears-format.md) 参照）

## Mechanical Checks（自動的に確認できる項目）

判断レビューの前に、機械的にチェックします:

- **Numeric ID の確認**: すべての要件見出しに numeric ID がある（`1`, `1.1`, `2` など）。
  見出し ID 欠落を scan
- **AC の存在**: すべての要件に EARS 形式の AC が 1 つ以上ある
  （`When` / `If` / `While` / `Where` / `The <system> shall` のいずれかで始まる文が含まれる）
- **実装語彙の混入チェック**: DB 名・フレームワーク名・API パターン等の技術用語が混入していないか scan

## レビュー・ループ

- Mechanical Checks を先に実施、続いて判断レビュー
- 問題が draft 内で閉じるなら修正して再レビュー
- **最大 2 パス**で確定するか、人間エスカレーションを選ぶ（無限ループを避ける）
- ゲート通過後に `requirements.md` を確定させる

## 参考

- [cc-sdd `requirements-review-gate.md`](https://github.com/gotalab/cc-sdd/blob/main/.kiro/settings/rules/requirements-review-gate.md)
