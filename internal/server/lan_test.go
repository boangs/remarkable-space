package server

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLanAllowed(t *testing.T) {
	cases := []struct {
		method, path string
		want         bool
	}{
		{"GET", "/qr", true},
		{"POST", "/documents", true},
		{"GET", "/fonts", true}, {"POST", "/fonts", true}, {"DELETE", "/fonts/a.ttf", true},
		{"POST", "/fonts/a.ttf/apply", false},
		{"GET", "/screens/a.png/preview", false},
		{"GET", "/hero/a.jpg", true},
		{"GET", "/ai-config", true}, {"PUT", "/ai-config", true},
		{"POST", "/ai-chat", false},
		{"GET", "/space/apps", true}, {"POST", "/space/apps", true}, {"DELETE", "/space/apps/plan", true},
		{"GET", "/space/uploads", true}, {"POST", "/space/uploads", false},
		{"POST", "/space/apps/stickers/upload/photo", true},
		{"GET", "/space/apps/flashcard/upload/anki", true},
		{"DELETE", "/space/apps/flashcard/upload/anki/deck", true},
		{"PUT", "/space/apps/stickers/upload/photo", false},
		{"GET", "/space/apps/plan/data/data", false},
		{"POST", "/space/apps/plan/service/start", false},
		{"GET", "/space/library/next", false},
		{"GET", "/screenshot", false},
		{"POST", "/apply", false},
		{"GET", "/bt/status", false},
	}
	for _, c := range cases {
		r := httptest.NewRequest(c.method, c.path, nil)
		if got := lanAllowed(r); got != c.want {
			t.Errorf("%s %s = %v, want %v", c.method, c.path, got, c.want)
		}
	}
}

func freePort(t *testing.T) int {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// 开 → 能连; 关 → 连不上 (手机那边是"无法连接", 不是一句提示)
func TestListenersOpenClose(t *testing.T) {
	port := freePort(t)
	l := newListeners(port, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "ok") }), "测试")
	url := fmt.Sprintf("http://127.0.0.1:%d/", port)
	l.sync([]string{"127.0.0.1"})
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal("开了却连不上:", err)
	}
	resp.Body.Close()
	l.sync(nil)
	time.Sleep(50 * time.Millisecond)
	c := http.Client{Timeout: time.Second}
	if _, err := c.Get(url); err == nil {
		t.Fatal("关了还能连上")
	}
	if len(l.addrs()) != 0 {
		t.Fatal(l.addrs())
	}
}

func TestIsUSBAddr(t *testing.T) {
	if !isUSBAddr(net.ParseIP("10.11.99.1")) || isUSBAddr(net.ParseIP("192.168.2.108")) {
		t.Fatal()
	}
}
