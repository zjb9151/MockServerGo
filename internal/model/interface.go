package model

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Normalize 校正接口的方法、路径和名称。
func (a *Interface) Normalize() error {
	a.Name = strings.TrimSpace(a.Name)
	if a.Name == "" {
		a.Name = "未命名接口"
	}
	a.Method = strings.ToUpper(strings.TrimSpace(a.Method))
	if a.Method == "" {
		return fmt.Errorf("请填写 HTTP 方法")
	}
	for _, part := range strings.Split(a.Method, ",") {
		if strings.TrimSpace(part) == "" {
			return fmt.Errorf("HTTP 方法不能为空")
		}
	}
	a.Method = joinMethod(a.Method)
	a.Path = strings.TrimSpace(a.Path)
	if a.Path == "" || !strings.HasPrefix(a.Path, "/") {
		return fmt.Errorf("路径必须以 / 开头")
	}
	if strings.ContainsAny(a.Path, " ?#") {
		return fmt.Errorf("路径不能包含空格、? 或 #，查询参数请写到规则里")
	}
	a.Description = strings.TrimSpace(a.Description)
	return nil
}

// DecodeInterface 解析接口。未提供 enabled 时默认启用。
func DecodeInterface(b []byte) (Interface, error) {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(b, &probe); err != nil {
		return Interface{}, err
	}
	var iface Interface
	if err := json.Unmarshal(b, &iface); err != nil {
		return Interface{}, err
	}
	if _, ok := probe["enabled"]; !ok {
		iface.Enabled = true
	}
	return iface, nil
}
