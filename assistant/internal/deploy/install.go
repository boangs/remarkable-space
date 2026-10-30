// Package deploy 把「空间」连同它依赖的底座一次装到设备上。
//
// 装两样东西:
//   - 底座: xovi、qmldiff 注入链与 hashtab、运行时注入 .so、中文汉化、输入法
//     后端。源码在本仓库 base/ (原 rmkit-cn), 只在系统升级逼着改时才动, 它才是真会把设备弄砖的部分 ——
//     hashtab 与固件对不上就是 qmldiff panic → xochitl crash → A/B 回滚。
//   - 空间: 外壳/内置应用/后端 space-server。它走 Qt.createComponent 异步加载,
//     QML 写坏只是面板打不开, 不会崩 xochitl。
//
// 主机侧逻辑是 installer/install.sh 的 Go 移植 (设备路径、文件清单、条件判断一一对应,
// 注释里标了行号), 设备端六阶段防砖脚本原样交给设备执行, 不重写。
package deploy

import (
	"github.com/boangs/remarkable-space/assistant/internal/i18n"

	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/boangs/remarkable-space/assistant/internal/bundle"
	"github.com/boangs/remarkable-space/assistant/internal/probe"
	"github.com/boangs/remarkable-space/assistant/internal/scripts"
	"github.com/boangs/remarkable-space/assistant/internal/sshx"
)

// FileEntry 是 payload 里一个文件: 安装包内路径 → 设备路径。
type FileEntry struct {
	Device string `json:"device"`
	Source string `json:"source"`
	Mode   int64  `json:"mode"`
	Size   int64  `json:"size"`
}

// Plan 是一次 rmkit-cn 安装的完整计划, 先给用户过目再执行。
type Plan struct {
	Arch          string `json:"arch"`
	ModelKey      string `json:"modelKey"`
	FWVersion     string `json:"fwVersion"`
	FWID          string `json:"fwId"`          // 构建时间-版本号, 写 .last_fw_version / 标记 hashtab 用 (见 probe.Info.FWID)
	BundleVersion string `json:"bundleVersion"` // 这次装的安装包版本, 部署后写进设备
	NeedXovi      bool   `json:"needXovi"`
	DeployRime    bool   `json:"deployRime"`
	DeployAudio   bool   `json:"deployAudio"` // install.sh: vendor/audio/rmkit-audio-<arch>.tar.gz 存在即部署 (蓝牙耳机放歌)
	QMLInject     bool   `json:"qmlInject"`
	Librarian     bool   `json:"librarian"`

	Files      []FileEntry `json:"files"`
	TotalBytes int64       `json:"totalBytes"`
	Warnings   []string    `json:"warnings"`

	zzHeader  string
	xoviTar   string
	xoviArch  string
	uploadBin string
	imeBin    string
	spaceBin  string
}

type arch struct {
	uploadBin, imeBin, imeHook, ext, xovi, qmdTool, qmlInject, qmlInjectImpl, rimeBin string
	spaceBin                                                                          string
}

// install.sh 97-127
func archFor(a string) (arch, error) {
	switch a {
	case "aarch64":
		return arch{"upload-server-aarch64", "ime-server", "ime_hook.so", "aarch64", "aarch64", "qmd-tool-aarch64",
			"qml_inject-aarch64.so", "qml_inject_impl-aarch64.so", "ime-server-rime-aarch64",
			"space-server-aarch64"}, nil
	case "armv7l":
		return arch{"upload-server-armv7", "ime-server-armv7", "ime_hook-armv7.so", "armv7", "arm32", "qmd-tool-armv7",
			"qml_inject-armv7.so", "qml_inject_impl-armv7.so", "ime-server-rime-armv7",
			"space-server-armv7"}, nil
	}
	return arch{}, fmt.Errorf(i18n.T("不支持的架构: %s (仅支持 aarch64 / armv7l)", "unsupported architecture: %s (only aarch64 / armv7l)"), a)
}

