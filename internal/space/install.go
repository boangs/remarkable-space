package space

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// 单个应用包解压上限, 防止畸形 zip 撑爆 /home。
const maxAppBytes = 256 << 20

// InstallZip 把一个应用包 (zip) 绿色安装到用户应用目录。
// 包结构: manifest.json 在 zip 根, 或者在唯一的一层顶级目录下 (GitHub 打包常见)。
// 流程: 校验清单 → 解压到临时目录 → 停掉同名后台 → 原子替换目录。失败不影响已装版本。
func (r *Registry) InstallZip(zr *zip.Reader, hdr http.Header, sup *Supervisor) (App, error) {
	prefix, m, err := findManifest(zr)
	if err != nil {
		return App{}, err
	}
	if !m.SupportsArch(r.Arch) {
		return App{}, fmt.Errorf("应用 %s 不支持本机架构 %s", m.ID, r.Arch)
	}
	if err := os.MkdirAll(r.UserDir, 0o755); err != nil {
		return App{}, err
	}
	tmp := filepath.Join(r.UserDir, ".tmp-"+m.ID)
	_ = os.RemoveAll(tmp)
	if err := extract(zr, prefix, tmp); err != nil {
		_ = os.RemoveAll(tmp)
		return App{}, err
	}
	final := filepath.Join(r.UserDir, m.ID)
	if sup != nil {
		_ = sup.Stop(m.ID)
	}
	_ = os.RemoveAll(final)
	if err := os.Rename(tmp, final); err != nil {
		_ = os.RemoveAll(tmp)
		return App{}, err
	}
	// 编译发布的应用: 把包里的 qml/<URI>/ 搬进固定的模块目录, 否则 import 找不到。
	// 放完整个应用目录之后再搬, 失败就把应用一起回滚 —— 半装的状态更难排查。
	if m.Module != nil {
		if err := r.installModule(final, m); err != nil {
			_ = os.RemoveAll(final)
			return App{}, err
		}
	} else if r.ModuleDir != "" {
		// 从编译发布改回明文的应用: 旧版在模块目录里留了一份 qml/<URI>/, 不清掉的话
		// import 还会解析到它 —— 装了新版行为却是旧的, 这种最难排查。
		// 清哪个目录按 ID 猜不出来 (URI 是清单里自定的), 所以扫一遍找 qmldir 里
		// 声明的模块名对得上旧 URI 的。这里简单处理: 按应用 ID 的首字母大写形式试一次,
		// 再加上应用目录里残留的 qml/ 下的名字。
		for _, name := range staleModuleNames(final, m.ID) {
			_ = os.RemoveAll(filepath.Join(r.ModuleDir, name))
		}
	}
	if m.Theme != "" {
		if err := r.linkTheme(final, m); err != nil {
			_ = os.RemoveAll(final)
			return App{}, err
		}
	}
	if len(m.Hooks) > 0 {
		if err := r.linkHooks(final, m); err != nil {
			_ = os.RemoveAll(final)
			return App{}, err
		}
	}
	if m.Stickers != "" {
		if err := r.linkStickers(final, m); err != nil {
			_ = os.RemoveAll(final)
			return App{}, err
		}
	}
	// 本体 (别人发布的那部分) 放最后装: 它要下几十 MB 又要跑脚本, 失败率比前面高,
	// 而应用目录这时已经完整 —— 用户至少能看到这个应用、能再点一次重装。
	if err := r.runInstall(final, m, hdr); err != nil {
		setProgress(func(p *Progress) { p.Phase = "error"; p.Error = err.Error() })
		return App{}, err
	}
	a, ok := r.Get(m.ID)
	if !ok {
		return App{}, errors.New("安装后在注册表里找不到应用 (目录扫描失败)")
	}
	return a, nil
}

// InstallFromURL 下载应用包再安装; sha256hex 非空时校验摘要。
// Progress 是一次安装的进度。设备上一次只装一个应用, 所以全局一份就够。
//
// 为什么要它: 下载几 MB 在设备的 Wi-Fi 上可能十几秒, 期间界面只能干等 —— 用户不知道
// 是在下载、卡住了, 还是已经失败。墨水屏刷新慢, 不适合跑动画, 给个百分比数字最合适。
type Progress struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Phase string `json:"phase"` // downloading / installing / done / error
	Got   int64  `json:"got"`
	Total int64  `json:"total"` // 0 = 服务器没给 Content-Length
	Error string `json:"error,omitempty"`
}

var (
	progMu sync.Mutex
	prog   Progress
)

// InstallProgress 读当前安装进度 (没有任务时 Phase 为空)。
func InstallProgress() Progress {
	progMu.Lock()
	defer progMu.Unlock()
	return prog
}

