package model

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// Flow 是规则编辑器里的流程图。开始节点取出字段，判断节点沿分支往下走，返回节点给出响应。
type Flow struct {
	Fields []FlowField `json:"fields"`
	Nodes  []FlowNode  `json:"nodes"`
}

// FlowField 是开始节点取出的一个字段。
type FlowField struct {
	ID     string `json:"id"`
	Source string `json:"source"`
	Key    string `json:"key"`
}

// FlowNode 是开始、判断或返回。
type FlowNode struct {
	ID              string       `json:"id"`
	Type            string       `json:"type"`
	X               float64      `json:"x,omitempty"`
	Y               float64      `json:"y,omitempty"`
	Placed          bool         `json:"placed,omitempty"`
	Next            string       `json:"next,omitempty"`
	FieldID         string       `json:"field_id,omitempty"`
	Branches        []FlowBranch `json:"branches,omitempty"`
	StatusCode      int          `json:"status_code,omitempty"`
	DelayMS         int          `json:"delay_ms,omitempty"`
	ResponseHeaders StringMap    `json:"response_headers,omitempty"`
	ResponseBody    string       `json:"response_body,omitempty"`
	Mode            string       `json:"mode,omitempty"`
	ProxyURL        string       `json:"proxy_url,omitempty"`
	Record          bool         `json:"record,omitempty"`
	StateSet        StringMap    `json:"state_set,omitempty"`
}

// FlowBranch 是判断节点的一条出口。Kind 为 when 或 else。
type FlowBranch struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Op    string `json:"op,omitempty"`
	Value string `json:"value,omitempty"`
	To    string `json:"to,omitempty"`
}

func (r *Rule) normalizeFlow() error {
	if r.Flow == nil {
		return nil
	}
	if len(r.Flow.Nodes) == 0 && len(r.Flow.Fields) == 0 {
		r.Flow = nil
		return nil
	}
	if err := r.Flow.normalize(); err != nil {
		return err
	}
	return nil
}

func (f *Flow) normalize() error {
	if f.Fields == nil {
		f.Fields = []FlowField{}
	}
	fields := map[string]FlowField{}
	for i := range f.Fields {
		field := &f.Fields[i]
		field.ID = strings.TrimSpace(field.ID)
		field.Source = strings.ToLower(strings.TrimSpace(field.Source))
		field.Key = strings.TrimSpace(field.Key)
		if field.ID == "" {
			return fmt.Errorf("流程图字段缺少编号")
		}
		if _, ok := fields[field.ID]; ok {
			return fmt.Errorf("流程图字段编号重复")
		}
		switch field.Source {
		case "header", "query", "body", "state":
		default:
			return fmt.Errorf("流程图字段来源只能是 Header、Query、Body 或场景状态")
		}
		if field.Key == "" {
			return fmt.Errorf("开始节点的字段名不能为空")
		}
		fields[field.ID] = *field
	}

	if len(f.Nodes) == 0 {
		return fmt.Errorf("流程图至少要有开始节点")
	}
	nodes := map[string]*FlowNode{}
	start := 0
	for i := range f.Nodes {
		node := &f.Nodes[i]
		node.ID = strings.TrimSpace(node.ID)
		node.Type = strings.ToLower(strings.TrimSpace(node.Type))
		if node.ID == "" {
			return fmt.Errorf("流程图节点缺少编号")
		}
		if _, ok := nodes[node.ID]; ok {
			return fmt.Errorf("流程图节点编号重复")
		}
		nodes[node.ID] = node
		switch node.Type {
		case "start":
			start++
			node.Next = strings.TrimSpace(node.Next)
			node.Branches = nil
		case "judge":
			node.Next = ""
			node.FieldID = strings.TrimSpace(node.FieldID)
			if _, ok := fields[node.FieldID]; !ok {
				return fmt.Errorf("判断节点要选择开始节点里的字段")
			}
			if err := normalizeBranches(node); err != nil {
				return err
			}
		case "return":
			node.Next = ""
			node.Branches = nil
			node.FieldID = ""
			if err := normalizeReturn(node); err != nil {
				return err
			}
		default:
			return fmt.Errorf("流程图节点类型无效")
		}
	}
	if start != 1 {
		return fmt.Errorf("流程图需要一个开始节点")
	}
	for _, node := range nodes {
		for _, target := range outgoing(node) {
			if target == "" {
				continue
			}
			if _, ok := nodes[target]; !ok {
				return fmt.Errorf("流程图分支指向了不存在的节点")
			}
		}
	}
	if err := walkNoCycle(nodes); err != nil {
		return err
	}
	return nil
}

