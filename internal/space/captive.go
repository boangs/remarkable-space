// 公开 Wi-Fi 的网页认证 (captive portal)。
//
// 设备连上需要认证的 Wi-Fi 后，所有 HTTP 会被劫持到门户页面。xochitl 没有浏览器，
// 用户就卡死了。这里做三件事：
//  1. 探测：请求 generate_204 类地址，被重定向/劫持就判定需要认证
//  2. 解析：抓门户页面，找出表单（action/method/字段），密码框所在表单为准
//  3. 提交：用设备账号密码 POST 表单，然后再探测一次确认放行
//
// 只覆盖标准表单型门户（企业/酒店/校园常见）。微信/短信验证码/JS 挑战型只能提示
// 用户用手机完成。
package space

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/net/html"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

// 探测地址: 国内优先 (gstatic 在国内经常不可达, 会误判成"网络异常")。
var captiveProbes = []string{
	"http://connect.rom.miui.com/generate_204",
	"http://connectivitycheck.gstatic.com/generate_204",
}

type captiveField struct {
	Name  string `json:"name"`
	Type  string `json:"type"`            // text / password / hidden / ...
	Label string `json:"label,omitempty"` // 尽量从 placeholder/前面的文字猜
	Value string `json:"value,omitempty"` // hidden 字段的默认值
}

type captiveInfo struct {
	NeedsAuth bool           `json:"needs_auth"`
	PortalURL string         `json:"portal_url,omitempty"`
	Title     string         `json:"title,omitempty"`
	Action    string         `json:"action,omitempty"`
	Method    string         `json:"method,omitempty"`
	Fields    []captiveField `json:"fields,omitempty"`
	Error     string         `json:"error,omitempty"`
}

func noRedirectClient(timeout time.Duration) *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	return &http.Client{
		Timeout:   timeout,
		Transport: tr,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// detectCaptive 探测当前网络是否需要网页认证。ctx 控制总时长。
func detectCaptive(ctx context.Context) captiveInfo {
	return detectCaptiveAt(ctx, captiveProbes)
}

// detectCaptiveAt 用给定的探测地址，测试里注入 httptest 用。
func detectCaptiveAt(ctx context.Context, probes []string) captiveInfo {
	var lastErr error
	for _, probe := range probes {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, probe, nil)
		if err != nil {
			continue
		}
		resp, err := noRedirectClient(6 * time.Second).Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		loc := resp.Header.Get("Location")
		status := resp.StatusCode
		body := []byte{}
		if status == http.StatusOK {
			body, _ = io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		}
		resp.Body.Close()
		switch {
		case status == http.StatusNoContent || status == http.StatusResetContent:
			return captiveInfo{NeedsAuth: false}
		case status >= 300 && status < 400 && loc != "":
			portal := resolveURL(probe, loc)
			return fetchPortal(ctx, portal)
		case status == http.StatusOK:
			if !looksLikePortal(body) {
				return captiveInfo{NeedsAuth: false}
			}
			// 部分门户 (Sitech 等) 劫持时直接 200 + JS 跳转, 没有 Location 头,
			// 先从跳转页里抽出真正的门户地址, 否则会把探测地址当成门户页解析。
			if target := extractRedirectTarget(body, probe); target != "" {
				return fetchPortal(ctx, target)
			}
			return fetchPortal(ctx, probe)
		}
		lastErr = fmt.Errorf("探测返回 HTTP %d", status)
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("无法连接探测地址")
	}
	return captiveInfo{Error: lastErr.Error()}
}

func resolveURL(base, ref string) string {
	b, err := url.Parse(base)
	if err != nil {
		return ref
	}
	r, err := url.Parse(ref)
	if err != nil {
		return ref
	}
	return b.ResolveReference(r).String()
}

func looksLikePortal(body []byte) bool {
	low := strings.ToLower(string(body))
	for _, k := range []string{"portal", "login", "认证", "登录", "captive", "logon"} {
		if strings.Contains(low, k) {
			return true
		}
	}
	return false
}

