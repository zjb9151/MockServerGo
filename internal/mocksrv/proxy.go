package mocksrv

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"csmock/internal/model"
)

var proxyClient = &http.Client{
	Timeout: 60 * time.Second,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

func joinURL(base, path, rawQuery string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(base))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("代理地址无效")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("代理地址仅支持 http 或 https")
	}
	if path == "" {
		path = "/"
	}
	u.Path = singleJoin(u.Path, path)
	u.RawPath = ""
	u.RawQuery = rawQuery
	u.Fragment = ""
	return u.String(), nil
}

func singleJoin(basePath, reqPath string) string {
	if basePath == "" || basePath == "/" {
		if strings.HasPrefix(reqPath, "/") {
			return reqPath
		}
		return "/" + reqPath
	}
	switch {
	case strings.HasSuffix(basePath, "/") && strings.HasPrefix(reqPath, "/"):
		return basePath + reqPath[1:]
	case !strings.HasSuffix(basePath, "/") && !strings.HasPrefix(reqPath, "/"):
		return basePath + "/" + reqPath
	default:
		return basePath + reqPath
	}
}

func forward(r *http.Request, body []byte, target string) (int, http.Header, []byte, error) {
	var reader io.Reader
	if len(body) > 0 {
		reader = bytes.NewReader(body)
	}
	out, err := http.NewRequestWithContext(r.Context(), r.Method, target, reader)
	if err != nil {
		return 0, nil, nil, err
	}
	copyRequestHeaders(out.Header, r.Header)
	if len(body) > 0 {
		out.ContentLength = int64(len(body))
	}
	resp, err := proxyClient.Do(out)
	if err != nil {
		return 0, nil, nil, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxProxyBody+1))
	if err != nil {
		return 0, nil, nil, err
	}
	if len(respBody) > maxProxyBody {
		return 0, nil, nil, fmt.Errorf("上游响应超过 8MB")
	}
	return resp.StatusCode, resp.Header, respBody, nil
}

func copyRequestHeaders(dst, src http.Header) {
	drop := map[string]struct{}{}
	for _, v := range src.Values("Connection") {
		for _, part := range strings.Split(v, ",") {
			name := strings.TrimSpace(part)
			if name != "" {
				drop[http.CanonicalHeaderKey(name)] = struct{}{}
			}
		}
	}
	for k, vals := range src {
		ck := http.CanonicalHeaderKey(k)
		if _, ok := drop[ck]; ok || skipForwardHeader(ck) {
			continue
		}
		for _, v := range vals {
			dst.Add(ck, v)
		}
	}
}

func skipForwardHeader(k string) bool {
	switch http.CanonicalHeaderKey(k) {
	case "Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization",
		"Te", "Trailer", "Transfer-Encoding", "Upgrade", "Host", "Content-Length", "Accept-Encoding":
		return true
	default:
		return false
	}
}

func flattenHeaders(h http.Header) model.StringMap {
	out := model.StringMap{}
	for k, vals := range h {
		if skipResponseHeader(k) || len(vals) == 0 {
			continue
		}
		out[k] = strings.Join(vals, ", ")
	}
	return out
}
