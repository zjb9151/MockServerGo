package render

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var tokenRe = regexp.MustCompile(`\{\{\s*([^}]+?)\s*\}\}`)

// Data 是渲染响应模板时能读到的请求信息。
type Data struct {
	Method   string
	Path     string
	RawQuery string
	Query    url.Values
	Params   map[string]string
	Header   http.Header
	Body     []byte
	State    map[string]string
}

// Render 替换 {{query.id}}、{{params.id}}、{{header.X}}、{{body.a.b}} 等变量。
func Render(s string, d Data) string {
	if !strings.Contains(s, "{{") {
		return s
	}
	doc, parsed := parseBody(d.Body)
	return tokenRe.ReplaceAllStringFunc(s, func(all string) string {
		inner := strings.TrimSpace(all[2 : len(all)-2])
		val, ok := resolve(inner, d, doc, parsed)
		if !ok {
			return all
		}
		return val
	})
}

func resolve(token string, d Data, doc any, parsed bool) (string, bool) {
	switch token {
	case "method":
		return d.Method, true
	case "path":
		return d.Path, true
	case "rawquery":
		return d.RawQuery, true
	case "uuid":
		return newUUID(), true
	case "now":
		return time.Now().Format(time.RFC3339), true
	}
	scope, key, ok := strings.Cut(token, ".")
	if !ok || key == "" {
		return "", false
	}
	switch scope {
	case "query":
		if d.Query == nil {
			return "", true
		}
		return d.Query.Get(key), true
	case "params":
		if d.Params == nil {
			return "", true
		}
		return d.Params[key], true
	case "header":
		return headerGet(d.Header, key), true
	case "state":
		if d.State == nil {
			return "", true
		}
		return d.State[key], true
	case "body":
		if !parsed {
			return "", true
		}
		val, found := lookup(doc, key)
		if !found {
			return "", true
		}
		return stringify(val), true
	default:
		return "", false
	}
}

func headerGet(h http.Header, key string) string {
	if h == nil {
		return ""
	}
	for k, vals := range h {
		if strings.EqualFold(k, key) && len(vals) > 0 {
			return vals[0]
		}
	}
	return ""
}

func parseBody(body []byte) (any, bool) {
	trim := bytes.TrimSpace(body)
	if len(trim) == 0 {
		return nil, false
	}
	dec := json.NewDecoder(bytes.NewReader(trim))
	dec.UseNumber()
	var doc any
	if err := dec.Decode(&doc); err != nil {
		return nil, false
	}
	return doc, true
}

func lookup(v any, path string) (any, bool) {
	cur := v
	for _, p := range strings.Split(path, ".") {
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

func newUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ""
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
