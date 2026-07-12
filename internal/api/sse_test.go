package api

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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

type safeRecorder struct {
	http.ResponseWriter
	mu     sync.Mutex
	body   bytes.Buffer
	header http.Header
	code   int
}

func (r *safeRecorder) Header() http.Header {
	if r.header == nil {
		r.header = make(http.Header)
	}
	return r.header
}

func (r *safeRecorder) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.code == 0 {
		r.code = 200
	}
	return r.body.Write(p)
}

func (r *safeRecorder) WriteHeader(code int) {
	r.code = code
}

func (r *safeRecorder) Flush() {}

func (r *safeRecorder) BodyString() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.body.String()
}

func TestSSEReceivesInitialPing(t *testing.T) {
	s := &Server{broadcaster: newBroadcaster()}
	defer s.StopBroadcaster()

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/live", nil).WithContext(ctx)
	rr := &safeRecorder{}

	go s.handleSSE(rr, req)

	// Poll for the initial ping; cancel context once found to stop the handler.
	deadline := time.After(500 * time.Millisecond)
	for {
		if strings.Contains(rr.BodyString(), "event: ping") {
			cancel()
			return
		}
		select {
		case <-deadline:
			cancel()
			t.Fatalf("initial ping not written: %q", rr.BodyString())
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
