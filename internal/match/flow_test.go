package match

import (
	"net/http"
	"net/url"
	"testing"

	"csmock/internal/model"
)

func TestFlowChoosesReturn(t *testing.T) {
	rule := model.Rule{
		Name: "下单", Enabled: true, Method: "POST", Path: "/order", StatusCode: 200,
		Flow: &model.Flow{
			Fields: []model.FlowField{
				{ID: "token", Source: "header", Key: "token"},
				{ID: "id", Source: "query", Key: "id"},
			},
			Nodes: []model.FlowNode{
				{ID: "start", Type: "start", Next: "token"},
				{ID: "token", Type: "judge", FieldID: "token", Branches: []model.FlowBranch{
					{ID: "yes", Kind: "when", Op: "eq", Value: "secret", To: "id"},
					{ID: "no", Kind: "else", To: "auth"},
				}},
				{ID: "id", Type: "judge", FieldID: "id", Branches: []model.FlowBranch{
					{ID: "id-yes", Kind: "when", Op: "eq", Value: "1001", To: "ok"},
					{ID: "id-no", Kind: "else", To: "bad"},
				}},
				{ID: "auth", Type: "return", StatusCode: 401, ResponseBody: "auth"},
				{ID: "bad", Type: "return", StatusCode: 400, ResponseBody: "query"},
				{ID: "ok", Type: "return", StatusCode: 200, ResponseBody: "ok"},
			},
		},
	}
	cases := []struct {
		name   string
		header string
		id     string
		want   string
		hit    bool
	}{
		{name: "bad token", header: "nope", id: "1001", want: "auth", hit: true},
		{name: "missing token", id: "1001", want: "auth", hit: true},
		{name: "bad query", header: "secret", id: "9", want: "query", hit: true},
		{name: "ok", header: "secret", id: "1001", want: "ok", hit: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			header := http.Header{}
			if tc.header != "" {
				header.Set("token", tc.header)
			}
			hit, ok := Find([]model.Rule{rule}, Request{
				Method: "POST",
				Path:   "/order",
				Header: header,
				Query:  url.Values{"id": {tc.id}},
			})
			if ok != tc.hit {
				t.Fatalf("hit=%v want %v", ok, tc.hit)
			}
			if ok && hit.Rule.ResponseBody != tc.want {
				t.Fatalf("body=%s want %s", hit.Rule.ResponseBody, tc.want)
			}
		})
	}
}

func TestFlowUnsetBranchDoesNotMatch(t *testing.T) {
	rule := model.Rule{
		Name: "只接成功", Enabled: true, Method: "GET", Path: "/only", StatusCode: 200,
		Flow: &model.Flow{
			Fields: []model.FlowField{{ID: "q", Source: "query", Key: "id"}},
			Nodes: []model.FlowNode{
				{ID: "start", Type: "start", Next: "j"},
				{ID: "j", Type: "judge", FieldID: "q", Branches: []model.FlowBranch{
					{ID: "yes", Kind: "when", Op: "eq", Value: "1", To: "ok"},
					{ID: "no", Kind: "else", To: ""},
				}},
				{ID: "ok", Type: "return", StatusCode: 200, ResponseBody: "ok"},
			},
		},
	}
	if _, ok := Find([]model.Rule{rule}, Request{Method: "GET", Path: "/only", Query: url.Values{"id": {"2"}}}); ok {
		t.Fatal("unset branch should not match")
	}
	hit, ok := Find([]model.Rule{rule}, Request{Method: "GET", Path: "/only", Query: url.Values{"id": {"1"}}})
	if !ok || hit.Rule.ResponseBody != "ok" {
		t.Fatal("matching branch should return ok")
	}
}
