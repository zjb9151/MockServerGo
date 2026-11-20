package model

import "testing"

func TestFlowRejectsCycle(t *testing.T) {
	rule := Rule{
		Name: "循环", Method: "GET", Path: "/", StatusCode: 200,
		Flow: &Flow{
			Fields: []FlowField{{ID: "q", Source: "query", Key: "id"}},
			Nodes: []FlowNode{
				{ID: "start", Type: "start", Next: "j"},
				{ID: "j", Type: "judge", FieldID: "q", Branches: []FlowBranch{
					{ID: "yes", Kind: "when", Op: "eq", Value: "1", To: "j"},
					{ID: "no", Kind: "else", To: ""},
				}},
			},
		},
	}
	if err := rule.Normalize(); err == nil {
		t.Fatal("cycle should be rejected")
	}
}
