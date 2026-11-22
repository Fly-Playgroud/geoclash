#!/usr/bin/env bash
# 交叉编译 geoclash 的全部发布目标，输出到 dist/。
# 用法：scripts/build.sh [版本号]，版本号默认取 git describe，失败时为 dev。
set -euo pipefail
cd "$(dirname "$0")/.."

VERSION="${1:-$(git describe --tags --always 2>/dev/null || echo dev)}"
TARGETS=(windows/amd64 windows/arm64 darwin/amd64 darwin/arm64 linux/amd64 linux/arm64)
export CGO_ENABLED=0 GOTOOLCHAIN=local

# 只删除旧的构建产物：dist/ 中可能还放着配置与状态文件（数据默认保存在可执行文件所在目录）
mkdir -p dist && rm -f dist/geoclash-*
for t in "${TARGETS[@]}"; do
  os="${t%/*}" arch="${t#*/}"
  out="dist/geoclash-${os}-${arch}"
  ldflags="-s -w -X main.version=${VERSION}"
  if [ "$os" = windows ]; then
    out+=".exe"
    # GUI 子系统：开机自启时不弹出控制台窗口；终端中运行时通过 AttachConsole 输出
    ldflags+=" -H=windowsgui"
  fi
  echo "building ${out}"
  GOOS="$os" GOARCH="$arch" go build -trimpath -ldflags "$ldflags" -o "$out" .
done
ls -l dist
