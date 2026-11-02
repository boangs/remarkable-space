# 底座 (base)

把「空间」接进 reMarkable 系统的那一层：xovi、qmldiff 注入链与 hashtab、运行时注入 `.so`
(侧栏「空间」入口就是它塞进去的)、新增系统语言 `.qm` 与中文键盘布局 `.rcc`、输入法后端、
系统升级后的自检与恢复脚本。**砖机风险全在这一层**，只在系统升级逼着改时才动。

2026-10-20 从 [boangs/rmkit](https://github.com/boangs/rmkit) (`0f17826`) 迁入，此前的提交历史留在那个仓库。
目录结构与原仓库一一对应，脚本里的相对路径不用改；设备上的路径也一律不变
(`/home/root/rmkit-cn/...`、`~/.local/share/rmkit-cn/...`)，改了用户数据会凭空消失。

| 目录 | 内容 |
|---|---|
| `intercept/` | `ime_hook.so` 与运行时注入 `qml-inject/` (薄钩子 + 胖库) |
| `qmd-src/` `qmd/` | qmldiff 补丁源、中文键盘布局 (`zh_CN.rcc` 的内容) |
| `installer/` | 设备端脚本：`install.sh`、`precheck.sh` (fail-open)、`fw-upgrade.sh`、`ota-watch.sh` … |
| `systemd/` | xochitl drop-in 与 OTA / 输入法服务 |
| `ime-go/` | 纯 Go 输入法后端 (rm2 回退引擎) |
| `tools/` | `qmd-tool`、`create_rcc.go`、librime / 蓝牙音频交叉编译脚本、hashtab 样本 |
| `vendor/` | 预编译的 xovi 与扩展、蓝牙音频包 |
| `scripts/` | 发版体检 (`check-dist.sh`、`lse_scan.py`)、翻译合并工具 |
| `docs/` | 架构、开发、设备矩阵、升级 SOP |

`installer/install.sh` 是旧的命令行安装器，现在只当参照：助手 (`assistant/internal/deploy`)
是它的 Go 移植，注释里的行号指向它。它还会去装已退役的 upload-server (`upload-server-go/` 没迁过来)，
不能再直接跑。

产物在 `dist/` (不进仓库)，助手打包时取它：`cd assistant && make bundle`。
librime 源码树 `third_party/` (约 430MB) 也只放本机。

没迁过来的 (仍在 rmkit 仓库)：Android 双启动 (`android/`)、旧桌面助手 (`desktop/`，已由
`assistant/` 取代)、旧后端 `upload-server-go/` (已由 `space-server` 取代)、`legacy/`、
固件 QML 导出 `qml-dump/`、输入法词库的原始语料 `ime-go/tools/build_dict/cpd.txt` (43MB，
编好的词库已在 `ime-go/pinyin/dict/`)。

第三方代码与许可证见 [NOTICE.md](NOTICE.md) 与 `third-party-licenses/`。
