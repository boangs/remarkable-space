package space

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// 第一次下到一半掐断连接, 之后按 Range 续传: 最终文件完整且 sha256 对得上
func TestFetchVerifiedResumesAfterReset(t *testing.T) {
	body := []byte(strings.Repeat("weread-payload-", 20000))
	sum := sha256.Sum256(body)
	want := hex.EncodeToString(sum[:])
	calls, ranged := 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		start := 0
		if rg := r.Header.Get("Range"); rg != "" {
			ranged++
			start, _ = strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(rg, "bytes="), "-"))
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, len(body)-1, len(body)))
			w.Header().Set("Content-Length", strconv.Itoa(len(body)-start))
			w.WriteHeader(http.StatusPartialContent)
		} else {
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		}
		if calls == 1 { // 模拟 connection reset: 写一半就断
			w.Write(body[:len(body)/2])
			w.(http.Flusher).Flush()
			conn, _, _ := w.(http.Hijacker).Hijack()
			conn.Close()
			return
		}
		w.Write(body[start:])
	}))
	defer srv.Close()

	dst := filepath.Join(t.TempDir(), "w.zip")
	if err := fetchVerified(srv.URL, dst, want, nil); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(dst)
	if len(got) != len(body) || ranged != 1 {
		t.Fatalf("len=%d want %d, ranged=%d", len(got), len(body), ranged)
	}
}

// 404 不重试
func TestFetchVerifiedNoRetryOn404(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.NotFound(w, r)
	}))
	defer srv.Close()
	err := fetchVerified(srv.URL, filepath.Join(t.TempDir(), "x"), strings.Repeat("0", 64), nil)
	if err == nil || calls != 1 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}

func TestFetchesForArch(t *testing.T) {
	fetch := []Fetch{
		{URL: "common"},
		{URL: "64", Arch: []string{"aarch64"}},
		{URL: "32", Arch: []string{"armv7"}},
	}
	for arch, want := range map[string]string{"aarch64": "common,64", "armv7": "common,32", "arm": "common"} {
		var urls []string
		for _, f := range fetchesForArch(fetch, arch) {
			urls = append(urls, f.URL)
		}
		if got := strings.Join(urls, ","); got != want {
			t.Errorf("fetchesForArch(%q) = %q, want %q", arch, got, want)
		}
	}
}

func TestFetchArchValidation(t *testing.T) {
	m := Manifest{
		ID: "x", Name: "X", Version: "1.0", Entry: "main.qml",
		Install: &Install{
			Fetch: []Fetch{{
				URL: "https://example.com/x.zip", SHA256: strings.Repeat("0", 64), Arch: []string{"armhf"},
			}},
			Script: "setup.sh",
		},
	}
	if err := m.Validate(); err == nil {
		t.Fatal("Validate() accepted an unknown fetch architecture")
	}
}
