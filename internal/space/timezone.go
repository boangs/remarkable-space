package space

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	_ "time/tzdata" // 时区数据打进二进制: 不依赖设备上 /usr/share/zoneinfo 齐不齐
)

// 时区。reMarkable 系统时区固定是 UTC、自己也没有时区设置, 外壳显示的「本地时间」由我们定:
// 设置里选的 (shell/config.json 的 timezone) > 环境变量 TZ > ~/.config/rmkit-cn/space.json > 北京时间。
//
//	GET /space/timezones   设置里可选的时区: [{name, zh, en, offset}] (offset 分钟, 按现在算, 夏令时也对)

// defaultTimezone is used when the user has not picked a zone and neither $TZ nor
// ~/.config/rmkit-cn/space.json says anything. Upstream defaulted to Beijing time;
// this fork targets Western Europe. It must be one of the names in tzChoices below,
// otherwise the settings list cannot highlight the current choice.
const defaultTimezone = "Europe/Berlin"

// tzChoice 设置里列出的时区 (按偏移从东到西; 同偏移只留用户最认得的那个城市)
var tzChoices = []struct{ Name, Zh, En string }{
	{"Pacific/Auckland", "新西兰 奥克兰", "Auckland"},
	{"Australia/Sydney", "澳大利亚 悉尼", "Sydney"},
	{"Asia/Tokyo", "日本 东京", "Tokyo"},
	{"Asia/Seoul", "韩国 首尔", "Seoul"},
	{"Asia/Shanghai", "中国 北京时间", "China (Beijing)"},
	{"Asia/Hong_Kong", "中国香港", "Hong Kong"},
	{"Asia/Taipei", "中国台北", "Taipei"},
	{"Asia/Singapore", "新加坡", "Singapore"},
	{"Asia/Bangkok", "泰国 曼谷", "Bangkok"},
	{"Asia/Kolkata", "印度", "India"},
	{"Asia/Dubai", "迪拜", "Dubai"},
	{"Europe/Moscow", "俄罗斯 莫斯科", "Moscow"},
	{"Europe/Berlin", "德国 / 法国 / 意大利", "Berlin / Paris / Rome"},
	{"Europe/London", "英国 伦敦", "London"},
	{"America/Sao_Paulo", "巴西 圣保罗", "São Paulo"},
	{"America/New_York", "美国东部 纽约", "US Eastern (New York)"},
	{"America/Chicago", "美国中部 芝加哥", "US Central (Chicago)"},
	{"America/Denver", "美国山地 丹佛", "US Mountain (Denver)"},
	{"America/Los_Angeles", "美国西部 洛杉矶", "US Pacific (Los Angeles)"},
	{"UTC", "协调世界时 UTC", "UTC"},
}

// tzPref 设置里选的时区 (没选是空)
func (h *Handler) tzPref() string {
	b, err := os.ReadFile(filepath.Join(h.Reg.DataDir, "shell", "config.json"))
	if err != nil {
		return ""
	}
	var cfg struct {
		Timezone string `json:"timezone"`
	}
	if json.Unmarshal(b, &cfg) != nil {
		return ""
	}
	return strings.TrimSpace(cfg.Timezone)
}

// localTimezone 外壳显示用的时区名与偏移 (分钟, 东八区 = 480)
func (h *Handler) localTimezone() (string, int) {
	name := h.tzPref()
	if name == "" {
		name = os.Getenv("TZ")
	}
	if name == "" {
		if home, err := os.UserHomeDir(); err == nil {
			var cfg struct {
				Timezone string `json:"timezone"`
			}
			if b, err := os.ReadFile(filepath.Join(home, ".config/rmkit-cn/space.json")); err == nil {
				_ = json.Unmarshal(b, &cfg)
				name = cfg.Timezone
			}
		}
	}
	if name == "" {
		name = defaultTimezone
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		// unknown zone name: fall back to the default (tzdata is embedded, so this load succeeds)
		name = defaultTimezone
		if loc, err = time.LoadLocation(name); err != nil {
			return name, 60
		}
	}
	_, off := time.Now().In(loc).Zone()
	return name, off / 60
}

func (h *Handler) timezones(w http.ResponseWriter, _ *http.Request) {
	type tz struct {
		Name   string `json:"name"`
		Zh     string `json:"zh"`
		En     string `json:"en"`
		Offset int    `json:"offset"`
	}
	now := time.Now()
	out := make([]tz, 0, len(tzChoices))
	for _, c := range tzChoices {
		loc, err := time.LoadLocation(c.Name)
		if err != nil {
			continue
		}
		_, off := now.In(loc).Zone()
		out = append(out, tz{c.Name, c.Zh, c.En, off / 60})
	}
	writeJSON(w, http.StatusOK, out)
}