// NewPlan 根据探测结果与安装包内容生成计划 (纯计算, 不碰设备)。
func NewPlan(info probe.Info, b *bundle.Bundle) (*Plan, error) {
	if b.Manifest.Component != bundle.ComponentSpace {
		return nil, fmt.Errorf(i18n.T("安装包组件是 %s, 不是 space", "bundle component is %s, not space"), b.Manifest.Component)
	}
	if info.InAndroidMode {
		return nil, errors.New(i18n.T("设备当前在 Android 模式, 请先回到 reMarkable 系统", "the device is in Android mode; return to the reMarkable system first"))
	}
	// 别的工具接管了 xochitl 的启动: 空间装上也不会出现, 还会和它抢端口、叠两套注入。
	// 什么都不写直接停, 对方的机器保持原样, 用哪个由用户自己决定。
	if info.ForeignLauncher != "" {
		return nil, fmt.Errorf(i18n.T("检测到其他工具接管了系统界面的启动 (%s, 如 rmtool), 空间无法与它同时安装。设备未做任何改动; 如要使用空间, 请先用该工具自己的卸载功能卸载它",
			"another tool has taken over how the system UI starts (%s, e.g. rmtool); Space cannot be installed alongside it. Nothing on the device was changed; to use Space, uninstall that tool with its own uninstaller first"), info.ForeignLauncher)
	}
	ar, err := archFor(info.Arch)
	if err != nil {
		return nil, err
	}
	p := &Plan{Arch: info.Arch, ModelKey: info.ModelKey, FWVersion: info.FWVersion, FWID: info.FWID(), BundleVersion: b.Manifest.Version, NeedXovi: !info.HaveXovi,
		Files: []FileEntry{}, Warnings: []string{}, // 保证 JSON 是 [] 而不是 null (前端直接 .map)
		xoviArch: ar.xovi, uploadBin: ar.uploadBin, imeBin: ar.imeBin, spaceBin: ar.spaceBin}
	if info.ModelKey == "unknown" {
		p.Warnings = append(p.Warnings, i18n.T("未识别的机型 (分辨率 "+info.Resolution+"), 按架构 "+info.Arch+" 部署; 未适配固件会由 fail-open 预检自动降级为不注入",
			"Unrecognized model (resolution "+info.Resolution+"); deploying by architecture "+info.Arch+". On unsupported firmware the fail-open precheck falls back to no injection"))
	}

	// install.sh 165-184: librime 三条硬门槛 (② 的 /rime/schema 检查由 mkbundle 打包时完成)
	if b.Has("dist/"+ar.rimeBin) && b.Has("dist/rime-prebuilt.tar.gz") && b.Has("dist/rime-runtime-data.tar.gz") {
		p.DeployRime = true
		p.imeBin = ar.rimeBin
	}
	// 曾经这里按 info.HasLSE 把 Cortex-A53 (ferrari) 回落到纯 Go 引擎 —— 那时 librime
	// 版是 chiappa 的 A55 SDK 编的, 内联了 LSE 原子指令, 在 A53 上一跑就 SIGILL。
	// 2026-10-04 已从源头修掉 (静态库压到 -mcpu=cortex-a53+crypto, libstdc++ 改动态链接),
	// 两台实测都正常, 所以不再回落。HasLSE 仍然探测, 排查同类问题时有用。
	// install.sh 375-394: 必备产物
	for _, f := range []string{"space-dist/" + ar.spaceBin, "dist/" + p.imeBin, "dist/" + ar.imeHook, "dist/" + ar.qmdTool,
		"vendor/extensions/xovi-message-broker-" + ar.ext + ".so", "dist/reMarkable_zh_CN.qm", "qmd/zh_CN.rcc"} {
		if !b.Has(f) {
			return nil, fmt.Errorf(i18n.T("安装包缺必备产物 %s", "bundle is missing required file %s"), f)
		}
	}
	p.DeployAudio = b.Has("vendor/audio/rmkit-audio-" + ar.ext + ".tar.gz")
	// librarian 在 3.28 上永远找不到 RM.Library 单例且无限重试, 每次探测漏 ~7KB 原生堆
	// (实测 RMPPM 跑 7 天 xochitl 占到 1.63GB, 打开 PDF 报 low on memory)。最初以为是
	// rm2 专有, 后来证实两个架构都中 —— 是固件问题不是机型问题, 所以无条件不部署。
	// 代价: 扫码上传的文件不热导入, 要重启 xochitl 才可见; 但它本来就没成功过。
	p.Librarian = false
	if p.NeedXovi {
		p.xoviTar = "vendor/xovi/xovi-" + ar.xovi + ".tar.gz"
		if !b.Has(p.xoviTar) || !b.Has("vendor/xovi/xochitl-xovi") {
			return nil, errors.New(i18n.T("设备没有 xovi 且安装包不含 vendor/xovi, 无法自动部署", "the device has no xovi and the bundle has no vendor/xovi; cannot deploy automatically"))
		}
	}
	// install.sh 的 ZZ_UNIT_HEADER: 按架构的 drop-in 头。
	// aarch64 绝不 After=home.mount / ConditionPathExists=/home/...: 原厂 xochitl 有 JobTimeoutSec=60s +
	// OnFailure=emergency.target, /home 晚到 (加密盘解锁 + fsck) 就 job 超时 → emergency → 三次切槽
	// (2026-10-24 rmpp 双槽砖)。改为 ExecStartPre 有上限地等 /home 25 s, 晚到由 rmkit-cn-reenable.service 兜底。
	if info.Arch == "armv7l" {
		p.zzHeader = "[Unit]"
	} else {
		p.zzHeader = "[Unit]\n\n[Service]\n" + `ExecStartPre=-/bin/sh -c 'for n in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20 21 22 23 24 25; do grep -q " /home " /proc/mounts && break; sleep 1; done; true'`
	}

	add := func(device, source string, mode int64) {
		if !b.Has(source) {
			return
		}
		p.Files = append(p.Files, FileEntry{Device: device, Source: source, Mode: mode, Size: b.Size(source)})
	}
	must := func(device, source string, mode int64) error {
		if !b.Has(source) {
			return fmt.Errorf(i18n.T("安装包缺 %s", "bundle is missing %s"), source)
		}
		add(device, source, mode)
		return nil
	}
	const base = "/home/root/rmkit-cn"
	const deploy = "/home/root/xovi/exthome/qt-resource-rebuilder"
	const spaceBase = "/home/root/remarkable-space"

	// install.sh 416-434: bin/
	for _, s := range []string{"scripts/version-switcher.sh", "installer/reenable.sh", "installer/fw-upgrade.sh",
		"installer/precheck.sh", "installer/qml-inject-lib.sh", "installer/ota-watch.sh"} {
		add(base+"/bin/"+path.Base(s), s, 0o755)
	}
	if err := must(base+"/bin/ime-server", "dist/"+p.imeBin, 0o755); err != nil {
		return nil, err
	}
	if p.DeployRime {
		add(base+"/rime-stage/rime-prebuilt.tar.gz", "dist/rime-prebuilt.tar.gz", 0o644)
		add(base+"/rime-stage/rime-runtime-data.tar.gz", "dist/rime-runtime-data.tar.gz", 0o644)
	}
	if p.DeployAudio {
		add(base+"/audio-stage/rmkit-audio.tar.gz", "vendor/audio/rmkit-audio-"+ar.ext+".tar.gz", 0o644)
	}
	add(base+"/bin/ime_hook.so", "dist/"+ar.imeHook, 0o755)
	add(base+"/bin/qmd-tool", "dist/"+ar.qmdTool, 0o755)
	// install.sh 436-453: 运行时 QML 注入 (可选)
	if b.Has("dist/"+ar.qmlInject) && b.Has("dist/"+ar.qmlInjectImpl) {
		p.QMLInject = true
		add(base+"/bin/qml_inject.so", "dist/"+ar.qmlInject, 0o755)
		add(base+"/bin/qml_inject_impl.so", "dist/"+ar.qmlInjectImpl, 0o755)
		for _, r := range []string{"adv_panel.qml", "glyph_ai_button.qml", "text_ai_button.qml", "pinyin_ime.qml", "icon_ai.svg"} {
			add(base+"/bin/"+r, "intercept/qml-inject/"+r, 0o755)
		}
	}
	// 「空间 (SPACE)」外壳 + 共享组件 + 内置应用 (整树, bin/ 下可执行)。
	// 仍落在 /home/root/rmkit-cn/space —— 这个路径硬编码在注入库里 (qml_inject_impl.cpp),
	// 改了要重编两个架构的 .so, 不值当。
	for _, n := range b.Names() {
		if strings.HasPrefix(n, "space/shell/") || strings.HasPrefix(n, "space/kit/") || strings.HasPrefix(n, "space/apps/") || strings.HasPrefix(n, "space/hooks/") || strings.HasPrefix(n, "space/themes/") {
			mode := int64(0o644)
			if strings.Contains(n, "/bin/") {
				mode = 0o755
			}
			add(base+"/"+n, n, mode)
		}
	}
	// install.sh 456-466: qmd-src (+compat)
	for _, n := range b.Names() {
		if strings.HasPrefix(n, "qmd-src/") && strings.HasSuffix(n, ".qmd") {
			add(base+"/"+n, n, 0o644)
		}
	}
	// install.sh 468-479: compiled-qmd/<fw> 缓存种子 (设备端阶段 3 会用设备 hashtab 重编覆盖)
	for _, n := range b.Names() {
		if strings.HasPrefix(n, "dist/") && strings.HasSuffix(n, ".qmd") {
			add(base+"/compiled-qmd/"+info.FWID()+"/"+path.Base(n), n, 0o644)
		}
	}
	add(base+"/compiled-qmd/"+info.FWID()+"/pinyin_interceptor.qmd", "qmd/pinyin_interceptor.qmd", 0o644)
	// install.sh 481-487: static/
	add(base+"/static/pinyin_interceptor.qmd", "qmd/pinyin_interceptor.qmd", 0o644)
	add(base+"/static/zh_CN.rcc", "qmd/zh_CN.rcc", 0o644)
	add(base+"/static/reMarkable_zh_CN.qm", "dist/reMarkable_zh_CN.qm", 0o644)
	// 空间后端 (space-server): 顶替已退役的 upload-server, 同样监听 8080, 路由逐条兼容。
	// 它自己带上传/蓝牙/字体锁屏图/AI/截图/二维码, 外加空间的注册表与绿色安装。
	add(spaceBase+"/space-server", "space-dist/"+ar.spaceBin, 0o755)
	add(spaceBase+"/static/qr.html", "space/static/qr.html", 0o644)

	// install.sh 496-502: qmd/
	add(base+"/qmd/pinyin_interceptor.qmd", "qmd/pinyin_interceptor.qmd", 0o644)
	add(base+"/qmd/zh_CN.rcc", "qmd/zh_CN.rcc", 0o644)
	add(base+"/qmd/zh_CN/keyboard_layout.json", "qmd/zh_CN/keyboard_layout.json", 0o644)
	// install.sh 504-511: qmldiff 加载目录 (阶段 2 会先清空再由阶段 3 重编, 这里是初始种子)
	for _, q := range []string{"advanced_panel.qmd", "language_zh_cn.qmd", "ai_text_button.qmd"} {
		add(deploy+"/"+q, "dist/"+q, 0o644)
	}
	add(deploy+"/pinyin_interceptor.qmd", "qmd/pinyin_interceptor.qmd", 0o644)
	add(deploy+"/zh_CN.rcc", "qmd/zh_CN.rcc", 0o644)
	// install.sh 513-517: 棋类资源
	for _, n := range b.Names() {
		if strings.HasPrefix(n, "assets/chess/") {
			add(deploy+"/chess/"+path.Base(n), n, 0o644)
		}
	}
	// install.sh 519-525: xovi 扩展
	if p.Librarian {
		add("/home/root/xovi/extensions.d/librarian.so", "vendor/extensions/librarian-"+ar.ext+".so", 0o755)
	}
	add("/home/root/xovi/extensions.d/xovi-message-broker.so", "vendor/extensions/xovi-message-broker-"+ar.ext+".so", 0o755)
	// install.sh 527-529: 中文 qm
	add("/usr/share/remarkable/xochitl/translations/reMarkable_zh_CN.qm", "dist/reMarkable_zh_CN.qm", 0o644)
	// install.sh 531-538: systemd 暂存 (设备端阶段 1 双写到 /etc)
	for _, n := range b.Names() {
		if strings.HasPrefix(n, "systemd/") {
			add("/tmp/rmkit-cn-systemd-staging/"+path.Base(n), n, 0o644)
		}
	}

	sort.Slice(p.Files, func(i, j int) bool { return p.Files[i].Device < p.Files[j].Device })
	for _, f := range p.Files {
		p.TotalBytes += f.Size
	}
	return p, nil
}

