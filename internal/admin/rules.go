package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"csmock/internal/match"
	"csmock/internal/model"
	"csmock/internal/render"
)

func (s *Server) meta(w http.ResponseWriter, r *http.Request) {
	rules, err := s.store.ListRules()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "读取规则失败")
		return
	}
	_, total, err := s.store.ListLogs("", 1, 0)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "读取日志失败")
		return
	}
	enabled := 0
	for _, rule := range rules {
		if rule.Enabled {
			enabled++
		}
	}
	fallback, _, _ := s.store.GetSetting("proxy_fallback")
	writeJSON(w, http.StatusOK, map[string]any{
		"version":         s.info.Version,
		"mock_addr":       s.info.MockAddr,
		"admin_addr":      s.info.AdminAddr,
		"data_dir":        s.info.DataDir,
		"db_path":         s.info.DBPath,
		"rules_file":      s.info.RulesFile,
		"proxy_fallback":  fallback,
		"rule_count":      len(rules),
		"enabled_count":   enabled,
		"interface_count": interfaceCount(s),
		"log_count":       total,
	})
}

func interfaceCount(s *Server) int {
	items, err := s.store.ListInterfaces()
	if err != nil {
		return 0
	}
	return len(items)
}

func (s *Server) listRules(w http.ResponseWriter, r *http.Request) {
	rules, err := s.store.ListRules()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "读取规则失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rules": rules})
}

func (s *Server) createRule(w http.ResponseWriter, r *http.Request) {
	rule, err := decodeRuleRequest(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	rule.ID = 0
	if err := s.prepareRule(&rule); err != nil {
		writeErr(w, statusOf(err), err.Error())
		return
	}
	if err := s.store.CreateRule(&rule); err != nil {
		writeErr(w, statusOf(err), err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, rule)
}

func (s *Server) ruleByID(w http.ResponseWriter, r *http.Request, rest string) {
	id, extra, ok := parseID(rest)
	if !ok || (extra != "" && extra != "toggle") {
		writeErr(w, http.StatusNotFound, "接口不存在")
		return
	}
	if extra == "toggle" {
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "方法不允许")
			return
		}
		rule, err := s.store.ToggleRule(id)
		if err != nil {
			writeErr(w, statusOf(err), err.Error())
			return
		}
		writeJSON(w, http.StatusOK, rule)
		return
	}
	switch r.Method {
	case http.MethodGet:
		rule, err := s.store.GetRule(id)
		if err != nil {
			writeErr(w, statusOf(err), err.Error())
			return
		}
		writeJSON(w, http.StatusOK, rule)
	case http.MethodPut:
		rule, err := decodeRuleRequest(r)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		rule.ID = id
		if err := s.prepareRule(&rule); err != nil {
			writeErr(w, statusOf(err), err.Error())
			return
		}
		old, err := s.store.GetRule(id)
		if err != nil {
			writeErr(w, statusOf(err), err.Error())
			return
		}
		rule.CreatedAt = old.CreatedAt
		if err := s.store.UpdateRule(&rule); err != nil {
			writeErr(w, statusOf(err), err.Error())
			return
		}
		writeJSON(w, http.StatusOK, rule)
	case http.MethodDelete:
		if err := s.store.DeleteRule(id); err != nil {
			writeErr(w, statusOf(err), err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	default:
		writeErr(w, http.StatusMethodNotAllowed, "方法不允许")
	}
}

func (s *Server) tryMatch(w http.ResponseWriter, r *http.Request) {
	body, err := readBody(r, 2<<20)
	if err != nil || int64(len(body)) > 2<<20 {
		writeErr(w, http.StatusBadRequest, "请求过大")
		return
	}
	var in struct {
		Method  string            `json:"method"`
		URL     string            `json:"url"`
		Headers map[string]string `json:"headers"`
		Body    string            `json:"body"`
		Draft   json.RawMessage   `json:"draft"`
	}
	if err := json.Unmarshal(body, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "试匹配参数无法解析")
		return
	}
	path, rawQuery, query, err := splitTryURL(in.URL)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	rules, err := s.store.ListMatchRules()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "读取规则失败")
		return
	}
	if len(bytes.TrimSpace(in.Draft)) > 0 && !bytes.Equal(bytes.TrimSpace(in.Draft), []byte("null")) {
		draft, err := model.DecodeRule(in.Draft)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "草稿规则无法解析")
			return
		}
		if err := draft.Normalize(); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		draft.Enabled = true
		replaced := false
		if draft.ID > 0 {
			for i := range rules {
				if rules[i].ID == draft.ID {
					rules[i] = draft
					replaced = true
					break
				}
			}
		}
		if !replaced {
			rules = append(rules, draft)
		}
	}
	state, err := s.store.StateMap()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "读取状态失败")
		return
	}
	header := http.Header{}
	for k, v := range in.Headers {
		header.Set(k, v)
	}
	req := match.Request{
		Method: in.Method,
		Path:   path,
		Query:  query,
		Header: header,
		Body:   []byte(in.Body),
		State:  state,
	}
	hits := match.Collect(rules, req)
	names := make([]map[string]any, 0, len(hits))
	for _, hit := range hits {
		names = append(names, map[string]any{
			"id":       hit.Rule.ID,
			"name":     hit.Rule.Name,
			"priority": hit.Rule.Priority,
			"method":   hit.Rule.Method,
			"path":     hit.Rule.Path,
		})
	}
	if len(hits) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"matched": false, "matches": names})
		return
	}
	win := hits[0]
	data := render.Data{
		Method:   strings.ToUpper(in.Method),
		Path:     path,
		RawQuery: rawQuery,
		Query:    query,
		Params:   win.Params,
		Header:   header,
		Body:     []byte(in.Body),
		State:    state,
	}
	result := map[string]any{
		"matched": true,
		"rule":    win.Rule,
		"params":  win.Params,
		"matches": names,
		"mode":    win.Rule.Mode,
	}
	if win.Rule.Mode == "proxy" {
		result["status_code"] = 0
		result["body"] = "这条规则会转发到 " + win.Rule.ProxyURL
		writeJSON(w, http.StatusOK, result)
		return
	}
	headers := model.StringMap{}
	for k, v := range win.Rule.ResponseHeaders {
		headers[k] = render.Render(v, data)
	}
	result["status_code"] = win.Rule.StatusCode
	result["headers"] = headers
	result["body"] = render.Render(win.Rule.ResponseBody, data)
	writeJSON(w, http.StatusOK, result)
}

