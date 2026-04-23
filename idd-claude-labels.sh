#!/usr/bin/env bash
# =============================================================================
# idd-claude: GitHub ラベル一括作成スクリプト
#
# 使い方:
#   cd /path/to/your-project
#   bash .github/scripts/idd-claude-labels.sh
#
#   # 明示的に repo を指定（repo 外から呼ぶ場合）
#   bash .github/scripts/idd-claude-labels.sh --repo owner/repo
#
#   # 既存ラベルの color / description を上書き更新
#   bash .github/scripts/idd-claude-labels.sh --force
#
# 依存: gh CLI（`gh auth login` 済み）
# =============================================================================

set -euo pipefail

REPO=""
FORCE=""

while [[ $# -gt 0 ]]; do
  case $1 in
    --repo)
      REPO="$2"
      shift 2
      ;;
    --force|-f)
      FORCE="--force"
      shift
      ;;
    -h|--help)
      sed -n '3,16p' "$0"
      exit 0
      ;;
    *)
      echo "未知のオプション: $1" >&2
      exit 1
      ;;
  esac
done

command -v gh >/dev/null 2>&1 || {
  echo "Error: 'gh' CLI が必要です。https://cli.github.com" >&2
  exit 1
}

REPO_ARG=()
if [ -n "$REPO" ]; then
  REPO_ARG=(--repo "$REPO")
fi

# Label definitions: name|color|description
LABELS=(
  "auto-dev|1f77b4|自動開発対象"
  "needs-decisions|f1c40f|人間の判断が必要"
  "awaiting-design-review|e67e22|設計 PR レビュー待ち（Architect 発動時）"
  "claude-picked-up|9b59b6|Claude Code 実行中"
  "ready-for-review|2ecc71|PR 作成完了"
  "claude-failed|e74c3c|自動実行が失敗"
  "skip-triage|95a5a6|Triage をスキップ"
)

echo "📌 idd-claude ラベルを作成します"
if [ -n "$REPO" ]; then
  echo "   対象: $REPO"
else
  echo "   対象: カレントディレクトリの git repo（gh auto-detect）"
fi
echo ""

CREATED=0
EXISTS=0
UPDATED=0
FAILED=0

for spec in "${LABELS[@]}"; do
  IFS="|" read -r NAME COLOR DESC <<< "$spec"
  printf "  %-25s ... " "$NAME"
  if [ -n "$FORCE" ]; then
    if gh label create "$NAME" --color "$COLOR" --description "$DESC" --force "${REPO_ARG[@]}" >/dev/null 2>&1; then
      echo "created/updated"
      UPDATED=$((UPDATED+1))
    else
      echo "FAILED"
      FAILED=$((FAILED+1))
    fi
  else
    if gh label create "$NAME" --color "$COLOR" --description "$DESC" "${REPO_ARG[@]}" 2>/dev/null; then
      echo "created"
      CREATED=$((CREATED+1))
    else
      # 既存ラベルかどうか確認
      if gh label list "${REPO_ARG[@]}" --limit 100 --json name --jq '.[].name' 2>/dev/null | grep -qx "$NAME"; then
        echo "already exists (skipped; use --force to update)"
        EXISTS=$((EXISTS+1))
      else
        echo "FAILED"
        FAILED=$((FAILED+1))
      fi
    fi
  fi
done

echo ""
echo "== 結果 =="
echo "  新規作成: $CREATED"
echo "  既存スキップ: $EXISTS"
echo "  上書き更新: $UPDATED"
echo "  失敗: $FAILED"

if [ "$FAILED" -gt 0 ]; then
  exit 1
fi
