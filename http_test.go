package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
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
