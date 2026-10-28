package space

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"regexp"
	"strings"
	"time"
)

// Upload 应用在手机扫码上传页 (/qr) 登记的一个标签页: 收什么文件、交给自己后台哪个接口。
//
//	"uploads": [{"id": "anki", "label": "闪卡", "label_en": "Flashcards",
//	             "accept": [".apkg", ".txt", ".csv"], "path": "/import", "list": "/decks",
//	             "hint": "…", "hint_en": "…"}]
//
// 空间不认识具体应用: 上传页按已安装应用的登记自动出标签页, 文件原样 (multipart, 字段 file)
// 转给应用后台的 path。list 可选: GET list 返回 [{name, label, size}] 作为页面上的文件列表,
// DELETE list/{name} 删一项。应用必须带后台 (manifest.service)。
type Upload struct {
	ID       string   `json:"id"`
	Label    string   `json:"label"`
	LabelEn  string   `json:"label_en,omitempty"`
	Accept   []string `json:"accept"`
	Path     string   `json:"path"`
	List     string   `json:"list,omitempty"`
	Hint     string   `json:"hint,omitempty"`
	HintEn   string   `json:"hint_en,omitempty"`
	Multiple bool     `json:"multiple,omitempty"`
}

var (
	uploadIDRe  = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)
	uploadExtRe = regexp.MustCompile(`^\.[a-z0-9]{1,10}$`)
	uploadPthRe = regexp.MustCompile(`^/[A-Za-z0-9/_-]{0,63}$`)
)

func (m *Manifest) validateUploads() error {
	if len(m.Uploads) == 0 {
		return nil
	}
	if m.Service == nil {
		return errors.New("uploads 要交给应用后台处理, 必须同时声明 service")
	}
	seen := map[string]bool{}
	for _, u := range m.Uploads {
		if !uploadIDRe.MatchString(u.ID) || seen[u.ID] {
			return fmt.Errorf("uploads 里的 id %q 不合法或重复", u.ID)
		}
		seen[u.ID] = true
		if strings.TrimSpace(u.Label) == "" {
			return fmt.Errorf("uploads.%s 缺 label", u.ID)
		}
		if len(u.Accept) == 0 {
			return fmt.Errorf("uploads.%s 缺 accept (扩展名列表, 如 .apkg)", u.ID)
		}
		for _, e := range u.Accept {
			if !uploadExtRe.MatchString(e) {
				return fmt.Errorf("uploads.%s 的扩展名 %q 不合法 (小写, 带点)", u.ID, e)
			}
		}
		for _, p := range []string{u.Path, u.List} {
			if p != "" && (!uploadPthRe.MatchString(p) || strings.Contains(p, "..")) {
				return fmt.Errorf("uploads.%s 的路径 %q 不合法", u.ID, p)
			}
		}
		if u.Path == "" {
			return fmt.Errorf("uploads.%s 缺 path", u.ID)
		}
	}
	return nil
}

// UploadSlot 上传页拿到的一项 (哪个应用的哪个标签页)
type UploadSlot struct {
	App       string `json:"app"`
	AppName   string `json:"app_name"`
	AppNameEn string `json:"app_name_en,omitempty"`
	Upload
}

// uploadSlots 已装、本机能跑的应用登记的上传标签页
func (h *Handler) uploadSlots() []UploadSlot {
	out := []UploadSlot{}
	for _, a := range h.Reg.List() {
		if a.Error != "" || a.Service == nil {
			continue
		}
		for _, u := range a.Uploads {
			out = append(out, UploadSlot{App: a.ID, AppName: a.Name, AppNameEn: a.NameEn, Upload: u})
		}
	}
	return out
}

func (h *Handler) findUpload(appID, slot string) (App, Upload, bool) {
	a, ok := h.Reg.Get(appID)
	if !ok || a.Error != "" || a.Service == nil {
		return App{}, Upload{}, false
	}
	for _, u := range a.Uploads {
		if u.ID == slot {
			return a, u, true
		}
	}
	return App{}, Upload{}, false
}

// 一次上传最大 (Anki 卡包带图能有几十 MB)
const maxAppUpload = 300 << 20

// uploadProxy 把扫码页的请求转给应用后台: POST → path, GET → list, DELETE …/{name} → list/{name}
func (h *Handler) uploadProxy(w http.ResponseWriter, r *http.Request) {
	if h.UploadGate != nil && !h.UploadGate() {
		fail(w, http.StatusServiceUnavailable, "请先在 reMarkable 上打开「空间」, 再上传")
		return
	}
	app, u, ok := h.findUpload(r.PathValue("id"), r.PathValue("slot"))
	if !ok {
		fail(w, http.StatusNotFound, "没有这个上传项 (应用没装或不收文件)")
		return
	}
	target := u.Path
	switch r.Method {
	case http.MethodPost:
	case http.MethodGet, http.MethodDelete:
		if u.List == "" {
			fail(w, http.StatusNotFound, "这个上传项没有文件列表")
			return
		}
		target = u.List
		if r.Method == http.MethodDelete {
			name := r.PathValue("name")
			if name == "" || strings.ContainsAny(name, "/\\") || name == "." || name == ".." {
				fail(w, http.StatusBadRequest, "名字不合法")
				return
			}
			target = path.Join(u.List, name)
		}
	}
	st, err := h.Sup.Start(app)
	if err != nil {
		fail(w, http.StatusBadGateway, "应用后台没起来: "+err.Error())
		return
	}
	base := st.URL
	if base == "" {
		base = fmt.Sprintf("http://127.0.0.1:%d", app.Service.Port)
	}
	var body io.Reader
	if r.Method == http.MethodPost {
		body = http.MaxBytesReader(w, r.Body, maxAppUpload)
	}
	req, err := http.NewRequestWithContext(r.Context(), r.Method, strings.TrimSuffix(base, "/")+target, body)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	if ct := r.Header.Get("Content-Type"); ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	if r.ContentLength > 0 {
		req.ContentLength = r.ContentLength
	}
	req.Header.Set("Accept-Language", r.Header.Get("Accept-Language"))
	resp, err := (&http.Client{Timeout: 10 * time.Minute}).Do(req)
	if err != nil {
		fail(w, http.StatusBadGateway, "转给应用失败: "+err.Error())
		return
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, io.LimitReader(resp.Body, 8<<20))
}
