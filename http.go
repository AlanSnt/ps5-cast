package main

import (
	"embed"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
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
// A request without a Host cannot come from a browser and is served.
func localHostsOnly(next http.Handler) http.Handler {
	var refused sync.Map // host names already reported in the console
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := strings.TrimSuffix((&url.URL{Host: r.Host}).Hostname(), ".")
		if host != "" && !strings.EqualFold(host, "localhost") && net.ParseIP(host) == nil {
			if _, seen := refused.LoadOrStore(host, true); !seen {
				log.Printf("HTTP: refused a request for %q — use localhost or the IP address instead of a name", host)
			}
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
	client := "HTTP viewer " + r.RemoteAddr
	queue, ok := sink.AddClient(client)
	if !ok {
		http.Error(w, "no stream in progress", http.StatusServiceUnavailable)
		return
	}
	defer sink.RemoveClient(queue)

	h := w.Header()
	h.Set("Content-Type", "video/x-flv")
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	log.Printf("+ %s", client)
	defer log.Printf("- %s", client)

	viewer := viewerWriter{w: w, conn: http.NewResponseController(w), flusher: flusher}
	queue.copyTo(viewer, r.Context().Done())
}

// viewerWriter sends each write to the viewer at once, with its own deadline:
// without one, a stalled viewer would block the write for minutes.
type viewerWriter struct {
	w       http.ResponseWriter
	conn    *http.ResponseController
	flusher http.Flusher
}

func (v viewerWriter) Write(b []byte) (int, error) {
	v.conn.SetWriteDeadline(time.Now().Add(flvWriteTimeout))
	n, err := v.w.Write(b)
	v.flusher.Flush()
	return n, err
}

func mustSub(fsys embed.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		panic(err)
	}
	return sub
}
