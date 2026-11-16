package main

import (
	"embed"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"strings"
	"time"
)

//go:embed web
var webFS embed.FS

// StartHTTP serves:
//   - /          player page (browser, fullscreen, to share on Discord)
//   - /live.flv  the stream as HTTP-FLV (OBS "Media Source", VLC, mpv…)
//   - /status    {"live":true|false}
func StartHTTP(addr string, sink *Sink) (*http.Server, *net.TCPAddr, error) {
	mux := http.NewServeMux()
	mux.Handle("GET /", http.FileServerFS(mustSub(webFS, "web")))
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		fmt.Fprintf(w, `{"live":%t}`, sink.Active())
	})
	mux.HandleFunc("GET /live.flv", func(w http.ResponseWriter, r *http.Request) {
		serveFLV(w, r, sink)
	})

	l, err := net.Listen("tcp4", addr)
	if err != nil {
		return nil, nil, err
	}
	srv := &http.Server{Handler: localHostsOnly(mux), ReadHeaderTimeout: 5 * time.Second}
	go srv.Serve(l)
	log.Printf("HTTP listening on %s", l.Addr())
	return srv, l.Addr().(*net.TCPAddr), nil
}

// localHostsOnly refuses requests that were not addressed to localhost or to
// an IP address. A DNS-rebinding page on another site can only reach this
// server through its own domain name, so it never gets past this check.
func localHostsOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(r.Host); err == nil {
			host = h
		}
		if !strings.EqualFold(host, "localhost") && net.ParseIP(strings.Trim(host, "[]")) == nil {
			http.Error(w, "host not allowed: open this page with localhost or the IP address", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// flvWriteTimeout is how long a viewer may accept no data before it is disconnected.
var flvWriteTimeout = 10 * time.Second

func serveFLV(w http.ResponseWriter, r *http.Request, sink *Sink) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}
	cw := newChanWriter(viewerQueue)
	out, ok := sink.AddClient(cw, cw)
	if !ok {
		http.Error(w, "no stream in progress", http.StatusServiceUnavailable)
		return
	}
	defer sink.RemoveClient(out)

	h := w.Header()
	h.Set("Content-Type", "video/x-flv")
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	client := r.RemoteAddr
	conn := http.NewResponseController(w)
	log.Printf("+ HTTP viewer %s", client)
	defer log.Printf("- HTTP viewer %s", client)

	// Each write gets its own deadline: without one, a stalled viewer would
	// block the write for minutes.
	write := func(b []byte) error {
		conn.SetWriteDeadline(time.Now().Add(flvWriteTimeout))
		_, err := w.Write(b)
		return err
	}

	for {
		select {
		case b := <-cw.ch:
			if write(b) != nil {
				return
			}
			// Drain what is already queued before flushing (fewer syscalls).
			for drained := false; !drained; {
				select {
				case b = <-cw.ch:
					if write(b) != nil {
						return
					}
				default:
					drained = true
				}
			}
			flusher.Flush()
		case <-cw.done:
			return
		case <-r.Context().Done():
			return
		}
	}
}

func mustSub(fsys embed.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		panic(err)
	}
	return sub
}
