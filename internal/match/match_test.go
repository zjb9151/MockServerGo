package match

import (
	"net/http"
	"net/url"
	"testing"

	"csmock/internal/model"
)

func TestMatchPathParams(t *testing.T) {
	cases := []struct {
		pattern string
		path    string
		ok      bool
		id      string
	}{
		{"/user/:id", "/user/42", true, "42"},
		{"/user/{id}", "/user/42", true, "42"},
		{"/user/:id/", "/user/42", true, "42"},
		{"/user/:id", "/user/42/orders", false, ""},
		{"/user/:id", "/users/42", false, ""},
		{"/", "/", true, ""},
		{"/health", "/health/", true, ""},
	}
	for _, tc := range cases {
		params, ok := MatchPath(tc.pattern, tc.path)
		if ok != tc.ok {
			t.Fatalf("%s vs %s ok=%v want %v", tc.pattern, tc.path, ok, tc.ok)
		}
		if tc.id != "" && params["id"] != tc.id {
			t.Fatalf("param=%v", params)
		}
	}
}

func TestFindRanksSpecificRuleHigher(t *testing.T) {
	rules := []model.Rule{
		{ID: 1, Name: "any", Enabled: true, Method: "*", Path: "/user/:id", StatusCode: 200},
		{ID: 2, Name: "exact", Enabled: true, Priority: 0, Method: "GET", Path: "/user/:id", QueryMatch: []model.Condition{{Key: "verbose", Op: "eq", Value: "1"}}, StatusCode: 201},
		{ID: 3, Name: "high", Enabled: true, Priority: 10, Method: "POST", Path: "/user/:id", StatusCode: 202},
	}
	hit, ok := Find(rules, Request{Method: "GET", Path: "/user/7", Query: url.Values{"verbose": {"1"}}})
	if !ok || hit.Rule.Name != "exact" {
		t.Fatalf("got %#v ok=%v", hit.Rule.Name, ok)
	}
	hit, ok = Find(rules, Request{Method: "GET", Path: "/user/7"})
	if !ok || hit.Rule.Name != "any" {
		t.Fatalf("plain got %s", hit.Rule.Name)
	}
	hit, ok = Find(rules, Request{Method: "POST", Path: "/user/7"})
	if !ok || hit.Rule.Name != "high" {
		t.Fatalf("post got %s", hit.Rule.Name)
	}
}

func TestQueryAndOr(t *testing.T) {
	rule := model.Rule{
		ID: 1, Enabled: true, Method: "GET", Path: "/search", StatusCode: 200,
		QueryMatch: []model.Condition{
			{Key: "a", Op: "eq", Value: "1"},
			{Key: "b", Op: "eq", Value: "2", Join: "and"},
			{Key: "c", Op: "eq", Value: "3", Join: "or"},
		},
	}
	match := func(values url.Values) bool {
		_, ok := Find([]model.Rule{rule}, Request{Method: "GET", Path: "/search", Query: values})
		return ok
	}
	if !match(url.Values{"a": {"1"}, "b": {"2"}}) {
		t.Fatal("a and b should match")
	}
	if !match(url.Values{"c": {"3"}}) {
		t.Fatal("c alone should match through or")
	}
	if match(url.Values{"a": {"1"}}) {
		t.Fatal("a without b should not match")
	}
	if match(url.Values{"b": {"2"}}) {
		t.Fatal("b without a should not match")
	}
	bothAnd := model.Rule{
		ID: 2, Enabled: true, Method: "GET", Path: "/search", StatusCode: 200,
		QueryMatch: []model.Condition{
			{Key: "a", Op: "eq", Value: "1"},
			{Key: "b", Op: "eq", Value: "2"},
		},
	}
	_, ok := Find([]model.Rule{bothAnd}, Request{Method: "GET", Path: "/search", Query: url.Values{"a": {"1"}}})
	if ok {
		t.Fatal("missing join should stay and")
	}
}

func TestBodyHeaderAndState(t *testing.T) {
	rules := []model.Rule{{
		ID: 1, Enabled: true, Method: "POST", Path: "/login", StatusCode: 200,
		HeaderMatch: []model.Condition{{Key: "X-Token", Op: "eq", Value: "secret"}},
		BodyMatch:   []model.Condition{{Key: "user.name", Op: "eq", Value: "ada"}, {Key: "age", Op: "gt", Value: "18"}},
		StateMatch:  []model.Condition{{Key: "auth", Op: "eq", Value: "guest"}},
	}}
	req := Request{
		Method: "POST",
		Path:   "/login",
		Header: http.Header{"X-Token": {"secret"}},
		Body:   []byte(`{"user":{"name":"ada"},"age":21}`),
		State:  map[string]string{"auth": "guest"},
	}
	if _, ok := Find(rules, req); !ok {
		t.Fatal("expected match")
	}
	req.Body = []byte(`{"user":{"name":"ada"},"age":10}`)
	if _, ok := Find(rules, req); ok {
		t.Fatal("age should not match")
	}
	req.Body = []byte(`{"user":{"name":"ada"},"age":21}`)
	req.Header.Set("x-token", "nope")
	if _, ok := Find(rules, req); ok {
		t.Fatal("header should not match")
	}
}
