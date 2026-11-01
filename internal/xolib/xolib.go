// Package xolib 往 xochitl 的文件库里放东西 (热导入), 不重启 xochitl。
//
// 分两步, 只有第一步在本进程里:
//  1. 把文件写进 xochitl 数据目录: <uuid>.<ext> + .content + .metadata (最后写 metadata)。
//  2. 通知 xochitl 的文件库加载这个 uuid —— 要调它内存里 Library 对象的 requestLoadEntry,
//     外部进程够不着 (xochitl 没有 D-Bus 服务, 也不监听目录), 由跑在 xochitl 里的常驻
//     hooks/root/library.qml 长连接取走、调完回报; 不依赖空间页面是否打开。
//
// 3.29 起 QML 拿到的文件库换成了只读的 LibraryFacade, 没有 requestLoadEntry 了。常驻桥发现
// 这一点就报「导入器模式」, 文档改走系统自己的导入器 DocumentImporter.importFromUrls
// (就是 USB 网页传文件用的那个): 本包把文件放进暂存目录, 用显示名当文件名, 常驻桥让导入器
// 复制进文件库, 暂存文件过 StagingKeep 删掉。3.28 及以前照旧走第一种, 行为不变。
//
// 为什么不用 librarian: 它在 3.28 上找不到改了名的 Library 单例, 无限重试漏堆,
// 一周把 xochitl 撑到 1.6GB。这里不往 xochitl 里塞任何原生代码。
package xolib

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// DefaultDir 是设备上 xochitl 的数据目录。
const DefaultDir = "/home/root/.local/share/remarkable/xochitl"

var ErrBadType = errors.New("仅支持 .pdf / .epub")

// Library 是对 xochitl 数据目录的一个写入口, 带着通知队列。
type Library struct {
	Dir     string
	Staging string // 导入器模式的暂存目录, 每个条目一个子目录 <id>/<显示名>.<ext>
	Queue   *Queue
	now     func() time.Time
}

// StagingKeep 导入器是异步复制的, 交出去之后暂存文件留这么久再删。
var StagingKeep = 10 * time.Minute

// New 建一个写入口; pendingFile 是待通知队列落盘的位置 (空 = 只在内存里, 测试用),
// 暂存目录放在它旁边 (不放 /tmp: tmpfs 吃内存)。
func New(dir, pendingFile string) *Library {
	staging := filepath.Join(os.TempDir(), "xolib-import")
	if pendingFile != "" {
		staging = filepath.Join(filepath.Dir(pendingFile), "library-import")
	}
	l := &Library{Dir: dir, Staging: staging, Queue: NewQueue(pendingFile), now: time.Now}
	l.sweepStaging()
	return l
}

// SetImporter 记下外壳报的模式; 切到导入器模式时, 把还没加载成的文档改成导入。
func (l *Library) SetImporter(on bool) {
	l.Queue.SetImporter(on, l.loadToImport)
}

// loadToImport 把一条 load 改成 import: 文档本体挪到暂存目录 (用显示名命名), 删掉数据目录里
// 这个 uuid 的其余文件 —— 文件库从没加载过它, 删了不留痕; 不删的话导入后再重启会出现两份。
// 文件夹 / 笔记本没有导入器可用, 返回 false (留在盘上, xochitl 重启时自己读到)。
func (l *Library) loadToImport(j *Job) bool {
	var meta struct{ VisibleName, Parent string }
	b, err := os.ReadFile(filepath.Join(l.Dir, j.ID+".metadata"))
	if err != nil || json.Unmarshal(b, &meta) != nil {
		return false
	}
	for _, ext := range []string{"pdf", "epub"} {
		src := filepath.Join(l.Dir, j.ID+"."+ext)
		if _, err := os.Stat(src); err != nil {
			continue
		}
		dst := l.stagingPath(j.ID, meta.VisibleName, ext)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return false
		}
		if err := os.Rename(src, dst); err != nil {
			return false
		}
		l.removeAll(j.ID)
		*j = Job{Op: OpImport, ID: j.ID, Path: dst, Parent: meta.Parent}
		return true
	}
	return false
}

// stagingPath 暂存文件路径。导入器拿文件名当显示名, 所以文件名就是显示名; 去掉 / 和
// 开头的点, 太长的按字节截断 (ext4 文件名上限 255 字节)。
func (l *Library) stagingPath(id, visibleName, ext string) string {
	name := strings.TrimLeft(strings.ReplaceAll(visibleName, "/", "／"), ".")
	if strings.TrimSpace(name) == "" {
		name = "未命名"
	}
	for len(name)+len(ext)+1 > 240 {
		r := []rune(name)
		name = string(r[:len(r)-1])
	}
	return filepath.Join(l.Staging, id, name+"."+ext)
}

// doneImport 导入交出去了: 过 StagingKeep 删暂存。
func (l *Library) doneImport(j Job) {
	dir := filepath.Dir(j.Path)
	if filepath.Dir(dir) != l.Staging {
		return
	}
	time.AfterFunc(StagingKeep, func() { os.RemoveAll(dir) })
}

