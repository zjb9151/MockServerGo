package store

import (
	"path/filepath"
	"testing"

	"csmock/internal/model"
)

func TestRulesLogsAndAdopt(t *testing.T) {
	st := openTest(t)
	if err := st.Seed(); err != nil {
		t.Fatal(err)
	}
	n, err := st.CountRules()
	if err != nil || n < 4 {
		t.Fatalf("seed count %d %v", n, err)
	}
	if err := st.Seed(); err != nil {
		t.Fatal(err)
	}
	n2, _ := st.CountRules()
	if n2 != n {
		t.Fatal("seed should run once")
	}

	iface := model.Interface{Name: "临时", Enabled: true, Method: "PUT", Path: "/temp/:id"}
	if err := st.CreateInterface(&iface); err != nil {
		t.Fatal(err)
	}
	rule := model.Rule{
		Name: "临时", Enabled: true, InterfaceID: iface.ID, Method: "PUT", Path: "/temp/:id", StatusCode: 204,
		ResponseHeaders: model.StringMap{"X-Test": "1"},
	}
	if err := rule.Normalize(); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateRule(&rule); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetRule(rule.ID)
	if err != nil || got.Path != "/temp/:id" || !got.Enabled || got.InterfaceID != iface.ID {
		t.Fatalf("%#v %v", got, err)
	}
	matched, err := st.ListMatchRules()
	if err != nil {
		t.Fatal(err)
	}
	seen := false
	for _, item := range matched {
		if item.ID == rule.ID {
			seen = true
		}
	}
	if !seen {
		t.Fatal("enabled interface rule missing from match list")
	}
	if _, err := st.ToggleInterface(iface.ID); err != nil {
		t.Fatal(err)
	}
	matched, err = st.ListMatchRules()
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range matched {
		if item.ID == rule.ID {
			t.Fatal("disabled interface still listed")
		}
	}
	if _, err := st.ToggleInterface(iface.ID); err != nil {
		t.Fatal(err)
	}
	got.StatusCode = 201
	if err := st.UpdateRule(&got); err != nil {
		t.Fatal(err)
	}
	toggled, err := st.ToggleRule(rule.ID)
	if err != nil || toggled.Enabled {
		t.Fatal(toggled.Enabled, err)
	}

	if _, err := st.InsertLog(model.RequestLog{
		Method: "GET", Path: "/book", StatusCode: 201, ResponseBody: "paged",
		ResponseHeaders: `{"X-Test":"1"}`, RemoteAddr: "127.0.0.1",
	}); err != nil {
		t.Fatal(err)
	}
	logs, total, err := st.ListLogs("book", 10, 0)
	if err != nil || total != 1 || logs[0].Path != "/book" {
		t.Fatalf("%v %d %v", logs, total, err)
	}
	adopted, err := st.AdoptLog(logs[0].ID)
	if err != nil || adopted.Path != "/book" || adopted.StatusCode != 201 || adopted.ResponseBody != "paged" {
		t.Fatalf("%#v %v", adopted, err)
	}
	if adopted.ResponseHeaders["X-Test"] != "1" {
		t.Fatalf("headers %#v", adopted.ResponseHeaders)
	}

	old := keepLogs
	keepLogs = 2
	t.Cleanup(func() { keepLogs = old })
	for i := 0; i < 4; i++ {
		if _, err := st.InsertLog(model.RequestLog{Method: "GET", Path: "/n", StatusCode: 200}); err != nil {
			t.Fatal(err)
		}
	}
	_, total, err = st.ListLogs("", 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 {
		t.Fatalf("retention total=%d", total)
	}
}

func openTest(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "csmock.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	return st
}
