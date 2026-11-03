package main

import (
	"github.com/boangs/remarkable-space/assistant/internal/i18n"

	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	goruntime "runtime"
	"strings"
	"sync"
	"time"

	"github.com/boangs/remarkable-space/assistant/internal/android"
	"github.com/boangs/remarkable-space/assistant/internal/bundle"
	"github.com/boangs/remarkable-space/assistant/internal/deploy"
	"github.com/boangs/remarkable-space/assistant/internal/netx"
	"github.com/boangs/remarkable-space/assistant/internal/probe"
	"github.com/boangs/remarkable-space/assistant/internal/scripts"
	"github.com/boangs/remarkable-space/assistant/internal/sshx"
	"github.com/wailsapp/wails/v2/pkg/runtime"
	"github.com/zalando/go-keyring"
	"golang.org/x/crypto/ssh"
)

// App 是 Wails 绑定层: 前端只能通过这里的方法与设备交互, 每个动作都会写审计日志。
type App struct {
	ctx    context.Context
	mu     sync.Mutex
	client *sshx.Client
	host   string // 本次会话的地址与密码, 连接断开 (设备重启) 时用来自动重连
	pass   string
	info   *probe.Info
	bundle *bundle.Bundle
	logF   *os.File
	logDir string
	appKey *sshx.AppKey
	cancel context.CancelFunc
}

// NewApp 创建应用。
func NewApp() *App { return &App{} }

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = os.TempDir()
	}
	a.logDir = filepath.Join(dir, "space-assistant", "logs")
	_ = os.MkdirAll(a.logDir, 0o755)
	a.logF, _ = os.Create(filepath.Join(a.logDir, time.Now().Format("20060102-150405")+".log"))
	a.log(i18n.T("空间助手启动; 审计日志目录 ", "Space Assistant started; audit log directory ") + a.logDir)
	if k, err := sshx.LoadOrCreateAppKey(filepath.Join(dir, "space-assistant")); err != nil {
		a.log(i18n.T("助手密钥不可用: ", "Assistant key unavailable: ") + err.Error())
	} else {
		a.appKey = k
		sshx.ExtraSigners = []ssh.Signer{k.Signer}
	}
}

func (a *App) shutdown(context.Context) {
	if a.client != nil {
		_ = a.client.Close()
	}
	if a.logF != nil {
		_ = a.logF.Close()
	}
}

// log 同时送前端 (事件 "log") 与本地审计文件。
func (a *App) log(line string) {
	ts := time.Now().Format("15:04:05")
	if a.logF != nil {
		_, _ = a.logF.WriteString(ts + " " + line + "\n")
	}
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, "log", ts+"  "+line)
	}
}

// SetLanguage 由前端在启动和切换语言时调用; 之后 Go 端日志、错误与设备端脚本输出都按它来。
func (a *App) SetLanguage(lang string) { i18n.Set(lang) }

// Version 返回版本与构建时间 (界面右上角显示, 便于确认拿到的是新版)。
func (a *App) Version() string {
	if buildTime == "" {
		return version
	}
	return version + " (" + buildTime + ")"
}

// ReleaseInfo 这一版助手配套的空间安装包从哪下、怎么验。
type ReleaseInfo struct {
	Tag    string `json:"tag"`    // 发布标签; 空 = 开发版 (下 latest)
	CNBase string `json:"cnBase"` // 国内镜像的根地址 (以 / 结尾); 空 = 没有国内镜像
	// Android 包所在的发布标签 (GitHub Release / 国内镜像目录)
	AndroidTag string `json:"androidTag"`
	AndroidVer string `json:"androidVer"`
}

// Release 给前端拼下载地址用。
func (a *App) Release() ReleaseInfo {
	return ReleaseInfo{Tag: releaseTag, CNBase: cnMirror, AndroidTag: androidTag, AndroidVer: androidVer}
}