func setProgress(f func(*Progress)) {
	progMu.Lock()
	defer progMu.Unlock()
	f(&prog)
}

// countingReader 边读边记字节数, 供进度查询。
type countingReader struct {
	r  io.Reader
	n  int64
	on func(int64)
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if n > 0 {
		c.n += int64(n)
		if c.on != nil {
			c.on(c.n)
		} else {
			setProgress(func(p *Progress) { p.Got = c.n }) // 应用本体下载 (payload.go) 走应用安装的进度
		}
	}
	return n, err
}

// hdr 里带的是取包的凭证 —— 粗筛用的 X-Space-Key, 以及握手模式下的 Bearer 票。
// 应用包和索引归同一道门管: 只挡索引是漏的, 链接一旦被记下来 (旧版索引、抓包),
// 不带凭证照样能一直下。
func (r *Registry) InstallFromURL(url, sha256hex string, hdr http.Header, sup *Supervisor) (App, error) {
	tmp, err := os.CreateTemp("", "space-app-*.zip")
	if err != nil {
		return App{}, err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	setProgress(func(p *Progress) { p.Phase = "downloading" })
	if err := downloadPkg(url, sha256hex, hdr, tmp, maxAppBytes, func(got, total int64) {
		setProgress(func(p *Progress) { p.Got, p.Total = got, total })
	}); err != nil {
		return App{}, err
	}
	n, _ := tmp.Seek(0, io.SeekEnd)
	// 下完了, 后面是校验+解包+搬模块。这几步在设备上也要一两秒 (模块包有好几 MB),
	// 界面上得换个说法, 不然进度卡在 100% 不动会让人以为死了。
	setProgress(func(p *Progress) { p.Phase = "installing" })
	zr, err := zip.NewReader(tmp, n)
	if err != nil {
		return App{}, fmt.Errorf("不是合法 zip: %w", err)
	}
	return r.InstallZip(zr, hdr, sup)
}

// Uninstall 删除用户目录里的应用 (内置应用不可卸载; 应用数据目录保留)。
func (r *Registry) Uninstall(id string, sup *Supervisor) error {
	if !idRe.MatchString(id) {
		return errors.New("id 不合法")
	}
	dir := filepath.Join(r.UserDir, id)
	if _, err := os.Stat(filepath.Join(dir, ManifestName)); err != nil {
		if _, e := os.Stat(filepath.Join(r.BuiltinDir, id, ManifestName)); e == nil {
			return errors.New("内置应用不能卸载 (随 rmkit-cn 一起管理)")
		}
		return errors.New("没有这个应用")
	}
	if sup != nil {
		_ = sup.Stop(id)
	}
	// 先记下模块名再删应用目录 —— 清单没了就不知道该收哪个模块
	var modDir string
	if m, err := LoadManifest(dir); err == nil {
		if m.Module != nil {
			modDir = filepath.Join(r.ModuleDir, moduleDirName(m.Module.URI))
		}
		// 本体 (装在应用目录之外的那部分) 得趁脚本还在的时候收掉
		r.runUninstall(dir, m)
	}
	r.unlinkStickers(dir)
	r.unlinkHooks(dir)
	r.unlinkTheme(dir)
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	if modDir != "" {
		_ = os.RemoveAll(modDir)
	}
	return nil
}

// moduleDirName 把 QML 模块 URI 转成目录名。URI 可以分段 (Space.Plan), 对应
// 嵌套目录 —— 这是 QML 的查找规则, 不是我们定的。
func moduleDirName(uri string) string {
	return filepath.Join(strings.Split(uri, ".")...)
}

// installModule 把应用包里的 qml/<URI>/ 搬到模块目录。
// 包里为什么是 qml/<URI>/: 这是 qt_add_qml_module 的 OUTPUT_DIRECTORY 布局, 原样带过来,
// 应用开发者不用额外摆放。
func (r *Registry) installModule(appDir string, m *Manifest) error {
	if r.ModuleDir == "" {
		return errors.New("后端没有配置模块目录, 装不了编译发布的应用")
	}
	name := moduleDirName(m.Module.URI)
	src := filepath.Join(appDir, "qml", name)
	if _, err := os.Stat(filepath.Join(src, "qmldir")); err != nil {
		return fmt.Errorf("应用包里缺 qml/%s/qmldir (声明了 module 就得带编译好的模块)", name)
	}
	dst := filepath.Join(r.ModuleDir, name)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	_ = os.RemoveAll(dst)
	if err := os.Rename(src, dst); err != nil {
		// 跨设备时 rename 会失败 (模块目录与应用目录可能不在同一文件系统), 退回复制
		if err := copyTree(src, dst); err != nil {
			return fmt.Errorf("装模块 %s 失败: %w", m.Module.URI, err)
		}
		_ = os.RemoveAll(src)
	}
	// 模块搬走后 qml/ 就空了 (URI 分段时还剩几层空壳), 顺手收掉, 别留在应用目录里
	for d := filepath.Dir(src); strings.HasPrefix(d, appDir+string(filepath.Separator)); d = filepath.Dir(d) {
		if os.Remove(d) != nil {
			break // 非空就停, Remove 对非空目录会失败
		}
	}
	return nil
}

// copyTree 递归复制目录, 保留可执行位 (.so 要能被 dlopen)。
func copyTree(src, dst string) error {
	return filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
		if err != nil {
			return err
		}
		defer out.Close()
		_, err = io.Copy(out, in)
		return err
	})
}