// 劫持页里常见的 JS 跳转写法: location.href="..." / location.replace("...") / location="..."
var jsRedirectRe = regexp.MustCompile(`(?i)location(?:\.href)?\s*(?:=|\.replace\()\s*["']([^"']+)["']`)

// extractRedirectTarget 从 "200 + JS 跳转" 型劫持页里提取真正的门户地址。
func extractRedirectTarget(body []byte, base string) string {
	if m := jsRedirectRe.FindSubmatch(body); m != nil {
		return resolveURL(base, string(m[1]))
	}
	// meta refresh: <meta http-equiv="refresh" content="0;url=...">
	low := strings.ToLower(string(body))
	if idx := strings.Index(low, `http-equiv="refresh"`); idx >= 0 {
		if u := strings.Index(low[idx:], "url="); u >= 0 {
			rest := low[idx+u+4:]
			if end := strings.IndexAny(rest, `"' >`); end > 0 {
				return resolveURL(base, rest[:end])
			}
		}
	}
	return ""
}

// fetchPortal 抓门户页面并解析出表单。
func fetchPortal(ctx context.Context, portalURL string) captiveInfo {
	out := captiveInfo{NeedsAuth: true, PortalURL: portalURL}
	client := noRedirectClient(8 * time.Second)
	// 跟随 3xx（门户常有跳转），最多 5 跳
	followed := 0
	cur := portalURL
	var body []byte
	for followed < 5 {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, cur, nil)
		if err != nil {
			out.Error = err.Error()
			return out
		}
		resp, err := client.Do(req)
		if err != nil {
			out.Error = err.Error()
			return out
		}
		if resp.StatusCode >= 300 && resp.StatusCode < 400 && resp.Header.Get("Location") != "" {
			cur = resolveURL(cur, resp.Header.Get("Location"))
			resp.Body.Close()
			followed++
			continue
		}
		body, _ = io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		out.PortalURL = cur
		break
	}
	if len(body) == 0 {
		return out
	}
	body = decodeHTML(body)
	title, form := parsePortal(body, out.PortalURL)
	out.Title = title
	if form != nil {
		out.Action, out.Method, out.Fields = form.action, form.method, form.fields
	}
	return out
}

// decodeHTML 把 GBK 页面转成 UTF-8（门户大多是 GBK，直接按 UTF-8 读会乱码）。
func decodeHTML(b []byte) []byte {
	head := strings.ToLower(string(b[:min(len(b), 2048)]))
	if strings.Contains(head, "charset=gb") || strings.Contains(head, "charset=\"gb") {
		if out, _, err := transform.Bytes(simplifiedchinese.GBK.NewDecoder(), b); err == nil {
			return out
		}
	}
	if !utf8Valid(b) {
		if out, _, err := transform.Bytes(simplifiedchinese.GBK.NewDecoder(), b); err == nil {
			return out
		}
	}
	return b
}