// LatestVersion 读安装包旁边的 space-version.txt —— 空间能在设备上自更新, 发了新版就把安装包
// 原地换成新版的, 这个文件跟着换; 助手据此显示「可更新到」, 不必为每个空间版本发新助手。
// 多个地址按顺序试, 都读不到返回空 (前端退回用发布标签比)。
func (a *App) LatestVersion(urls []string, proxy string) string {
	client, err := netx.Client(proxy)
	if err != nil {
		return ""
	}
	client.Timeout = 8 * time.Second
	for _, u := range urls {
		resp, err := client.Get(u)
		if err != nil {
			continue
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 64))
		resp.Body.Close()
		v := strings.TrimSpace(string(b))
		if resp.StatusCode == 200 && validVersion.MatchString(v) {
			return v
		}
	}
	return ""
}

var validVersion = regexp.MustCompile(`^v\d+(\.\d+)*(-[0-9A-Za-z.]+)?$`)

// LogDir 返回审计日志目录。
func (a *App) LogDir() string { return a.logDir }

// OpenLogDir 用系统文件管理器打开日志目录。
// 不能用 runtime.BrowserOpenURL: Wails 2.15 起它拒绝 file:// 且不报错, 按钮点了没反应。
func (a *App) OpenLogDir() error {
	var cmd *exec.Cmd
	switch goruntime.GOOS {
	case "darwin":
		cmd = exec.Command("open", a.logDir)
	case "windows":
		cmd = exec.Command("explorer", a.logDir)
	default:
		cmd = exec.Command("xdg-open", a.logDir)
	}
	if err := cmd.Start(); err != nil {
		a.log(i18n.T("打开日志目录失败: ", "Failed to open the log directory: ") + err.Error())
		return fmt.Errorf(i18n.T("打不开日志目录, 请手动前往 %s", "cannot open the log directory; go to %s manually"), a.logDir)
	}
	// explorer 成功时退出码也是 1, 不等它、不看退出码, 回收掉进程即可
	go func() { _ = cmd.Wait() }()
	return nil
}

const keyringService = "space-assistant"

// 旧版 rmkit 助手用过的钥匙串条目名; 读取时兜底, 这样从旧助手换过来不用重输密码。
const legacyKeyringService = "rmkit-assistant"

// LoadPassword 从系统钥匙串 (macOS Keychain / Windows 凭据管理器) 取该地址保存过的密码。
func (a *App) LoadPassword(host string) string {
	host = strings.TrimSpace(host)
	if pw, err := keyring.Get(keyringService, host); err == nil {
		return pw
	}
	if pw, err := keyring.Get(legacyKeyringService, host); err == nil {
		return pw
	}
	return ""
}

// ForgetPassword 删除钥匙串里保存的密码。
func (a *App) ForgetPassword(host string) {
	_ = keyring.Delete(keyringService, strings.TrimSpace(host))
	a.log(i18n.T("已从系统钥匙串删除 "+host+" 的密码", "Removed the password for "+host+" from the system keychain"))
}

