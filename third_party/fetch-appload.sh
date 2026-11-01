#!/bin/sh
# 取 rm-appload 的官方发布包, 给安装包用。
#
# 为什么不直接把 .so 放进仓库: 它是 GPL-3.0 的第三方二进制, 我们一旦随安装包分发,
# 就要跟着承担"提供对应源码"的义务。构建时从官方拉则不构成我们分发, 仓库也不必
# 背 4.8 MB 的二进制。
#
# 版本和 sha256 写死在这里 —— 对方换了包要当场知道, 而不是装到设备上才发现。
# 升级 appload 就改这两行, 顺手跑一遍真机。
set -eu
# 注意目录名: 这个仓库根有 go.mod, vendor/ 是 Go 的保留目录 ——
# 放在那儿 go build 会要 vendor/modules.txt 然后拒绝编译。

VERSION=v0.6.0
BASE=https://github.com/asivery/rm-appload/releases/download/$VERSION

# arch:文件名:sha256
SPECS="aarch64:appload-aarch64.tar.gz:2e7912d15df910631dab847ea5d344081ad75c02a46aaac78bdf731accc53a1f armv7:appload-arm32.tar.gz:25aa54735742ea63c0a043cba5496987dd4ca742d8b3fc26a21b7368372bf44f"

HERE=$(cd "$(dirname "$0")" && pwd)
OUT="$HERE/appload"
mkdir -p "$OUT"

for spec in $SPECS; do
  arch=$(echo "$spec" | cut -d: -f1)
  file=$(echo "$spec" | cut -d: -f2)
  want=$(echo "$spec" | cut -d: -f3)
  dst="$OUT/$file"

  if [ -f "$dst" ]; then
    got=$(shasum -a 256 "$dst" 2>/dev/null | cut -d' ' -f1 || sha256sum "$dst" | cut -d' ' -f1)
    if [ "$got" = "$want" ]; then
      echo "  · $file 已有且校验通过"
      continue
    fi
    echo "  · $file 校验不符, 重新下载"
    rm -f "$dst"
  fi

  echo "  ↓ $file ($VERSION)"
  curl -sSL --max-time 180 -o "$dst.part" "$BASE/$file"
  got=$(shasum -a 256 "$dst.part" 2>/dev/null | cut -d' ' -f1 || sha256sum "$dst.part" | cut -d' ' -f1)
  if [ "$got" != "$want" ]; then
    rm -f "$dst.part"
    echo "✗ $file 校验不符 (期望 $want, 实际 $got) —— 上游可能换了包, 别用" >&2
    exit 1
  fi
  mv "$dst.part" "$dst"
done

# 摊开给 mkbundle 用: appload.so 进 extensions.d, shims 进 /home/root/shims
for spec in $SPECS; do
  arch=$(echo "$spec" | cut -d: -f1)
  file=$(echo "$spec" | cut -d: -f2)
  rm -rf "$OUT/$arch" && mkdir -p "$OUT/$arch"
  tar xzf "$OUT/$file" -C "$OUT/$arch"
  echo "✓ appload $VERSION ($arch) 就位: $(ls "$OUT/$arch" | tr '\n' ' ')"
done
