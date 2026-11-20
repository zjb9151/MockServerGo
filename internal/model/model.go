package model

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

const Version = "1.0.0"

// Condition 是一条匹配条件。Key 为参数名、Header 名或 JSON 路径。
// Join 只用于 Query 的第二条及之后，表示和上一条是 and 还是 or。空值按 and。
type Condition struct {
	Join  string `json:"join,omitempty"`
	Key   string `json:"key"`
	Op    string `json:"op"`
	Value string `json:"value"`
}

func (c *Condition) UnmarshalJSON(b []byte) error {
	var aux struct {
		Join  string     `json:"join"`
		Key   string     `json:"key"`
		Op    string     `json:"op"`
		Value FlexString `json:"value"`
	}
	if err := json.Unmarshal(b, &aux); err != nil {
		return err
	}
	c.Join = aux.Join
	c.Key = aux.Key
	c.Op = aux.Op
	c.Value = string(aux.Value)
	return nil
}

// StringMap 在 JSON/YAML 里允许数字、布尔写成无引号标量。
type StringMap map[string]string

func (m *StringMap) UnmarshalJSON(b []byte) error {
	if bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
		*m = StringMap{}
		return nil
	}
	var raw map[string]FlexString
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	out := make(StringMap, len(raw))
	for k, v := range raw {
		out[k] = string(v)
	}
	*m = out
	return nil
}

func (m StringMap) MarshalJSON() ([]byte, error) {
	if m == nil {
		return []byte("{}"), nil
	}
	return json.Marshal(map[string]string(m))
}

// Rule 是一条 Mock 规则。
type Rule struct {
	ID              int64       `json:"id"`
	Name            string      `json:"name"`
	Enabled         bool        `json:"enabled"`
	Priority        int         `json:"priority"`
	Method          string      `json:"method"`
	Path            string      `json:"path"`
	QueryMatch      []Condition `json:"query_match"`
	HeaderMatch     []Condition `json:"header_match"`
	BodyMatch       []Condition `json:"body_match"`
	StateMatch      []Condition `json:"state_match"`
	StatusCode      int         `json:"status_code"`
	ResponseHeaders StringMap   `json:"response_headers"`
	ResponseBody    string      `json:"response_body"`
	DelayMS         int         `json:"delay_ms"`
	Mode            string      `json:"mode"`
	ProxyURL        string      `json:"proxy_url"`
	Record          bool        `json:"record"`
	StateSet        StringMap   `json:"state_set"`
	Description     string      `json:"description"`
	CreatedAt       string      `json:"created_at"`
	UpdatedAt       string      `json:"updated_at"`
	InterfaceID     int64       `json:"interface_id"`
	Flow            *Flow       `json:"flow,omitempty"`
}