// Connect 连接设备并探测。remember=true 时把密码存进系统钥匙串 (不写明文文件)。
func (a *App) Connect(host, password string, remember bool) (*probe.Info, error) {
	if remember && password != "" {
		if err := keyring.Set(keyringService, strings.TrimSpace(host), password); err != nil {
			a.log(i18n.T("保存密码到系统钥匙串失败: ", "Failed to save the password to the system keychain: ") + err.Error())
		} else {
			a.log(i18n.T("密码已保存到系统钥匙串 (", "Password saved to the system keychain (") + strings.TrimSpace(host) + ")")
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.client != nil {
		_ = a.client.Close()
		a.client = nil
	}
	host = strings.TrimSpace(host)
	if host == "" {
		host = "10.11.99.1"
	}
	ctx, cancel := context.WithTimeout(a.ctx, 20*time.Second)
	defer cancel()
	c, err := sshx.Dial(ctx, host, password, a.log)
	if err != nil {
		a.log(i18n.T("连接失败: ", "Connection failed: ") + err.Error())
		return nil, err
	}
	a.client = c
	a.host, a.pass = host, password
	info, err := a.probeLocked()
	if err == nil && a.appKey != nil && !info.InAndroidMode {
		// Android 模式下密码登录不可用 (rootfs 本体的 /etc/shadow 锁定), 趁在 reMarkable 模式把助手公钥装进去
		kctx, kcancel := context.WithTimeout(a.ctx, 15*time.Second)
		if kerr := c.InstallAuthorizedKey(kctx, a.appKey.Public); kerr != nil {
			a.log(i18n.T("安装助手公钥失败 (Android 模式下可能连不上): ", "Failed to install the assistant public key (Android mode may not connect): ") + kerr.Error())
		}
		kcancel()
	}
	return info, err
}

// ensureAlive 检查连接是否还活着 (设备重启/切换 Android 模式后旧连接会 EOF), 断了就用本次
// 会话记住的地址和密码自动重连一次。调用方须持有 a.mu。
func (a *App) ensureAlive() error {
	if a.client == nil {
		return errors.New(i18n.T("未连接设备", "no device connected"))
	}
	ctx, cancel := context.WithTimeout(a.ctx, 8*time.Second)
	defer cancel()
	if _, err := a.client.Run(ctx, "true"); err == nil {
		return nil
	}
	if a.host == "" {
		return errors.New(i18n.T("连接已断开, 请重新连接", "connection lost; connect again"))
	}
	a.log(i18n.T("连接已断开 (设备可能重启过), 自动重连 ", "Connection lost (the device may have rebooted); reconnecting to ") + a.host + " ...")
	_ = a.client.Close()
	a.client = nil
	var last error
	for i := 0; i < 6; i++ { // 设备重启中时多等一会, 最多约 1 分钟
		dctx, dcancel := context.WithTimeout(a.ctx, 15*time.Second)
		c, err := sshx.Dial(dctx, a.host, a.pass, a.log)
		dcancel()
		if err == nil {
			a.client = c
			return nil
		}
		last = err
		select {
		case <-a.ctx.Done():
			return a.ctx.Err()
		case <-time.After(8 * time.Second):
		}
	}
	return fmt.Errorf(i18n.T("自动重连失败: %w", "automatic reconnect failed: %w"), last)
}

// Probe 重新探测已连接设备。
func (a *App) Probe() (*probe.Info, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.probeLocked()
}

func (a *App) probeLocked() (*probe.Info, error) {
	if err := a.ensureAlive(); err != nil {
		a.log(i18n.T("探测失败: ", "Probe failed: ") + err.Error())
		return nil, err
	}
	ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
	defer cancel()
	info, err := probe.Run(ctx, a.client)
	if err != nil {
		a.log(i18n.T("探测失败: ", "Probe failed: ") + err.Error())
		return nil, err
	}
	// 语言包独立于底座发版 (商店应用 langpack)。底座自带的只是首次安装的基线,
	// 有新版本时在界面上提示一句, 让用户去商店更新。
	if info.RmkitInstalled {
		info.LangpackLatest = a.langpackLatest()
	}
	a.info = &info
	mode := "reMarkable"
	if info.InAndroidMode {
		mode = "Android"
	}
	a.log(fmt.Sprintf(i18n.T("设备: %s / %s / 固件 %s / 槽 %s / 模式 %s / rootfs 剩 %dMB / home 剩 %dMB", "Device: %s / %s / firmware %s / slot %s / mode %s / rootfs free %dMB / home free %dMB"),
		info.Model, info.Arch, info.FWVersion, info.ActiveSlot, mode, info.RootFreeMB, info.HomeFreeMB))
	return &info, nil
}

// langpackLatest 取商店里系统语言包的最新版本。
// 商店索引接口要设备票, 助手拿不到 —— 让设备本地的 space-server 去拉 (它本来就在维护这份索引)。
// 失败就返回空, 界面只是不显示提示, 不挡安装。
func (a *App) langpackLatest() string {
	if a.client == nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(a.ctx, 15*time.Second)
	defer cancel()
	res, err := a.client.Run(ctx, "wget -qO- --timeout=8 'http://127.0.0.1:8080/space/store' 2>/dev/null")
	if err != nil {
		return ""
	}
	var idx struct {
		Apps []struct {
			ID      string `json:"id"`
			Version string `json:"version"`
		} `json:"apps"`
	}
	if json.Unmarshal([]byte(res.Stdout), &idx) != nil {
		return ""
	}
	for _, app := range idx.Apps {
		if app.ID == "langpack" {
			return app.Version
		}
	}
	return ""
}

// Disconnect 断开。
func (a *App) Disconnect() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.client != nil {
		_ = a.client.Close()
		a.client = nil
		a.info = nil
		a.host, a.pass = "", ""
		a.log(i18n.T("已断开", "Disconnected"))
	}
}

// BundleInfo 是给前端看的安装包摘要。
type BundleInfo struct {
	Path      string `json:"path"`
	Component string `json:"component"`
	Version   string `json:"version"`
	Created   string `json:"created"`
	Notes     string `json:"notes"`
	Files     int    `json:"files"`
	Bytes     int64  `json:"bytes"`
}

// ChooseBundle 弹文件选择框选本地安装包。
func (a *App) ChooseBundle() (*BundleInfo, error) {
	path, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title:   i18n.T("选择安装包", "Choose a bundle") + " (space-bundle-*.zip / android-rmppm-bundle-*.zip)",
		Filters: []runtime.FileFilter{{DisplayName: i18n.T("安装包 (*.zip)", "Bundle (*.zip)"), Pattern: "*.zip"}},
	})
	if err != nil || path == "" {
		return nil, err
	}
	return a.OpenBundle(path)
}

