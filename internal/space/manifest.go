// Package space 是「空间 (SPACE)」启动台的后端: 应用注册表、绿色安装/卸载、应用私有后台进程托管。
//
// 设计原则 (高内聚, 低耦合):
//   - 本包对任何具体应用一无所知, 只认应用目录里的 manifest.json。
//   - upload-server 只把本包挂到 /space/ 路径下, 不参与应用逻辑。
//   - 应用自己的后端 (如播放器) 由应用包自带, 在 manifest 里声明, 本包按需拉起/收掉,
//     应用前端直接访问自己的后端端口, 不经过 upload-server。
package space

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ShellVersion 是外壳 (Space.qml) 与本后端共同遵守的应用契约版本。
// manifest.min_shell 大于它的应用不会被加载。必须与 Space.qml 的 shellVersion 一致。
//
//	1: 初版
//	2: 文件库桥 /space/library/* (热导入、建文件夹/笔记本); 手写画布 (应用仓库 lib/ink) 要它
//	3: 贴纸包 (manifest.stickers); 取商店索引时带 X-Space-Shell, 商店不给旧外壳列它装不了的条目
const ShellVersion = 3

// ManifestName 是应用目录里清单文件的固定名字。
const ManifestName = "manifest.json"

// DisplayName picks the name to show for a UI language:
// Names[lang] first, then NameEn (for any language other than zh-CN, which is what apps
// from the upstream store ship), and finally Name. Reading a nil map is fine in Go.
func (m *Manifest) DisplayName(lang string) string {
	if n := m.Names[lang]; n != "" {
		return n
	}
	if lang != "zh-CN" && m.NameEn != "" {
		return m.NameEn
	}
	return m.Name
}

// Service 声明应用自带的后台进程。
type Service struct {
	Exec      string   `json:"exec"`                // 相对应用目录的可执行文件, 如 bin/music-server
	Args      []string `json:"args,omitempty"`      // 附加参数
	Port      int      `json:"port,omitempty"`      // 监听端口 (>0 时外壳会等它就绪并把 serviceUrl 交给前端)
	Health    string   `json:"health,omitempty"`    // 就绪探测路径, 默认 "/"
	KeepAlive bool     `json:"keepalive,omitempty"` // 退出应用界面后是否继续跑 (后台播放等)
	AutoStart bool     `json:"autostart,omitempty"` // upload-server 启动时是否自动拉起
}

// Launch 声明"点图标即启动外部程序"的应用 (无 QML 界面), 如 KOReader / Android。
type Launch struct {
	Type    string `json:"type"`              // "post": 向 upload-server 发 POST; "appload": 走 xovi appload 单例
	Path    string `json:"path,omitempty"`    // post 的路径 (appload 时作为兜底)
	ID      string `json:"id,omitempty"`      // appload 的应用标识, 如 external::koreader
	Confirm string `json:"confirm,omitempty"` // 非空则先弹确认框 (重启类操作)
}

// Module 声明应用以编译后的 QML 模块形式发布 (而不是明文 .qml 文件)。
//
// 为什么要这个: 明文发布的应用解压就是源码, 付费应用等于白送。编译成 QML 模块后,
// 包里只有 .so —— qmlcachegen 把 QML 做成 AOT 函数, 不是压缩也不是打包 (实测 112KB 的
// main.qml 变成 8380 个 AOT 符号, 源码的注释/属性行/函数定义都搜不到)。
//
// 模块装到固定的 qmlmods 目录 —— QML_IMPORT_PATH 是 xochitl 启动时定下的, 不可能
// 每装一个应用就改一次环境变量再重启。
type Module struct {
	URI    string `json:"uri"`              // import 的模块名, 如 Plan; 同时是 qmlmods 下的目录名
	Main   string `json:"main,omitempty"`   // 界面入口类型名, 如 Main
	Widget string `json:"widget,omitempty"` // 首页小组件类型名, 如 Widget
}

