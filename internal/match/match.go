package match

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"csmock/internal/model"
)

// Request 是参与匹配的请求快照。
type Request struct {
	Method string
	Path   string
	Query  url.Values
	Header http.Header
	Body   []byte
	State  map[string]string
}

// Hit 是一条命中的规则以及路径参数。
type Hit struct {
	Rule   model.Rule
	Params map[string]string
	rank   rank
}

type rank struct {
	priority int
	explicit int
	staticN  int
	conds    int
	id       int64
}

// Collect 返回全部命中的规则，按生效顺序排列。
func Collect(rules []model.Rule, req Request) []Hit {
	hits := make([]Hit, 0)
	for i := range rules {
		rule := rules[i]
		if !rule.Enabled {
			continue
		}
		explicit, ok := methodOK(rule.Method, req.Method)
		if !ok {
			continue
		}
		params, ok := MatchPath(rule.Path, req.Path)
		if !ok {
			continue
		}
		if !matchConds(rule.QueryMatch, req.Query) {
			continue
		}
		if !matchHeader(rule.HeaderMatch, req.Header) {
			continue
		}
		if !matchBody(rule.BodyMatch, req.Body) {
			continue
		}
		if !matchState(rule.StateMatch, req.State) {
			continue
		}
		if params == nil {
			params = map[string]string{}
		}
		ex := 0
		if explicit {
			ex = 1
		}
		hits = append(hits, Hit{
			Rule:   rule,
			Params: params,
			rank: rank{
				priority: rule.Priority,
				explicit: ex,
				staticN:  staticCount(rule.Path),
				conds:    len(rule.QueryMatch) + len(rule.HeaderMatch) + len(rule.BodyMatch) + len(rule.StateMatch),
				id:       rule.ID,
			},
		})
	}
	sortHits(hits)
	return hits
}

// Find 返回实际会生效的那条规则。
func Find(rules []model.Rule, req Request) (Hit, bool) {
	hits := Collect(rules, req)
	if len(hits) == 0 {
		return Hit{}, false
	}
	return hits[0], true
}

func sortHits(hits []Hit) {
	for i := 1; i < len(hits); i++ {
		j := i
		for j > 0 && less(hits[j-1], hits[j]) {
			hits[j-1], hits[j] = hits[j], hits[j-1]
			j--
		}
	}
}

func less(a, b Hit) bool {
	if a.rank.priority != b.rank.priority {
		return a.rank.priority < b.rank.priority
	}
	if a.rank.explicit != b.rank.explicit {
		return a.rank.explicit < b.rank.explicit
	}
	if a.rank.staticN != b.rank.staticN {
		return a.rank.staticN < b.rank.staticN
	}
	if a.rank.conds != b.rank.conds {
		return a.rank.conds < b.rank.conds
	}
	return a.rank.id < b.rank.id
}

func methodOK(ruleMethod, reqMethod string) (explicit bool, ok bool) {
	m := strings.ToUpper(strings.TrimSpace(ruleMethod))
	reqMethod = strings.ToUpper(strings.TrimSpace(reqMethod))
	if m == "" || m == "*" || m == "ANY" {
		return false, true
	}
	for _, part := range strings.Split(m, ",") {
		if strings.TrimSpace(part) == reqMethod {
			return true, true
		}
	}
	return true, false
}

// MatchPath 同时支持 :id 与 {id}。
func MatchPath(pattern, actual string) (map[string]string, bool) {
	ps := splitPath(pattern)
	as := splitPath(actual)
	if len(ps) != len(as) {
		return nil, false
	}
	params := make(map[string]string, len(ps))
	for i := range ps {
		name, isParam := paramName(ps[i])
		if isParam {
			if as[i] == "" || strings.Contains(name, "/") {
				return nil, false
			}
			params[name] = as[i]
			continue
		}
		if ps[i] != as[i] {
			return nil, false
		}
	}
	return params, true
}

func splitPath(p string) []string {
	if p == "" {
		p = "/"
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	if len(p) > 1 {
		p = strings.TrimSuffix(p, "/")
	}
	if p == "/" {
		return []string{}
	}
	return strings.Split(strings.Trim(p, "/"), "/")
}

func paramName(seg string) (string, bool) {
	if strings.HasPrefix(seg, ":") && len(seg) > 1 {
		return seg[1:], true
	}
	if strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}") && len(seg) > 2 {
		return seg[1 : len(seg)-1], true
	}
	return "", false
}

func staticCount(pattern string) int {
	n := 0
	for _, seg := range splitPath(pattern) {
		if _, ok := paramName(seg); !ok {
			n++
		}
	}
	return n
}

func matchConds(conds []model.Condition, q url.Values) bool {
	for _, c := range conds {
		if !matchValues(c, q[c.Key]) {
			return false
		}
	}
	return true
}

