package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
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

// A DNS-rebinding page reaches the server through its own domain name: only
// requests addressed to localhost or to an IP address may be served.
func TestOnlyLocalhostAndIPHostsAreServed(t *testing.T) {
	srv, bound, err := StartHTTP("127.0.0.1:0", NewSink("none", ""))
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	port := fmt.Sprint(bound.Port)

	for host, want := range map[string]int{
		"127.0.0.1:" + port:      http.StatusOK,
		"localhost:" + port:      http.StatusOK,
		"LOCALHOST:" + port:      http.StatusOK,
		"192.168.1.20:" + port:   http.StatusOK,
		"192.168.1.20":           http.StatusOK,
		"localhost.:" + port:     http.StatusOK,
		"[::1]:" + port:          http.StatusOK,
		"[::1]":                  http.StatusOK,
		"evil.example:" + port:   http.StatusForbidden,
		"evil.example":           http.StatusForbidden,
		"my-pc.local:" + port:    http.StatusForbidden,
		"localhost.evil.example": http.StatusForbidden,
	} {
		req, err := http.NewRequest(http.MethodGet, "http://"+bound.String()+"/status", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Host = host
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("Host %q: status = %d, want %d", host, resp.StatusCode, want)
		}
	}
}

// A browser always sends a Host header, so a request without one cannot come
// from a rebinding page: old HTTP/1.0 clients keep working.
func TestRequestWithoutHostIsServed(t *testing.T) {
	srv, bound, err := StartHTTP("127.0.0.1:0", NewSink("none", ""))
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	conn, err := net.Dial("tcp", bound.String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	fmt.Fprint(conn, "GET /status HTTP/1.0\r\n\r\n")

	status, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(status, "200") {
		t.Errorf("status line = %q, want 200", status)
	}
}

// OBS and VLC never show the body of a 403: the console must say why.
func TestRefusedHostIsLoggedOnce(t *testing.T) {
	srv, bound, err := StartHTTP("127.0.0.1:0", NewSink("none", ""))
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	var logged bytes.Buffer
	log.SetOutput(&logged)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	for range 3 {
		req, err := http.NewRequest(http.MethodGet, "http://"+bound.String()+"/status", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Host = "my-pc.test"
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}

	if got := strings.Count(logged.String(), "my-pc.test"); got != 1 {
		t.Errorf("refused host logged %d times, want once:\n%s", got, logged.String())
	}
}

// A viewer that joins mid-stream gets a complete FLV stream: the file header,
// the decoder configuration received before it joined, then the next keyframe
// with its timestamp brought back to zero.
func TestViewerJoiningMidStreamGetsADecodableStream(t *testing.T) {
	sink := liveSink(t)
	srv := flvServer(t, sink)
	config := []byte{0x17, 0, 0, 0, 0, 0xC0}
	sink.Write(tagVideo, 0, config)
	sink.Write(tagVideo, 5000, keyframe)

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	sink.Write(tagVideo, 7000, []byte{0x27, 1, 0, 0, 0, 0xAA}) // not a keyframe: skipped
	sink.Write(tagVideo, 8000, keyframe)

	var want bytes.Buffer
	stream := bufio.NewWriter(&want)
	stream.Write(flvHeader)
	encodeTag(stream, flvTag{tagVideo, 0, config})
	encodeTag(stream, flvTag{tagVideo, 0, keyframe})
	stream.Flush()

	got := make([]byte, want.Len())
	if _, err := io.ReadFull(resp.Body, got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want.Bytes()) {
		t.Errorf("the viewer got %d bytes that are not the header, the configuration and the keyframe", len(got))
	}
}
