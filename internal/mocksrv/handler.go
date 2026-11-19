package mocksrv

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"csmock/internal/live"
	"csmock/internal/match"
	"csmock/internal/model"
	"csmock/internal/render"
	"csmock/internal/store"
)

const (
	maxRequestBody = 2 << 20
	maxProxyBody   = 8 << 20
	maxRecordBody  = 256 << 10
)

// Handler 是 Mock 端口的请求入口。
type Handler struct {
	store *store.Store
	hub   *live.Hub
}

func New(st *store.Store, hub *live.Hub) http.Handler {
	return &Handler{store: st, hub: hub}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	body, tooBig := readBody(w, r)
	if tooBig {
		return
	}

	entry := model.RequestLog{
		Method:     r.Method,
		Path:       requestPath(r),
		Query:      r.URL.RawQuery,
		Headers:    headerJSON(r.Header),
		Body:       string(body),
		RemoteAddr: r.RemoteAddr,
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
	}
	defer func() {
		entry.DurationMS = time.Since(start).Milliseconds()
		entry.Body = clipText(entry.Body, 64<<10)
		entry.ResponseBody = clipText(entry.ResponseBody, 256<<10)
		id, err := h.store.InsertLog(entry)
		if err != nil {
			log.Printf("写入请求日志失败: %v", err)
		} else {
			entry.ID = id
		}
		if h.hub != nil {
			h.hub.Publish(entry)
			live.WriteConsole(entry)
		}
	}()

	rules, err := h.store.ListMatchRules()
	if err != nil {
		entry.Error = err.Error()
		entry.StatusCode = http.StatusInternalServerError
		writeJSON(w, entry.StatusCode, map[string]string{"error": "读取规则失败"})
		return
	}
	state, err := h.store.StateMap()
	if err != nil {
		entry.Error = err.Error()
		entry.StatusCode = http.StatusInternalServerError
		writeJSON(w, entry.StatusCode, map[string]string{"error": "读取状态失败"})
		return
	}

	req := match.Request{
		Method: r.Method,
		Path:   requestPath(r),
		Query:  r.URL.Query(),
		Header: r.Header,
		Body:   body,
		State:  state,
	}
	hit, ok := match.Find(rules, req)
	data := render.Data{
		Method:   r.Method,
		Path:     requestPath(r),
		RawQuery: r.URL.RawQuery,
		Query:    r.URL.Query(),
		Header:   r.Header,
		Body:     body,
		State:    state,
	}

	if !ok {
		fallback, _, ferr := h.store.GetSetting("proxy_fallback")
		if ferr != nil {
			entry.Error = ferr.Error()
			entry.StatusCode = http.StatusInternalServerError
			writeJSON(w, entry.StatusCode, map[string]string{"error": "读取代理设置失败"})
			return
		}
		if strings.TrimSpace(fallback) != "" {
			h.serveProxy(w, r, body, strings.TrimSpace(fallback), nil, &entry)
			return
		}
		entry.StatusCode = http.StatusNotFound
		payload := map[string]string{
			"error":  "没有匹配的 Mock 规则",
			"method": r.Method,
			"path":   requestPath(r),
		}
		raw, _ := json.Marshal(payload)
		entry.ResponseBody = string(raw)
		entry.ResponseHeaders = `{"Content-Type":"application/json; charset=utf-8"}`
		writeJSON(w, entry.StatusCode, payload)
		return
	}

	rule := hit.Rule
	entry.Matched = true
	entry.RuleName = rule.Name
	id := rule.ID
	entry.RuleID = &id
	data.Params = hit.Params

	if err := sleepContext(r.Context(), time.Duration(rule.DelayMS)*time.Millisecond); err != nil {
		entry.Error = "客户端已断开"
		entry.StatusCode = 499
		return
	}

	if rule.Mode == "proxy" {
		h.serveProxy(w, r, body, rule.ProxyURL, &rule, &entry)
		if entry.Error == "" {
			h.applyState(rule, data)
		}
		return
	}

	headers := model.StringMap{}
	for k, v := range rule.ResponseHeaders {
		headers[k] = render.Render(v, data)
	}
	respBody := render.Render(rule.ResponseBody, data)
	if !hasHeader(headers, "Content-Type") {
		headers["Content-Type"] = sniffType([]byte(respBody))
	}
	entry.StatusCode = rule.StatusCode
	entry.ResponseBody = respBody
	hb, _ := json.Marshal(headers)
	entry.ResponseHeaders = string(hb)
	h.applyState(rule, data)
	writeReady(w, rule.StatusCode, headers, []byte(respBody))
}

