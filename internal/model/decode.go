package model

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// Document 是 JSON/YAML 规则文件。
type Document struct {
	Replace bool   `json:"replace"`
	Rules   []Rule `json:"rules"`
}

// DecodeDocument 接受 JSON 对象、JSON 数组或 YAML。
func DecodeDocument(b []byte) (Document, error) {
	trim := bytes.TrimSpace(b)
	if len(trim) == 0 {
		return Document{}, fmt.Errorf("配置内容为空")
	}
	trim = bytes.TrimPrefix(trim, []byte{0xEF, 0xBB, 0xBF})

	if trim[0] == '[' {
		var raws []json.RawMessage
		if err := json.Unmarshal(trim, &raws); err != nil {
			return Document{}, fmt.Errorf("解析 JSON 数组失败: %w", err)
		}
		return rulesFromRaw(false, raws)
	}

	var raw any
	var err error
	if trim[0] == '{' {
		err = json.Unmarshal(trim, &raw)
		if err != nil {
			return Document{}, fmt.Errorf("解析 JSON 失败: %w", err)
		}
	} else {
		if err = yaml.Unmarshal(trim, &raw); err != nil {
			return Document{}, fmt.Errorf("解析 YAML 失败: %w", err)
		}
	}
	jb, err := json.Marshal(raw)
	if err != nil {
		return Document{}, err
	}

	var probe map[string]json.RawMessage
	if err := json.Unmarshal(jb, &probe); err != nil {
		return Document{}, fmt.Errorf("配置必须是对象或规则数组")
	}
	rulesRaw, ok := probe["rules"]
	if !ok {
		return Document{}, fmt.Errorf("未找到 rules 字段")
	}
	var raws []json.RawMessage
	if err := json.Unmarshal(rulesRaw, &raws); err != nil {
		return Document{}, fmt.Errorf("rules 必须是数组")
	}
	replace := false
	if v, ok := probe["replace"]; ok {
		_ = json.Unmarshal(v, &replace)
	}
	return rulesFromRaw(replace, raws)
}

func rulesFromRaw(replace bool, raws []json.RawMessage) (Document, error) {
	doc := Document{Replace: replace, Rules: make([]Rule, 0, len(raws))}
	for i, raw := range raws {
		rule, err := DecodeRule(raw)
		if err != nil {
			return Document{}, fmt.Errorf("第 %d 条规则无法解析: %w", i+1, err)
		}
		if err := rule.Normalize(); err != nil {
			name := strings.TrimSpace(rule.Name)
			if name == "" {
				name = fmt.Sprintf("第 %d 条", i+1)
			}
			return Document{}, fmt.Errorf("规则「%s」无效: %w", name, err)
		}
		doc.Rules = append(doc.Rules, rule)
	}
	return doc, nil
}

// EncodeDocument 按 JSON 字段名导出 YAML 或 JSON。
func EncodeDocument(doc Document, format string) ([]byte, error) {
	payload := map[string]any{
		"replace": doc.Replace,
		"rules":   doc.Rules,
	}
	jb, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	if strings.EqualFold(format, "yaml") || strings.EqualFold(format, "yml") {
		var raw any
		if err := json.Unmarshal(jb, &raw); err != nil {
			return nil, err
		}
		return yaml.Marshal(raw)
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(payload); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