// sweepStaging 启动时清掉上次没删的暂存 (队列里还等着导入的留下)。
func (l *Library) sweepStaging() {
	ents, err := os.ReadDir(l.Staging)
	if err != nil {
		return
	}
	keep := map[string]bool{}
	l.Queue.mu.Lock()
	for _, j := range l.Queue.pending {
		keep[j.ID] = true
	}
	l.Queue.mu.Unlock()
	for _, e := range ents {
		if !keep[e.Name()] {
			os.RemoveAll(filepath.Join(l.Staging, e.Name()))
		}
	}
}

// Entry 是导入后的条目。
type Entry struct {
	ID          string `json:"uuid"`
	VisibleName string `json:"name"`
	Type        string `json:"type"` // pdf / epub
	loaded      bool   // 已经由系统建好 (导入器模式), 不用再等 load 回报
}

// ImportDocument 把一份 PDF / EPUB / rmdoc (reMarkable 原生笔记本包, 只有导入器模式认) 写进文件库并排队通知。parent 为空 = 「我的文件」根目录。
// visibleName 是文件库里显示的名字 (一般是去掉扩展名的原文件名)。
func (l *Library) ImportDocument(r io.Reader, visibleName, ext, parent string) (Entry, error) {
	ext = strings.TrimPrefix(strings.ToLower(ext), ".")
	if ext != "pdf" && ext != "epub" && ext != "rmdoc" {
		return Entry{}, ErrBadType
	}
	id, err := newUUID()
	if err != nil {
		return Entry{}, err
	}
	if strings.TrimSpace(visibleName) == "" {
		visibleName = "未命名"
	}
	if l.Queue.Importer() {
		return l.stageDocument(r, id, visibleName, ext, parent)
	}
	if ext == "rmdoc" {
		return Entry{}, ErrBadType // 老固件没有系统导入器, 直接写文件库只会写进一个 zip, 不认
	}
	// 顺序: 本体 → .content → .metadata。xochitl 下次扫目录 (或重启) 只认带 .metadata 的条目,
	// metadata 最后落地, 就不会出现「有条目没本体」的半成品。
	if err := l.writeFile(id+"."+ext, r); err != nil {
		return Entry{}, err
	}
	content := map[string]any{"fileType": ext}
	if err := l.writeJSON(id+".content", content); err != nil {
		l.removeAll(id)
		return Entry{}, err
	}
	ms := strconv.FormatInt(l.now().UnixMilli(), 10)
	meta := map[string]any{
		"createdTime": ms, "lastModified": ms, "lastOpened": "0", "lastOpenedPage": 0,
		"new": false, "parent": parent, "pinned": false, "source": "",
		"type": "DocumentType", "visibleName": visibleName,
	}
	if err := l.writeJSON(id+".metadata", meta); err != nil {
		l.removeAll(id)
		return Entry{}, err
	}
	l.Queue.Enqueue(Job{Op: OpLoad, ID: id})
	return Entry{ID: id, VisibleName: visibleName, Type: ext}, nil
}

// stageDocument 导入器模式: 写进暂存目录, 排一条 import。
func (l *Library) stageDocument(r io.Reader, id, visibleName, ext, parent string) (Entry, error) {
	dst := l.stagingPath(id, visibleName, ext)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return Entry{}, err
	}
	tmp := dst + ".tmp"
	f, err := os.Create(tmp)
	if err == nil {
		_, err = io.Copy(f, r)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}
	if err == nil {
		err = os.Rename(tmp, dst)
	}
	if err != nil {
		os.RemoveAll(filepath.Dir(dst))
		return Entry{}, err
	}
	l.Queue.Enqueue(Job{Op: OpImport, ID: id, Path: dst, Parent: parent})
	return Entry{ID: id, VisibleName: visibleName, Type: ext}, nil
}

// writeFile 先写同目录下的临时名再 rename: 断电也不会留下半个文件。
// 临时名以 . 开头且不是 uuid 形状, xochitl 扫目录时不会把它当条目。
func (l *Library) writeFile(name string, r io.Reader) error {
	if err := os.MkdirAll(l.Dir, 0o755); err != nil {
		return err
	}
	tmp := filepath.Join(l.Dir, ".xolib-"+name+".tmp")
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, filepath.Join(l.Dir, name))
}

func (l *Library) writeJSON(name string, v any) error {
	b, err := json.MarshalIndent(v, "", "    ")
	if err != nil {
		return err
	}
	return l.writeFile(name, strings.NewReader(string(b)))
}

func (l *Library) removeAll(id string) {
	matches, _ := filepath.Glob(filepath.Join(l.Dir, id+".*"))
	for _, m := range matches {
		os.Remove(m)
	}
}

// newUUID 生成 v4 UUID (xochitl 的条目 ID 就是这个格式)。
func newUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}
