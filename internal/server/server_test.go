package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/boangs/remarkable-space/internal/xolib"
)

func newTestServer(t *testing.T) (*Server, http.Handler, string) {
	t.Helper()
	root := t.TempDir()
	cfg := Config{
		StaticDir:  filepath.Join(root, "static"),
		FontsDir:   filepath.Join(root, "fonts"),
		ScreensDir: filepath.Join(root, "screens"),
		MusicDir:   filepath.Join(root, "music"),
		HeroDir:    filepath.Join(root, "hero"),
		XochitlDir: filepath.Join(root, "xochitl"),
	}
	if err := os.MkdirAll(cfg.StaticDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.StaticDir, "qr.html"), []byte("上传到 reMarkable"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	srv.lib.Queue.Next(context.Background(), 0, true) // 默认算「空间」开着; 测拦截的用例自己 Close
	return srv, srv.Routes(), root
}

func multipartUpload(t *testing.T, fieldName, filename, contentType string, content []byte) (*bytes.Buffer, string) {
	t.Helper()
	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	h := make(map[string][]string)
	h["Content-Disposition"] = []string{`form-data; name="` + fieldName + `"; filename="` + filename + `"`}
	if contentType != "" {
		h["Content-Type"] = []string{contentType}
	}
	w, err := mw.CreatePart(h)
	if err != nil {
		t.Fatal(err)
	}
	w.Write(content)
	mw.Close()
	return body, mw.FormDataContentType()
}

func TestUploadFont(t *testing.T) {
	_, h, root := newTestServer(t)
	body, ct := multipartUpload(t, "file", "MiSans.ttf", "application/octet-stream", []byte("fake-font"))
	req := httptest.NewRequest("POST", "/fonts", body)
	req.Header.Set("Content-Type", ct)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body)
	}
	if _, err := os.Stat(filepath.Join(root, "fonts", "MiSans.ttf")); err != nil {
		t.Fatalf("font not saved: %v", err)
	}
}

func TestListFontsEmpty(t *testing.T) {
	_, h, _ := newTestServer(t)
	req := httptest.NewRequest("GET", "/fonts", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("status=%d", rr.Code)
	}
	if strings.TrimSpace(rr.Body.String()) != "[]" {
		t.Fatalf("expected [], got %q", rr.Body.String())
	}
}

func TestUploadFontPathTraversal(t *testing.T) {
	_, h, root := newTestServer(t)
	body, ct := multipartUpload(t, "file", "../evil.ttf", "", []byte("x"))
	req := httptest.NewRequest("POST", "/fonts", body)
	req.Header.Set("Content-Type", ct)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body)
	}
	// filepath.Base 过滤后应当落在 fonts/ 内的 evil.ttf
	if _, err := os.Stat(filepath.Join(root, "fonts", "evil.ttf")); err != nil {
		t.Fatalf("expected fonts/evil.ttf to exist: %v", err)
	}
	// 不应跑到 root
	if _, err := os.Stat(filepath.Join(root, "evil.ttf")); err == nil {
		t.Fatal("path traversal escaped sandbox")
	}
}

func TestUploadFontWrongExt(t *testing.T) {
	_, h, _ := newTestServer(t)
	body, ct := multipartUpload(t, "file", "evil.exe", "", []byte("MZ"))
	req := httptest.NewRequest("POST", "/fonts", body)
	req.Header.Set("Content-Type", ct)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 400 {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body)
	}
}

func TestDeleteFontNotFound(t *testing.T) {
	_, h, _ := newTestServer(t)
	req := httptest.NewRequest("DELETE", "/fonts/nonexistent.ttf", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 404 {
		t.Fatalf("status=%d", rr.Code)
	}
}

func TestUploadScreenWrongFormat(t *testing.T) {
	_, h, _ := newTestServer(t)
	body, ct := multipartUpload(t, "file", "x.jpg", "image/jpeg", []byte("jpg"))
	req := httptest.NewRequest("POST", "/screens", body)
	req.Header.Set("Content-Type", ct)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 400 {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body)
	}
}

// ---- /documents (xolib 常驻文件库桥热导入) ----