// OpenBundle 打开并校验安装包 (逐文件 sha256)。
func (a *App) OpenBundle(path string) (*BundleInfo, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.bundle != nil {
		_ = a.bundle.Close()
		a.bundle = nil
	}
	a.log(i18n.T("校验安装包 ", "Verifying bundle ") + path)
	b, err := bundle.Open(path, true)
	if err != nil {
		a.log(i18n.T("安装包无效: ", "Invalid bundle: ") + err.Error())
		return nil, err
	}
	a.bundle = b
	var total int64
	for _, e := range b.Manifest.Files {
		total += e.Size
	}
	a.log(fmt.Sprintf(i18n.T("安装包 OK: %s 版本 %s, %d 个文件", "Bundle OK: %s version %s, %d files"), b.Manifest.Component, b.Manifest.Version, len(b.Manifest.Files)))
	return &BundleInfo{Path: path, Component: string(b.Manifest.Component), Version: b.Manifest.Version,
		Created: b.Manifest.Created, Notes: b.Manifest.Notes, Files: len(b.Manifest.Files), Bytes: total}, nil
}

// DetectProxy 探测可用代理 (环境变量 / macOS 系统代理 / Windows 注册表)。
// 只在下载失败后提示用 —— 默认不预填: 各人代理不一样, 填错了反而连不上。
func (a *App) DetectProxy() string { return netx.DetectProxy() }

// DownloadBundle 从 URL 下载安装包到本机缓存目录 (进度走事件 "progress"), 然后打开校验。
// proxy 为空则直连。不比对整包 sha256 (同版本会替换安装包); 下坏的包由 OpenBundle 的
// 逐文件校验拦下。
// 多个地址按顺序试, 前一个不行再换下一个。GitHub 在国内常连不上, 而自建服务器
// 带宽有限 —— 两头都留着, 谁通用谁。
func (a *App) DownloadBundle(urls []string, proxy string) (*BundleInfo, error) {
	if len(urls) == 0 {
		return nil, errors.New(i18n.T("没有可用的下载地址", "no download URL available"))
	}
	var lastErr error
	for i, u := range urls {
		b, err := a.downloadOne(u, proxy)
		if err == nil {
			return b, nil
		}
		lastErr = err
		// 用户主动取消就别接着试下一个了 —— 他按的是"取消"不是"换一个"
		if errors.Is(err, context.Canceled) {
			return nil, err
		}
		if i < len(urls)-1 {
			a.log("✗ " + scrubErr(err, urls))
			a.log(i18n.T("换下一个地址再试…", "Trying the next source…"))
		}
	}
	// 返回给前端的也要过一道 —— 前端会原样 appendLog, 不过滤就等于白过滤。
	return nil, errors.New(scrubErr(lastErr, urls))
}

// scrubURL 把报错里的完整地址换成文件名。net/http 的错误长这样:
//
//	Get "https://example.com/space/x.zip": dial tcp ...
//
// 用户截图发日志求助时, 不该顺带把分发地址一起发出去。
func scrubURL(s string) string {
	return urlInErr.ReplaceAllStringFunc(s, func(m string) string {
		u, err := url.Parse(strings.Trim(m, `"`))
		if err != nil {
			return i18n.T("服务器", "server")
		}
		// 只取路径里的文件名。主机名、路径、query 一概不留 —— 用户截图发日志
		// 求助时, 不该顺带把分发地址发出去。
		name := path.Base(u.Path)
		if name == "" || name == "." || name == "/" || strings.HasSuffix(u.Path, "/") {
			return i18n.T("服务器", "server")
		}
		return name
	})
}

