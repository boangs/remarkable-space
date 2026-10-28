package server

import (
	"regexp"
	"runtime"

	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/boangs/remarkable-space/internal/space"

	"github.com/boangs/remarkable-space/internal/qr"
	"github.com/boangs/remarkable-space/internal/xolib"
)

const (
	UploadPort     = 8080
	MaxUploadBytes = 200 << 20 // 200 MiB
)

// LoadWait 上传后等外壳把文件通知进文件库的时间。外壳开着时通常 1 秒内就回报;
// 等不到也不算失败 —— 文件已在磁盘上, 下次打开空间会补上。(变量: 测试里调短)
var LoadWait = 8 * time.Second

var (
	allowedFontExts   = map[string]struct{}{".ttf": {}, ".otf": {}}
	allowedScreenExts = map[string]struct{}{".png": {}}
	// 音乐: mp3 是主力 (播放器是 mpg123), 另外几种放进来是因为手机分享出来的文件常是它们,
	// 拦在门外用户只会困惑; 播不了的曲库里会标出来, 比传不进去好。
	allowedMusicExts = map[string]struct{}{".mp3": {}, ".m4a": {}, ".flac": {}, ".wav": {}, ".ogg": {}}
	// 首页插图: jpg 为主 (照片与水彩都压得住), png 留给带透明的
	allowedHeroExts = map[string]struct{}{".jpg": {}, ".jpeg": {}, ".png": {}}
	allowedDocExts  = map[string]struct{}{".pdf": {}, ".epub": {}, ".rmdoc": {}}
)

// Config 是构造服务器需要的所有可配项.
type Config struct {
	StaticDir      string // 静态资源目录 (qr.html)
	FontsDir       string // 字体存放目录
	ScreensDir     string // 锁屏图存放目录
	MusicDir       string // 音乐存放目录 (音乐应用扫这里)
	HeroDir        string // 「空间」首页插图目录 (用户自己传的)
	XochitlDir     string // xochitl 数据目录 (文档热导入写到这里), 空 = 设备默认路径
	FontsActiveDir string // 字体激活符号链接目录 (~/.local/share/fonts)
	XochitlConf    string // xochitl 配置路径 (~/.config/remarkable/xochitl.conf)
	// 「空间 (SPACE)」启动台: 内置应用目录 (安装器管理) / 用户应用目录 (绿色安装) / 应用数据目录
	SpaceDir      string
	SpaceUserDir  string
	SpaceDataDir  string
	SpaceModDir   string // 编译发布的应用的 QML 模块目录 (要与 xochitl 的 QML_IMPORT_PATH 一致)
	SpaceBaseURL  string // 告诉应用后台 upload-server 在哪
	SpaceStore    string // 应用商店索引 URL (空 = 不提供)
	SpaceStoreAPI string // 商店握手接口 (空 = 不握手, 直接取上面那份静态索引)
}

type Server struct {
	cfg   Config
	space *space.Handler
	lib   *xolib.Library // 往 xochitl 文件库里放东西 (热导入), 只在「空间」开着时可用
	lan   *listeners     // 扫码上传的对外端口 (ServeExternal 启动后才有)
}

func New(cfg Config) (*Server, error) {
	// 空 = 这项功能不启用 (比如没配插图目录就没有首页插图库)。拿空串去 MkdirAll
	// 会直接失败, 让整个后端因为一个可选目录起不来 —— 不值得。
	for _, d := range []string{cfg.FontsDir, cfg.ScreensDir, cfg.MusicDir, cfg.HeroDir} {
		if d == "" {
			continue
		}
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, fmt.Errorf("mkdir %s: %w", d, err)
		}
	}
	s := &Server{cfg: cfg}
	xdir := cfg.XochitlDir
	if xdir == "" {
		xdir = xolib.DefaultDir
	}
	pending := ""
	if cfg.SpaceDataDir != "" {
		pending = filepath.Join(cfg.SpaceDataDir, "shell", "library-pending.json")
	}
	s.lib = xolib.New(xdir, pending)
	if cfg.SpaceDir != "" {
		s.space = space.New(filepath.Join(cfg.SpaceDir, "apps"), cfg.SpaceUserDir, cfg.SpaceDataDir,
			cfg.SpaceModDir, deviceArch(), cfg.SpaceBaseURL)
		s.space.StoreURL = cfg.SpaceStore
		s.space.StoreAPI = cfg.SpaceStoreAPI
		// 设备凭据和应用目录放在一起 (…/space/device.json), 跟 .dev 标记同级
		if cfg.SpaceUserDir != "" {
			s.space.SetupAuth(filepath.Join(filepath.Dir(cfg.SpaceUserDir), "device.json"))
		}
	}
	return s, nil
}

