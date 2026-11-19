package live

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"csmock/internal/model"
)

// Hub 把每次请求的输入输出推给正在看实时监控的连接。
type Hub struct {
	mu   sync.Mutex
	subs map[chan model.RequestLog]struct{}
}

func New() *Hub {
	return &Hub{subs: map[chan model.RequestLog]struct{}{}}
}

// Publish 非阻塞地分发一条记录。订阅者来不及读时丢弃这一条，避免拖住 Mock 请求。
func (h *Hub) Publish(entry model.RequestLog) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- entry:
		default:
		}
	}
}

// Subscribe 返回事件通道和取消函数。取消后通道不再收到新事件。
func (h *Hub) Subscribe() (<-chan model.RequestLog, func()) {
	ch := make(chan model.RequestLog, 32)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	var once sync.Once
	cancel := func() {
		once.Do(func() {
			h.mu.Lock()
			delete(h.subs, ch)
			h.mu.Unlock()
		})
	}
	return ch, cancel
}

// WriteConsole 把一次交互的输入和输出打到标准输出。
func WriteConsole(entry model.RequestLog) {
	when := entry.CreatedAt
	if t, err := time.Parse(time.RFC3339, entry.CreatedAt); err == nil {
		when = t.Local().Format("15:04:05")
	}
	rule := entry.RuleName
	if rule == "" {
		rule = "未匹配"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\n──── %s  %s %s", when, entry.Method, entry.Path)
	if entry.Query != "" {
		fmt.Fprintf(&b, "?%s", entry.Query)
	}
	fmt.Fprintf(&b, "  -> %d  %s  %dms\n", entry.StatusCode, rule, entry.DurationMS)
	fmt.Fprintf(&b, "输入\n%s", block(entry.Headers, entry.Body))
	fmt.Fprintf(&b, "输出\n%s", block(entry.ResponseHeaders, entry.ResponseBody))
	if entry.Error != "" {
		fmt.Fprintf(&b, "错误  %s\n", entry.Error)
	}
	log.Print(b.String())
}

func block(headers, body string) string {
	var b strings.Builder
	if strings.TrimSpace(headers) != "" && headers != "{}" {
		fmt.Fprintf(&b, "  Headers:\n%s", indent(pretty(headers)))
	}
	fmt.Fprintf(&b, "  Body:\n%s", indent(pretty(body)))
	return b.String()
}

func pretty(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "(空)\n"
	}
	var buf bytes.Buffer
	if json.Indent(&buf, []byte(raw), "", "  ") == nil {
		raw = buf.String()
	}
	const max = 4000
	if len(raw) > max {
		raw = raw[:max] + "\n…(控制台已截断，完整内容在监控页)"
	}
	return raw + "\n"
}

func indent(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, line := range lines {
		lines[i] = "    " + line
	}
	return strings.Join(lines, "\n") + "\n"
}
