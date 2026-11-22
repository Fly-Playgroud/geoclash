#!/usr/bin/env bash
# 用上一个 tag 到指定 tag 之间的提交信息生成发布说明（Markdown），输出到标准输出。
# 每个提交的标题作为小标题，正文（改动要点）原样保留；末尾附版本对比链接。
# 用法：scripts/release-notes.sh <tag> [owner/repo]
set -euo pipefail
cd "$(dirname "$0")/.."

TAG="${1:?用法：scripts/release-notes.sh <tag> [owner/repo]}"
REPO="${2:-${GITHUB_REPOSITORY:-}}"
PREV="$(git describe --tags --abbrev=0 "${TAG}^" 2>/dev/null || true)"
RANGE="${PREV:+${PREV}..}${TAG}"

echo "## Changes"
echo
git log --no-merges --reverse --format='### %s%n%n%b%n' "$RANGE" | cat -s
if [ -n "$PREV" ] && [ -n "$REPO" ]; then
  echo "**Full Changelog**: https://github.com/${REPO}/compare/${PREV}...${TAG}"
fi
