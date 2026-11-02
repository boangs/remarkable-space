# 系统界面语言 (reMarkable_*.qm)

`reMarkable_zh_CN.ts` / `reMarkable_zh_TW.ts` / `reMarkable_ja.ts` / `reMarkable_ko.ts` 是源,
`base/dist/` 里对应的 `.qm` 是产物 (dist 不进仓库, 发版前重新生成)。原系统自带 en / de / fr / es。

```sh
for lang in zh_CN zh_TW ja ko; do
  /opt/homebrew/opt/qt6/bin/lrelease "base/i18n/reMarkable_${lang}.ts" -qm "base/dist/reMarkable_${lang}.qm"
done
```

改译文直接改 .ts (标准 Qt Linguist 格式, 按 context 分组)。**上下文规律 (2026-10-20 在 3.29 上实测):**
QML 里的 qsTr 以文件名为上下文 —— 设置页标题 General / Cloud / Security / Display / Accessibility 分别在
`General` / `Cloud` / `Security` / `Display` / `Accessibility` 上下文里 (和早就有的 `Account` / `Battery` /
`Storage` / `Help` / `Wifi` 一样); C++ 的 tr() 以类名 (含命名空间) 为上下文。老固件用过的上下文
(`SettingsModel` 等) 一律保留, 只加不删。切换系统语言时 Qt 会重新加载并重算 qsTr, 改完不用重启。

设备上这些文件在 `/usr/share/remarkable/xochitl/translations/`, 系统升级后由
`fw-upgrade.sh` 从 `/home/root/rmkit-cn/static/` 放回去; 切换系统语言时 Qt 会重新加载, 不用重启。