func matchHeader(conds []model.Condition, h http.Header) bool {
	if h == nil {
		h = http.Header{}
	}
	for _, c := range conds {
		if !matchValues(c, headerValues(h, c.Key)) {
			return false
		}
	}
	return true
}

func headerValues(h http.Header, key string) []string {
	var out []string
	for k, vals := range h {
		if strings.EqualFold(k, key) {
			out = append(out, vals...)
		}
	}
	return out
}

func matchValues(c model.Condition, vals []string) bool {
	op := strings.ToLower(strings.TrimSpace(c.Op))
	if op == "" {
		op = "eq"
	}
	present := len(vals) > 0
	switch op {
	case "exists":
		return present
	case "not_exists":
		return !present
	}
	if !present {
		return false
	}
	if op == "ne" || op == "neq" {
		for _, v := range vals {
			if !compare(op, v, c.Value) {
				return false
			}
		}
		return true
	}
	for _, v := range vals {
		if compare(op, v, c.Value) {
			return true
		}
	}
	return false
}

func matchState(conds []model.Condition, state map[string]string) bool {
	for _, c := range conds {
		val, ok := state[c.Key]
		op := strings.ToLower(strings.TrimSpace(c.Op))
		if op == "exists" {
			if !ok {
				return false
			}
			continue
		}
		if op == "not_exists" {
			if ok {
				return false
			}
			continue
		}
		if !ok || !compare(op, val, c.Value) {
			return false
		}
	}
	return true
}

func matchBody(conds []model.Condition, body []byte) bool {
	if len(conds) == 0 {
		return true
	}
	var doc any
	parsed := false
	trim := bytes.TrimSpace(body)
	if len(trim) > 0 {
		dec := json.NewDecoder(bytes.NewReader(trim))
		dec.UseNumber()
		if err := dec.Decode(&doc); err == nil {
			parsed = true
		}
	}
	for _, c := range conds {
		op := strings.ToLower(strings.TrimSpace(c.Op))
		if c.Key == "$raw" {
			switch op {
			case "exists":
				if len(body) == 0 {
					return false
				}
			case "not_exists":
				if len(body) > 0 {
					return false
				}
			default:
				if !compare(op, string(body), c.Value) {
					return false
				}
			}
			continue
		}
		if !parsed {
			if op == "not_exists" {
				continue
			}
			return false
		}
		val, ok := lookup(doc, c.Key)
		if op == "exists" {
			if !ok {
				return false
			}
			continue
		}
		if op == "not_exists" {
			if ok {
				return false
			}
			continue
		}
		if !ok || !compare(op, stringify(val), c.Value) {
			return false
		}
	}
	return true
}

func lookup(v any, path string) (any, bool) {
	cur := v
	for _, p := range strings.Split(path, ".") {
		if p == "" {
			return nil, false
		}
		switch node := cur.(type) {
		case map[string]any:
			child, ok := node[p]
			if !ok {
				return nil, false
			}
			cur = child
		case []any:
			i, err := strconv.Atoi(p)
			if err != nil || i < 0 || i >= len(node) {
				return nil, false
			}
			cur = node[i]
		default:
			return nil, false
		}
	}
	return cur, true
}

func stringify(v any) string {
	switch t := v.(type) {
	case nil:
		return "null"
	case string:
		return t
	case json.Number:
		return t.String()
	case bool:
		if t {
			return "true"
		}
		return "false"
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return ""
		}
		return string(b)
	}
}

func compare(op, actual, expect string) bool {
	switch op {
	case "", "eq":
		return actual == expect
	case "ne", "neq":
		return actual != expect
	case "ieq":
		return strings.EqualFold(actual, expect)
	case "contains":
		return strings.Contains(actual, expect)
	case "prefix":
		return strings.HasPrefix(actual, expect)
	case "suffix":
		return strings.HasSuffix(actual, expect)
	case "regex":
		re, err := regexp.Compile(expect)
		if err != nil {
			return false
		}
		return re.MatchString(actual)
	case "gt", "lt", "gte", "lte":
		return cmpOrdered(op, actual, expect)
	default:
		return false
	}
}

func cmpOrdered(op, a, b string) bool {
	fa, ea := strconv.ParseFloat(a, 64)
	fb, eb := strconv.ParseFloat(b, 64)
	var c int
	if ea == nil && eb == nil {
		switch {
		case fa < fb:
			c = -1
		case fa > fb:
			c = 1
		default:
			c = 0
		}
	} else {
		c = strings.Compare(a, b)
	}
	switch op {
	case "gt":
		return c > 0
	case "lt":
		return c < 0
	case "gte":
		return c >= 0
	case "lte":
		return c <= 0
	default:
		return false
	}
}
