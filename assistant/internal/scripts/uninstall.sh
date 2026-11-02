# 由 installer/install.sh 的 --uninstall 段原样提取 (去掉外层 ssh 引号转义); 改动请两边同步
REMOTE_BASE=/home/root/rmkit-cn
    systemctl stop    rmkit-cn-upload.service rmkit-cn-version.path rmkit-cn-ime-http.service 2>/dev/null || true
    systemctl disable rmkit-cn-upload.service rmkit-cn-version.path rmkit-cn-ime-http.service 2>/dev/null || true
    # rm2 老安装还有 OTA 定时器与冷启动重启服务, 以前漏清 (2026-10-18)
    systemctl stop    rmkit-cn-ota.timer 2>/dev/null || true
    systemctl disable rmkit-cn-ota.timer rmkit-cn-rm2-reenable.service rmkit-cn-reenable.service 2>/dev/null || true
    # 历史 Python IME unit (现已归档到 legacy/ime-py/), 老设备上可能残留, 一并清
    systemctl stop    rmkit-cn-ime.service rmkit-cn-ime-udev.service 2>/dev/null || true
    systemctl disable rmkit-cn-ime.service rmkit-cn-ime-udev.service 2>/dev/null || true
    # 空间后端顶替了 rmkit-cn-upload, 原来这里只停老的那个, 新的会留着跑
    systemctl stop    remarkable-space.service 2>/dev/null || true
    systemctl disable remarkable-space.service 2>/dev/null || true
    rm -rf $REMOTE_BASE /home/root/remarkable-space
    # 蓝牙音频组件 (bluealsa/mpg123 + D-Bus 策略只在 /etc 上层 tmpfs, 重启即消)
    killall -q mpg123 2>/dev/null || true
    killall -q bluealsa 2>/dev/null || true
    rm -rf /home/root/.local/opt/rmkit-audio
    rm -f /etc/dbus-1/system.d/bluealsa.conf 2>/dev/null || true
    # 清 upper (overlay 上层 tmpfs) + lower (ext4) 的 unit / drop-in
    mount -o remount,rw / 2>/dev/null || true
    MNT=/tmp/rmkit-cn-uninst-rootfs
    mkdir -p $MNT && mount --bind / $MNT 2>/dev/null || true
    for D in /etc $MNT/etc; do
      rm -f $D/systemd/system/rmkit-cn-*.service $D/systemd/system/rmkit-cn-*.path $D/systemd/system/rmkit-cn-*.timer
      rm -f $D/systemd/system/*.wants/rmkit-cn-*
      rm -f $D/systemd/system/xochitl.service.d/zz-rmkit-cn.conf \
            $D/systemd/system/xochitl.service.d/zz-rmkit-cn.conf.bak* \
            $D/systemd/system/xochitl.service.d/zz-rmkit-cn.conf.old
      # 空间自己的那份 xochitl drop-in 一直漏着没清
      rm -f $D/systemd/system/xochitl.service.d/zz-space.conf
      rmdir $D/systemd/system/xochitl.service.d 2>/dev/null || true
      # 空间后端的 unit 与它的 drop-in (应用商店握手接口)
      rm -f $D/systemd/system/remarkable-space.service
      rm -f $D/systemd/system/remarkable-space.service.d/10-store-api.conf
      rmdir $D/systemd/system/remarkable-space.service.d 2>/dev/null || true
      rm -f $D/systemd/system/multi-user.target.wants/remarkable-space.service
    done
    if mountpoint -q $MNT 2>/dev/null; then sync; umount -l $MNT 2>/dev/null || true; rmdir $MNT 2>/dev/null || true; fi
    rm -f /etc/udev/rules.d/99-rmkit-cn-ime.rules
    # 清理 XOVI QMD 文件 (通配: 顶层 *.qmd 全部由 rmkit-cn 投放, 同 precheck.sh 的
    # rm -f $DEPLOY/*.qmd; 逐个列名历史上漏过 glyph_selection_ai / pinyin_interceptor,
    # 残留 qmd 配上新固件 hashtab 会让 qmldiff panic。别的扩展用子目录如 chess/, 不受影响)
    rm -f /home/root/xovi/exthome/qt-resource-rebuilder/*.qmd
    rm -f /home/root/xovi/exthome/qt-resource-rebuilder/zh_CN.rcc
    rm -rf /home/root/xovi/exthome/qt-resource-rebuilder/zh_CN
    # 清理 xovi 扩展
    rm -f /home/root/xovi/extensions.d/librarian.so /home/root/xovi/extensions.d/xovi-message-broker.so
    # 清理我们新增的系统翻译 qm
    mount -o remount,rw / 2>/dev/null || true
    rm -f /usr/share/remarkable/xochitl/translations/reMarkable_zh_CN.qm \
          /usr/share/remarkable/xochitl/translations/reMarkable_zh_TW.qm \
          /usr/share/remarkable/xochitl/translations/reMarkable_ja.qm \
          /usr/share/remarkable/xochitl/translations/reMarkable_ko.qm
    systemctl daemon-reload
    udevadm control --reload-rules 2>/dev/null || true
    if [ "$UI_LANG" = en ]; then echo 'Uninstall complete'; else echo '卸载完成'; fi
