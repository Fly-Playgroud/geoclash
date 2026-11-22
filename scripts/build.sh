#!/usr/bin/env bash
# 交叉编译 geoclash 的全部发布目标，并打包成发布用的压缩包，输出到 dist/。
# 用法：scripts/build.sh [版本号]，版本号默认取 git describe，失败时为 dev。
#
# 产物：
#   dist/geoclash-<os>-<arch>[.exe]          裸二进制（本地直接使用）
#   dist/geoclash_<版本>_<os>_<arch>.tar.gz  macOS / Linux 发布包
#   dist/geoclash_<版本>_windows_<arch>.zip  Windows 发布包
#   dist/SHA256SUMS                          发布包校验和
#
# 发布包由 scripts/pack 生成：程序统一命名为 geoclash(.exe) 并带可执行权限，附带中英文 README。
set -euo pipefail
cd "$(dirname "$0")/.."

VERSION="${1:-$(git describe --tags --always 2>/dev/null || echo dev)}"
VERSION="${VERSION#v}"
TARGETS=(windows/amd64 windows/arm64 darwin/amd64 darwin/arm64 linux/amd64 linux/arm64)
export GOTOOLCHAIN=local

# 只删除旧的构建产物：dist/ 中可能还放着配置与状态文件（数据默认保存在可执行文件所在目录）
mkdir -p dist && rm -f dist/geoclash-* dist/geoclash_* dist/SHA256SUMS

for t in "${TARGETS[@]}"; do
  os="${t%/*}" arch="${t#*/}"
  bin="dist/geoclash-${os}-${arch}"
  name="geoclash"
  ext="tar.gz"
  ldflags="-s -w -X main.version=${VERSION}"
  if [ "$os" = windows ]; then
    bin+=".exe"
    name+=".exe"
    ext="zip"
    # GUI 子系统：开机自启时不弹出控制台窗口；终端中运行时通过 AttachConsole 输出
    ldflags+=" -H=windowsgui"
  fi
  echo "building ${bin}"
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -trimpath -ldflags "$ldflags" -o "$bin" .

  pkg="geoclash_${VERSION}_${os}_${arch}"
  go run ./scripts/pack "dist/${pkg}.${ext}" "$pkg" "${bin}:${name}" README.md README.zh-CN.md
done

(cd dist && sha256sum geoclash_*.tar.gz geoclash_*.zip > SHA256SUMS)
ls -l dist