// Space 暴露启动台后端 (main 用它做自启; 没配置时返回空实现, 调用安全)。
func (s *Server) Space() *space.Handler {
	if s.space == nil {
		return space.New("", "", "", "", deviceArch(), "")
	}
	return s.space
}

// deviceArch 把 Go 的架构名映射成安装器/清单用的名字。
func deviceArch() string {
	switch runtime.GOARCH {
	case "arm64":
		return "aarch64"
	case "arm":
		return "armv7"
	}
	return runtime.GOARCH
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", s.index)
	mux.HandleFunc("GET /qr", s.qrPage)
	mux.HandleFunc("GET /qr-info", s.qrInfo)
	mux.HandleFunc("GET /qr.png", s.qrPNG)

	// 扫码上传只在「空间」开着时可用: 文档热导入得由空间外壳通知 xochitl, 空间关了它就做不到;
	// 其余几类一并关上, 免得手机上一半能传一半不能, 让人摸不着头脑。
	mux.HandleFunc("GET /fonts", s.listFonts)
	mux.HandleFunc("POST /fonts", s.needSpace(s.uploadFont))
	mux.HandleFunc("DELETE /fonts/{name}", s.needSpace(s.deleteFont))
	mux.HandleFunc("GET /fonts/active", s.activeFont)
	mux.HandleFunc("POST /fonts/{name}/apply", s.applyFont)

	mux.HandleFunc("GET /screens", s.listScreens)
	mux.HandleFunc("POST /screens", s.needSpace(s.uploadScreen))
	mux.HandleFunc("GET /hero", s.listHero)
	mux.HandleFunc("POST /hero", s.needSpace(s.uploadHero))
	mux.HandleFunc("GET /hero/{name}", s.previewHero)
	mux.HandleFunc("DELETE /hero/{name}", s.needSpace(s.deleteHero))
	mux.HandleFunc("GET /music", s.listMusic)
	mux.HandleFunc("POST /music", s.needSpace(s.uploadMusic))
	mux.HandleFunc("DELETE /music/{name}", s.needSpace(s.deleteMusic))
	mux.HandleFunc("DELETE /screens/{name}", s.needSpace(s.deleteScreen))
	mux.HandleFunc("GET /screens/active", s.activeScreen)
	mux.HandleFunc("POST /screens/{name}/apply", s.applyScreen)
	mux.HandleFunc("GET /screens/{name}/preview", s.previewScreen)

	mux.HandleFunc("POST /documents", s.needSpace(s.uploadDocument))
	s.lib.Mount(mux) // 空间外壳取「通知文件库」的活: /space/library/*

	mux.HandleFunc("GET /screenshot", s.screenshotPNG) // 排版核对用: 当前屏幕 PNG

	// 「空间」启动台: 应用注册表 / 绿色安装 / 应用后台托管 (internal/space, 与具体应用无关)
	if s.space != nil {
		s.space.UploadGate = s.lib.Queue.Active
		s.space.Mount(mux)
	}

	// AI 已拆成商店应用 (后台 127.0.0.1:9190); 扫码页 (手机填 Key) 仍调这里, 转发过去
	mux.HandleFunc("GET /ai-config", s.aiConfigProxy)
	mux.HandleFunc("PUT /ai-config", s.aiConfigProxy)

	mux.HandleFunc("POST /apply", s.applyAll)

	mux.HandleFunc("POST /apps/koreader/launch", s.launchKoreader)
	mux.HandleFunc("POST /apps/weread/launch", s.launchWeRead)
	// 蓝牙耳机配对 (高级面板"蓝牙"页)
	mux.HandleFunc("GET /bt/status", s.btStatus)
	mux.HandleFunc("POST /bt/scan", s.btScan)
	mux.HandleFunc("POST /bt/pair", s.btPair)
	mux.HandleFunc("POST /bt/connect", s.btConnect)
	mux.HandleFunc("POST /bt/disconnect", s.btDisconnect)
	mux.HandleFunc("POST /bt/remove", s.btRemove)
	mux.HandleFunc("POST /apps/android/launch", s.launchAndroid)
	mux.HandleFunc("GET /apps/caps", s.capabilities)

	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.Dir(s.cfg.StaticDir))))

	return accessLog(mux)
}

func accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s (%s)", r.RemoteAddr, r.Method, r.URL.Path, time.Since(start))
	})
}

// ---- 工具 ----

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func httpError(w http.ResponseWriter, code int, detail string) {
	writeJSON(w, code, map[string]string{"detail": detail})
}

// safeJoin 防 path traversal: 只取 name 的 base, 校验拼接后仍在 base 下.
func safeJoin(base, name string) (string, error) {
	clean := filepath.Base(name)
	if clean == "." || clean == "/" || clean == "" {
		return "", errors.New("非法文件名")
	}
	full := filepath.Join(base, clean)
	rel, err := filepath.Rel(base, full)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", errors.New("非法文件名")
	}
	return full, nil
}

func listFiles(dir string, allowed map[string]struct{}) []map[string]any {
	out := []map[string]any{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	type item struct {
		name string
		size int64
	}
	items := []item{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if _, ok := allowed[ext]; !ok {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		items = append(items, item{e.Name(), info.Size()})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].name < items[j].name })
	for _, it := range items {
		out = append(out, map[string]any{"name": it.name, "size": it.size})
	}
	return out
}

func saveUpload(w http.ResponseWriter, r *http.Request, dir string, allowed map[string]struct{}, allowedDesc string) (name string, size int64, ok bool) {
	r.Body = http.MaxBytesReader(w, r.Body, MaxUploadBytes)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		httpError(w, http.StatusBadRequest, "上传解析失败: "+err.Error())
		return "", 0, false
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		httpError(w, http.StatusBadRequest, "缺少 file 字段")
		return "", 0, false
	}
	defer file.Close()

	if header.Filename == "" {
		httpError(w, http.StatusBadRequest, "文件名不能为空")
		return "", 0, false
	}
	safe := filepath.Base(header.Filename)
	ext := strings.ToLower(filepath.Ext(safe))
	if _, okExt := allowed[ext]; !okExt {
		httpError(w, http.StatusBadRequest, "仅支持 "+allowedDesc+" 文件")
		return "", 0, false
	}

	dest, err := safeJoin(dir, safe)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return "", 0, false
	}
	out, err := os.Create(dest)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "创建文件失败: "+err.Error())
		return "", 0, false
	}
	defer out.Close()
	n, err := io.Copy(out, file)
	if err != nil {
		os.Remove(dest)
		httpError(w, http.StatusInternalServerError, "写入文件失败: "+err.Error())
		return "", 0, false
	}
	return safe, n, true
}

// ---- 静态页面 ----

// index: 旧的 index.html (只有字体/壁纸) 已并入 qr.html, 根路径直接跳过去。
func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	http.Redirect(w, r, "/qr", http.StatusFound)
}

func (s *Server) qrPage(w http.ResponseWriter, r *http.Request) {
	if !s.lib.Queue.Active() {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprint(w, spaceClosedPage)
		return
	}
	http.ServeFile(w, r, filepath.Join(s.cfg.StaticDir, "qr.html"))
}

// needSpace: 「空间」没开着就拒绝。手机扫码页上看到的是这句提示。
func (s *Server) needSpace(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.lib.Queue.Active() {
			httpError(w, http.StatusServiceUnavailable, spaceClosedText)
			return
		}
		h(w, r)
	}
}

const spaceClosedText = "请先在 reMarkable 上打开「空间」, 再上传"

const spaceClosedPage = `<!doctype html><html lang="zh"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1"><title>上传到 reMarkable</title>
<style>body{font-family:-apple-system,system-ui,sans-serif;margin:0;padding:48px 24px;color:#111;background:#fff}
h1{font-size:22px;margin:0 0 12px}p{font-size:16px;line-height:1.6;color:#444;margin:0 0 20px}
a{display:inline-block;padding:12px 20px;border:2px solid #111;border-radius:10px;color:#111;text-decoration:none}</style>
</head><body><h1>「空间」没有打开</h1>
<p>扫码上传只在 reMarkable 上打开「空间」时可用。请在设备侧栏点「空间」, 再刷新这个页面。</p>
<a href="/qr">刷新</a></body></html>`

// ---- QR ----

