<!-- SPDX-License-Identifier: MIT -->
<!-- Adapted from cc-sdd (https://github.com/gotalab/cc-sdd), MIT License, Copyright (c) gotalab -->

# 設計書レビューゲート（Architect 自己レビュー）

Architect が `design.md` を書き終える前に、このゲートに従ってドラフトをレビューし、
問題があれば修正、問題なければ確定します。

## 要件カバレッジレビュー

- `requirements.md` の **すべての numeric requirement ID**（1, 1.1, 2, 2.1 ...）が design.md の
  Requirements Traceability マッピングに現れ、具体的なコンポーネント・契約・フロー・データモデル・
  運用判断のいずれかで裏打ちされているか
- 外部依存・連携点・ランタイム前提・マイグレーション・可観測性・セキュリティ・
  パフォーマンス目標が明示的に design.md に反映されているか
- カバー不足が draft 不完全さなら修正、**要件自体が曖昧なら requirements に戻す**
  （設計側で要件を勝手に発明しない）

## アーキテクチャ準備レビュー

- コンポーネント境界が、実装タスクの担当を推測せず割り当てられる程度に明示されているか
- Interfaces / Contracts / State transitions / 統合境界が、実装と検証のために十分具体的か
- build vs adopt の判断のうち、アーキテクチャに材料的に影響するものが design.md に記録されているか
- ランタイム前提・マイグレーション・ロールアウト制約・検証フック・障害モードのうち、
  実装順序やリスクに影響するものが可視化されているか

## 実行可能性レビュー

- 設計が、隠れた前提なしで**境界のあるタスク列**として実装可能か
- 並列実装を意図する箇所では parallel-safe な境界が見えているか
- 投機的抽象化（将来スコープのためだけに存在するコンポーネント／アダプタ／インターフェース）を排除しているか
- tasks.md で直接参照できない程に曖昧なセクションは、確定前に書き直す

## Mechanical Checks（自動確認項目）

判断レビューの前に、機械的に確認します:

- **Requirements traceability**: requirements.md から numeric ID を全抽出し、design.md のどこかに
  出現するか scan。未参照 ID を報告
- **File Structure Plan の充填**: File Structure Plan セクションに具体的なファイルパスが
  書かれているか（"TBD" やプレースホルダを検出）
- **orphan component なし**: design.md の Components セクションに挙がったコンポーネント名のうち、
  File Structure Plan に対応ファイルが無いものを検出

## レビュー・ループ

- Mechanical Checks → 判断レビューの順
- 問題が draft 内で閉じるなら修正して再レビュー
- **最大 2 パス**で確定するか、要件フェーズに戻す（無限ループを避ける）
- ゲート通過後に `design.md` を確定させる

## 参考

- [cc-sdd `design-review-gate.md`](https://github.com/gotalab/cc-sdd/blob/main/.kiro/settings/rules/design-review-gate.md)
