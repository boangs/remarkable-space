# 由 installer/install.sh 的 tar 接收段原样提取: 从 stdin 解 payload 树到 /
  set -e
  mount -o remount,rw / 2>/dev/null || true
  mkdir -p /home/root/.local/share/rmkit-cn/fonts \
           /home/root/.local/share/rmkit-cn/screens \
           /home/root/.local/share/rmkit-cn/space/apps \
           /home/root/.local/share/rmkit-cn/space/data \
           /home/root/.local/share/fonts \
           /usr/share/remarkable/xochitl/translations \
           /home/root/xovi/exthome/qt-resource-rebuilder
  # --no-same-owner 防止 tar 把 macOS 端打包时的 uid (xurx=502) 还原到设备文件,
  # 否则 /home/root owner 被改成 502 导致 sshd PAM/xochitl home 访问全卡死, 设备砖机
  # 内置应用目录完全由安装器管, 先清空再解包 —— tar 只覆盖同名文件, 不会删已经移走的。
  # 应用从内置挪到商店后 (五个小游戏就是这么挪的), 老设备上的内置副本会一直留着:
  # 用户从商店装了新版再删掉, 内置那份又冒出来, 看着就是"删不掉"。
  # 用户自己装的在 ~/.local/share/.../apps, 不在这里, 清空伤不到。
  rm -rf /home/root/rmkit-cn/space/apps
  cd / && tar -xzf - --no-same-owner --no-same-permissions
  # 兜底: 把 rmkit 自己投放的目录 owner 设回 root, 防御任何残留 502。
  # 只限 rmkit/xovi 路径, 绝不 -R 整个 /home/root: 单槽 Android 的系统与 /data 也在 /home/root 下,
  # 整树 chown 会把 Android 应用数据属主全改成 root (应用全崩) 并清掉 setuid 位 (2026-09-07 事故)。
  chown root:root /home/root
  chown -R root:root /home/root/rmkit-cn /home/root/xovi /home/root/remarkable-space \
                     /home/root/.local/share/rmkit-cn 2>/dev/null || true
  chmod 755 /home/root
  [ -d /home/root/.ssh ] && chmod 700 /home/root/.ssh
  [ -f /home/root/.ssh/authorized_keys ] && chmod 600 /home/root/.ssh/authorized_keys
  # 语言包由商店独立管理 (系统语言包应用), 会随官方固件新增词条单独发版。
  # 助手安装包里的 .qm 是打包时的版本, 可能比用户装的新版旧 —— 装过商店语言包就恢复它,
  # 别把助手包里的旧翻译覆盖回去。标记文件由 langpack 的 install.sh 写入。
  LP=/home/root/.local/share/rmkit-cn/space/apps/langpack/translations
  if [ -f /home/root/rmkit-cn/.langpack_version ] && [ -d "$LP" ]; then
      for lang in zh_CN zh_TW ja ko; do
          [ -f "$LP/reMarkable_${lang}.qm" ] || continue
          cp -f "$LP/reMarkable_${lang}.qm" /usr/share/remarkable/xochitl/translations/
          cp -f "$LP/reMarkable_${lang}.qm" /home/root/rmkit-cn/static/
      done
  fi
  exit 0
