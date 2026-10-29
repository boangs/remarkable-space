# 由 installer/install.sh 的 REMOTE_EOF 段 (六阶段防砖部署) 原样提取; 需要 env FW_VERSION (=固件身份 构建时间-版本号) ZZ_HEADER_FLAT QML_INJECT_DEPLOYED; 改动请两边同步
set -e

# 双语输出: 助手按界面语言传 UI_LANG=zh|en; m 选一条文案, say 直接打印
m() { if [ "$UI_LANG" = en ]; then printf '%s' "$2"; else printf '%s' "$1"; fi; }
say() { echo "$(m "$1" "$2")"; }

HASHTAB=/home/root/xovi/exthome/qt-resource-rebuilder/hashtab
HASHTAB_FW=/home/root/xovi/exthome/qt-resource-rebuilder/hashtab.fw_version
DEPLOY=/home/root/xovi/exthome/qt-resource-rebuilder
QMD_TOOL=/home/root/rmkit-cn/bin/qmd-tool
QMD_SRC=/home/root/rmkit-cn/qmd-src
RMKIT_DIR=/home/root/rmkit-cn
DROPIN=/etc/systemd/system/xochitl.service.d/zz-rmkit-cn.conf

# 失败时回退: 删除任何已写入的 drop-in, 让 xochitl 走出厂默认启动
abort_safe() {
  local reason="$1"
  echo "  ✗ $reason"
  say "  → 回退: 删除 drop-in (如有), 让 xochitl 走出厂默认" "  → Rolling back: removing the drop-in (if any) so xochitl boots stock"
  rm -f $DROPIN
  mkdir -p /tmp/lc && mount --bind / /tmp/lc 2>/dev/null || true
  mount -o remount,rw /tmp/lc 2>/dev/null || true
  rm -f /tmp/lc$DROPIN
  sync; umount -l /tmp/lc 2>/dev/null || true; rmdir /tmp/lc 2>/dev/null || true
  systemctl daemon-reload 2>/dev/null || true
  systemctl start xochitl.service 2>/dev/null || true  # 保证 xochitl 默认起来
  exit 1
}

