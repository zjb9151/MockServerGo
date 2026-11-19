package live

import (
	"testing"
	"time"

	"csmock/internal/model"
)

func TestPublishReachesSubscriber(t *testing.T) {
	hub := New()
	ch, cancel := hub.Subscribe()
	defer cancel()

	hub.Publish(model.RequestLog{Method: "POST", Path: "/login", StatusCode: 200, Body: `{"a":1}`, ResponseBody: "ok"})

	select {
	case got := <-ch:
		if got.Path != "/login" || got.ResponseBody != "ok" {
			t.Fatalf("%#v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("没有收到实时事件")
	}

	cancel()
	hub.Publish(model.RequestLog{Path: "/after"})
	select {
	case got := <-ch:
		t.Fatalf("取消后仍收到 %#v", got)
	case <-time.After(50 * time.Millisecond):
	}
}