// qrFocus: 内置的 doc/font/screen/space/ai, 或应用登记的 <应用>.<上传项>; 其他值忽略.
var qrAppFocusRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}\.[a-z0-9][a-z0-9_-]{0,31}$`)

func qrFocus(r *http.Request) string {
	f := r.URL.Query().Get("focus")
	switch f {
	case "doc", "font", "screen", "space", "ai":
		return f
	}
	if qrAppFocusRe.MatchString(f) { // 应用登记的上传项: <应用 id>.<上传项 id>
		return f
	}
	return ""
}

func qrTargetURL(ip, focus string) string {
	url := fmt.Sprintf("http://%s:%d/qr", ip, UploadPort)
	if focus != "" {
		url += "?focus=" + focus
	}
	return url
}

func (s *Server) qrInfo(w http.ResponseWriter, r *http.Request) {
	ip, err := qr.DetectLanIP()
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"available": false,
			"reason":    err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"available": true,
		"ip":        ip,
		"port":      UploadPort,
		"url":       qrTargetURL(ip, qrFocus(r)),
	})
}

func (s *Server) qrPNG(w http.ResponseWriter, r *http.Request) {
	ip, err := qr.DetectLanIP()
	if err != nil {
		httpError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	png, err := qr.PNG(qrTargetURL(ip, qrFocus(r)))
	if err != nil {
		httpError(w, http.StatusInternalServerError, "生成 QR 失败: "+err.Error())
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Write(png)
}

// ---- 字体 ----

func (s *Server) listFonts(w http.ResponseWriter, r *http.Request) {
	items := listFiles(s.cfg.FontsDir, allowedFontExts)
	active := s.detectActiveFont()
	for _, it := range items {
		it["active"] = it["name"] == active
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) uploadFont(w http.ResponseWriter, r *http.Request) {
	name, size, ok := saveUpload(w, r, s.cfg.FontsDir, allowedFontExts, ".ttf / .otf")
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"name": name, "size": size})
}

// ---- 音乐 ----
//
// 音乐本身是第三方应用, 外壳按说不该认识它。这里仍放通用上传页, 是因为扫码上传是
// 手机侧唯一的入口 —— 不然传首歌还得开电脑 scp。落点就是音乐应用扫的那个目录。
// 等上传页支持"按应用清单发现可上传的应用"之后, 这段可以收掉。

func (s *Server) listMusic(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, listFiles(s.cfg.MusicDir, allowedMusicExts))
}

func (s *Server) uploadMusic(w http.ResponseWriter, r *http.Request) {
	name, size, ok := saveUpload(w, r, s.cfg.MusicDir, allowedMusicExts, ".mp3 / .m4a / .flac / .wav / .ogg")
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"name": name, "size": size})
}

func (s *Server) deleteMusic(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	target, err := safeJoin(s.cfg.MusicDir, name)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := os.Remove(target); err != nil {
		if os.IsNotExist(err) {
			httpError(w, http.StatusNotFound, "文件不存在")
			return
		}
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": filepath.Base(name)})
}

// ---- 首页插图 ----
//
// 空间首页顶部那张大卡: 装了天气就由天气的小组件整卡接管, 没装就垫一幅画。
// 这里管的是"没装天气时垫哪一幅" —— 内置一幅山水, 用户也可以自己传, 像换休眠壁纸那样。
// 存在空间自己的目录下, 不和锁屏图混: 两者尺寸与用途都不一样 (这个是 2:1 横幅)。

func (s *Server) listHero(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, listFiles(s.cfg.HeroDir, allowedHeroExts))
}

func (s *Server) uploadHero(w http.ResponseWriter, r *http.Request) {
	name, size, ok := saveUpload(w, r, s.cfg.HeroDir, allowedHeroExts, ".jpg / .png")
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"name": name, "size": size})
}

// previewHero 把插图原样送出去 —— 列表接口只给文件名, 界面光有名字选不出哪张画。
func (s *Server) previewHero(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	target, err := safeJoin(s.cfg.HeroDir, name)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := os.Stat(target); err != nil {
		httpError(w, http.StatusNotFound, "插图不存在")
		return
	}
	w.Header().Set("Cache-Control", "max-age=60")
	http.ServeFile(w, r, target)
}

func (s *Server) deleteHero(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	target, err := safeJoin(s.cfg.HeroDir, name)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := os.Remove(target); err != nil {
		if os.IsNotExist(err) {
			httpError(w, http.StatusNotFound, "文件不存在")
			return
		}
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": filepath.Base(name)})
}

func (s *Server) deleteFont(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	target, err := safeJoin(s.cfg.FontsDir, name)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := os.Remove(target); err != nil {
		if os.IsNotExist(err) {
			httpError(w, http.StatusNotFound, "文件不存在")
			return
		}
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": filepath.Base(name)})
}

// ---- 锁屏图 ----

func (s *Server) listScreens(w http.ResponseWriter, r *http.Request) {
	items := listFiles(s.cfg.ScreensDir, allowedScreenExts)
	active := s.detectActiveScreen()
	for _, it := range items {
		it["active"] = it["name"] == active
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) uploadScreen(w http.ResponseWriter, r *http.Request) {
	name, size, ok := saveUpload(w, r, s.cfg.ScreensDir, allowedScreenExts, ".png")
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"name": name, "size": size})
}

func (s *Server) deleteScreen(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	target, err := safeJoin(s.cfg.ScreensDir, name)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := os.Remove(target); err != nil {
		if os.IsNotExist(err) {
			httpError(w, http.StatusNotFound, "文件不存在")
			return
		}
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": filepath.Base(name)})
}

// ---- 文档 (热导入: xolib 写进 xochitl 数据目录, 空间外壳通知文件库加载) ----

func randomToken() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func (s *Server) uploadDocument(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, MaxUploadBytes)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		httpError(w, http.StatusBadRequest, "上传解析失败: "+err.Error())
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		httpError(w, http.StatusBadRequest, "缺少 file 字段")
		return
	}
	defer file.Close()

	if header.Filename == "" {
		httpError(w, http.StatusBadRequest, "文件名不能为空")
		return
	}
	safe := filepath.Base(header.Filename)
	ext := strings.ToLower(filepath.Ext(safe))
	if _, ok := allowedDocExts[ext]; !ok {
		httpError(w, http.StatusBadRequest, "仅支持 .pdf / .epub / .rmdoc 文件")
		return
	}

	// 文件名原样当显示名 (去扩展名)。不再有 librarian 那套「逗号会被当分隔符」的坑:
	// 显示名只进 .metadata 的 JSON, 磁盘上的文件名是新生成的 uuid。
	e, err := s.lib.ImportDocument(file, strings.TrimSuffix(safe, ext), ext, "")
	if err != nil {
		httpError(w, http.StatusInternalServerError, "写入文件库失败: "+err.Error())
		return
	}
	res, ok := s.lib.Queue.Wait(e.ID, LoadWait)
	loaded := ok && res.OK
	if !loaded {
		fmt.Fprintf(os.Stderr, "文档 %s 已写入, 文件库尚未加载 (回报=%v %+v), 下次打开空间补上\n", e.ID, ok, res)
	}
	size := header.Size
	writeJSON(w, http.StatusOK, map[string]any{
		"name":   safe,
		"size":   size,
		"uuid":   e.ID,
		"loaded": loaded,
	})
}

func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}

// screenshotPNG 把当前屏幕抓成 PNG (只在本机/USB 网段可达的 8080 上, 用来核对面板排版)。
func (s *Server) screenshotPNG(w http.ResponseWriter, r *http.Request) {
	img, err := rmppScreenshot()
	if err != nil {
		// 和 AI 识字一样退回 DRM 接口 (重启后帧缓冲不一定紧挨 card0, 第一条路会失败)
		var err2 error
		if img, err2 = drmScreenshot(); err2 != nil {
			httpError(w, http.StatusInternalServerError, err.Error()+" | drm: "+err2.Error())
			return
		}
	}
	w.Header().Set("Content-Type", "image/png")
	_ = png.Encode(w, img)
}

// aiConfigProxy 把扫码页的 /ai-config 转给「AI」应用的后台。没装 AI (后台连不上) 时说清楚去哪装。
func (s *Server) aiConfigProxy(w http.ResponseWriter, r *http.Request) {
	req, err := http.NewRequestWithContext(r.Context(), r.Method, aiAppURL+"/ai-config", r.Body)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		httpError(w, http.StatusServiceUnavailable, "请先在商店安装「AI」")
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, io.LimitReader(resp.Body, 1<<20))
}

// aiAppURL 「AI」应用后台 (它的 manifest.service.port)
const aiAppURL = "http://127.0.0.1:9190"