// scrubErr 在 scrubURL 之上再抹掉主机名本身。DNS 和 TCP 的错误不带 scheme,
// 长这样: `dial tcp: lookup example.com: no such host` —— 正则抓不到, 得拿
// 本次用过的地址逐个替换。
func scrubErr(err error, urls []string) string {
	s := scrubURL(err.Error())
	for _, raw := range urls {
		u, e := url.Parse(raw)
		if e != nil {
			continue
		}
		for _, h := range []string{u.Host, u.Hostname()} {
			if h != "" {
				s = strings.ReplaceAll(s, h, i18n.T("服务器", "server"))
			}
		}
	}
	return s
}

var urlInErr = regexp.MustCompile(`https?://[^\s"]+`)

func (a *App) downloadOne(url, proxy string) (*BundleInfo, error) {
	dir, _ := os.UserCacheDir()
	dir = filepath.Join(dir, "space-assistant")
	_ = os.MkdirAll(dir, 0o755)
	dst := filepath.Join(dir, filepath.Base(strings.Split(url, "?")[0]))
	// 日志只报文件名, 不报地址 —— 日志会被用户截图发出来, 分发地址没必要跟着走。
	name := filepath.Base(strings.Split(url, "?")[0])
	if strings.TrimSpace(proxy) != "" {
		a.log(i18n.T("下载 "+name+" (经代理 "+strings.TrimSpace(proxy)+")", "Downloading "+name+" (via proxy "+strings.TrimSpace(proxy)+")"))
	} else {
		a.log(i18n.T("下载 "+name+" (直连)", "Downloading "+name+" (direct)"))
	}
	client, err := netx.Client(proxy)
	if err != nil {
		return nil, fmt.Errorf(i18n.T("代理地址无效: %w", "invalid proxy address: %w"), err)
	}
	parent := a.ctx
	if parent == nil { // 没跑在 Wails 里 (测试), 照样能下载
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	a.mu.Lock()
	a.cancel = cancel
	a.mu.Unlock()
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		if strings.Contains(url, "github.com") {
			return nil, fmt.Errorf(i18n.T("下载失败: %w (GitHub 直连在国内常超时, 可在上方填代理地址)", "download failed: %w (you can set a proxy in Settings)"), err)
		}
		return nil, fmt.Errorf(i18n.T("下载失败: %w", "download failed: %w"), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf(i18n.T("下载失败: HTTP %d (%s)", "download failed: HTTP %d (%s)"), resp.StatusCode, url)
	}
	f, err := os.Create(dst + ".part")
	if err != nil {
		return nil, err
	}
	pw := &progressWriter{total: resp.ContentLength, emit: func(done, total int64) {
		runtime.EventsEmit(a.ctx, "progress", map[string]int64{"done": done, "total": total})
	}}
	_, err = io.Copy(io.MultiWriter(f, pw), resp.Body)
	_ = f.Close()
	if err != nil {
		_ = os.Remove(dst + ".part")
		return nil, fmt.Errorf(i18n.T("下载中断: %w", "download interrupted: %w"), err)
	}
	if err := os.Rename(dst+".part", dst); err != nil {
		return nil, err
	}
	return a.OpenBundle(dst)
}

// PlanSpace 生成「空间 + 底座」的安装计划 (纯计算, 不碰设备)。
func (a *App) PlanSpace() (*deploy.Plan, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.info == nil || a.bundle == nil {
		return nil, errors.New(i18n.T("先连接设备并选择安装包", "connect a device and choose a bundle first"))
	}
	return deploy.NewPlan(*a.info, a.bundle)
}

// RunSpace 执行「空间 + 底座」的安装/更新。
func (a *App) RunSpace() error {
	return a.runTask(i18n.T("安装空间", "Install Space"), func(ctx context.Context) error {
		p, err := deploy.NewPlan(*a.info, a.bundle)
		if err != nil {
			return err
		}
		return deploy.Run(ctx, a.client, a.bundle, p, a.log)
	})
}

// RepairPanel 恢复侧栏入口 (清熔断 + 重启 xochitl)。
func (a *App) RepairPanel() error {
	return a.runTask(i18n.T("恢复侧栏入口", "Restore sidebar entry"), func(ctx context.Context) error { return deploy.RepairPanel(ctx, a.client) })
}

// UninstallSpace 卸载空间与底座, 设备恢复出厂启动配置。
func (a *App) UninstallSpace() error {
	return a.runTask(i18n.T("卸载空间", "Uninstall Space"), func(ctx context.Context) error { return deploy.Uninstall(ctx, a.client) })
}

// EmergencyRepair 紧急修复: 不停地连设备, 一连上就让它以原厂状态启动 (停用两个槽的
// xochitl drop-in、拉熔断、搬走 xovi 扩展, 见 scripts/rescue.sh)。
//
// 给起不来的设备用: 开机途中 USB 网络和 SSH 比界面先起来 (Paper Pro Move 上试过), 抓住
// 这个窗口就能在看门狗切槽之前把启动链清干净。所以不走 Connect 那套 (探测、装公钥都要
// 时间), 连上直接跑脚本。不需要设备事先连过助手。
func (a *App) EmergencyRepair(host, password string) error {
	host = strings.TrimSpace(host)
	if host == "" {
		host = "10.11.99.1"
	}
	ctx, cancel := context.WithTimeout(a.ctx, 15*time.Minute)
	defer cancel()
	a.mu.Lock()
	a.cancel = cancel // 「取消」按钮
	a.mu.Unlock()

	a.log(i18n.T("===== 紧急修复开始 =====", "===== Emergency repair started ====="))
	a.log(i18n.T("正在等设备 "+host+" … 设备卡在开机或反复重启都没关系, 保持 USB 连着, 一连上就会自动修复", "Waiting for device "+host+" … it is fine if it is stuck booting or rebooting; keep USB connected and the repair runs as soon as it connects"))
	var c *sshx.Client
	authFails := 0
	for n := 1; c == nil; n++ {
		dctx, dcancel := context.WithTimeout(ctx, 4*time.Second)
		cl, err := sshx.Dial(dctx, host, password, func(string) {})
		dcancel()
		if err == nil {
			c = cl
			break
		}
		// 密码错了就别一直转; 但没填密码时开机早期 /home 未挂、密钥登录会失败, 要接着等
		var badPw sshx.BadPassword
		if errors.As(err, &badPw) && password != "" {
			if authFails++; authFails >= 3 {
				a.log(i18n.T("✗ 紧急修复: ", "✗ Emergency repair: ") + err.Error())
				return err
			}
		}
		if n%15 == 0 {
			a.log(fmt.Sprintf(i18n.T("还在等设备 (已试 %d 次): %s", "Still waiting for the device (attempt %d): %s"), n, err.Error()))
		}
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				err = errors.New(i18n.T("15 分钟内没连上设备。请确认 USB 线连着、设备在开机或重启中, 再试一次", "could not reach the device within 15 minutes; check the USB cable and that the device is booting or rebooting, then try again"))
			} else {
				err = errors.New(i18n.T("已取消", "cancelled"))
			}
			a.log(i18n.T("✗ 紧急修复: ", "✗ Emergency repair: ") + err.Error())
			return err
		case <-time.After(time.Second):
		}
	}
	defer c.Close()
	a.log(i18n.T("已连上设备, 开始修复", "Connected, starting repair"))
	rctx, rcancel := context.WithTimeout(ctx, 3*time.Minute)
	defer rcancel()
	if err := c.RunScript(rctx, scripts.Rescue, map[string]string{"UI_LANG": i18n.Lang()}); err != nil {
		a.log(i18n.T("✗ 紧急修复失败: ", "✗ Emergency repair failed: ") + err.Error())
		return err
	}
	a.log(i18n.T("✓ 紧急修复完成", "✓ Emergency repair done"))
	return nil
}