func normalizeBranches(node *FlowNode) error {
	if len(node.Branches) == 0 {
		return fmt.Errorf("判断节点至少要有一条出口")
	}
	whens := make([]FlowBranch, 0, len(node.Branches))
	elses := make([]FlowBranch, 0, 1)
	seen := map[string]struct{}{}
	for _, branch := range node.Branches {
		branch.ID = strings.TrimSpace(branch.ID)
		branch.Kind = strings.ToLower(strings.TrimSpace(branch.Kind))
		branch.Op = strings.ToLower(strings.TrimSpace(branch.Op))
		branch.To = strings.TrimSpace(branch.To)
		branch.Value = branch.Value
		if branch.ID == "" {
			return fmt.Errorf("判断分支缺少编号")
		}
		if _, ok := seen[branch.ID]; ok {
			return fmt.Errorf("判断分支编号重复")
		}
		seen[branch.ID] = struct{}{}
		switch branch.Kind {
		case "when":
			if branch.Op == "" {
				branch.Op = "eq"
			}
			if _, ok := validOps[branch.Op]; !ok {
				return fmt.Errorf("判断不支持操作 %s", branch.Op)
			}
			if branch.Op == "regex" {
				if _, err := regexp.Compile(branch.Value); err != nil {
					return fmt.Errorf("判断的正则无效: %s", err.Error())
				}
			}
			whens = append(whens, branch)
		case "else":
			branch.Op = ""
			branch.Value = ""
			elses = append(elses, branch)
		default:
			return fmt.Errorf("判断分支只能是满足或不满足")
		}
	}
	if len(whens) == 0 {
		return fmt.Errorf("判断节点至少要有一条满足出口")
	}
	if len(elses) != 1 {
		return fmt.Errorf("判断节点需要一条其他出口")
	}
	node.Branches = append(whens, elses[0])
	return nil
}

func normalizeReturn(node *FlowNode) error {
	if node.StatusCode == 0 {
		node.StatusCode = 200
	}
	if node.StatusCode < 100 || node.StatusCode > 599 {
		return fmt.Errorf("返回节点的状态码必须在 100 到 599 之间")
	}
	if node.DelayMS < 0 {
		return fmt.Errorf("返回节点的延迟不能为负数")
	}
	if node.DelayMS > 300000 {
		return fmt.Errorf("返回节点的延迟不能超过 300000 毫秒")
	}
	node.Mode = strings.ToLower(strings.TrimSpace(node.Mode))
	if node.Mode == "" {
		node.Mode = "mock"
	}
	if node.Mode != "mock" && node.Mode != "proxy" {
		return fmt.Errorf("返回节点的模式只能是自定义响应或转发")
	}
	node.ProxyURL = strings.TrimSpace(node.ProxyURL)
	if node.Mode == "proxy" {
		if err := validateProxy(node.ProxyURL); err != nil {
			return err
		}
	}
	node.ResponseHeaders = cleanStringMap(node.ResponseHeaders, "响应 Header 名不能为空")
	var err error
	node.StateSet, err = cleanStateMap(node.StateSet)
	return err
}

func outgoing(node *FlowNode) []string {
	if node.Type == "start" {
		return []string{node.Next}
	}
	if node.Type != "judge" {
		return nil
	}
	out := make([]string, 0, len(node.Branches))
	for _, branch := range node.Branches {
		out = append(out, branch.To)
	}
	return out
}

func walkNoCycle(nodes map[string]*FlowNode) error {
	var start *FlowNode
	for _, node := range nodes {
		if node.Type == "start" {
			start = node
			break
		}
	}
	state := map[string]int{}
	var visit func(id string) error
	visit = func(id string) error {
		if id == "" {
			return nil
		}
		switch state[id] {
		case 1:
			return fmt.Errorf("流程图里存在循环")
		case 2:
			return nil
		}
		node := nodes[id]
		if node == nil {
			return nil
		}
		state[id] = 1
		for _, next := range outgoing(node) {
			if err := visit(next); err != nil {
				return err
			}
		}
		state[id] = 2
		return nil
	}
	return visit(start.ID)
}

func cleanStringMap(in StringMap, _ string) StringMap {
	out := StringMap{}
	for k, v := range in {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		out[k] = v
	}
	return out
}

func cleanStateMap(in StringMap) (StringMap, error) {
	out := StringMap{}
	for k, v := range in {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		if strings.Contains(k, "/") {
			return nil, fmt.Errorf("状态键不能包含 /")
		}
		out[k] = v
	}
	return out, nil
}

func validateProxy(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("代理地址需要是完整的 http 或 https URL")
	}
	return nil
}