func splitTryURL(raw string) (string, string, url.Values, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", nil, errString("请填写请求路径")
	}
	if !strings.Contains(raw, "://") {
		if !strings.HasPrefix(raw, "/") {
			raw = "/" + raw
		}
		raw = "http://try.local" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Path == "" {
		return "", "", nil, errString("路径无法解析")
	}
	return u.Path, u.RawQuery, u.Query(), nil
}

type errString string

func (e errString) Error() string { return string(e) }

func decodeRuleRequest(r *http.Request) (model.Rule, error) {
	body, err := readBody(r, 2<<20)
	if err != nil {
		return model.Rule{}, errString("读取请求失败")
	}
	if int64(len(body)) > 2<<20 {
		return model.Rule{}, errString("规则内容过大")
	}
	rule, err := model.DecodeRule(body)
	if err != nil {
		return model.Rule{}, errString("规则 JSON 无法解析")
	}
	return rule, nil
}

func (s *Server) prepareRule(rule *model.Rule) error {
	if rule.InterfaceID > 0 {
		iface, err := s.store.GetInterface(rule.InterfaceID)
		if err != nil {
			return err
		}
		rule.Method = iface.Method
		rule.Path = iface.Path
	}
	return rule.Normalize()
}

func statusOf(err error) int {
	if err == nil {
		return http.StatusOK
	}
	if strings.Contains(err.Error(), "不存在") {
		return http.StatusNotFound
	}
	return http.StatusBadRequest
}

func atoiDefault(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}
