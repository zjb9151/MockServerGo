package model

import "testing"

func TestDecodeYAMLAndRoundTrip(t *testing.T) {
	doc, err := DecodeDocument([]byte(`
replace: false
rules:
  - name: 订单
    method: GET
    path: /order/{id}
    priority: 5
    query_match:
      - key: status
        op: eq
        value: 2
    status_code: 202
    delay_ms: 15
    response_body: '{"id":"{{params.id}}"}'
    response_headers:
      X-Mock: 1
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Rules) != 1 {
		t.Fatalf("rules=%d", len(doc.Rules))
	}
	rule := doc.Rules[0]
	if !rule.Enabled || rule.StatusCode != 202 || rule.DelayMS != 15 || rule.Priority != 5 {
		t.Fatalf("%#v", rule)
	}
	if rule.QueryMatch[0].Value != "2" || rule.ResponseHeaders["X-Mock"] != "1" {
		t.Fatalf("value=%s header=%v", rule.QueryMatch[0].Value, rule.ResponseHeaders)
	}
	raw, err := EncodeDocument(doc, "json")
	if err != nil {
		t.Fatal(err)
	}
	again, err := DecodeDocument(raw)
	if err != nil {
		t.Fatal(err)
	}
	if again.Rules[0].Path != "/order/{id}" || again.Rules[0].Name != "订单" {
		t.Fatalf("%#v", again.Rules[0])
	}
}

func TestNormalizeRejectsBadPath(t *testing.T) {
	rule := Rule{Method: "GET", Path: "user", StatusCode: 200}
	if err := rule.Normalize(); err == nil {
		t.Fatal("expected error")
	}
}