// Run 执行计划。log 收到的每一行都同时进本地审计日志。
func Run(ctx context.Context, c *sshx.Client, b *bundle.Bundle, p *Plan, log sshx.Logger) error {
	if p.NeedXovi { // install.sh 186-213
		log(i18n.T("设备无 xovi 或 xovi 不完整, 自动部署...", "xovi missing or incomplete on the device, deploying it..."))
		for _, s := range []string{p.xoviTar, "vendor/xovi/xochitl-xovi"} {
			rc, err := b.Open(s)
			if err != nil {
				return err
			}
			err = c.WriteFile(ctx, "/tmp/"+path.Base(s), rc, "644")
			_ = rc.Close()
			if err != nil {
				return err
			}
		}
		if err := c.RunLogged(ctx, fmt.Sprintf(`set -e
# 设备上可能已有别的工具装的不完整 xovi: 先解到暂存目录, 再逐个 mv 覆盖 (rename 不改正在被映射的 .so),
# 它自己的其他扩展不动
S=/home/root/.xovi-staging
rm -rf $S && mkdir -p $S && cd $S
tar -xzf /tmp/%[1]s --no-same-owner --no-same-permissions
cd $S/xovi
find . -type d | while read d; do mkdir -p "/home/root/xovi/$d"; done
find . ! -type d | while read f; do mv -f "$f" "/home/root/xovi/$f"; done
cd /home/root && rm -rf $S
cp /tmp/xochitl-xovi /home/root/xovi/xochitl-xovi.new && mv -f /home/root/xovi/xochitl-xovi.new /home/root/xovi/xochitl-xovi
chmod +x /home/root/xovi/xochitl-xovi
chown -R root:root /home/root/xovi
rm /tmp/%[1]s /tmp/xochitl-xovi
echo '  ✓ xovi '`+i18n.T("部署完成", "deployed"), path.Base(p.xoviTar)), nil); err != nil {
			return fmt.Errorf(i18n.T("部署 xovi 失败: %w", "failed to deploy xovi: %w"), err)
		}
	}

	// 这几个目录整个由安装包管: tar 只覆盖不删, 先清掉 —— 旧版留下的挂载文件会继续挂在笔记里;
	// shell/kit 可能是设备上自更新留下的链接 (指向 rel/<版本>/), 连同 rel/ 与 .prev 一起清掉, 装回真目录。
	if err := c.RunLogged(ctx, "cd /home/root/rmkit-cn/space 2>/dev/null && rm -rf hooks themes shell kit rel shell.prev kit.prev; true", nil); err != nil {
		return err
	}

	// install.sh 543-566: 单次 tar 流式传输
	log(fmt.Sprintf(i18n.T("传输文件 (%d 个文件, %.1f MB, 单次 SSH 流式)...", "Transferring files (%d files, %.1f MB, single SSH stream)..."), len(p.Files), float64(p.TotalBytes)/1e6))
	start := time.Now()
	pr, pw := io.Pipe()
	go func() { pw.CloseWithError(writeTar(pw, b, p.Files)) }()
	if err := c.StreamTar(ctx, pr, scripts.ReceivePayload); err != nil {
		_ = pr.Close()
		return fmt.Errorf(i18n.T("传输失败: %w", "transfer failed: %w"), err)
	}
	log(fmt.Sprintf(i18n.T("传输完成 (%.0fs)", "Transfer done (%.0fs)"), time.Since(start).Seconds()))

	// install.sh 568-573: 必须在 reenable 之前写 .last_fw_version (防 fw-upgrade.sh 误触发)
	if err := c.WriteFile(ctx, "/home/root/rmkit-cn/.last_fw_version", strings.NewReader(p.FWID), "644"); err != nil {
		return err
	}
	log(i18n.T("✓ .last_fw_version 已写入 (", "✓ .last_fw_version written (") + p.FWID + ")")

	// 记一笔装的是哪个安装包。助手下次连上就能告诉用户当前版本, 也才好判断要不要更新。
	if p.BundleVersion != "" {
		if err := c.WriteFile(ctx, "/home/root/rmkit-cn/.space_version", strings.NewReader(p.BundleVersion), "644"); err != nil {
			return err
		}
	}

	// 架构自检: 趁还没碰服务和 drop-in, 先确认后端二进制在这台设备上真能执行。
	// 架构推错 (给 armv7 的机器推 aarch64) 只会在运行时报 Exec format error, 等到
	// 六阶段跑完才发现就晚了 —— 那时旧服务已经 mask, 上传/字体/蓝牙/AI 会一起失效。
	// 这里失败就直接退出, 设备还是原样。
	res, err := c.Run(ctx, "/home/root/remarkable-space/space-server -h >/dev/null 2>&1; echo $?")
	if err != nil {
		return fmt.Errorf(i18n.T("后端自检执行失败: %w", "backend self-check could not run: %w"), err)
	}
	switch strings.TrimSpace(res.Stdout) {
	case "126", "127":
		return fmt.Errorf(i18n.T("后端二进制在设备上跑不起来 (架构不符?), 已停止安装, 设备未改动; 安装包里给的是 %s 版",
			"the backend binary does not run on this device (wrong architecture?); install stopped, device untouched; the bundle provides the %s build"), p.spaceBin)
	}
	log(i18n.T("✓ 后端二进制自检通过", "✓ Backend binary self-check passed"))

	// 侧栏入口切成「空间」的开关 —— 注入库同时检查这个标记和 Space.qml 是否存在
	// (qml_inject_impl.cpp: useSpace)。少了它侧栏显示的还是"高级", 根本进不去空间。
	if err := c.WriteFile(ctx, "/home/root/rmkit-cn/space/.primary", strings.NewReader(""), "644"); err != nil {
		return err
	}
	log(i18n.T("✓ 侧栏入口已切到「空间」", "✓ Sidebar entry switched to Space"))

	// 空间自己的 xochitl drop-in。单独一个文件, 不动底座那份 zz-rmkit-cn.conf ——
	// systemd 会把 xochitl.service.d/ 下所有 .conf 合并, 所以空间加能力不必让用户
	// 先去更新那个已经冻结的公开底座。/etc 是 tmpfs overlay, 老规矩双写 ext4 lower,
	// 只写上层的话重启就没了。
	if b.Has(spaceDropinSrc) {
		conf, err := b.ReadAll(spaceDropinSrc)
		if err != nil {
			return err
		}
		if err := c.WriteFile(ctx, "/tmp/zz-space.conf", bytes.NewReader(conf), "644"); err != nil {
			return err
		}
		if err := c.RunScript(ctx, spaceDropinScript, nil); err != nil {
			return fmt.Errorf(i18n.T("装空间的 xochitl drop-in 失败: %w", "failed to install the Space xochitl drop-in: %w"), err)
		}
		log(i18n.T("✓ 空间 drop-in 已装 (编译应用的模块搜索路径)", "✓ Space drop-in installed (module search path for compiled apps)"))
	}

	// 后端的 drop-in: 应用商店的握手接口。静态索引入口已经关掉了, 不配这个的设备
	// 取不到商店 —— 所以它和上面那份一样是装机必备, 不是可选项。
	if b.Has(storeAPIDropinSrc) {
		conf, err := b.ReadAll(storeAPIDropinSrc)
		if err != nil {
			return err
		}
		if err := c.WriteFile(ctx, "/tmp/10-store-api.conf", bytes.NewReader(conf), "644"); err != nil {
			return err
		}
		if err := c.RunScript(ctx, storeAPIDropinScript, nil); err != nil {
			return fmt.Errorf(i18n.T("装应用商店 drop-in 失败: %w", "failed to install the app catalog drop-in: %w"), err)
		}
		log(i18n.T("✓ 应用商店握手已配置", "✓ App catalog handshake configured"))
	}

	// rm-appload: KOReader 这类要 framebuffer 的程序靠它拿画布。装的是官方发布包,
	// 版本和 sha256 锁在 third_party/fetch-appload.sh 里 —— 这东西对固件版本敏感,
	// 旧版配新固件会让 xochitl 崩到切槽, 所以绝不能随便换。
	if b.Has(apploadSrc) {
		for _, f := range []struct{ src, dst string }{
			{apploadSrc, "/home/root/xovi/extensions.d/appload.so"},
			{"third_party/appload/qtfb-shim.so", "/home/root/shims/qtfb-shim.so"},
			{"third_party/appload/qtfb-shim-32bit.so", "/home/root/shims/qtfb-shim-32bit.so"},
		} {
			data, err := b.ReadAll(f.src)
			if err != nil {
				return err
			}
			if err := c.WriteFile(ctx, "/tmp/"+path.Base(f.dst), bytes.NewReader(data), "755"); err != nil {
				return err
			}
		}
		if err := c.RunScript(ctx, apploadScript, nil); err != nil {
			return fmt.Errorf(i18n.T("装 AppLoad 失败: %w", "failed to install AppLoad: %w"), err)
		}
		log(i18n.T("✓ AppLoad 已装 (KOReader 等外部应用要用)", "✓ AppLoad installed (needed by KOReader and other external apps)"))
	}

	if p.DeployRime { // install.sh 575-612
		log(i18n.T("部署 rime 词库 (整句输入)...", "Deploying the rime dictionary (sentence input)..."))
		if err := c.RunScript(ctx, scripts.RimeSetup, map[string]string{"UI_LANG": i18n.Lang()}); err != nil {
			return fmt.Errorf(i18n.T("rime 词库部署失败: %w", "failed to deploy the rime dictionary: %w"), err)
		}
	}

	if p.DeployAudio { // install.sh AUDIO_EOF 段
		log(i18n.T("部署蓝牙音频组件 (bluealsa + mpg123)...", "Deploying Bluetooth audio components (bluealsa + mpg123)..."))
		if err := c.RunScript(ctx, scripts.AudioSetup, map[string]string{"UI_LANG": i18n.Lang()}); err != nil {
			return fmt.Errorf(i18n.T("蓝牙音频组件部署失败: %w", "failed to deploy Bluetooth audio components: %w"), err)
		}
	}

	// install.sh 614-855: 设备端六阶段防砖部署
	log(i18n.T("配置系统服务 + 编译 + 启动 (设备端六阶段)...", "Configuring services + compiling + starting (six stages on the device)..."))
	env := map[string]string{
		"FW_VERSION":          p.FWID,
		"ZZ_HEADER_FLAT":      strings.ReplaceAll(p.zzHeader, "\n", `\n`),
		"QML_INJECT_DEPLOYED": boolStr(p.QMLInject),
		"UI_LANG":             i18n.Lang(),
	}
	if err := c.RunScript(ctx, scripts.DeployStages, env); err != nil {
		return fmt.Errorf(i18n.T("设备端部署失败 (已按 abort_safe 回退到出厂启动): %w", "deployment on the device failed (rolled back to stock boot via abort_safe): %w"), err)
	}
	return nil
}

