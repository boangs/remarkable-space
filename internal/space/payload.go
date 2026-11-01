package space

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// 有些应用的"本体"是别人发布的 —— 比如微信读书那个 41 MB 的官方包。我们不能也不该
// 重新分发它 (版权, 以及跟着人家升级的负担), 所以商店里只放一份几百字节的描述加上
// 我们自己写的安装脚本, 让**设备直接从官方地址下载**。
//
// 边界说清楚: 脚本以 root 跑, 所以它必须是我们自己写的、随包一起审计的。从网上抓一个
// 脚本下来执行是另一回事, 这里不做。下载物用 sha256 锁死 —— 对方换了包当场就知道,
// 而不是装上之后才发现。

// 下载 41 MB 不能落在 /tmp: 那是 tmpfs, 占的是内存。设备总共 2 GB, 解压时还要再占
// 一份, 够把 xochitl 挤到 low on memory。放 /home 下的隐藏目录, 那是 ext4。
const payloadWorkDir = ".install-work"

func fetchesForArch(fetch []Fetch, arch string) []Fetch {
	out := make([]Fetch, 0, len(fetch))
	for _, f := range fetch {
		if f.SupportsArch(arch) {
			out = append(out, f)
		}
	}
	return out
}

// runInstall 执行应用的 install 段: 下载 → 校验 → 跑脚本。
// appDir 是应用自己的目录 (脚本和 manifest 都在里头)。
func (r *Registry) runInstall(appDir string, m *Manifest, hdr http.Header) error {
	if m.Install == nil || (len(m.Install.Fetch) == 0 && m.Install.Script == "") {
		return nil
	}
	work := filepath.Join(r.UserDir, payloadWorkDir, m.ID)
	_ = os.RemoveAll(work)
	if err := os.MkdirAll(work, 0o755); err != nil {
		return err
	}
	// 装完就清: 这些是几十 MB 的中间物, 留着只会占地方
	defer os.RemoveAll(filepath.Join(r.UserDir, payloadWorkDir))

	fetches := fetchesForArch(m.Install.Fetch, r.Arch)
	if len(m.Install.Fetch) > 0 && len(fetches) == 0 {
		return fmt.Errorf("应用 %s 没有本机架构 %s 的安装包", m.ID, r.Arch)
	}
	for i, f := range fetches {
		name := f.To
		if name == "" {
			name = path.Base(strings.Split(f.URL, "?")[0])
		}
		setProgress(func(p *Progress) {
			p.Phase = "fetching"
			p.Got, p.Total = 0, 0
			if len(fetches) > 1 {
				p.Name = fmt.Sprintf("%s (本体 %d/%d)", m.Name, i+1, len(fetches))
			} else {
				p.Name = m.Name + " 本体"
			}
		})
		if err := fetchVerified(f.URL, filepath.Join(work, name), f.SHA256, hdr); err != nil {
			return fmt.Errorf("取 %s 失败: %w", name, err)
		}
	}

	if m.Install.Script == "" {
		return nil
	}
	setProgress(func(p *Progress) { p.Phase = "installing"; p.Name = m.Name })
	return runAppScript(appDir, m.Install.Script, work, m.ID)
}

// runUninstall 在删应用目录之前跑卸载脚本, 把本体一并收掉。
// 脚本失败不挡卸载 —— 用户点了删除就该删掉, 顶多是本体残留在设备上,
// 总比"删不掉"强 (那会让人反复点, 以为是界面坏了)。
func (r *Registry) runUninstall(appDir string, m *Manifest) {
	if m.Install == nil || m.Install.Uninstall == "" {
		return
	}
	work := filepath.Join(r.UserDir, payloadWorkDir, m.ID+"-uninst")
	_ = os.MkdirAll(work, 0o755)
	defer os.RemoveAll(work)
	if err := runAppScript(appDir, m.Install.Uninstall, work, m.ID); err != nil {
		logStore("%s 的卸载脚本出错 (应用仍会删除): %v", m.ID, err)
	}
}

// runAppScript 跑应用包里自带的脚本。
// 工作目录是放下载物的临时目录, SPACE_APP_DIR 指向应用自己的目录。
func runAppScript(appDir, rel, work, id string) error {
	script := filepath.Join(appDir, filepath.Clean(rel))
	// 再挡一道: manifest 校验过是相对路径了, 但那份 manifest 是从包里读的,
	// 这里按实际落地的路径核一遍, 别让它跑出应用目录。
	if !strings.HasPrefix(script, filepath.Clean(appDir)+string(os.PathSeparator)) {
		return fmt.Errorf("脚本 %q 不在应用目录内", rel)
	}
	if _, err := os.Stat(script); err != nil {
		return fmt.Errorf("找不到脚本 %s: %w", rel, err)
	}
	// 装一个几十 MB 的本体可能要解压、拷贝、改权限, 给足时间但别无限等
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/sh", script)
	cmd.Dir = work
	cmd.Env = append(os.Environ(),
		"SPACE_APP_ID="+id,
		"SPACE_APP_DIR="+appDir,
		"HOME=/home/root",
	)
	out, err := cmd.CombinedOutput()
	if len(out) > 0 {
		logStore("%s 安装脚本输出:\n%s", id, strings.TrimRight(string(out), "\n"))
	}
	if err != nil {
		// 把脚本最后几行带给用户 —— 只说"退出码 1"他没法judge是网络还是空间不够
		return fmt.Errorf("%w%s", err, tailLines(string(out), 3))
	}
	return nil
}

