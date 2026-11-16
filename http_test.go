package main

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// liveSink returns a sink with a stream in progress and no local player.
func liveSink(t *testing.T) *Sink {
	t.Helper()
	sink := NewSink("none", "")
	if !sink.BeginStream() {
		t.Fatal("BeginStream refused a first stream")
	}
	t.Cleanup(sink.Close)
	return sink
}

func flvServer(t *testing.T, sink *Sink) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serveFLV(w, r, sink)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// Another website open in the user's browser must not be able to read the stream.
func TestLiveFLVIsNotReadableCrossOrigin(t *testing.T) {
	srv := flvServer(t, liveSink(t))

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Access-Control-Allow-Origin = %q, want no CORS header", got)
	}
}

// A viewer whose connection stalls (phone asleep, Wi-Fi gone) must not keep its
// handler and socket alive.
func TestStalledViewerIsReleased(t *testing.T) {
	previous := flvWriteTimeout
	flvWriteTimeout = 200 * time.Millisecond
	t.Cleanup(func() { flvWriteTimeout = previous })

	sink := liveSink(t)
	released := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(released)
		serveFLV(w, r, sink)
	}))
	t.Cleanup(srv.Close)

	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })                          // runs before srv.Close, which waits for the handler
	fmt.Fprint(conn, "GET / HTTP/1.1\r\nHost: ps5cast\r\n\r\n") // and never read the answer

	// Feed more than the socket buffers and the viewer's queue can absorb.
	frame := append([]byte{0x17, 1, 0, 0, 0}, make([]byte, 64<<10)...)
	deadline := time.After(10 * time.Second)
	for ts := uint32(0); ; ts++ {
		sink.Write(tagVideo, ts, frame)
		select {
		case <-released:
			return
		case <-deadline:
			t.Fatal("the handler of a stalled viewer is still running")
		case <-time.After(time.Millisecond):
		}
	}
}