// Uninstall 执行 install.sh --uninstall 的设备端脚本。
func Uninstall(ctx context.Context, c *sshx.Client) error {
	return c.RunScript(ctx, scripts.Uninstall, map[string]string{"UI_LANG": i18n.Lang()})
}

// spaceDropinSrc 是安装包里空间那份 xochitl drop-in 的路径。
const spaceDropinSrc = "space-systemd/zz-space.conf"

// spaceDropinScript 把它双写到 /etc 与 ext4 lower。与底座的 drop-in 并存, 互不覆盖。
const spaceDropinScript = `set -eu
mkdir -p /tmp/lc && mount --bind / /tmp/lc 2>/dev/null || true
mount -o remount,rw /tmp/lc 2>/dev/null || true
for d in /etc/systemd/system /tmp/lc/etc/systemd/system; do
  mkdir -p "$d/xochitl.service.d"
  cp /tmp/zz-space.conf "$d/xochitl.service.d/zz-space.conf"
  chmod 644 "$d/xochitl.service.d/zz-space.conf"
done
sync; umount -l /tmp/lc 2>/dev/null || true; rmdir /tmp/lc 2>/dev/null || true
rm -f /tmp/zz-space.conf
systemctl daemon-reload
`

// storeAPIDropinSrc 是安装包里后端那份 drop-in 的路径 (应用商店握手接口)。
const storeAPIDropinSrc = "space-systemd/10-store-api.conf"