// fetchVerified 下载并核对 sha256。校验不过就删掉, 绝不留下一个"可能是对的"文件。
//
// 下载会断: 网友反馈过取微信读书本体时 "read: connection reset by peer" —— 设备走中国
// 移动的 IPv6 去腾讯 CDN, 下到一半被对端重置。只试一次的话 40 MB 一断就前功尽弃, 所以:
//   - 最多试 fetchAttempts 次, 每次用 Range 从断点接着下 (服务器不支持续传就从头来);
//   - 断过一次之后只走 IPv4 —— 断在某条 IPv6 线路上的, 换 IPv4 多半就通了;
//   - 4xx (地址错、没权限) 重试也没用, 直接报。
//
// 续传过的文件无法边下边算摘要, 所以下完整个文件再算一遍 sha256。
func fetchVerified(url, dst, want string, hdr http.Header) error {
	part := dst + ".part"
	_ = os.Remove(part)
	var err error
	for attempt := 1; attempt <= fetchAttempts; attempt++ {
		if attempt > 1 {
			logStore("下载 %s 第 %d 次失败 (%v), %d 秒后改走 IPv4 续传", path.Base(dst), attempt-1, err, attempt*2)
			time.Sleep(time.Duration(attempt*2) * time.Second)
		}
		if err = fetchOnce(url, part, hdr, attempt > 1); err == nil {
			break
		}
		var se *httpStatusError
		if errors.As(err, &se) && se.code >= 400 && se.code < 500 && se.code != 408 && se.code != 429 {
			break
		}
	}
	if err != nil {
		_ = os.Remove(part)
		return err
	}
	got, err := fileSHA256(part)
	if err != nil {
		_ = os.Remove(part)
		return err
	}
	if !strings.EqualFold(got, want) {
		_ = os.Remove(part)
		return fmt.Errorf("校验不符 (期望 %s…, 实际 %s…) —— 对方可能换了包, 别装", want[:12], got[:12])
	}
	return os.Rename(part, dst)
}

const fetchAttempts = 5

type httpStatusError struct{ code int }

func (e *httpStatusError) Error() string { return fmt.Sprintf("HTTP %d", e.code) }

// fetchOnce 下一次: part 里已有的部分用 Range 续上。ipv4 为真时只拨 IPv4。
func fetchOnce(url, part string, hdr http.Header, ipv4 bool) error {
	var off int64
	if st, err := os.Stat(part); err == nil {
		off = st.Size()
	}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	// 商店自己的凭证只发给商店。本体多半在别人的 CDN 上, 别把票带过去。
	if isOurStore(url, hdr) {
		for k, vs := range hdr {
			for _, v := range vs {
				req.Header.Add(k, v)
			}
		}
	}
	if off > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", off))
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if ipv4 {
		d := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
		tr.DialContext = func(ctx context.Context, _, addr string) (net.Conn, error) {
			return d.DialContext(ctx, "tcp4", addr)
		}
	}
	resp, err := (&http.Client{Timeout: 30 * time.Minute, Transport: tr}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	flags := os.O_CREATE | os.O_WRONLY
	switch {
	case off > 0 && resp.StatusCode == http.StatusPartialContent:
		flags |= os.O_APPEND
	case off > 0 && resp.StatusCode == http.StatusRequestedRangeNotSatisfiable:
		return nil // 上次其实已经下完了, 交给 sha256 判定
	case resp.StatusCode == http.StatusOK:
		flags |= os.O_TRUNC // 服务器不支持续传: 从头来
		off = 0
	default:
		return &httpStatusError{resp.StatusCode}
	}
	f, err := os.OpenFile(part, flags, 0o644)
	if err != nil {
		return err
	}
	cr := &countingReader{r: resp.Body, n: off}
	setProgress(func(p *Progress) {
		p.Got = off
		if resp.ContentLength >= 0 {
			p.Total = off + resp.ContentLength
		}
	})
	_, err = io.Copy(f, cr)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

func fileSHA256(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// isOurStore 判断这个地址是不是我们自己的商店。只有它才配带上票。
func isOurStore(url string, hdr http.Header) bool {
	if len(hdr) == 0 {
		return false
	}
	for _, p := range storeHosts {
		if strings.HasPrefix(url, p) {
			return true
		}
	}
	return false
}

// storeHosts 由后端在启动时按 StoreAPI 填。为空时任何外部下载都不带凭证 —— 这是安全的默认。
var storeHosts []string

func tailLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) == 0 || (len(lines) == 1 && lines[0] == "") {
		return ""
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return ": " + strings.Join(lines, " / ")
}