func (h *Handler) serveProxy(w http.ResponseWriter, r *http.Request, body []byte, base string, rule *model.Rule, entry *model.RequestLog) {
	target, err := joinURL(base, requestPath(r), r.URL.RawQuery)
	if err != nil {
		entry.Error = err.Error()
		entry.StatusCode = http.StatusBadGateway
		writeJSON(w, entry.StatusCode, map[string]string{"error": err.Error()})
		return
	}
	status, hdr, respBody, err := forward(r, body, target)
	if err != nil {
		entry.Error = err.Error()
		entry.StatusCode = http.StatusBadGateway
		writeJSON(w, entry.StatusCode, map[string]string{"error": "代理请求失败: " + err.Error()})
		return
	}
	flat := flattenHeaders(hdr)
	entry.StatusCode = status
	entry.ResponseBody = string(respBody)
	hb, _ := json.Marshal(flat)
	entry.ResponseHeaders = string(hb)
	if rule != nil && rule.Record {
		rec := respBody
		if len(rec) > maxRecordBody {
			rec = rec[:maxRecordBody]
		}
		if err := h.store.SaveRecording(rule.ID, status, flat, string(rec)); err != nil {
			log.Printf("录制响应失败: %v", err)
		}
	}
	writeProxy(w, status, hdr, respBody)
}

func (h *Handler) applyState(rule model.Rule, data render.Data) {
	if len(rule.StateSet) == 0 {
		return
	}
	pairs := make(map[string]string, len(rule.StateSet))
	for k, v := range rule.StateSet {
		pairs[k] = render.Render(v, data)
	}
	if err := h.store.SetStatePairs(pairs); err != nil {
		log.Printf("写入场景状态失败: %v", err)
	}
}

func readBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	if r.Body == nil {
		return nil, false
	}
	defer r.Body.Close()
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "请求体超过 2MB"})
		return nil, true
	}
	return body, false
}

func requestPath(r *http.Request) string {
	if r.URL == nil || r.URL.Path == "" {
		return "/"
	}
	return r.URL.Path
}

func headerJSON(h http.Header) string {
	if len(h) == 0 {
		return "{}"
	}
	b, err := json.Marshal(h)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func writeReady(w http.ResponseWriter, status int, headers model.StringMap, body []byte) {
	for k, v := range headers {
		if skipResponseHeader(k) {
			continue
		}
		w.Header().Set(k, v)
	}
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", sniffType(body))
	}
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func writeProxy(w http.ResponseWriter, status int, hdr http.Header, body []byte) {
	for k, vals := range hdr {
		if skipResponseHeader(k) {
			continue
		}
		for _, v := range vals {
			w.Header().Add(k, v)
		}
	}
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func sniffType(body []byte) string {
	t := bytes.TrimSpace(body)
	if len(t) == 0 {
		return "text/plain; charset=utf-8"
	}
	if t[0] == '{' || t[0] == '[' {
		return "application/json; charset=utf-8"
	}
	if bytes.HasPrefix(t, []byte("<?xml")) {
		return "application/xml; charset=utf-8"
	}
	return "text/plain; charset=utf-8"
}

func clipText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func hasHeader(h model.StringMap, name string) bool {
	for k := range h {
		if strings.EqualFold(k, name) {
			return true
		}
	}
	return false
}

func skipResponseHeader(k string) bool {
	switch strings.ToLower(k) {
	case "content-length", "transfer-encoding", "connection", "keep-alive", "proxy-connection", "trailer", "upgrade":
		return true
	default:
		return false
	}
}

func sleepContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
