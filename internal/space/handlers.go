package space

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Handler 把注册表 + 进程托管暴露为 HTTP 接口, 全部挂在 /space/ 下:
//
//	GET    /space/apps                      应用列表 (外壳首页 / 手机管理页共用)
//	POST   /space/apps                      上传 zip 安装 (multipart 字段 file, 或 body 直接是 zip)
//	POST   /space/apps/install-url          {"url": ..., "sha256": ...} 从网址安装 (应用商店用; 后台跑, 立即返回)
//	GET    /space/apps/install-progress     查上面那次安装的进度
//
// 这里没有、也不该有任何具体应用的路由: 天气的地图合成一度挂在这儿, 那意味着外壳认识
// "weather" 这个应用 —— 它一旦能装能删, 删掉之后后端还留着四个专用路由。现在天气和音乐
// 一样自带后端 (weather-server), 空间只按清单把它拉起来, 不关心它干什么。
//
//	DELETE /space/apps/{id}                 卸载 (仅用户目录)
//	GET    /space/apps/{id}/service         后台状态
//	POST   /space/apps/{id}/service/start   拉起后台 (就绪后返回)
//	POST   /space/apps/{id}/service/stop    收掉后台
//	GET    /space/apps/{id}/data/{key}      读应用数据 (JSON, 存在 dataDir/<key>.json; 纯 QML 应用的持久化)
//	PUT    /space/apps/{id}/data/{key}      写应用数据 (body 是 JSON, ≤1MB)
//	GET    /space/recent                    最近在读的文档 (书名/页码/进度, 取自 xochitl 元数据)
type Handler struct {
	Reg *Registry
	Sup *Supervisor
	// 应用商店索引地址 (JSON, 见 space/README.md "应用商店"); 空 = 不提供商店
	StoreURL string
	// 取索引时附带的客户端标识, 放在 X-Space-Key 头里。官方商店的 nginx 认这个头,
	// 不带就 404 —— 用浏览器直接打开索引地址看不到东西。空 = 不带这个头,
	// 自建商店 (和本地开发) 不受影响。值在编译期用 -ldflags -X 注进 storeKey, 不进仓库。
	StoreKey string
	// 官方商店的握手接口 (形如 https://boangs.com/rmkit-space/api), 见 storeauth.go。
	// 配了就走三步握手, 索引按这台设备定制; 空 = 退回直接取 StoreURL 那份静态索引,
	// 自建商店只要摆一个 JSON 文件就能用, 不必实现握手。
	StoreAPI string

	auth *StoreAuth

	storeMu   sync.Mutex
	storeAt   time.Time
	storeBody []byte
}

// SetupAuth 按当前配置装上握手客户端。StoreAPI 为空时什么也不做 —— 那条路上
// 索引是一份静态 JSON, 没有握手对端。
func (h *Handler) SetupAuth(credPath string) {
	if h.StoreAPI == "" {
		return
	}
	h.auth = newStoreAuth(h.StoreAPI, credPath, h.StoreKey, Identity())
	// 装"本体"时可能要下别人 CDN 上的东西 —— 票只发给我们自己的商店
	storeHosts = []string{strings.TrimSuffix(h.StoreAPI, "/")}
}

// 商店索引缓存时长: 设备 Wi-Fi 常年休眠、DNS 不稳, 拉一次能用就多用一会儿
const storeCacheTTL = 10 * time.Minute

// New 组装默认的注册表 + 托管器。
// New 建后端。moduleDir 放编译发布的应用的 QML 模块, 必须与 xochitl 的 QML_IMPORT_PATH
// 一致 —— 那个环境变量是启动时定下的, 装应用时改不了。
func New(builtinDir, userDir, dataDir, moduleDir, arch, baseURL string) *Handler {
	for _, d := range []string{userDir, dataDir, moduleDir} {
		if d != "" {
			_ = os.MkdirAll(d, 0o755)
		}
	}
	return &Handler{
		Reg: &Registry{BuiltinDir: builtinDir, UserDir: userDir, DataDir: dataDir,
			ModuleDir: moduleDir, Arch: arch},
		Sup:      NewSupervisor(baseURL),
		StoreKey: defaultStoreKey(),
	}
}