// ExportDiagnostics 导出诊断: 只读收集版本、两个槽的启动配置、底座/空间状态和最近几次开机的
// 系统日志, 存成一个文件并打开所在文件夹, 让用户发给开发者。设备起不来、切槽、空间没了时用 ——
// 助手自己的日志只记得它连着时做了什么, 设备在两次连接之间怎么重启的只有设备端日志知道。
func (a *App) ExportDiagnostics() (string, error) {
	a.mu.Lock()
	if err := a.ensureAlive(); err != nil {
		a.mu.Unlock()
		return "", err
	}
	c := a.client
	a.mu.Unlock()
	a.log(i18n.T("正在收集诊断信息 (只读, 不改设备上的任何东西)...", "Collecting diagnostics (read-only, nothing on the device is changed)..."))
	ctx, cancel := context.WithTimeout(a.ctx, 2*time.Minute)
	defer cancel()
	res, err := c.Run(ctx, "bash -s <<'SPACE_DIAG_EOF'\n"+scripts.Diagnose+"\nSPACE_DIAG_EOF")
	if err != nil && res.Stdout == "" {
		a.log(i18n.T("✗ 收集诊断失败: ", "✗ Diagnostics failed: ") + err.Error())
		return "", err
	}
	text := fmt.Sprintf(i18n.T("空间助手 %s 导出的诊断 (%s)\n", "Diagnostics exported by Space Assistant %s (%s)\n"), a.Version(), time.Now().Format("2006-01-02 15:04:05")) +
		scrubDiag(res.Stdout)
	if res.Stderr != "" {
		text += i18n.T("\n======== 设备端报错 ========\n", "\n======== Device-side errors ========\n") + scrubDiag(res.Stderr)
	}
	path := filepath.Join(a.logDir, "diagnose-"+time.Now().Format("20060102-150405")+".txt")
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		return "", err
	}
	a.log(i18n.T("✓ 诊断已保存: "+path+" —— 请把这个文件发给开发者", "✓ Diagnostics saved: "+path+" — please send this file to the developer"))
	_ = a.OpenLogDir()
	return path, nil
}