// Manifest 是应用清单 (manifest.json)。
type Manifest struct {
	ID          string   `json:"id"`                    // 唯一标识: 小写字母/数字/-/_ , 1-32 位, 也是目录名
	Name        string   `json:"name"`                  // 显示名 (中文)
	NameEn      string   `json:"name_en,omitempty"`     // 英文名 (可选)
	Version     string   `json:"version"`               // 语义化版本
	Description string   `json:"description,omitempty"` // 一句话说明
	Icon        string   `json:"icon,omitempty"`        // 图标: 相对路径 / 绝对路径 / qrc: 或 file: URL
	Entry       string   `json:"entry,omitempty"`       // 界面入口 QML (相对路径), 与 launch 二选一或同时有
	Widget      string   `json:"widget,omitempty"`      // 首页小组件 QML (相对路径, 可选)
	WidgetSize  string   `json:"widget_size,omitempty"` // 小组件尺寸: half (半宽, 默认) / hero (首页顶部整宽大卡)
	WidgetSlot  string   `json:"widget_slot,omitempty"` // 指定首页位置: plan (今日计划那张卡); 不填就进「正在进行」
	Category    string   `json:"category,omitempty"`    // system / settings / reader / tool / game / other
	Order       int      `json:"order,omitempty"`       // 同类内排序 (小的在前)
	Arch        []string `json:"arch,omitempty"`        // 支持的架构: aarch64 / armv7; 空 = 都支持
	MinShell    int      `json:"min_shell,omitempty"`   // 需要的最低外壳契约版本
	Service     *Service `json:"service,omitempty"`
	Launch      *Launch  `json:"launch,omitempty"`
	Module      *Module  `json:"module,omitempty"`  // 编译发布: 与 entry/widget 二选一
	Install     *Install `json:"install,omitempty"` // 装完应用包之后还要装的"本体"
	// Stickers 贴纸包: 应用目录里放贴纸包的子目录 (下面每个子目录是一个包)。这种条目只为了走
	// 商店的下载/升级/删除, 不出现在应用格子里; 装好后由安装器把包挂进贴纸目录。
	Stickers string `json:"stickers,omitempty"`
	// Hooks 挂进 xochitl 界面的组件: 挂点 → 应用目录里的 .qml。挂点见 HookPoints。
	// 这些组件跑在 xochitl 进程里, 写坏了会拖垮整个界面, 所以只有商店签名的专属包能声明
	// (安装时验 .integrity), 开发模式 (.dev) 例外。注入库按文件熔断, 见 base/intercept/qml-inject。
	Hooks map[string][]string `json:"hooks,omitempty"`
	// Theme 主题包: 应用目录里放主题的子目录 (里面一个 theme.json)。装好后链进用户主题目录, 见 theme.go。
	Theme string `json:"theme,omitempty"`
	// Names holds display names per language code, e.g. {"fr": "Météo", "de": "Wetter"}.
	// See DisplayName for the lookup order. Optional.
	Names map[string]string `json:"names,omitempty"`
}

// HookPoints 注入库开放的挂点 (base/intercept/qml-inject/qml_inject_impl.cpp 的「通用挂载点」一节)
var HookPoints = map[string]bool{"sidebar": true, "document": true, "selection": true, "textmenu": true, "root": true}

// Install 描述"应用包之外还得装点别的"。
//
// 有些应用的本体是别人发布的, 我们不能也不该重新分发 (版权, 以及跟着人家升级的负担)。
// 这种就只在商店里放一份几百字节的描述 + 我们自己写的安装脚本, 让设备**直接从官方
// 地址下载**。Fetch 里的 sha256 是硬要求: 对方换了包我们当场知道, 而不是装上才发现。
type Install struct {
	Fetch []Fetch `json:"fetch,omitempty"`
	// Script 应用包里自带的安装脚本 (相对路径)。它跑在 root 下, 所以必须是我们自己
	// 写的、可审计的 —— 绝不执行从网上抓来的脚本。工作目录是放下载物的临时目录,
	// 环境变量 SPACE_APP_DIR 指向应用自己的目录。
	Script string `json:"script,omitempty"`
	// Uninstall 卸载脚本 (相对路径)。没有的话卸载只删应用目录, 本体会留在设备上。
	Uninstall string `json:"uninstall,omitempty"`
	// Note 安装前给用户看的一句话 (要下多大、装到哪)。
	Note string `json:"note,omitempty"`
}

type Fetch struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	// To 存成什么文件名 (默认取 URL 最后一段)。
	To string `json:"to,omitempty"`
}

var idRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

