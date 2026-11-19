package render

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestRenderTokens(t *testing.T) {
	out := Render(`id={{params.id}} q={{query.verbose}} h={{header.X-Token}} user={{body.user.name}} st={{state.auth}} {{method}} {{path}} {{rawquery}}`, Data{
		Method:   "POST",
		Path:     "/login",
		RawQuery: "a=1",
		Query:    url.Values{"verbose": {"yes"}},
		Params:   map[string]string{"id": "7"},
		Header:   http.Header{"X-Token": {"abc"}},
		Body:     []byte(`{"user":{"name":"demo"}}`),
		State:    map[string]string{"auth": "logged_in"},
	})
	for _, want := range []string{"id=7", "q=yes", "h=abc", "user=demo", "st=logged_in", "POST", "/login", "a=1"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %s in %s", want, out)
		}
	}
	if !strings.Contains(Render("{{uuid}}", Data{}), "-") {
		t.Fatal("uuid")
	}
	if Render("{{unknown.x}}", Data{}) != "{{unknown.x}}" {
		t.Fatal("unknown token should stay")
	}
}