// 日志里万一带出凭证 (JWT / key=value 形式的 token、password), 发出去之前遮掉。
var diagSecretRe = regexp.MustCompile(`(?i)(eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9._-]+|((?:token|password|passwd|secret|authorization)\s*[=:]\s*)((?:bearer\s+)?"[^"]*"|(?:bearer\s+)?\S+))`)

func scrubDiag(s string) string {
	return diagSecretRe.ReplaceAllStringFunc(s, func(m string) string {
		if sub := diagSecretRe.FindStringSubmatch(m); sub != nil && sub[2] != "" {
			return sub[2] + "***"
		}
		return "***"
	})
}

// PlanAndroid 生成单槽 Android 安装计划 (含预检阻断项)。
func (a *App) PlanAndroid(replaceSystem bool) (*android.Plan, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.info == nil || a.bundle == nil {
		return nil, errors.New(i18n.T("先连接设备并选择安装包", "connect a device and choose a bundle first"))
	}
	present, err := a.androidSystemPresent()
	if err != nil {
		return nil, err
	}
	return android.NewPlan(*a.info, a.bundle, present, replaceSystem)
}

func (a *App) androidSystemPresent() (bool, error) {
	if err := a.ensureAlive(); err != nil {
		return false, err
	}
	ctx, cancel := context.WithTimeout(a.ctx, 15*time.Second)
	defer cancel()
	res, err := a.client.Run(ctx, "[ -e /home/root/android-system/system/bin/init ] && echo yes || echo no")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(res.Stdout) == "yes", nil
}

// RunAndroid 执行单槽 Android 安装。
func (a *App) RunAndroid(replaceSystem bool) error {
	return a.runTask(i18n.T("安装单槽 Android", "Install single-slot Android"), func(ctx context.Context) error {
		present, err := a.androidSystemPresent()
		if err != nil {
			return err
		}
		p, err := android.NewPlan(*a.info, a.bundle, present, replaceSystem)
		if err != nil {
			return err
		}
		return android.Run(ctx, a.client, a.bundle, p, a.log)
	})
}

// UninstallAndroid 卸载单槽 Android。
func (a *App) UninstallAndroid(removeData bool) error {
	return a.runTask(i18n.T("卸载单槽 Android", "Uninstall single-slot Android"), func(ctx context.Context) error { return android.Uninstall(ctx, a.client, removeData) })
}

// BootAndroid 重启进 Android。
func (a *App) BootAndroid() error {
	return a.runTask(i18n.T("重启进 Android", "Reboot into Android"), func(ctx context.Context) error { return android.BootAndroid(ctx, a.client) })
}