func utf8Valid(b []byte) bool {
	for len(b) > 0 {
		if b[0] < 0x80 {
			b = b[1:]
			continue
		}
		_, size := utf8.DecodeRune(b)
		if size == 0 {
			return false
		}
		b = b[size:]
	}
	return true
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

type portalForm struct {
	action string
	method string
	fields []captiveField
}

// parsePortal 从 HTML 里找标题和"带密码框的表单"。找不到带密码的表单就返回 nil。
func parsePortal(body []byte, pageURL string) (string, *portalForm) {
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return "", nil
	}
	var title string
	var forms []*portalForm
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "title" && title == "" {
			title = strings.TrimSpace(textOf(n))
		}
		if n.Type == html.ElementNode && n.Data == "form" {
			if f := parseForm(n, pageURL); f != nil {
				forms = append(forms, f)
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	if len(forms) == 0 {
		return title, nil
	}
	// 优先带密码框的表单，其次第一个
	for _, f := range forms {
		for _, fld := range f.fields {
			if fld.Type == "password" {
				return title, f
			}
		}
	}
	return title, forms[0]
}

func parseForm(form *html.Node, pageURL string) *portalForm {
	action := attr(form, "action")
	if action == "" {
		action = pageURL
	} else {
		action = resolveURL(pageURL, action)
	}
	method := strings.ToUpper(strings.TrimSpace(attr(form, "method")))
	if method == "" {
		method = "GET"
	}
	f := &portalForm{action: action, method: method}
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "input" {
			name := attr(n, "name")
			if name != "" {
				typ := strings.ToLower(attr(n, "type"))
				if typ == "" {
					typ = "text"
				}
				label := attr(n, "placeholder")
				if label == "" {
					label = nearLabel(n)
				}
				f.fields = append(f.fields, captiveField{Name: name, Type: typ, Label: label, Value: attr(n, "value")})
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(form)
	return f
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, key) {
			return a.Val
		}
	}
	return ""
}

// nearLabel 找 input 前面最近的文本（label/td 里的"手机号码"这类）。
func nearLabel(n *html.Node) string {
	for p := n.Parent; p != nil; p = p.Parent {
		if p.Type == html.ElementNode && (p.Data == "td" || p.Data == "label" || p.Data == "div") {
			t := strings.TrimSpace(textOf(p))
			if t != "" && len([]rune(t)) <= 16 {
				return t
			}
		}
		if p.Data == "form" || p.Data == "body" {
			break
		}
	}
	return ""
}

func textOf(n *html.Node) string {
	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(x *html.Node) {
		if x.Type == html.TextNode {
			sb.WriteString(x.Data)
		}
		for c := x.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return sb.String()
}

// handleCaptive GET /space/net/captive
func (h *Handler) handleCaptive(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	writeJSON(w, http.StatusOK, detectCaptive(ctx))
}

// handleCaptiveLogin POST /space/net/login
// 入参: {portal_url, action, method, fields:{name:value}}。提交完再探测一次确认放行。
func (h *Handler) handleCaptiveLogin(w http.ResponseWriter, r *http.Request) {
	var in struct {
		PortalURL string            `json:"portal_url"`
		Action    string            `json:"action"`
		Method    string            `json:"method"`
		Fields    map[string]string `json:"fields"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, "请求格式不对")
		return
	}
	if in.Action == "" {
		in.Action = in.PortalURL
	}
	if !sameHost(in.PortalURL, in.Action) {
		fail(w, http.StatusBadRequest, "认证地址和门户地址不一致")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if err := submitPortal(ctx, in.PortalURL, in.Action, in.Method, in.Fields); err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	// 提交后再探测：没被重定向就是放行了
	info := detectCaptive(ctx)
	if info.NeedsAuth {
		msg := "认证没有生效，请检查账号密码"
		if info.Error != "" {
			msg += "（" + info.Error + "）"
		}
		fail(w, http.StatusUnauthorized, msg)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func sameHost(a, b string) bool {
	ua, err := url.Parse(a)
	if err != nil {
		return false
	}
	ub, err := url.Parse(b)
	if err != nil {
		return false
	}
	return strings.EqualFold(ua.Hostname(), ub.Hostname())
}

// submitPortal 先 GET 门户拿 cookie，再按表单 method 提交字段。
func submitPortal(ctx context.Context, portalURL, action, method string, fields map[string]string) error {
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Timeout: 15 * time.Second, Jar: jar}
	// 先打开门户页面，让服务器下发会话 cookie
	if req, err := http.NewRequestWithContext(ctx, http.MethodGet, portalURL, nil); err == nil {
		if resp, err := client.Do(req); err == nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 256<<10))
			resp.Body.Close()
		}
	}
	values := url.Values{}
	for k, v := range fields {
		values.Set(k, v)
	}
	if strings.EqualFold(method, "GET") {
		u := action
		if strings.Contains(u, "?") {
			u += "&" + values.Encode()
		} else {
			u += "?" + values.Encode()
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return err
		}
		return doDiscard(client, req)
	}
	body := values.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, action, strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Referer", portalURL)
	return doDiscard(client, req)
}

func doDiscard(client *http.Client, req *http.Request) error {
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 256<<10))
	return nil
}
