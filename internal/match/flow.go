package match

import (
	"bytes"
	"encoding/json"
	"strings"

	"csmock/internal/model"
)

// applyFlow 沿着流程图走到一个返回节点，并把该节点的响应写回规则。
// 没有流程图时 used 为 false，调用方继续用原来的条件。
func applyFlow(rule model.Rule, req Request) (out model.Rule, used bool, ok bool) {
	if rule.Flow == nil || len(rule.Flow.Nodes) == 0 {
		return rule, false, false
	}
	node, hit := walkFlow(rule.Flow, req)
	if !hit {
		return rule, true, false
	}
	if node.StatusCode != 0 {
		rule.StatusCode = node.StatusCode
	}
	rule.DelayMS = node.DelayMS
	rule.ResponseBody = node.ResponseBody
	if node.ResponseHeaders != nil {
		rule.ResponseHeaders = node.ResponseHeaders
	}
	mode := strings.ToLower(strings.TrimSpace(node.Mode))
	if mode == "" {
		mode = "mock"
	}
	rule.Mode = mode
	rule.ProxyURL = node.ProxyURL
	rule.Record = node.Record
	if node.StateSet != nil {
		rule.StateSet = node.StateSet
	} else {
		rule.StateSet = model.StringMap{}
	}
	return rule, true, true
}

func walkFlow(flow *model.Flow, req Request) (model.FlowNode, bool) {
	nodes := make(map[string]model.FlowNode, len(flow.Nodes))
	fields := make(map[string]model.FlowField, len(flow.Fields))
	var start model.FlowNode
	foundStart := false
	for _, field := range flow.Fields {
		fields[field.ID] = field
	}
	for _, node := range flow.Nodes {
		nodes[node.ID] = node
		if node.Type == "start" {
			start = node
			foundStart = true
		}
	}
	if !foundStart {
		return model.FlowNode{}, false
	}
	seen := map[string]bool{}
	cur := start.Next
	for cur != "" {
		if seen[cur] {
			return model.FlowNode{}, false
		}
		seen[cur] = true
		node, ok := nodes[cur]
		if !ok {
			return model.FlowNode{}, false
		}
		switch node.Type {
		case "return":
			return node, true
		case "judge":
			next := ""
			matched := false
			vals := flowValues(fields[node.FieldID], req)
			for _, branch := range node.Branches {
				if branch.Kind == "else" {
					next = branch.To
					matched = true
					break
				}
				if matchValues(model.Condition{Op: branch.Op, Value: branch.Value}, vals) {
					next = branch.To
					matched = true
					break
				}
			}
			if !matched || next == "" {
				return model.FlowNode{}, false
			}
			cur = next
		default:
			return model.FlowNode{}, false
		}
	}
	return model.FlowNode{}, false
}

func flowValues(field model.FlowField, req Request) []string {
	switch strings.ToLower(field.Source) {
	case "header":
		return headerValues(req.Header, field.Key)
	case "query":
		if req.Query == nil {
			return nil
		}
		return req.Query[field.Key]
	case "state":
		if req.State == nil {
			return nil
		}
		val, ok := req.State[field.Key]
		if !ok {
			return nil
		}
		return []string{val}
	case "body":
		return bodyValues(req.Body, field.Key)
	default:
		return nil
	}
}

func bodyValues(body []byte, key string) []string {
	if key == "$raw" {
		if len(bytes.TrimSpace(body)) == 0 {
			return nil
		}
		return []string{string(body)}
	}
	trim := bytes.TrimSpace(body)
	if len(trim) == 0 {
		return nil
	}
	var doc any
	dec := json.NewDecoder(bytes.NewReader(trim))
	dec.UseNumber()
	if err := dec.Decode(&doc); err != nil {
		return nil
	}
	val, ok := lookup(doc, key)
	if !ok {
		return nil
	}
	return []string{stringify(val)}
}