// Interface 是一个可被调用的接口，下面可以挂多条规则。
type Interface struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Enabled     bool   `json:"enabled"`
	Method      string `json:"method"`
	Path        string `json:"path"`
	Description string `json:"description"`
	RuleCount   int    `json:"rule_count"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

// RequestLog 记录一次打到 Mock 端口的请求。
type RequestLog struct {
	ID              int64  `json:"id"`
	RuleID          *int64 `json:"rule_id"`
	RuleName        string `json:"rule_name"`
	Method          string `json:"method"`
	Path            string `json:"path"`
	Query           string `json:"query"`
	Headers         string `json:"headers"`
	Body            string `json:"body"`
	StatusCode      int    `json:"status_code"`
	Matched         bool   `json:"matched"`
	RemoteAddr      string `json:"remote_addr"`
	DurationMS      int64  `json:"duration_ms"`
	ResponseHeaders string `json:"response_headers"`
	ResponseBody    string `json:"response_body"`
	Error           string `json:"error"`
	CreatedAt       string `json:"created_at"`
}

// StateVar 是场景状态里的一个键。
type StateVar struct {
	Key       string `json:"key"`
	Value     string `json:"value"`
	UpdatedAt string `json:"updated_at"`
}

// FlexString 把 JSON 数字和布尔收成字符串，方便 YAML 里不写引号。
type FlexString string

func (f *FlexString) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || bytes.Equal(b, []byte("null")) {
		*f = ""
		return nil
	}
	if b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*f = FlexString(s)
		return nil
	}
	*f = FlexString(b)
	return nil
}

var validOps = map[string]struct{}{
	"eq": {}, "ne": {}, "neq": {}, "ieq": {},
	"contains": {}, "prefix": {}, "suffix": {}, "regex": {},
	"gt": {}, "gte": {}, "lt": {}, "lte": {},
	"exists": {}, "not_exists": {},
}

// FillDefaults 把空切片和空 map 补成可用的零值，避免 JSON 出现 null。
func (r *Rule) FillDefaults() {
	if r.QueryMatch == nil {
		r.QueryMatch = []Condition{}
	}
	if r.HeaderMatch == nil {
		r.HeaderMatch = []Condition{}
	}
	if r.BodyMatch == nil {
		r.BodyMatch = []Condition{}
	}
	if r.StateMatch == nil {
		r.StateMatch = []Condition{}
	}
	if r.ResponseHeaders == nil {
		r.ResponseHeaders = StringMap{}
	}
	if r.StateSet == nil {
		r.StateSet = StringMap{}
	}
}

// Normalize 校正并校验规则，供保存前调用。
func (r *Rule) Normalize() error {
	r.Name = strings.TrimSpace(r.Name)
	if r.Name == "" {
		r.Name = "未命名规则"
	}
	r.Method = strings.ToUpper(strings.TrimSpace(r.Method))
	if r.Method == "" {
		r.Method = "*"
	}
	for _, part := range strings.Split(r.Method, ",") {
		p := strings.TrimSpace(part)
		if p == "" {
			return fmt.Errorf("HTTP 方法不能为空")
		}
	}
	r.Method = joinMethod(r.Method)

	r.Path = strings.TrimSpace(r.Path)
	if r.Path == "" || !strings.HasPrefix(r.Path, "/") {
		return fmt.Errorf("路径必须以 / 开头")
	}
	if strings.ContainsAny(r.Path, " ?#") {
		return fmt.Errorf("路径不能包含空格、? 或 #，查询参数请写到匹配条件里")
	}

	if r.StatusCode == 0 {
		r.StatusCode = 200
	}
	if r.StatusCode < 100 || r.StatusCode > 599 {
		return fmt.Errorf("状态码必须在 100 到 599 之间")
	}
	if r.DelayMS < 0 {
		return fmt.Errorf("延迟不能为负数")
	}
	if r.DelayMS > 300000 {
		return fmt.Errorf("延迟不能超过 300000 毫秒")
	}
	if r.Priority < -100000 || r.Priority > 100000 {
		return fmt.Errorf("优先级超出范围")
	}

	r.Mode = strings.ToLower(strings.TrimSpace(r.Mode))
	if r.Mode == "" {
		r.Mode = "mock"
	}
	if r.Mode != "mock" && r.Mode != "proxy" {
		return fmt.Errorf("模式只能是 mock 或 proxy")
	}
	r.ProxyURL = strings.TrimSpace(r.ProxyURL)
	if r.Mode == "proxy" {
		u, err := url.Parse(r.ProxyURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("代理地址需要是完整的 http 或 https URL")
		}
	}

	r.FillDefaults()
	if err := normalizeConds("Query", r.QueryMatch); err != nil {
		return err
	}
	if err := normalizeConds("Header", r.HeaderMatch); err != nil {
		return err
	}
	if err := normalizeConds("Body", r.BodyMatch); err != nil {
		return err
	}
	if err := normalizeConds("状态", r.StateMatch); err != nil {
		return err
	}

	cleanHeaders := StringMap{}
	for k, v := range r.ResponseHeaders {
		k = strings.TrimSpace(k)
		if k == "" {
			return fmt.Errorf("响应 Header 名不能为空")
		}
		cleanHeaders[k] = v
	}
	r.ResponseHeaders = cleanHeaders

	cleanState := StringMap{}
	for k, v := range r.StateSet {
		k = strings.TrimSpace(k)
		if k == "" {
			return fmt.Errorf("状态键不能为空")
		}
		if strings.Contains(k, "/") {
			return fmt.Errorf("状态键不能包含 /")
		}
		cleanState[k] = v
	}
	r.StateSet = cleanState
	r.Description = strings.TrimSpace(r.Description)
	return r.normalizeFlow()
}

func joinMethod(method string) string {
	parts := strings.Split(method, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, strings.TrimSpace(p))
	}
	return strings.Join(out, ",")
}

func normalizeConds(label string, conds []Condition) error {
	for i := range conds {
		conds[i].Key = strings.TrimSpace(conds[i].Key)
		conds[i].Op = strings.ToLower(strings.TrimSpace(conds[i].Op))
		if conds[i].Op == "" {
			conds[i].Op = "eq"
		}
		join := strings.ToLower(strings.TrimSpace(conds[i].Join))
		if label != "Query" || i == 0 {
			join = ""
		} else if join == "" || join == "and" || join == "&&" {
			join = "and"
		} else if join != "or" {
			return fmt.Errorf("%s 匹配条件的关系只能是 and 或 or", label)
		}
		conds[i].Join = join
		if conds[i].Key == "" {
			return fmt.Errorf("%s 匹配条件缺少字段名", label)
		}
		if _, ok := validOps[conds[i].Op]; !ok {
			return fmt.Errorf("%s 匹配不支持操作 %s", label, conds[i].Op)
		}
		if conds[i].Op == "regex" {
			if _, err := regexp.Compile(conds[i].Value); err != nil {
				return fmt.Errorf("%s 匹配的正则无效: %s", label, err.Error())
			}
		}
	}
	return nil
}

// DecodeRule 解析一条规则。未提供 enabled 时默认启用。
func DecodeRule(b []byte) (Rule, error) {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(b, &probe); err != nil {
		return Rule{}, err
	}
	var rule Rule
	if err := json.Unmarshal(b, &rule); err != nil {
		return Rule{}, err
	}
	if _, ok := probe["enabled"]; !ok {
		rule.Enabled = true
	}
	rule.FillDefaults()
	return rule, nil
}
