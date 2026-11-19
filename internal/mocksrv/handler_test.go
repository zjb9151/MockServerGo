package mocksrv

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"csmock/internal/live"
	"csmock/internal/model"
	"csmock/internal/store"
)

func TestMockFlow(t *testing.T) {
	st := openStore(t)
	if err := st.Seed(); err != nil {
		t.Fatal(err)
	}
	slow := saveRule(t, st, model.Rule{
		Name: "慢接口", Enabled: true, Method: "GET", Path: "/slow", StatusCode: 200,
		DelayMS: 80, ResponseBody: "wait", ResponseHeaders: model.StringMap{"X-Slow": "1"},
	})
	_ = slow
	srv := httptest.NewServer(New(st, nil))
	t.Cleanup(srv.Close)

	res := do(t, srv, http.MethodGet, "/user/7?verbose=yes", "", nil)
	if res.StatusCode != 200 || !strings.Contains(res.Body, `"id": "7"`) || !strings.Contains(res.Body, "yes") {
		t.Fatalf("user %#v", res)
	}

	res = do(t, srv, http.MethodPost, "/login", `{"username":"demo"}`, map[string]string{"Content-Type": "application/json"})
	if res.StatusCode != 200 || !strings.Contains(res.Body, "demo") || !strings.Contains(res.Body, "token") {
		t.Fatalf("login %#v", res)
	}
	res = do(t, srv, http.MethodGet, "/me", "", nil)
	if res.StatusCode != 200 || !strings.Contains(res.Body, "demo") {
		t.Fatalf("me %#v", res)
	}
	if err := st.ClearState(); err != nil {
		t.Fatal(err)
	}
	res = do(t, srv, http.MethodGet, "/me", "", nil)
	if res.StatusCode != 401 {
		t.Fatalf("me after clear %d", res.StatusCode)
	}

	start := time.Now()
	res = do(t, srv, http.MethodGet, "/slow", "", nil)
	if res.StatusCode != 200 || res.Body != "wait" || res.Header.Get("X-Slow") != "1" {
		t.Fatalf("slow %#v", res)
	}
	if time.Since(start) < 60*time.Millisecond {
		t.Fatalf("delay too short: %s", time.Since(start))
	}

	res = do(t, srv, http.MethodGet, "/missing", "", nil)
	if res.StatusCode != 404 || !strings.Contains(res.Body, "没有匹配") {
		t.Fatalf("missing %#v", res)
	}
}

func TestProxyRecord(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/upstream/item" || r.URL.Query().Get("q") != "1" {
			t.Errorf("upstream url %s", r.URL.String())
		}
		w.Header().Set("X-Up", "yes")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("from-up"))
	}))
	t.Cleanup(up.Close)

	st := openStore(t)
	rule := saveRule(t, st, model.Rule{
		Name: "代理", Enabled: true, Method: "GET", Path: "/upstream/item",
		Mode: "proxy", ProxyURL: up.URL, Record: true, StatusCode: 200,
	})
	srv := httptest.NewServer(New(st, nil))
	t.Cleanup(srv.Close)
	res := do(t, srv, http.MethodGet, "/upstream/item?q=1", "", nil)
	if res.StatusCode != 201 || res.Body != "from-up" || res.Header.Get("X-Up") != "yes" {
		t.Fatalf("%#v", res)
	}
	got, err := st.GetRule(rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ResponseBody != "from-up" || got.StatusCode != 201 || got.Mode != "proxy" {
		t.Fatalf("recorded %#v", got)
	}
}

func TestLiveMonitorReceivesExchange(t *testing.T) {
	st := openStore(t)
	rule := saveRule(t, st, model.Rule{
		Name: "hi", Enabled: true, Method: "GET", Path: "/hi", StatusCode: 200,
		ResponseBody: `{"pong":true}`,
	})
	_ = rule
	hub := live.New()
	events, cancel := hub.Subscribe()
	defer cancel()
	srv := httptest.NewServer(New(st, hub))
	t.Cleanup(srv.Close)

	res, err := srv.Client().Get(srv.URL + "/hi")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()

	select {
	case got := <-events:
		if got.Path != "/hi" || got.StatusCode != 200 || !strings.Contains(got.ResponseBody, "pong") || got.ID == 0 {
			t.Fatalf("%#v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("实时监控没有收到这次请求")
	}
}

func TestJoinURL(t *testing.T) {
	got, err := joinURL("https://example.com/api/", "/v1/users", "a=1")
	if err != nil || got != "https://example.com/api/v1/users?a=1" {
		t.Fatalf("%s %v", got, err)
	}
}

type httpResult struct {
	StatusCode int
	Body       string
	Header     http.Header
}

func do(t *testing.T, srv *httptest.Server, method, path, body string, headers map[string]string) httpResult {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, srv.URL+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return httpResult{StatusCode: res.StatusCode, Body: string(b), Header: res.Header}
}

func saveRule(t *testing.T, st *store.Store, rule model.Rule) model.Rule {
	t.Helper()
	iface := model.Interface{Name: rule.Name, Enabled: true, Method: rule.Method, Path: rule.Path}
	if err := st.CreateInterface(&iface); err != nil {
		t.Fatal(err)
	}
	rule.InterfaceID = iface.ID
	if err := rule.Normalize(); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateRule(&rule); err != nil {
		t.Fatal(err)
	}
	return rule
}

func openStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "MockServerGo.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	return st
}