# ───── 阶段 1: 装 systemd .service unit (双写 + wants symlink 双写) ─────
say "  → 阶段 1/6: 装 systemd service units..." "  → Stage 1/6: installing systemd service units..."
mkdir -p /tmp/lc && mount --bind / /tmp/lc 2>/dev/null || true
mount -o remount,rw /tmp/lc 2>/dev/null || true
mkdir -p /etc/systemd/system /tmp/lc/etc/systemd/system
mkdir -p /etc/systemd/system/multi-user.target.wants /tmp/lc/etc/systemd/system/multi-user.target.wants
for f in /tmp/rmkit-cn-systemd-staging/*.service /tmp/rmkit-cn-systemd-staging/*.path /tmp/rmkit-cn-systemd-staging/*.timer; do
  [ -f "$f" ] || continue
  base=$(basename "$f")
  cp "$f" /etc/systemd/system/$base; chmod 644 /etc/systemd/system/$base
  cp "$f" /tmp/lc/etc/systemd/system/$base; chmod 644 /tmp/lc/etc/systemd/system/$base
  case "$base" in
    remarkable-space.service|rmkit-cn-ime-http.service|rmkit-cn-version.path|rmkit-cn-ota.timer)
      ln -sf /etc/systemd/system/$base /etc/systemd/system/multi-user.target.wants/$base
      ln -sf /etc/systemd/system/$base /tmp/lc/etc/systemd/system/multi-user.target.wants/$base
      ;;
    rmkit-cn-ota-reboot.service)
      # 挂在 reboot.target 上 (重启前补跑 OTA 钩子), 不进 multi-user
      mkdir -p /etc/systemd/system/reboot.target.wants /tmp/lc/etc/systemd/system/reboot.target.wants
      ln -sf /etc/systemd/system/$base /etc/systemd/system/reboot.target.wants/$base
      ln -sf /etc/systemd/system/$base /tmp/lc/etc/systemd/system/reboot.target.wants/$base
      ;;
    rmkit-cn-rm2-reenable.service)
      # 仅 rm2 (armv7) 启用: rm2 的 /home 挂载 After=xochitl.service, 冷启动注入
      # 落空需本单元重启一次恢复。其它机型 /home 早于 xochitl, 用不上 (文件仍拷入
      # /etc 但不建 wants symlink → 不启用)。
      if [ "$(uname -m)" = "armv7l" ]; then
        ln -sf /etc/systemd/system/$base /etc/systemd/system/multi-user.target.wants/$base
        ln -sf /etc/systemd/system/$base /tmp/lc/etc/systemd/system/multi-user.target.wants/$base
      fi
      ;;
    rmkit-cn-reenable.service)
      # 仅 aarch64 (rmpp/rmppm) 启用: xochitl 不再等 /home, /home 晚到时它原生启动, 本单元在
      # /home 挂好后重启一次补上注入 (见单元文件头的说明)。
      if [ "$(uname -m)" = "aarch64" ]; then
        ln -sf /etc/systemd/system/$base /etc/systemd/system/multi-user.target.wants/$base
        ln -sf /etc/systemd/system/$base /tmp/lc/etc/systemd/system/multi-user.target.wants/$base
      fi
      ;;
  esac
done
# upload-server 已退役, 它的活全归 remarkable-space (同样 8080)。老设备上可能还留着
# 它的自启 symlink, 不撤掉重启后两个进程抢端口 → 撤 symlink 并 mask 到 /dev/null。
# 两层都做: 只写 /etc 的话重启就没了 (/etc 是 tmpfs overlay)。
for d in /etc/systemd/system /tmp/lc/etc/systemd/system; do
  rm -f $d/multi-user.target.wants/rmkit-cn-upload.service
  ln -sf /dev/null $d/rmkit-cn-upload.service
done
sync; umount -l /tmp/lc 2>/dev/null || true; rmdir /tmp/lc 2>/dev/null || true
systemctl stop rmkit-cn-upload.service 2>/dev/null || true
systemctl daemon-reload

# ───── 阶段 2: 清旧 .qmd + 生成 hashtab (版本必须匹配 /etc/version) ─────
say "  → 阶段 2/6: hashtab (固件 $FW_VERSION)..." "  → Stage 2/6: hashtab (firmware $FW_VERSION)..."

# 强制清空 $DEPLOY 旧 .qmd! 否则临时 xochitl 加载它们时,
# 旧 .qmd hash 跟新 hashtab 不匹配 → qmldiff Rust panic → 临时 xochitl 死 → hashtab 没写出
say "    清空旧 .qmd (避免临时 xochitl 加载旧 hash 时 panic)..." "    Clearing old .qmd files (so the temporary xochitl does not panic on stale hashes)..."
mkdir -p $DEPLOY
rm -f $DEPLOY/*.qmd $DEPLOY/*.rcc

# 判断是否需要重生 hashtab
NEEDS_REGEN=0
REGEN_REASON=""
if [ ! -f "$HASHTAB" ]; then
  NEEDS_REGEN=1; REGEN_REASON="$(m "hashtab 不存在" "no hashtab")"
elif [ "$(wc -c < $HASHTAB)" -lt 100000 ]; then
  NEEDS_REGEN=1; REGEN_REASON="$(m "hashtab 损坏 (<100KB)" "hashtab corrupt (<100KB)")"
elif [ ! -f "$HASHTAB_FW" ]; then
  NEEDS_REGEN=1; REGEN_REASON="$(m "hashtab 缺版本标记 (老版本残留)" "hashtab has no version marker (left by an old version)")"
elif [ "$(cat $HASHTAB_FW 2>/dev/null)" != "$FW_VERSION" ]; then
  NEEDS_REGEN=1; REGEN_REASON="$(m "hashtab 版本 $(cat $HASHTAB_FW 2>/dev/null) ≠ 固件 $FW_VERSION (OTA 升级了)" "hashtab version $(cat $HASHTAB_FW 2>/dev/null) ≠ firmware $FW_VERSION (OTA update)")"
fi

if [ $NEEDS_REGEN -eq 1 ]; then
  say "    → 重生 hashtab (原因: $REGEN_REASON)" "    → Regenerating hashtab ($REGEN_REASON)"
  systemctl stop xochitl.service 2>/dev/null || true
  sleep 1
  pidof xochitl >/dev/null 2>&1 && kill -15 $(pidof xochitl) 2>/dev/null || true
  sleep 2
  rm -f $HASHTAB $HASHTAB_FW
  # 临时 xochitl 跑 LD_PRELOAD=xovi.so + QMLDIFF_HASHTAB_CREATE, qt-resource-rebuilder 会写 hashtab
  # 此时 $DEPLOY 里已经没有 .qmd, 不会 panic
  QMLDIFF_HASHTAB_CREATE=$HASHTAB QML_DISABLE_DISK_CACHE=1 \
    LD_PRELOAD=/home/root/xovi/xovi.so /usr/bin/xochitl > /tmp/hashtab_gen.log 2>&1 &
  XPID=$!
  for i in $(seq 1 90); do
    if [ -f "$HASHTAB" ] && [ "$(wc -c < $HASHTAB)" -gt 100000 ]; then
      sleep 2; break
    fi
    sleep 1
  done
  kill -15 $XPID 2>/dev/null || true; sleep 3; kill -9 $XPID 2>/dev/null || true
  if [ ! -f "$HASHTAB" ] || [ "$(wc -c < $HASHTAB)" -lt 100000 ]; then
    abort_safe "$(m "hashtab 生成失败! 看 /tmp/hashtab_gen.log" "hashtab generation failed! See /tmp/hashtab_gen.log")"
  fi
  # 写版本标记 — 关键! 这是下次跑 install.sh 判断 hashtab 是否过期的依据
  echo "$FW_VERSION" > $HASHTAB_FW
  say "    ✓ hashtab ($(wc -c < $HASHTAB) bytes, 标记固件版本 $FW_VERSION)" "    ✓ hashtab ($(wc -c < $HASHTAB) bytes, marked firmware $FW_VERSION)"
else
  say "    → hashtab 已存在且版本匹配 ($(wc -c < $HASHTAB) bytes, 固件 $FW_VERSION)" "    → hashtab present and matches ($(wc -c < $HASHTAB) bytes, firmware $FW_VERSION)"
fi

# ───── 阶段 3: 用新 hashtab 编译 .qmd → inject 目录 + cache ─────
say "  → 阶段 3/6: 编译 .qmd..." "  → Stage 3/6: compiling .qmd..."
CACHE=$RMKIT_DIR/compiled-qmd/$FW_VERSION
mkdir -p $CACHE
COMPILE_FAILED=0
COMPILED=0
for src in $QMD_SRC/*.qmd; do
  [ -f "$src" ] || continue
  base=$(basename $src)
  if $QMD_TOOL hash -hashtab $HASHTAB $src > $DEPLOY/$base 2>/tmp/qmd-$base.err; then
    cp $DEPLOY/$base $CACHE/$base
    COMPILED=$((COMPILED+1))
    echo "    ✓ $base ($(wc -c < $DEPLOY/$base) bytes)"
  else
    rm -f $DEPLOY/$base
    COMPILE_FAILED=$((COMPILE_FAILED+1))
    say "    ✗ $base 编译失败:" "    ✗ $base failed to compile:"
    head -n 3 /tmp/qmd-$base.err 2>/dev/null | sed 's/^/        /'
  fi
done
# 静态资源
[ -f $RMKIT_DIR/static/pinyin_interceptor.qmd ] && cp $RMKIT_DIR/static/pinyin_interceptor.qmd $DEPLOY/
[ -f $RMKIT_DIR/static/zh_CN.rcc ] && cp $RMKIT_DIR/static/zh_CN.rcc $DEPLOY/

# ───── 阶段 4: 验证 — 编译失败计数为 0 才能继续 ─────
say "  → 阶段 4/6: 验证 .qmd hash 命中..." "  → Stage 4/6: verifying .qmd hash hits..."
if [ $COMPILE_FAILED -gt 0 ]; then
  abort_safe "$(m "$COMPILE_FAILED 个 .qmd 编译失败 (hash 不命中), 拒绝写 drop-in" "$COMPILE_FAILED .qmd failed to compile (hash miss); refusing to write the drop-in")"
fi
if [ $COMPILED -eq 0 ]; then
  abort_safe "$(m "没有任何 .qmd 编译成功 ($QMD_SRC 是空的?)" "No .qmd compiled at all (is $QMD_SRC empty?)")"
fi
say "    ✓ $COMPILED 个 .qmd 全部 hash 命中" "    ✓ all $COMPILED .qmd hashes hit"

# ───── 阶段 5: 写最终 drop-in (验证通过后才写, 含 ime_hook + zh_CN.rcc) ─────
say "  → 阶段 5/6: 写最终 drop-in..." "  → Stage 5/6: writing the final drop-in..."
mkdir -p /etc/systemd/system/xochitl.service.d
mkdir -p /tmp/lc && mount --bind / /tmp/lc
mount -o remount,rw /tmp/lc
mkdir -p /tmp/lc/etc/systemd/system/xochitl.service.d
# fail-open: LD_PRELOAD 指向 active/ symlink, precheck.sh 每次启动前决定挂/摘
# ([Service] 段与 systemd/zz-rmkit-cn.conf 保持一致, [Unit] 头按架构注入)
# qml_inject.so 只在本次真的部署了产物时才进 LD_PRELOAD — armv7 无此产物, 写进去
# 只会让 ld.so 每次启动刷一条 cannot be preloaded 警告 (无害但污染日志/误导诊断)。
PRELOAD="/home/root/rmkit-cn/active/xovi.so:/home/root/rmkit-cn/active/ime_hook.so"
if [ "$QML_INJECT_DEPLOYED" = "1" ]; then
  PRELOAD="$PRELOAD:/home/root/rmkit-cn/active/qml_inject.so"
fi
cat > /tmp/zz-rmkit-cn-final.conf <<EOF
$(printf '%b' "$ZZ_HEADER_FLAT")

[Service]
WatchdogSec=0
ExecStartPre=-/bin/sh /home/root/rmkit-cn/bin/precheck.sh
Environment="QML_DISABLE_DISK_CACHE=1"
Environment="QML_XHR_ALLOW_FILE_WRITE=1"
Environment="QML_XHR_ALLOW_FILE_READ=1"
Environment="LD_PRELOAD=$PRELOAD"
Environment="QT_RESOURCE_REBUILDER_PATH=/home/root/xovi/exthome/qt-resource-rebuilder/zh_CN.rcc"
EOF
# 首次建 active symlink + 清熔断残留 (阶段 4 已验证 qmd 全部命中, 初始注入是安全的;
# 之后每次启动由 precheck.sh 接管 symlink 生死)
mkdir -p /home/root/rmkit-cn/active /home/root/rmkit-cn/quarantine
ln -sf /home/root/xovi/xovi.so /home/root/rmkit-cn/active/xovi.so
ln -sf /home/root/rmkit-cn/bin/ime_hook.so /home/root/rmkit-cn/active/ime_hook.so
if [ "$QML_INJECT_DEPLOYED" = "1" ] && [ -f /home/root/rmkit-cn/bin/qml_inject_impl.so ]; then
  ln -sf /home/root/rmkit-cn/bin/qml_inject.so /home/root/rmkit-cn/active/qml_inject.so
fi
rm -f /home/root/rmkit-cn/.fuse_tripped /home/root/rmkit-cn/.starts
cp /tmp/zz-rmkit-cn-final.conf $DROPIN
cp /tmp/zz-rmkit-cn-final.conf /tmp/lc$DROPIN
chmod 644 $DROPIN /tmp/lc$DROPIN
sync; umount -l /tmp/lc 2>/dev/null || true; rmdir /tmp/lc 2>/dev/null || true
rm -f /tmp/zz-rmkit-cn-final.conf
systemctl daemon-reload
say "    ✓ drop-in 写入 (tmpfs + ext4 lower 双写持久化)" "    ✓ drop-in written (tmpfs + ext4 lower, persisted twice)"

# ───── 阶段 6: 启动 services + xochitl ─────
say "  → 阶段 6/6: 启动服务..." "  → Stage 6/6: starting services..."
# restart 而不是 start: 升级时这两个服务本来就在跑, start 是空操作, 新二进制要等设备重启才生效
# (2026-10-20 rmpp 升到 v1.0.11 后商店还是 404, 就是旧后台进程没换)
systemctl restart remarkable-space.service rmkit-cn-ime-http.service 2>/dev/null || true
systemctl start rmkit-cn-ota.timer 2>/dev/null || true
[ -f /etc/systemd/system/rmkit-cn-version.path ] && systemctl start rmkit-cn-version.path 2>/dev/null || true

# 阶段 2 stop 了 xochitl, 现在 start (drop-in 首次生效)
# 如果阶段 2 没 stop (hashtab 跳过), xochitl 在跑出厂版本, 这里 restart 让 drop-in 生效
say "    → start xochitl (drop-in 首次生效)..." "    → starting xochitl (drop-in takes effect)..."
if pidof xochitl >/dev/null 2>&1; then
  systemctl restart xochitl.service
else
  systemctl start xochitl.service
fi
sleep 5
# 安全验证: xochitl 必须真的活着, 否则立即回退删 drop-in
# (start-limit 锁死之前抓住)
XPID=$(pidof xochitl 2>/dev/null)
if [ -z "$XPID" ]; then
  say "    ✗ xochitl 没启动起来! 立即回退..." "    ✗ xochitl did not start! Rolling back..."
  say "    journal 倒数 20 行:" "    last 20 journal lines:"
  journalctl -u xochitl.service --no-pager -n 20 2>&1 | sed 's/^/      /' || true
  abort_safe "$(m "xochitl 启动失败 (.qmd 验证通过但运行时 panic? 看 journal)" "xochitl failed to start (.qmd verified but panicked at runtime? see journal)")"
fi
# 再等 5 秒确认稳定 (避免起来又 crash)
sleep 5
XPID2=$(pidof xochitl 2>/dev/null)
if [ -z "$XPID2" ] || [ "$XPID" != "$XPID2" ]; then
  say "    ✗ xochitl 起来但很快 crash/重启 (PID $XPID → $XPID2)!" "    ✗ xochitl started but crashed/restarted right away (PID $XPID → $XPID2)!"
  journalctl -u xochitl.service --no-pager -n 30 2>&1 | sed 's/^/      /' || true
  abort_safe "xochitl crash loop"
fi
echo "    ✓ xochitl (PID $XPID, $(m "稳定 10 秒" "stable for 10 s")) — xovi: $(grep -c xovi /proc/$XPID/maps 2>/dev/null), ime_hook: $(grep -c ime_hook /proc/$XPID/maps 2>/dev/null)"

rm -rf /tmp/rmkit-cn-systemd-staging
say "  ✓ 部署完成" "  ✓ Deployment complete"