// ReturnToStock 从 Android 模式回 reMarkable。
func (a *App) ReturnToStock() error {
	return a.runTask(i18n.T("回 reMarkable 系统", "Return to the reMarkable system"), func(ctx context.Context) error { return android.ReturnToStock(ctx, a.client) })
}

// ResetAndroidData 清空 Android 数据目录 (reMarkable 模式下)。
func (a *App) ResetAndroidData() error {
	return a.runTask(i18n.T("重置 Android 数据", "Reset Android data"), func(ctx context.Context) error { return android.ResetData(ctx, a.client) })
}

// ChooseAPKs 弹多选框选本机 APK。
func (a *App) ChooseAPKs() ([]string, error) {
	return runtime.OpenMultipleFilesDialog(a.ctx, runtime.OpenDialogOptions{
		Title:   i18n.T("选择要安装到 Android 的 APK", "Choose APKs to install into Android"),
		Filters: []runtime.FileFilter{{DisplayName: i18n.T("Android 应用 (*.apk)", "Android app (*.apk)"), Pattern: "*.apk"}},
	})
}

// InstallAPKs 在 Android 模式下安装 APK。
func (a *App) InstallAPKs(paths []string) error {
	return a.runTask(fmt.Sprintf(i18n.T("安装 %d 个 APK", "Install %d APKs"), len(paths)), func(ctx context.Context) error {
		return android.InstallAPKs(ctx, a.client, paths, a.log)
	})
}

// Confirm 弹原生确认框 (Wails 的 WebView 不支持 window.confirm)。
func (a *App) Confirm(title, message string) bool {
	ok, cancel := i18n.T("继续", "Continue"), i18n.T("取消", "Cancel")
	r, err := runtime.MessageDialog(a.ctx, runtime.MessageDialogOptions{
		Type: runtime.QuestionDialog, Title: title, Message: message,
		Buttons: []string{ok, cancel}, DefaultButton: ok, CancelButton: cancel,
	})
	if err != nil {
		return false
	}
	return r == ok || r == "Yes" || r == "Ok"
}

// DiagnoseAndroid 收集 Android 启动诊断文本, 同时写进日志目录一个文件方便发 Issue。
func (a *App) DiagnoseAndroid() (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureAlive(); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(a.ctx, 60*time.Second)
	defer cancel()
	text, err := android.Diagnose(ctx, a.client)
	if err != nil {
		return "", err
	}
	path := filepath.Join(a.logDir, "android-diag-"+time.Now().Format("20060102-150405")+".txt")
	_ = os.WriteFile(path, []byte(text), 0o644)
	a.log(i18n.T("Android 启动诊断已保存: ", "Android boot diagnostics saved: ") + path)
	return text, nil
}

// Cancel 取消正在执行的任务 (只是中断 SSH 会话; 设备端脚本自带 abort_safe 回退)。
func (a *App) Cancel() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cancel != nil {
		a.cancel()
	}
}

func (a *App) runTask(name string, fn func(ctx context.Context) error) error {
	a.mu.Lock()
	if a.client == nil || a.info == nil {
		a.mu.Unlock()
		return errors.New(i18n.T("未连接设备", "no device connected"))
	}
	if err := a.ensureAlive(); err != nil {
		a.mu.Unlock()
		a.log("✗ " + name + ": " + err.Error())
		return err
	}
	ctx, cancel := context.WithCancel(a.ctx)
	a.cancel = cancel
	a.mu.Unlock()
	defer cancel()
	a.log("===== " + name + i18n.T(" 开始 =====", " started ====="))
	err := fn(ctx)
	if err != nil {
		a.log("✗ " + name + i18n.T(" 失败: ", " failed: ") + err.Error())
		return err
	}
	a.log("✓ " + name + i18n.T(" 完成", " done"))
	return nil
}

type progressWriter struct {
	done, total int64
	last        time.Time
	emit        func(done, total int64)
}

func (p *progressWriter) Write(b []byte) (int, error) {
	p.done += int64(len(b))
	if time.Since(p.last) > 200*time.Millisecond {
		p.last = time.Now()
		p.emit(p.done, p.total)
	}
	return len(b), nil
}
