package admin

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"csmock/internal/store"
)

func TestAdminPagesAndAPI(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "csmock.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(New(st, Info{
		Version: "test", MockAddr: "127.0.0.1:8080", AdminAddr: "127.0.0.1:9090",
		DataDir: t.TempDir(), DBPath: st.Path(),
	}, nil))
	t.Cleanup(srv.Close)

	page := get(t, srv, "/")
	if page.StatusCode != 200 || !strings.Contains(page.Body, "MockServerGo") || !strings.Contains(page.Body, "/app.js") {
		t.Fatalf("page %#v", page.StatusCode)
	}
	if css := get(t, srv, "/app.css"); css.StatusCode != 200 || !strings.Contains(css.Body, "--teal") {
		t.Fatalf("css %d", css.StatusCode)
	}

	iface := post(t, srv, "/api/interfaces", `{"name":"搜索","method":"GET","path":"/search","enabled":true}`)
	if iface.StatusCode != 201 || !strings.Contains(iface.Body, `"id"`) {
		t.Fatalf("interface %d %s", iface.StatusCode, iface.Body)
	}
	var createdIface struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal([]byte(iface.Body), &createdIface); err != nil || createdIface.ID == 0 {
		t.Fatalf("interface id %s", iface.Body)
	}
	created := post(t, srv, "/api/rules", `{
		"name":"搜索","interface_id":`+jsonID(createdIface.ID)+`,"enabled":true,"status_code":200,
		"query_match":[{"key":"q","op":"exists"}],
		"response_body":"{\"q\":\"{{query.q}}\"}"
	}`)
	if created.StatusCode != 201 || !strings.Contains(created.Body, `"id"`) {
		t.Fatalf("create %d %s", created.StatusCode, created.Body)
	}
	tried := post(t, srv, "/api/try", `{"method":"GET","url":"/search?q=go"}`)
	if tried.StatusCode != 200 || !strings.Contains(tried.Body, `"matched":true`) || !strings.Contains(tried.Body, "go") {
		t.Fatalf("try %s", tried.Body)
	}

	bad := send(t, srv, http.MethodPut, "/api/settings", `{"mock_addr":"8080","admin_addr":"127.0.0.1:9090","proxy_fallback":""}`)
	if bad.StatusCode != 400 {
		t.Fatalf("settings %d %s", bad.StatusCode, bad.Body)
	}
	imported := post(t, srv, "/api/import?replace=0", "rules:\n  - name: yaml 规则\n    method: POST\n    path: /yaml\n    response_body: ok\n")
	if imported.StatusCode != 200 || !strings.Contains(imported.Body, `"created":1`) {
		t.Fatalf("import %d %s", imported.StatusCode, imported.Body)
	}
}

type result struct {
	StatusCode int
	Body       string
}

func jsonID(id int64) string {
	b, _ := json.Marshal(id)
	return string(b)
}

func get(t *testing.T, srv *httptest.Server, path string) result {
	t.Helper()
	res, err := srv.Client().Get(srv.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return result{res.StatusCode, string(b)}
}

func post(t *testing.T, srv *httptest.Server, path, body string) result {
	t.Helper()
	return send(t, srv, http.MethodPost, path, body)
}

func send(t *testing.T, srv *httptest.Server, method, path, body string) result {
	t.Helper()
	req, err := http.NewRequest(method, srv.URL+path, bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return result{res.StatusCode, string(b)}
}
