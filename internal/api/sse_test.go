package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestBroadcasterStopIsIdempotentAndBroadcastAfterStopDoesNotBlock(t *testing.T) {
	b := newBroadcaster()
	b.Stop()
	b.Stop()

	done := make(chan struct{})
	go func() {
		b.Broadcast("event", map[string]string{"ok": "true"})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Broadcast blocked after Stop")
	}
}

func TestBroadcasterClientReceivesBroadcastAndDoneIsIdempotent(t *testing.T) {
	b := newBroadcaster()
	defer b.Stop()

	ch, done := b.AddClient()
	done()
	done()

	ch, done = b.AddClient()
	defer done()
	b.Broadcast("test", map[string]string{"message": "hello"})

	select {
	case msg, ok := <-ch:
		if !ok {
			t.Fatal("client channel unexpectedly closed")
		}
		text := string(msg)
		if !strings.Contains(text, "event: test") || !strings.Contains(text, "hello") {
			t.Fatalf("unexpected SSE payload: %q", text)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timed out waiting for broadcast")
	}
}

func TestSSEReceivesInitialPing(t *testing.T) {
	s := &Server{broadcaster: newBroadcaster()}
	defer s.StopBroadcaster()

	req := httptest.NewRequest(http.MethodGet, "/live", nil)
	rr := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		s.handleSSE(rr, req)
		close(done)
	}()

	deadline := time.After(500 * time.Millisecond)
	for {
		if strings.Contains(rr.Body.String(), "event: ping") {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("initial ping not written: %q", rr.Body.String())
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func TestSSEPathSkipsTimeout(t *testing.T) {
	if !isSSEPath("/live") || !isSSEPath("/api/live") {
		t.Fatal("expected /live and /api/live to be treated as SSE paths")
	}
	if isSSEPath("/api/events") {
		t.Fatal("non-SSE path was treated as SSE")
	}
}
