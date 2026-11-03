# reMarkable 空间 (SPACE)

[English](README.md) · 中文

「空间」是 reMarkable 上的应用桌面：首页卡片一眼看到今日计划、天气和正在播放的音乐，
应用从商店一键安装、随时卸载；安装不替换系统镜像，但会接入设备启动流程。

## 截图

| 空间首页 | 天气 | 计划 |
|---|---|---|
| ![空间首页](docs/photos/space-1017.jpg) | ![天气](docs/photos/weather-1017.jpg) | ![计划](docs/photos/plan-1017.jpg) |

| 音乐 | 播客 | 电台 |
|---|---|---|
| ![音乐](docs/photos/music-1017.jpg) | ![播客](docs/photos/podcast-1017.jpg) | ![电台](docs/photos/radio-1017.jpg) |

## 下载安装

用「空间助手」装：电脑用 USB 线连上 reMarkable，助手会把空间连同它需要的底座一次装好。

| 助手 | 下载 |
|---|---|
| Mac | [space-assistant-mac.zip](https://github.com/boangs/remarkable-space/releases/latest/download/space-assistant-mac.zip) |
| Windows | [space-assistant-windows.exe](https://github.com/boangs/remarkable-space/releases/latest/download/space-assistant-windows.exe) |

校验值见 [Release](https://github.com/boangs/remarkable-space/releases/latest) 里的 `SHA256SUMS`。

助手装空间时还要下载约 140 MB 的安装包，默认走国内下载，可以在助手的「设置 → 下载来源」里改成 GitHub。
下不动会自动换另一处；下完按助手里写死的校验值核对，对不上不会装。

支持 reMarkable 2、Paper Pro、Paper Pro Move。Mac 首次打开如果提示「无法验证开发者」，
在「系统设置 → 隐私与安全性」里点「仍要打开」。

## 升级 reMarkable 系统后，空间不见了？请等 2 分钟

升级完第一次开机，你会先看到**原厂的英文界面，没有空间**。这是正常的，空间没有丢：它正在后台按新系统重新调整。

- 请**等 1 到 2 分钟，什么都不要动**。
- 调整好以后，**屏幕会自己闪一下、重启一次界面**，中文和空间就回来了。
- 这段时间里请不要重启设备，不要重装空间，也不要去刷机。打断它反而可能让设备起不来。

只有升级后第一次开机这样，以后开机都和平时一样。

**等了 5 分钟还是英文、没有空间，或者中文还在、但空间里的应用全不见了：** 用助手连上设备，重新安装一次空间即可。（v1.0.6 之前装的空间，升级系统后可能出现应用全不见的情况；用 v1.0.6 及以后的助手装过一次，以后升级系统会自动恢复。）

**设备卡住开不了机、反复重启：** 用 USB 线连到电脑，打开空间助手，在连接页展开「设备起不来」，填好 SSH 密码，点「紧急修复」，再让设备重启。修复后设备以原厂状态启动，笔记不受影响，之后用助手重新装空间就行。

## 目录

以下是给开发者的说明。空间负责首页小组件、应用注册表、绿色安装和应用后台托管，
原是 [rmkit-cn](https://github.com/boangs/rmkit) 的一部分。2026-10-20 起底座也迁进来了（`base/`），rmkit 仓库不再更新。

| 这里 | 在 rmkit-cn 里的位置 | 说明 |
|---|---|---|
| `shell/` | `space/shell/` | 外壳 `Space.qml`：标签页、小组件挂载、应用创建销毁、给应用的 `space` API |
| `kit/` | `space/kit/` | 共享 QML 组件（`SRow` / `STile` / `SButton` …）与图标 |
| `base/` | 整个仓库 | 底座：注入链、系统语言、输入法后端、升级恢复脚本，见 [base/README.md](base/README.md) |
| `apps/` | `space/apps/` | **内置**应用，随安装器部署。只放系统功能入口（设置类）——第三方应用走商店安装，源码在另一个仓库 |
| `internal/space/` | `upload-server-go/internal/space/` | 后端 Go 包：注册表、安装、后台托管、最近在读、天气地图渲染 |
| `cmd/space-server/` | 服务入口 | Space 服务启动入口 |
| `assistant/` | 安装工具 | 桌面安装、卸载、紧急修复与诊断工具 |

## 设计约定

- **外壳不认任何具体应用**。应用靠 `manifest.json` 自我描述，外壳只读注册表。
- **应用之间互不可见**，只能通过外壳给的 `space` API 说话。
- **墨水屏优先**：大面积浅色会被抖成灰，所以底一律纯白，颜色只用在小面积实心元素上。
- **免缓存令牌**：QML 引擎按 URL 缓存编译结果与位图。开发模式下外壳给 `file://` 加
  `?t=<rev>`（`rev` = 应用目录最新改动时间），文件没动就命中缓存，改了自动失效。
  应用内部拼资源路径要走 `space.bust(url)`，否则换了素材看不见。
- **相对 import 不带令牌**：`.js` 和子 QML 文件改了必须重启 xochitl，所以配色表、
  插画清单这类要频繁调的东西放在 `main.qml` 里或外部 JSON（XHR 读）。

## 应用清单字段

多语言支持没有改变 manifest 字段协议。`name` 和 `version` 是必填项；`name_en`、`description_en` 是可选的英文回退文案。
外壳跟随设备当前界面语言，并通过 `space.lang` 提供给应用。外壳在 `shell/i18n/` 内置了 `en`、`de`、`fr`、`es`、
`zh-CN`、`zh-TW`、`ja`、`ko` 词典。应用可在 `i18n/<lang>.json` 提供界面文案，通过 `space.tr(key)` 读取；缺少的界面词条
回退到应用自己的 `i18n/en.json`。应用词典还可提供 `_app_name`、`_app_description`、`_launch_confirm` 这几个保留键。
应用名称和简介缺少对应翻译时，回退到 manifest 中可选的英文文案，再回退到 `name` 或 `description`。这些翻译放在词典文件中，
不是 manifest 新字段。

```jsonc
{
  "id": "weather",              // 唯一; 1-32 位小写字母、数字、- 或 _
  "name": "天气",
  "version": "1.0.0",          // 必填
  "entry": "main.qml",          // 入口; 没有则走 launch
  "widget": "widget.qml",       // 可选, 首页小组件
  "widget_size": "hero|half",
  "widget_slot": "plan",        // 可选, 指定首页位置
  "arch": ["aarch64", "armv7"], // 空 = 都支持
  "service": { "exec": "...", "port": 9100, "keepalive": true }
}
```

## 许可

[GNU GPL v3.0](LICENSE)。v1.0.10 及以前的版本以 Apache-2.0 发布，仍按 Apache-2.0 授权；第三方代码见 [base/NOTICE.md](base/NOTICE.md)。