// findManifest 在 zip 里定位 manifest.json, 返回它所在的前缀 ("" 或 "dir/") 和解析结果。
func findManifest(zr *zip.Reader) (string, *Manifest, error) {
	var root, nested *zip.File
	nestedPrefix := ""
	for _, f := range zr.File {
		name := path.Clean(f.Name)
		if name == ManifestName {
			root = f
			break
		}
		if parts := strings.Split(name, "/"); len(parts) == 2 && parts[1] == ManifestName {
			if nested != nil && nestedPrefix != parts[0]+"/" {
				return "", nil, errors.New("zip 里有多个顶级目录都带 manifest.json, 不知道装哪个")
			}
			nested, nestedPrefix = f, parts[0]+"/"
		}
	}
	f, prefix := root, ""
	if f == nil {
		f, prefix = nested, nestedPrefix
	}
	if f == nil {
		return "", nil, errors.New("zip 里找不到 manifest.json (须在根或唯一顶级目录下)")
	}
	rc, err := f.Open()
	if err != nil {
		return "", nil, err
	}
	defer rc.Close()
	b, err := io.ReadAll(io.LimitReader(rc, 1<<20))
	if err != nil {
		return "", nil, err
	}
	m, err := ParseManifest(b)
	if err != nil {
		return "", nil, err
	}
	return prefix, m, nil
}

// extract 把 prefix 下的条目解到 dst, 拒绝路径穿越, 限制总大小, 保留可执行位。
func extract(zr *zip.Reader, prefix, dst string) error {
	var total int64
	for _, f := range zr.File {
		if !strings.HasPrefix(f.Name, prefix) {
			continue
		}
		rel := strings.TrimPrefix(f.Name, prefix)
		if rel == "" {
			continue
		}
		clean := path.Clean(rel)
		if path.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") {
			return fmt.Errorf("zip 条目路径不安全: %q", f.Name)
		}
		target := filepath.Join(dst, filepath.FromSlash(clean))
		if f.FileInfo().IsDir() || strings.HasSuffix(f.Name, "/") {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if f.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("zip 里不允许符号链接: %q", f.Name)
		}
		total += int64(f.UncompressedSize64)
		if total > maxAppBytes {
			return errors.New("应用包解压后超过 256MB")
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if f.Mode()&0o111 != 0 || strings.HasPrefix(clean, "bin/") {
			mode = 0o755
		}
		if err := writeEntry(f, target, mode); err != nil {
			return err
		}
	}
	return nil
}

func writeEntry(f *zip.File, target string, mode os.FileMode) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, io.LimitReader(rc, maxAppBytes))
	return err
}

// staleModuleNames 猜这个应用过去可能用过的模块目录名。
// 应用从 module 改回 entry 之后, 新清单里不再有 URI, 没法直接知道该删哪个目录;
// 这里按两条线索找: ① 包里残留的 qml/<Name>/ ② 应用 ID 的首字母大写形式
// (约定俗成: id=plan 的模块 URI 就是 Plan)。多删一个不存在的目录没有副作用。
func staleModuleNames(appDir, id string) []string {
	var out []string
	if ents, err := os.ReadDir(filepath.Join(appDir, "qml")); err == nil {
		for _, e := range ents {
			if e.IsDir() {
				out = append(out, e.Name())
			}
		}
	}
	if id != "" {
		out = append(out, strings.ToUpper(id[:1])+id[1:])
	}
	return out
}

// StickerDir 贴纸目录 (和应用目录同级): 空间自带的包是真目录, 商店装的包是指向应用目录的链接。
func (r *Registry) StickerDir() string { return filepath.Join(filepath.Dir(r.UserDir), "stickers") }