// storeKey 是官方商店的客户端标识, 由编译期注入 (make STORE_KEY=...), 仓库里恒为空字符串 ——
// 它不是秘密学意义上的密钥, 只是一道"别用浏览器直接翻索引"的门: 二进制在用户手里,
// strings 或抓包都能取到。真要防白嫖得在下载环节做授权, 不在这儿。
var storeKey string

// 环境变量优先, 这样换密钥不必重新发一版二进制 —— 在 xochitl 的 drop-in 里加一行就行。
func defaultStoreKey() string {
	if v := os.Getenv("SPACE_STORE_KEY"); v != "" {
		return v
	}
	return storeKey
}

// Mount 注册路由。
func (h *Handler) Mount(mux *http.ServeMux) {
	mux.HandleFunc("GET /space/apps", h.list)
	mux.HandleFunc("GET /space/timezones", h.timezones)
	mux.HandleFunc("POST /space/apps", h.installUpload)
	mux.HandleFunc("POST /space/apps/install-url", h.installURL)
	mux.HandleFunc("GET /space/apps/install-progress", h.installProgressHandler)
	mux.HandleFunc("DELETE /space/apps/{id}", h.uninstall)
	mux.HandleFunc("GET /space/apps/{id}/service", h.serviceStatus)
	mux.HandleFunc("POST /space/apps/{id}/service/start", h.serviceStart)
	mux.HandleFunc("POST /space/apps/{id}/service/stop", h.serviceStop)
	mux.HandleFunc("GET /space/apps/{id}/data/{key}", h.dataGet)
	mux.HandleFunc("PUT /space/apps/{id}/data/{key}", h.dataPut)
	mux.HandleFunc("GET /space/store", h.store)
	mux.HandleFunc("GET /space/device", h.device)
	mux.HandleFunc("GET /space/recent", h.recent)
	mux.HandleFunc("GET /space/hooks/disabled", h.hooksDisabled)
	mux.HandleFunc("GET /space/update", h.updateStatus)
	mux.HandleFunc("POST /space/update", h.updateStart)
	mux.HandleFunc("GET /space/themes", h.themeList)
	mux.HandleFunc("GET /space/theme", h.themeActive)
	mux.HandleFunc("PUT /space/theme", h.themeSet)
}

// hooksDisabled 注入库熔断停用的挂载组件 (挂上后 xochitl 连续两次很快退出)。外壳拿它提示用户:
// 哪个应用的什么被停用了, 更新或重装这个应用就会再试 (文件变了 = 新的熔断记录)。
func (h *Handler) hooksDisabled(w http.ResponseWriter, r *http.Request) {
	type item struct {
		File string `json:"file"`
		App  string `json:"app,omitempty"`  // 应用 id; 空 = 空间自带的
		Name string `json:"name,omitempty"` // 应用显示名
	}
	out := []item{}
	stateDir := filepath.Join(filepath.Dir(h.Reg.UserDir), "hooks-state")
	ents, _ := os.ReadDir(stateDir)
	for _, e := range ents {
		if !strings.HasPrefix(e.Name(), "disabled-") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(stateDir, e.Name()))
		if err != nil {
			continue
		}
		file, _, _ := strings.Cut(string(b), "\n")
		it := item{File: file}
		// 应用的挂载组件是 hooks/<挂点>/<应用>-<文件> 的链接, 跟过去就是应用目录
		if t, err := os.Readlink(file); err == nil && strings.HasPrefix(t, h.Reg.UserDir+string(filepath.Separator)) {
			id, _, _ := strings.Cut(strings.TrimPrefix(t, h.Reg.UserDir+string(filepath.Separator)), string(filepath.Separator))
			it.App = id
			if a, ok := h.Reg.Get(id); ok {
				it.Name = a.Name
			}
		}
		out = append(out, it)
	}
	writeJSON(w, http.StatusOK, map[string]any{"disabled": out})
}

var dataKeyRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)

// dataPath 把 (应用 id, key) 映射到 dataDir/<id>/<key>.json; id/key 都严格限制字符, 不会出目录。
func (h *Handler) dataPath(id, key string) (string, bool) {
	if !idRe.MatchString(id) || !dataKeyRe.MatchString(key) {
		return "", false
	}
	return filepath.Join(h.Reg.DataDir, id, key+".json"), true
}