// fakeShell 扮演 xochitl 里的空间外壳: 长连接取活, 回报 ok。
func fakeShell(t *testing.T, srv *Server, ok bool) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() {
		for ctx.Err() == nil {
			for _, j := range srv.lib.Queue.Next(ctx, 200*time.Millisecond, true) {
				srv.lib.Queue.Done(xolib.Result{Op: j.Op, ID: j.ID, OK: ok})
			}
		}
	}()
	time.Sleep(20 * time.Millisecond)
}

func postDoc(t *testing.T, h http.Handler, name string, content []byte) *httptest.ResponseRecorder {
	t.Helper()
	body, ct := multipartUpload(t, "file", name, "application/octet-stream", content)
	req := httptest.NewRequest("POST", "/documents", body)
	req.Header.Set("Content-Type", ct)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestUploadDocumentLoaded(t *testing.T) {
	srv, h, root := newTestServer(t)
	fakeShell(t, srv, true)
	rr := postDoc(t, h, "book, 第2版.pdf", []byte("%PDF-1.4 fake"))
	if rr.Code != 200 {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body)
	}
	var resp map[string]any
	json.Unmarshal(rr.Body.Bytes(), &resp)
	id, _ := resp["uuid"].(string)
	if resp["loaded"] != true || len(id) != 36 {
		t.Fatalf("%v", resp)
	}
	xdir := filepath.Join(root, "xochitl")
	if b, _ := os.ReadFile(filepath.Join(xdir, id+".pdf")); string(b) != "%PDF-1.4 fake" {
		t.Fatal("本体没写进 xochitl 目录")
	}
	// 文件名里的逗号原样进显示名 (librarian 时代会被当分隔符切断)
	b, _ := os.ReadFile(filepath.Join(xdir, id+".metadata"))
	if !strings.Contains(string(b), `"visibleName": "book, 第2版"`) {
		t.Fatalf("metadata: %s", b)
	}
}

// 外壳没来回报 (比如刚好在这几秒里关了空间): 文件照样写进去, 只是 loaded=false, 下次打开补上
func TestUploadDocumentNotLoadedYet(t *testing.T) {
	srv, h, _ := newTestServer(t)
	srv.lib.Queue.Next(context.Background(), 0, true) // 算开着, 但没人取活
	prev := LoadWait
	LoadWait = 50 * time.Millisecond
	t.Cleanup(func() { LoadWait = prev })
	rr := postDoc(t, h, "a.epub", []byte("PK"))
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"loaded":false`) {
		t.Fatalf("%d %s", rr.Code, rr.Body)
	}
	if srv.lib.Queue.Pending() != 1 {
		t.Fatal("应留在待通知队列里")
	}
}

func TestUploadDocumentRejectsExe(t *testing.T) {
	srv, h, _ := newTestServer(t)
	fakeShell(t, srv, true)
	if rr := postDoc(t, h, "evil.exe", []byte("MZ")); rr.Code != 400 {
		t.Fatalf("status=%d", rr.Code)
	}
}

func TestUploadsWorkWithoutSpaceOpen(t *testing.T) {
	srv, h, _ := newTestServer(t)
	fakeShell(t, srv, true)
	srv.lib.Queue.Close()
	if rr := postDoc(t, h, "a.pdf", []byte("%PDF")); rr.Code != 200 {
		t.Fatalf("文档上传: %d %s", rr.Code, rr.Body)
	}
	body, ct := multipartUpload(t, "file", "a.ttf", "font/ttf", []byte("x"))
	req := httptest.NewRequest("POST", "/fonts", body)
	req.Header.Set("Content-Type", ct)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("字体上传: %d", rr.Code)
	}
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/qr", nil))
	if rr.Code != 200 {
		t.Fatalf("扫码页: %d", rr.Code)
	}
}

func TestQRPage(t *testing.T) {
	_, h, _ := newTestServer(t)
	req := httptest.NewRequest("GET", "/qr", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("status=%d", rr.Code)
	}
	body, _ := io.ReadAll(rr.Body)
	if !bytes.Contains(body, []byte("上传到 reMarkable")) {
		t.Errorf("qr.html content missing")
	}
	page, err := os.ReadFile("../../static/qr.html")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(page, []byte("fetch('/space/uploads' + window.location.search)")) {
		t.Error("上传列表必须保留专属二维码的 focus 参数")
	}
}

// 确保无意中的回退 (httpError 只用 JSON)
var _ = errors.Is
