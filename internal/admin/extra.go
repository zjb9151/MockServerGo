package admin

import (
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"strings"

	"csmock/internal/model"
)

func (s *Server) listLogs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	items, total, err := s.store.ListLogs(q.Get("q"), atoiDefault(q.Get("limit"), 20), atoiDefault(q.Get("offset"), 0))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "读取日志失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"logs": items, "total": total})
}

func (s *Server) clearLogs(w http.ResponseWriter, r *http.Request) {
	if err := s.store.ClearLogs(); err != nil {
		writeErr(w, http.StatusInternalServerError, "清空日志失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) logByID(w http.ResponseWriter, r *http.Request, rest string) {
	id, extra, ok := parseID(rest)
	if !ok || (extra != "" && extra != "adopt") {
		writeErr(w, http.StatusNotFound, "接口不存在")
		return
	}
	if extra == "adopt" {
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "方法不允许")
			return
		}
		rule, err := s.store.AdoptLog(id)
		if err != nil {
			writeErr(w, statusOf(err), err.Error())
			return
		}
		writeJSON(w, http.StatusOK, rule)
		return
	}
	switch r.Method {
	case http.MethodGet:
		item, err := s.store.GetLog(id)
		if err != nil {
			writeErr(w, statusOf(err), err.Error())
			return
		}
		writeJSON(w, http.StatusOK, item)
	case http.MethodDelete:
		if err := s.store.DeleteLog(id); err != nil {
			writeErr(w, statusOf(err), err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	default:
		writeErr(w, http.StatusMethodNotAllowed, "方法不允许")
	}
}

func (s *Server) listState(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListState()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "读取状态失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"states": items})
}

func (s *Server) putState(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	body, err := readBody(r, 1<<20)
	if err != nil || len(body) > 1<<20 {
		writeErr(w, http.StatusBadRequest, "请求过大")
		return
	}
	if err := json.Unmarshal(body, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "状态内容无法解析")
		return
	}
	in.Key = strings.TrimSpace(in.Key)
	if in.Key == "" || strings.Contains(in.Key, "/") {
		writeErr(w, http.StatusBadRequest, "状态键无效")
		return
	}
	if err := s.store.SetState(in.Key, in.Value); err != nil {
		writeErr(w, http.StatusInternalServerError, "保存状态失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"key": in.Key, "value": in.Value})
}

func (s *Server) clearState(w http.ResponseWriter, r *http.Request) {
	if err := s.store.ClearState(); err != nil {
		writeErr(w, http.StatusInternalServerError, "清空状态失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) stateByKey(w http.ResponseWriter, r *http.Request, key string) {
	if r.Method != http.MethodDelete {
		writeErr(w, http.StatusMethodNotAllowed, "方法不允许")
		return
	}
	key, err := url.PathUnescape(key)
	if err != nil || strings.TrimSpace(key) == "" || strings.Contains(key, "/") {
		writeErr(w, http.StatusBadRequest, "状态键无效")
		return
	}
	if err := s.store.DeleteState(key); err != nil {
		writeErr(w, statusOf(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) exportRules(w http.ResponseWriter, r *http.Request) {
	rules, err := s.store.ListRules()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "读取规则失败")
		return
	}
	format := r.URL.Query().Get("format")
	doc := model.Document{Replace: false, Rules: rules}
	b, err := model.EncodeDocument(doc, format)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "导出失败")
		return
	}
	filename := "csmock-rules.json"
	contentType := "application/json; charset=utf-8"
	if strings.EqualFold(format, "yaml") || strings.EqualFold(format, "yml") {
		filename = "csmock-rules.yaml"
		contentType = "application/yaml; charset=utf-8"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", "attachment; filename=\""+filename+"\"")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(b)
}

func (s *Server) importRules(w http.ResponseWriter, r *http.Request) {
	body, err := readBody(r, 8<<20)
	if err != nil || len(body) > 8<<20 {
		writeErr(w, http.StatusBadRequest, "导入内容过大")
		return
	}
	doc, err := model.DecodeDocument(body)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	replace := doc.Replace
	switch r.URL.Query().Get("replace") {
	case "1", "true":
		replace = true
	case "0", "false":
		replace = false
	}
	result, err := s.store.Import(doc, replace)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	saved, err := s.store.Settings()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "读取设置失败")
		return
	}
	mockAddr := saved["mock_addr"]
	adminAddr := saved["admin_addr"]
	if mockAddr == "" {
		mockAddr = s.info.MockAddr
	}
	if adminAddr == "" {
		adminAddr = s.info.AdminAddr
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"mock_addr":         mockAddr,
		"admin_addr":        adminAddr,
		"proxy_fallback":    saved["proxy_fallback"],
		"mock_addr_active":  s.info.MockAddr,
		"admin_addr_active": s.info.AdminAddr,
	})
}

func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	body, err := readBody(r, 1<<20)
	if err != nil || len(body) > 1<<20 {
		writeErr(w, http.StatusBadRequest, "请求过大")
		return
	}
	var in struct {
		MockAddr      string `json:"mock_addr"`
		AdminAddr     string `json:"admin_addr"`
		ProxyFallback string `json:"proxy_fallback"`
	}
	if err := json.Unmarshal(body, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "设置内容无法解析")
		return
	}
	in.MockAddr = strings.TrimSpace(in.MockAddr)
	in.AdminAddr = strings.TrimSpace(in.AdminAddr)
	in.ProxyFallback = strings.TrimSpace(in.ProxyFallback)
	if _, _, err := net.SplitHostPort(in.MockAddr); err != nil {
		writeErr(w, http.StatusBadRequest, "Mock 监听地址需要是 host:port，例如 0.0.0.0:8080")
		return
	}
	if _, _, err := net.SplitHostPort(in.AdminAddr); err != nil {
		writeErr(w, http.StatusBadRequest, "管理页监听地址需要是 host:port，例如 127.0.0.1:9090")
		return
	}
	if in.ProxyFallback != "" {
		u, err := url.Parse(in.ProxyFallback)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			writeErr(w, http.StatusBadRequest, "兜底代理需要是完整的 http 或 https 地址")
			return
		}
	}
	if err := s.store.SetSetting("mock_addr", in.MockAddr); err != nil {
		writeErr(w, http.StatusInternalServerError, "保存设置失败")
		return
	}
	if err := s.store.SetSetting("admin_addr", in.AdminAddr); err != nil {
		writeErr(w, http.StatusInternalServerError, "保存设置失败")
		return
	}
	if err := s.store.SetSetting("proxy_fallback", in.ProxyFallback); err != nil {
		writeErr(w, http.StatusInternalServerError, "保存设置失败")
		return
	}
	s.getSettings(w, r)
}