func (h *Handler) dataGet(w http.ResponseWriter, r *http.Request) {
	p, ok := h.dataPath(r.PathValue("id"), r.PathValue("key"))
	if !ok {
		fail(w, http.StatusBadRequest, "id 或 key 不合法")
		return
	}
	b, err := os.ReadFile(p)
	if err != nil {
		fail(w, http.StatusNotFound, "没有这份数据")
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = w.Write(b)
}

func (h *Handler) dataPut(w http.ResponseWriter, r *http.Request) {
	p, ok := h.dataPath(r.PathValue("id"), r.PathValue("key"))
	if !ok {
		fail(w, http.StatusBadRequest, "id 或 key 不合法")
		return
	}
	b, err := io.ReadAll(io.LimitReader(r.Body, 1<<20+1))
	if err != nil || len(b) > 1<<20 || !json.Valid(b) {
		fail(w, http.StatusBadRequest, "body 必须是 ≤1MB 的合法 JSON")
		return
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := os.WriteFile(p, b, 0o644); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// store 转发应用商店索引 (带缓存)。索引格式:
//
//	{"apps": [{"id","name","version","description","category","arch":[...],"zip":"https://...","sha256":"..."}]}
//
// 由设备端拉取而不是让面板直连: 面板里的 XHR 拿不到设备的 DNS 兜底与超时控制。
func (h *Handler) store(w http.ResponseWriter, r *http.Request) {
	if h.StoreURL == "" && h.StoreAPI == "" {
		fail(w, http.StatusNotFound, "没有配置应用商店")
		return
	}
	h.storeMu.Lock()
	defer h.storeMu.Unlock()
	refresh := r.URL.Query().Get("refresh") == "1"
	if refresh && h.auth != nil {
		// 票是自包含的, 授权列表是签票那一刻的快照。用户刚买完回来点刷新, 只换索引
		// 不换票的话还是看到"需购买", 得等票过期 (默认 5 分钟) —— 那几分钟里他只会
		// 以为没生效。手动刷新就连票一起重来。
		h.auth.Invalidate()
	}
	if h.storeBody == nil || time.Since(h.storeAt) > storeCacheTTL || refresh {
		body, err := h.fetchIndex()
		if err != nil {
			fail(w, http.StatusBadGateway, err.Error())
			return
		}
		h.storeBody, h.storeAt = body, time.Now()
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = w.Write(localizeIndex(h.storeBody, h.CurrentLang()))
}

// localizeIndex 按界面语言给商店索引里的应用名和简介定稿 (和 list 对已装应用的做法一致):
// 英文界面下有 name_en / description_en 的条目换成英文, 没填的保持原文。缓存里存的是原始
// 索引, 每次响应时再换 —— 用户切语言不用等缓存过期。解析不了就原样返回, 不能因为本地化
// 把商店弄没了。
func localizeIndex(body []byte, lang string) []byte {
	if lang == "zh-CN" {
		return body
	}
	var idx map[string]any
	if err := json.Unmarshal(body, &idx); err != nil {
		return body
	}
	apps, _ := idx["apps"].([]any)
	changed := false
	for _, it := range apps {
		a, ok := it.(map[string]any)
		if !ok {
			continue
		}
		for _, k := range []string{"name", "description"} {
			if v, _ := a[k+"_en"].(string); v != "" {
				a[k] = v
				changed = true
			}
		}
	}
	if !changed {
		return body
	}
	out, err := json.Marshal(idx)
	if err != nil {
		return body
	}
	return out
}

func (h *Handler) fetchIndex() ([]byte, error) {
	if h.auth != nil {
		return h.fetchIndexAuthed()
	}
	return h.fetchIndexStatic()
}

// 握手那条路: 索引按这台设备定制 (没授权的付费应用只有条目和购买入口, 没有下载链接)。
func (h *Handler) fetchIndexAuthed() ([]byte, error) {
	body, err := h.getIndexWithTicket()
	if err != nil && isAuthErr(err) {
		// 票可能被服务端提前作废了 (封禁、密钥轮转)。丢掉重握一次, 只试这一次。
		h.auth.Invalidate()
		body, err = h.getIndexWithTicket()
	}
	return body, err
}

func (h *Handler) getIndexWithTicket() ([]byte, error) {
	ticket, err := h.auth.Ticket()
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodGet, h.StoreAPI+"/index", nil)
	if err != nil {
		return nil, fmt.Errorf("商店地址无效: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+ticket)
	req.Header.Set("X-Space-Shell", strconv.Itoa(ShellVersion))
	if h.StoreKey != "" {
		req.Header.Set("X-Space-Key", h.StoreKey)
	}
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("连不上应用商店: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, &storeHTTPError{status: resp.StatusCode, msg: storeErrMsg(resp.StatusCode, body)}
	}
	if err != nil || !json.Valid(body) {
		return nil, fmt.Errorf("应用商店返回的索引看不懂")
	}
	return body, nil
}

// 静态那条路: 索引就是一份摆在那儿的 JSON。自建商店走这条, 不必实现握手。
func (h *Handler) fetchIndexStatic() ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, h.StoreURL, nil)
	if err != nil {
		return nil, fmt.Errorf("商店地址无效: %w", err)
	}
	if h.StoreKey != "" {
		req.Header.Set("X-Space-Key", h.StoreKey)
	}
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("连不上应用商店: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil || resp.StatusCode != http.StatusOK || !json.Valid(body) {
		if resp.StatusCode == http.StatusNotFound {
			return nil, fmt.Errorf("应用商店拒绝了这次请求 (HTTP 404): 这个版本没有商店密钥, 或密钥已经换了")
		}
		return nil, fmt.Errorf("应用商店索引无效 (HTTP %d)", resp.StatusCode)
	}
	return body, nil
}

// AutoStart 拉起所有声明了 autostart 的应用后台 (upload-server 启动时调一次)。
// afterInstall 声明了 autostart 的应用装好就拉起后台 (AI、贴纸的笔记内按钮/面板要它一直在),
// 不等下次开机。
func (h *Handler) afterInstall(a App) {
	if a.Service != nil && a.Service.AutoStart && a.Error == "" {
		if _, err := h.Sup.Start(a); err != nil {
			log.Printf("space: %s 装好后拉起后台失败: %v", a.ID, err)
		}
	}
}

func (h *Handler) AutoStart() {
	for _, a := range h.Reg.List() {
		if a.Service != nil && a.Service.AutoStart && a.Error == "" {
			if _, err := h.Sup.Start(a); err != nil {
				log.Printf("space: %s 后台自启失败: %v", a.ID, err)
			}
		}
	}
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	apps := h.Reg.List()
	lang := h.CurrentLang()
	for i := range apps {
		// Settle the app name for the UI language, so the front end gets the name to show.
		// An app with no translation keeps its original name - better than showing an id.
		apps[i].Name = apps[i].DisplayName(lang)
		if apps[i].Service != nil {
			st := h.Sup.Status(apps[i].ID)
			if st.Running {
				apps[i].ServiceURL = st.URL
			}
		}
	}
	tzName, tzOffset := h.localTimezone()
	writeJSON(w, http.StatusOK, map[string]any{
		"shell":        ShellVersion,
		"arch":         h.Reg.Arch,
		"userDir":      h.Reg.UserDir,
		"apps":         apps,
		"timezone":     tzName,
		"tzOffset":     tzOffset, // 分钟, 东八区 = 480; 设备系统时区是 UTC, 外壳用它显示本地时间
		"dev":          devMode(h.Reg.UserDir),
		"lang":         lang, // 唯一的语言结果, 外壳直接用
		"spaceVersion": h.SpaceVersion(),
		"langPref":     h.LangPref(), // 用户显式选的; 空 = 跟随系统
		"langSystem":   SystemLang(), // xochitl 正在用的界面语言; 非空时外壳跟它走, 隐藏语言设置
	})
}

// devMode: 用户应用目录旁放一个 .dev 文件, 外壳每次打开应用都绕过 QML 组件缓存 (改完 QML 不用重启 xochitl)。
// 生产环境别开: 每次打开都会多缓存一份编译单元。
func devMode(userDir string) bool {
	_, err := os.Stat(filepath.Join(filepath.Dir(userDir), ".dev"))
	return err == nil
}

func (h *Handler) installUpload(w http.ResponseWriter, r *http.Request) {
	var data []byte
	if f, _, err := r.FormFile("file"); err == nil {
		defer f.Close()
		data, err = io.ReadAll(io.LimitReader(f, maxAppBytes+1))
		if err != nil {
			fail(w, http.StatusBadRequest, err.Error())
			return
		}
	} else {
		var err error
		data, err = io.ReadAll(io.LimitReader(r.Body, maxAppBytes+1))
		if err != nil {
			fail(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if len(data) > maxAppBytes {
		fail(w, http.StatusRequestEntityTooLarge, "应用包超过 256MB")
		return
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		fail(w, http.StatusBadRequest, "不是合法 zip: "+err.Error())
		return
	}
	app, err := h.Reg.InstallZip(zr, nil, h.Sup)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	h.afterInstall(app)
	writeJSON(w, http.StatusOK, app)
}

func (h *Handler) installURL(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL    string `json:"url"`
		SHA256 string `json:"sha256"`
		ID     string `json:"id"`   // 仅用于进度显示
		Name   string `json:"name"` // 同上
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.URL == "" {
		fail(w, http.StatusBadRequest, "需要 {\"url\": ...}")
		return
	}
	// 后台装, 立刻返回 —— 下载几 MB 在设备上要十几秒, 同步等的话界面只能干等, 连
	// "在下载" 还是 "卡住了" 都分不出。前端拿 /space/apps/install-progress 轮询。
	if p := InstallProgress(); p.Phase == "downloading" || p.Phase == "installing" {
		fail(w, http.StatusConflict, "正在安装 "+p.Name+", 等它装完再来")
		return
	}
	setProgress(func(p *Progress) {
		*p = Progress{ID: req.ID, Name: req.Name, Phase: "downloading"}
	})
	go func() {
		app, err := h.Reg.InstallFromURL(req.URL, req.SHA256, h.downloadHeader(), h.Sup)
		setProgress(func(p *Progress) {
			if err != nil {
				p.Phase, p.Error = "error", err.Error()
				return
			}
			p.Phase, p.ID, p.Name = "done", app.ID, app.Name
		})
		if err == nil {
			h.afterInstall(app)
		}
	}()
	writeJSON(w, http.StatusAccepted, map[string]any{"started": true})
}

// installProgressHandler 给前端轮询用。设备上一次只装一个, 所以是全局单任务。
func (h *Handler) installProgressHandler(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, InstallProgress())
}

func (h *Handler) uninstall(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.Reg.Uninstall(id, h.Sup); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": id})
}

func (h *Handler) serviceStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.Sup.Status(r.PathValue("id")))
}

func (h *Handler) serviceStart(w http.ResponseWriter, r *http.Request) {
	app, ok := h.Reg.Get(r.PathValue("id"))
	if !ok {
		fail(w, http.StatusNotFound, "没有这个应用")
		return
	}
	st, err := h.Sup.Start(app)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (h *Handler) serviceStop(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.Sup.Stop(id); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, h.Sup.Status(id))
}

// DataDirOf 给应用私有数据目录 (供 upload-server 其它模块引用, 避免各处拼路径)。
func (h *Handler) DataDirOf(id string) string { return filepath.Join(h.Reg.DataDir, id) }

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, code int, detail string) {
	writeJSON(w, code, map[string]any{"error": detail, "detail": detail})
}

// downloadHeader 装上取应用包要带的凭证。握手模式下票可能取不到 (没网、被封),
// 那就只带粗筛头让服务端去拒 —— 在这里直接报错的话, 自建商店也跟着下不了。
func (h *Handler) downloadHeader() http.Header {
	hdr := http.Header{}
	if h.StoreKey != "" {
		hdr.Set("X-Space-Key", h.StoreKey)
	}
	if h.auth != nil {
		if tk, err := h.auth.Ticket(); err == nil {
			hdr.Set("Authorization", "Bearer "+tk)
		} else {
			logStore("取应用包时没拿到票: %v", err)
		}
	}
	return hdr
}

// device 把这台设备的身份给界面。买应用时用户要把设备码抄给我们, 所以得有地方看。
// 只给设备码, 不给机器标识 —— 界面上用不着, 少一处可能被截图发出去的东西。
func (h *Handler) device(w http.ResponseWriter, r *http.Request) {
	id := Identity()
	writeJSON(w, http.StatusOK, map[string]any{
		"code": id.Code,
		// 读不到机器标识的设备连不了商店, 界面上得说清楚, 不能只显示一个空白
		"ok": id.Code != "",
	})
}
