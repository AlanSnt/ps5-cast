package main

import (
	"embed"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"time"
)

//go:embed web/index.html web/mpegts.js
var webFS embed.FS

// StartHTTP sert :
//   - /          page de lecture (navigateur, plein écran, à partager sur Discord)
//   - /live.flv  le flux en HTTP-FLV (OBS « Source média », VLC, mpv…)
//   - /status    {"live":true|false}
func StartHTTP(addr string, sink *Sink) (*http.Server, error) {
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
		return nil, err
	}
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go srv.Serve(l)
	log.Printf("HTTP en écoute sur %s", addr)
	return srv, nil
}

func serveFLV(w http.ResponseWriter, r *http.Request, sink *Sink) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming non supporté", http.StatusInternalServerError)
		return
	}
	cw := newChanWriter(240) // ~4 s de marge avant de couper un client lent
	out, ok := sink.AddClient(cw, cw)
	if !ok {
		http.Error(w, "aucun flux en cours", http.StatusServiceUnavailable)
		return
	}
	defer sink.RemoveClient(out)

	h := w.Header()
	h.Set("Content-Type", "video/x-flv")
	h.Set("Cache-Control", "no-store")
	h.Set("Access-Control-Allow-Origin", "*")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	client := r.RemoteAddr
	log.Printf("+ spectateur HTTP %s", client)
	defer log.Printf("- spectateur HTTP %s", client)

	for {
		select {
		case b := <-cw.ch:
			if _, err := w.Write(b); err != nil {
				return
			}
			// Vide ce qui est déjà en attente avant de flusher (moins d'appels système).
			for drained := false; !drained; {
				select {
				case b = <-cw.ch:
					if _, err := w.Write(b); err != nil {
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