// storeAPIDropinScript 同样双写 /etc 与 ext4 lower —— /etc 是 tmpfs overlay,
// 只写上层的话重启就退回"取不到商店"了。
const storeAPIDropinScript = `set -eu
mkdir -p /tmp/lc && mount --bind / /tmp/lc 2>/dev/null || true
mount -o remount,rw /tmp/lc 2>/dev/null || true
for d in /etc/systemd/system /tmp/lc/etc/systemd/system; do
  mkdir -p "$d/remarkable-space.service.d"
  cp /tmp/10-store-api.conf "$d/remarkable-space.service.d/10-store-api.conf"
  chmod 644 "$d/remarkable-space.service.d/10-store-api.conf"
done
sync; umount -l /tmp/lc 2>/dev/null || true; rmdir /tmp/lc 2>/dev/null || true
rm -f /tmp/10-store-api.conf
systemctl daemon-reload
`

// apploadSrc 是安装包里 appload 扩展的路径。
const apploadSrc = "third_party/appload/appload.so"

// apploadScript 把 appload 放进 xovi 的扩展目录, shim 放进 /home/root/shims。
// 已启用的扩展都不带 .conf (xovi 按目录认), 所以只拷 .so 就是启用。
const apploadScript = `set -eu
mkdir -p /home/root/xovi/extensions.d /home/root/shims
# 同名的 .bak/.old 会让 xovi 报 "processed more than once" 然后 fatal, 备份绝不能留在这个目录
rm -f /home/root/xovi/extensions.d/appload.so.bak /home/root/xovi/extensions.d/appload.so.old
mv /tmp/appload.so /home/root/xovi/extensions.d/appload.so
mv /tmp/qtfb-shim.so /home/root/shims/qtfb-shim.so
mv /tmp/qtfb-shim-32bit.so /home/root/shims/qtfb-shim-32bit.so
chmod 755 /home/root/xovi/extensions.d/appload.so /home/root/shims/qtfb-shim.so /home/root/shims/qtfb-shim-32bit.so
# 老版本可能躺在 inactive-extensions 里, 一并换掉, 免得下次有人手工启用旧的
[ -f /home/root/xovi/inactive-extensions/appload.so ] && cp /home/root/xovi/extensions.d/appload.so /home/root/xovi/inactive-extensions/appload.so || true
`