// QML 模块名与类型名都必须大写字母开头 —— 这是 QML 的硬规矩, 小写的不会被注册成类型。
// URI 允许 . 分段 (如 Space.Plan), 目录按段拆开。
var moduleURIRe = regexp.MustCompile(`^[A-Z][A-Za-z0-9]*(\.[A-Z][A-Za-z0-9]*)*$`)
var qmlTypeRe = regexp.MustCompile(`^[A-Z][A-Za-z0-9]*$`)
var sha256Re = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)

var categoryOrder = map[string]int{
	"system": 0, "settings": 1, "reader": 2, "tool": 3, "game": 4, "other": 5,
}

// Validate 检查清单是否自洽; 出错信息面向应用开发者。
func (m *Manifest) Validate() error {
	if !idRe.MatchString(m.ID) {
		return fmt.Errorf("id %q 不合法 (小写字母/数字/-/_, 1-32 位, 首字符须为字母或数字)", m.ID)
	}
	if strings.TrimSpace(m.Name) == "" {
		return errors.New("name 不能为空")
	}
	if strings.TrimSpace(m.Version) == "" {
		return errors.New("version 不能为空")
	}
	if m.Theme != "" && !isRelativeInside(m.Theme) && m.Theme != "." {
		return fmt.Errorf("theme 必须是应用目录内的相对路径: %q", m.Theme)
	}
	if m.Stickers != "" && !isRelativeInside(m.Stickers) {
		return fmt.Errorf("stickers 必须是应用目录内的相对路径: %q", m.Stickers)
	}
	for pt, files := range m.Hooks {
		if !HookPoints[pt] {
			return fmt.Errorf("hooks 里的挂点 %q 不认识 (sidebar/document/selection/textmenu/root)", pt)
		}
		for _, f := range files {
			if !isRelativeInside(f) || filepath.Ext(f) != ".qml" {
				return fmt.Errorf("hooks.%s 必须是应用目录内的 .qml: %q", pt, f)
			}
		}
	}
	if m.Entry == "" && m.Launch == nil && m.Module == nil && m.Stickers == "" && len(m.Hooks) == 0 && m.Theme == "" {
		return errors.New("entry (界面 QML)、module (编译模块)、launch (外部启动) 、stickers (贴纸包)、hooks (挂载组件) 与 theme (主题) 至少要有一个")
	}
	if m.Module != nil {
		if !moduleURIRe.MatchString(m.Module.URI) {
			return fmt.Errorf("module.uri %q 不合法 (QML 模块名: 大写字母开头, 只能字母数字, 可用 . 分段)", m.Module.URI)
		}
		if m.Module.Main == "" && m.Module.Widget == "" {
			return errors.New("module 至少要声明 main 或 widget 之一")
		}
		for _, f := range []struct{ name, val string }{{"module.main", m.Module.Main}, {"module.widget", m.Module.Widget}} {
			if f.val != "" && !qmlTypeRe.MatchString(f.val) {
				return fmt.Errorf("%s %q 不合法 (QML 类型名: 大写字母开头, 只能字母数字)", f.name, f.val)
			}
		}
		if m.Entry != "" || m.Widget != "" {
			return errors.New("module 与 entry/widget 不能同时声明 (前者是编译发布, 后者是明文发布)")
		}
		if m.Module.Widget != "" && m.WidgetSize == "" {
			m.WidgetSize = "half"
		}
	}
	for _, f := range []struct{ name, val string }{{"entry", m.Entry}, {"widget", m.Widget}} {
		if f.val != "" && !isRelativeInside(f.val) {
			return fmt.Errorf("%s 必须是应用目录内的相对路径: %q", f.name, f.val)
		}
	}
	if m.Icon != "" && !isURL(m.Icon) && !filepath.IsAbs(m.Icon) && !isRelativeInside(m.Icon) {
		return fmt.Errorf("icon 必须是相对路径、绝对路径或 qrc:/file: URL: %q", m.Icon)
	}
	if m.Category == "" {
		m.Category = "tool"
	}
	if m.Widget != "" && m.WidgetSize == "" {
		m.WidgetSize = "half"
	}
	if m.WidgetSize != "" && m.WidgetSize != "half" && m.WidgetSize != "hero" {
		return fmt.Errorf("widget_size %q 不认识 (half / hero)", m.WidgetSize)
	}
	if _, ok := categoryOrder[m.Category]; !ok {
		return fmt.Errorf("category %q 不认识 (system/settings/reader/tool/game/other)", m.Category)
	}
	if m.Install != nil {
		for i, f := range m.Install.Fetch {
			if !strings.HasPrefix(f.URL, "https://") && !strings.HasPrefix(f.URL, "http://") {
				return fmt.Errorf("install.fetch[%d].url 必须是 http(s): %q", i, f.URL)
			}
			// sha256 是硬要求, 不是可选项。它同时顶两件事:
			//   1. 对方换了包要当场知道, 而不是装上才发现;
			//   2. 明文 http 下中间人改不了内容 —— 改了就对不上, 装不进去。
			// 所以这里允许 http: 镜像可能是个纯 IP 的国内节点 (签不了 CA 证书),
			// 而下载物都是公开的开源包, https 多出来的传输加密没什么可保的。
			// 但**绝不能**因此放松 sha256。
			if !sha256Re.MatchString(f.SHA256) {
				return fmt.Errorf("install.fetch[%d].sha256 必须是 64 位十六进制 (明文 http 全靠它保证内容没被换)", i)
			}
			if f.To != "" && (!isRelativeInside(f.To) || strings.Contains(f.To, "/")) {
				return fmt.Errorf("install.fetch[%d].to 只能是文件名: %q", i, f.To)
			}
		}
		for _, f := range []struct{ name, val string }{
			{"install.script", m.Install.Script}, {"install.uninstall", m.Install.Uninstall},
		} {
			if f.val != "" && !isRelativeInside(f.val) {
				return fmt.Errorf("%s 必须是应用目录内的相对路径: %q", f.name, f.val)
			}
		}
		if len(m.Install.Fetch) > 0 && m.Install.Script == "" {
			return errors.New("install.fetch 得配 install.script —— 光下载不装没有意义")
		}
	}
	if m.MinShell > ShellVersion {
		return fmt.Errorf("需要外壳契约版本 %d, 本机只有 %d", m.MinShell, ShellVersion)
	}
	for _, a := range m.Arch {
		if a != "aarch64" && a != "armv7" {
			return fmt.Errorf("arch %q 不认识 (aarch64 / armv7)", a)
		}
	}
	if s := m.Service; s != nil {
		if !isRelativeInside(s.Exec) {
			return fmt.Errorf("service.exec 必须是应用目录内的相对路径: %q", s.Exec)
		}
		if s.Port != 0 && (s.Port < 1024 || s.Port > 65535) {
			return fmt.Errorf("service.port %d 超出 1024-65535", s.Port)
		}
		if s.Health != "" && !strings.HasPrefix(s.Health, "/") {
			return fmt.Errorf("service.health 必须以 / 开头: %q", s.Health)
		}
	}
	if l := m.Launch; l != nil {
		switch l.Type {
		case "post":
			if !strings.HasPrefix(l.Path, "/") {
				return fmt.Errorf("launch.path 必须以 / 开头: %q", l.Path)
			}
		case "appload":
			if l.ID == "" {
				return errors.New("launch.type=appload 需要 launch.id")
			}
			if l.Path != "" && !strings.HasPrefix(l.Path, "/") {
				return fmt.Errorf("launch.path 必须以 / 开头: %q", l.Path)
			}
		default:
			return fmt.Errorf("launch.type %q 不认识 (post / appload)", l.Type)
		}
	}
	return nil
}

// SupportsArch 判断清单是否支持给定架构 (空 = 全支持)。
func (m *Manifest) SupportsArch(arch string) bool {
	if len(m.Arch) == 0 {
		return true
	}
	for _, a := range m.Arch {
		if a == arch {
			return true
		}
	}
	return false
}

// LoadManifest 读取并校验 dir/manifest.json。
func LoadManifest(dir string) (*Manifest, error) {
	b, err := os.ReadFile(filepath.Join(dir, ManifestName))
	if err != nil {
		return nil, err
	}
	return ParseManifest(b)
}

// ParseManifest 解析并校验清单内容。
func ParseManifest(b []byte) (*Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("manifest.json 不是合法 JSON: %w", err)
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

// isRelativeInside: 相对路径, 不含 .., 不是绝对路径, 不是 URL。
func isRelativeInside(p string) bool {
	if p == "" || filepath.IsAbs(p) || isURL(p) {
		return false
	}
	clean := filepath.Clean(p)
	return clean != "." && !strings.HasPrefix(clean, "..")
}

func isURL(p string) bool {
	return strings.HasPrefix(p, "qrc:") || strings.HasPrefix(p, "file:")
}
