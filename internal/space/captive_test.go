package space

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

func gbk(s string) []byte {
	out, _, err := transform.Bytes(simplifiedchinese.GBK.NewEncoder(), []byte(s))
	if err != nil {
		panic(err)
	}
	return out
}

const portalHTML = `<html><head><meta http-equiv=Content-Type content="text/html; charset=GBK"><title>欢迎登录 PORTAL认证页面</title></head><body>
<form name="loginForm" action='logon.cgi' method="post">
<table><tr><td>手机号码</td><td><input type="text" id="PtUser" name="PtUser"/></td></tr>
<tr><td>认证密码</td><td><input type="password" id="PtPwd" name="PtPwd"/></td></tr></table>
<input type="hidden" name="PtButton" value="Logon"/></form></body></html>`

func TestParsePortalGBK(t *testing.T) {
	page := "http://portal.example/portal"
	title, f := parsePortal(gbk(portalHTML), page)
	if !strings.Contains(title, "PORTAL") {
		t.Fatalf("title = %q", title)
	}
	if f == nil {
		t.Fatal("没解析出表单")
	}
	if f.action != "http://portal.example/logon.cgi" {
		t.Fatalf("action = %q", f.action)
	}
	if f.method != "POST" {
		t.Fatalf("method = %q", f.method)
	}
	var user, pwd, btn bool
	for _, fld := range f.fields {
		switch fld.Name {
		case "PtUser":
			user = fld.Type == "text"
		case "PtPwd":
			pwd = fld.Type == "password"
		case "PtButton":
			btn = fld.Value == "Logon" && fld.Type == "hidden"
		}
	}
	if !user || !pwd || !btn {
		t.Fatalf("字段不全: %+v", f.fields)
	}
}

func TestDetectAndLogin(t *testing.T) {
	var authed atomic.Bool
	mux := http.NewServeMux()
	mux.HandleFunc("/generate_204", func(w http.ResponseWriter, r *http.Request) {
		if authed.Load() {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.Redirect(w, r, "/portal/", http.StatusFound)
	})
	// 真实门户会 301 到带结尾斜杠的目录，否则 action='logon.cgi' 会被解析到根目录
	mux.HandleFunc("/portal", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/portal/", http.StatusMovedPermanently)
	})
	mux.HandleFunc("/portal/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=GBK")
		_, _ = w.Write(gbk(portalHTML))
	})
	mux.HandleFunc("/portal/logon.cgi", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "form", http.StatusBadRequest)
			return
		}
		if r.FormValue("PtUser") == "186" && r.FormValue("PtPwd") == "pw" && r.FormValue("PtButton") == "Logon" {
			authed.Store(true)
			_, _ = w.Write([]byte("<html><title>认证成功</title></html>"))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<html><title>认证失败</title></html>"))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	info := detectCaptiveAt(ctx, []string{ts.URL + "/generate_204"})
	if !info.NeedsAuth {
		t.Fatalf("应判定需要认证: %+v", info)
	}
	if info.Title == "" || len(info.Fields) == 0 {
		t.Fatalf("门户没解析出来: %+v", info)
	}
	if !strings.HasSuffix(info.Action, "/portal/logon.cgi") {
		t.Fatalf("action = %q", info.Action)
	}

	if err := submitPortal(ctx, info.PortalURL, info.Action, info.Method, map[string]string{
		"PtUser": "186", "PtPwd": "pw", "PtButton": "Logon",
	}); err != nil {
		t.Fatalf("提交失败: %v", err)
	}
	if !authed.Load() {
		t.Fatal("服务端没收到正确凭证")
	}
	after := detectCaptiveAt(ctx, []string{ts.URL + "/generate_204"})
	if after.NeedsAuth {
		t.Fatalf("认证后仍判定需要认证: %+v", after)
	}
}