// writeTar 按 install.sh 547 的方式打包: uid/gid 0, 路径以 ./ 开头, gzip。
func writeTar(w io.Writer, b *bundle.Bundle, files []FileEntry) error {
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	dirs := map[string]bool{}
	now := time.Now()
	for _, f := range files {
		dir := path.Dir(f.Device)
		var chain []string
		for d := dir; d != "/" && !dirs[d]; d = path.Dir(d) {
			chain = append([]string{d}, chain...)
		}
		for _, d := range chain {
			dirs[d] = true
			if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeDir, Name: "." + d + "/", Mode: 0o755, ModTime: now}); err != nil {
				return err
			}
		}
		if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: "." + f.Device, Mode: f.Mode, Size: f.Size, ModTime: now}); err != nil {
			return err
		}
		rc, err := b.Open(f.Source)
		if err != nil {
			return err
		}
		n, err := io.Copy(tw, rc)
		_ = rc.Close()
		if err != nil {
			return fmt.Errorf(i18n.T("打包 %s: %w", "packing %s: %w"), f.Source, err)
		}
		if n != f.Size {
			return fmt.Errorf(i18n.T("打包 %s: 大小 %d 与清单 %d 不符", "packing %s: size %d does not match manifest %d"), f.Source, n, f.Size)
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

func boolStr(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// FWNumber 把 /etc/version 转成可比较的整数 (非数字返回 0)。
func FWNumber(fw string) int64 {
	n, _ := strconv.ParseInt(strings.TrimSpace(fw), 10, 64)
	return n
}

// RepairPanel 清除 rmkit-cn 的 crashloop 熔断与启动计数并重启一次 xochitl (侧栏入口或中文凭空消失时用)。
// 熔断误判的典型场景: 进 Android 再回原厂 = 600 秒内两次启动, 再多一次就被当成崩溃循环。
func RepairPanel(ctx context.Context, c *sshx.Client) error {
	script := i18n.T(`set -e
R=/home/root/rmkit-cn
[ "$(cat /proc/1/comm)" = systemd ] || { echo '请先回到 reMarkable 系统'; exit 1; }
echo "熔断标记: $(cat $R/.fuse_tripped 2>/dev/null || echo 无)  600s 内启动次数: $(awk -v n=$(date +%s) 'n-$1<600' $R/.starts 2>/dev/null | wc -l)"
rm -f $R/.fuse_tripped; : > $R/.starts
date +%s > $R/.intentional_restart
systemctl restart xochitl.service
sleep 10
if pidof xochitl >/dev/null; then echo "  ✓ xochitl 已重启 (PID $(pidof xochitl)), 注入: xovi=$(grep -c xovi /proc/$(pidof xochitl)/maps) ime_hook=$(grep -c ime_hook /proc/$(pidof xochitl)/maps)"; else echo '  ✗ xochitl 没起来, 看 journalctl -u xochitl'; exit 1; fi`,
		`set -e
R=/home/root/rmkit-cn
[ "$(cat /proc/1/comm)" = systemd ] || { echo 'Return to the reMarkable system first'; exit 1; }
echo "fuse marker: $(cat $R/.fuse_tripped 2>/dev/null || echo none)  starts within 600s: $(awk -v n=$(date +%s) 'n-$1<600' $R/.starts 2>/dev/null | wc -l)"
rm -f $R/.fuse_tripped; : > $R/.starts
date +%s > $R/.intentional_restart
systemctl restart xochitl.service
sleep 10
if pidof xochitl >/dev/null; then echo "  ✓ xochitl restarted (PID $(pidof xochitl)), injected: xovi=$(grep -c xovi /proc/$(pidof xochitl)/maps) ime_hook=$(grep -c ime_hook /proc/$(pidof xochitl)/maps)"; else echo '  ✗ xochitl did not come up, see journalctl -u xochitl'; exit 1; fi`)
	return c.RunLogged(ctx, script, nil)
}
