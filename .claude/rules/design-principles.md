<!-- SPDX-License-Identifier: MIT -->
<!-- Adapted from cc-sdd (https://github.com/gotalab/cc-sdd), MIT License, Copyright (c) gotalab -->

# design.md 記述原則

Architect が `design.md` を書く際の全体方針と必須セクションの設計思想。

## 目的

異なる実装者の間で**実装の一貫性**を担保するのに十分な詳細を提供し、解釈のズレを防ぐこと。

## アプローチ

- 実装判断に直接影響する必須セクションを含める
- 実装ミスの防止に不可欠でない optional セクションは省略する
- 詳細度は機能の複雑度に合わせる
- 散文より **図・表** を優先する

## 警告: 1000 行を超えたら複雑すぎる

1000 行を超える design.md は、機能の複雑度が高すぎる可能性があります。設計の単純化、
もしくは複数 spec への分割（複数 Issue に切り直し）を検討してください。

## セクション順序の柔軟性

標準順序（Overview → Architecture → File Structure Plan → …）から逸脱して、
`Requirements Traceability` を前に配置したり、`Data Models` を `Architecture` の近くに
置いたりしても構いません。読みやすさのため調整してください。

各セクション内では **Summary → Scope → Decisions → Impacts/Risks** の流れを保ち、
レビュワーが一貫してスキャンできるようにします。

## 必須セクション

| セクション | 目的 | 備考 |
|---|---|---|
| Overview | 機能の狙い・ユーザー・インパクト | 2-3 段落で簡潔に |
| Goals / Non-Goals | 明示的な対象と除外 | スコープ境界を誤読されない |
| Architecture Pattern & Boundary Map | 採用パターンと境界 | 複雑機能では Mermaid 図必須 |
| Technology Stack | レイヤごとの技術選定 | 表形式 |
| File Structure Plan | ディレクトリと責務 | **tasks.md の `_Boundary:_` を駆動する** |
| Requirements Traceability | req ID → 設計要素 | 複雑機能のみ（単純 1:1 なら Components で代替可） |
| Components and Interfaces | コンポーネント詳細 / 契約 | Service / API / Event / Batch / State の契約を明示 |
| Data Models | ドメイン / 論理 / 物理モデル | 該当する範囲のみ |
| Error Handling | エラー戦略・カテゴリ・監視 | 具体的な例を含める |
| Testing Strategy | 単体／結合／E2E／性能 | 各 3-5 項目 |

## Optional セクション（必要時のみ）

- **Security Considerations**: 認証・機密情報を扱う機能、外部連携、ユーザー権限が絡む機能
- **Performance & Scalability**: 性能目標・高負荷・スケール懸念がある機能
- **Migration Strategy**: スキーマ・データ移動を伴う場合。Mermaid flowchart で段階を可視化
- **Supporting References**: 長大な型定義・ベンダー比較表など、本文で読みにくい付随情報

## File Structure Plan の書き方

- **小機能**: 個別ファイル単位で責務を列挙
- **大機能**: ディレクトリレベル + per-domain パターンを記述、非自明なファイルのみ個別列挙

例:

```
src/
├── domain-a/              # Domain A responsibility
│   ├── controller.ts      # Endpoint handlers
│   ├── service.ts         # Business logic
│   └── types.ts           # Domain types
├── domain-b/              # Domain B (same pattern as domain-a)
└── shared/
    └── cross-cutting.ts   # Non-obvious: why this exists
```

- 繰り返し構造の場合は、パターンを 1 回だけ記述（"domain-b follows same pattern as domain-a"）
- 個別ファイルはその責務がパスから自明でない場合のみ個別列挙

## 参考

- [cc-sdd `design.md` テンプレート](https://github.com/gotalab/cc-sdd/blob/main/.kiro/settings/templates/specs/design.md)