// linkStickers 把贴纸包应用里的每个包挂进贴纸目录。同名的旧包是链接或商店专属包 (带 .seal) 才替换,
// 空间自带的明文包不动 —— 商店的包不该覆盖掉随空间发的那份。
func (r *Registry) linkStickers(appDir string, m *Manifest) error {
	src := filepath.Join(appDir, m.Stickers)
	ents, err := os.ReadDir(src)
	if err != nil {
		return fmt.Errorf("贴纸包里缺 %s/: %w", m.Stickers, err)
	}
	root := r.StickerDir()
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	for _, e := range ents {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		dst := filepath.Join(root, e.Name())
		if st, err := os.Lstat(dst); err == nil {
			if st.Mode()&os.ModeSymlink != 0 {
				_ = os.Remove(dst)
			} else if _, err := os.Stat(filepath.Join(dst, ".seal")); err == nil {
				_ = os.RemoveAll(dst)
			} else {
				continue
			}
		}
		if err := os.Symlink(filepath.Join(src, e.Name()), dst); err != nil {
			return err
		}
	}
	return nil
}

// unlinkStickers 删掉贴纸目录里指向 appDir 的链接 (卸载贴纸包应用时)
func (r *Registry) unlinkStickers(appDir string) {
	root := r.StickerDir()
	ents, _ := os.ReadDir(root)
	for _, e := range ents {
		p := filepath.Join(root, e.Name())
		if t, err := os.Readlink(p); err == nil && strings.HasPrefix(t, appDir+string(filepath.Separator)) {
			_ = os.Remove(p)
		}
	}
}

// HookDir 应用挂载组件的链接目录 (注入库扫的第二个目录; 第一个是空间自带的 /home/root/rmkit-cn/space/hooks)
func (r *Registry) HookDir() string { return filepath.Join(filepath.Dir(r.UserDir), "hooks") }

// linkHooks 把应用声明的挂载组件链进 hooks/<挂点>/<应用>-<文件名>。先摘掉这个应用旧的, 再按清单建。
func (r *Registry) linkHooks(appDir string, m *Manifest) error {
	if !devMode(r.UserDir) {
		if err := VerifyIntegrity(appDir, m.ID); err != nil {
			return fmt.Errorf("「%s」要挂进笔记界面, 只能从商店安装 (%v)", m.Name, err)
		}
	}
	r.unlinkHooks(appDir)
	for pt, files := range m.Hooks {
		dir := filepath.Join(r.HookDir(), pt)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		for _, f := range files {
			src := filepath.Join(appDir, f)
			if _, err := os.Stat(src); err != nil {
				return fmt.Errorf("hooks.%s 声明的 %s 不在包里", pt, f)
			}
			dst := filepath.Join(dir, m.ID+"-"+filepath.Base(f))
			_ = os.Remove(dst)
			if err := os.Symlink(src, dst); err != nil {
				return err
			}
		}
	}
	return nil
}

// unlinkHooks 摘掉 hooks/*/ 里指向 appDir 的链接
func (r *Registry) unlinkHooks(appDir string) {
	pts, _ := os.ReadDir(r.HookDir())
	for _, pt := range pts {
		dir := filepath.Join(r.HookDir(), pt.Name())
		ents, _ := os.ReadDir(dir)
		for _, e := range ents {
			p := filepath.Join(dir, e.Name())
			if t, err := os.Readlink(p); err == nil && strings.HasPrefix(t, appDir+string(filepath.Separator)) {
				_ = os.Remove(p)
			}
		}
	}
}

// downloadPkg 下载应用包 / 更新包写进 f, 带取包凭证 hdr; 超过 limit 或 sha256 对不上都算失败。
// onProgress(已下字节, 总字节) 给界面显示进度 (总字节 0 = 服务器没给 Content-Length)。
func downloadPkg(url, sha256hex string, hdr http.Header, f *os.File, limit int64, onProgress func(int64, int64)) error {
	// 连接 / 等响应头单独限时: Wi-Fi 连着却上不了外网时 15 秒就报错, 别干等到 5 分钟的总超时
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.DialContext = (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	tr.ResponseHeaderTimeout = 30 * time.Second
	client := &http.Client{Timeout: 5 * time.Minute, Transport: tr}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("下载失败: 地址无效: %w", err)
	}
	for k, vs := range hdr {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("下载失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("下载失败: HTTP %d", resp.StatusCode)
	}
	total := resp.ContentLength
	onProgress(0, total)
	h := sha256.New()
	cr := &countingReader{r: io.LimitReader(resp.Body, limit+1), on: func(n int64) { onProgress(n, total) }}
	n, err := io.Copy(io.MultiWriter(f, h), cr)
	if err != nil {
		return fmt.Errorf("下载失败: %w", err)
	}
	if n > limit {
		return fmt.Errorf("包超过 %dMB", limit>>20)
	}
	if sha256hex != "" && !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), sha256hex) {
		return errors.New("sha256 校验不通过")
	}
	return nil
}
