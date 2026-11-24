package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	recentLines  = 500 // about two minutes of a stream shown by mpv
	measureEvery = time.Second
	probeEvery   = 5 * time.Second
)

// While the log is off, the latest lines are kept in memory, so turning it on
// in the middle of a lag also records what led to it.
type debugLog struct {
	path  string
	intro []string

	mu     sync.Mutex
	file   *os.File // nil while the log is off
	recent []string
}

func newDebugLog(dir string, started time.Time, intro []string) *debugLog {
	path := filepath.Join(dir, "ps5cast-debug-"+started.Format("20060102-150405")+".log")
	if absolute, err := filepath.Abs(path); err == nil {
		path = absolute
	}
	return &debugLog{path: path, intro: intro}
}

func (d *debugLog) Active() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.file != nil
}

func (d *debugLog) Toggle() {
	if d.Active() {
		d.Stop()
		log.Printf("debug log off: %s", d.path)
		return
	}
	if err := d.open(); err != nil {
		log.Printf("debug log: %v", err)
		return
	}
	log.Printf("debug log on → %s", d.path)
}

func (d *debugLog) open() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.file != nil {
		return nil
	}
	file, err := os.OpenFile(d.path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	d.file = file
	for _, text := range d.intro {
		d.put(debugLine("info", text))
	}
	d.put(debugLine("info", fmt.Sprintf("the next %d lines were recorded before the debug log was turned on", len(d.recent))))
	recent := d.recent
	d.recent = nil
	for _, line := range recent {
		d.put(line)
	}
	if d.file == nil { // put closed it: the disk is full, or the file went away
		return fmt.Errorf("cannot write to %s", d.path)
	}
	return nil
}

func (d *debugLog) Stop() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.file != nil {
		d.file.Close()
		d.file = nil
	}
}

func (d *debugLog) Line(source, text string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.put(debugLine(source, text))
}

func debugLine(source, text string) string {
	return fmt.Sprintf("%s %-6s %s\n", time.Now().Format("2006-01-02 15:04:05.000"), source, text)
}

// The caller holds d.mu.
func (d *debugLog) put(line string) {
	if d.file == nil {
		d.recent = append(d.recent, line)
		if len(d.recent) > recentLines {
			d.recent = d.recent[1:]
		}
		return
	}
	if _, err := d.file.WriteString(line); err != nil {
		// Not through the log package: it writes to this debug log.
		fmt.Fprintf(os.Stderr, "debug log stopped: %v\n", err)
		d.file.Close()
		d.file = nil
	}
}

func (d *debugLog) Writer(source string) io.Writer {
	return &lineWriter{record: func(line string) { d.Line(source, line) }}
}

type lineWriter struct {
	record func(line string)

	mu      sync.Mutex
	partial []byte
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.partial = append(w.partial, p...)
	for {
		end := bytes.IndexAny(w.partial, "\r\n")
		if end < 0 {
			return len(p), nil
		}
		if end > 0 {
			w.record(string(w.partial[:end]))
		}
		w.partial = w.partial[end+1:]
	}
}

// measure also runs while the log is off, as long as a stream is in progress:
// these are the lines kept for the moment the log is turned on.
func (d *debugLog) measure(ctx context.Context, sink *Sink, pc *machine) {
	tick := time.NewTicker(measureEvery)
	defer tick.Stop()
	previous := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-tick.C:
			elapsed := now.Sub(previous)
			previous = now
			report, live := sink.Report(now, elapsed)
			if !live && !d.Active() {
				continue
			}
			if live {
				d.Line("stream", report.arrival)
				for _, reader := range report.readers {
					d.Line("reader", reader)
				}
			}
			d.note("pc", pc.load(now))
		}
	}
}

func (d *debugLog) probe(ctx context.Context, sink *Sink, upstream string) {
	tick := time.NewTicker(probeEvery)
	defer tick.Stop()
	var programs processes
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-tick.C:
			logging := d.Active()
			if !logging && !sink.Active() {
				continue
			}
			d.note("procs", programs.busiest(now, int32(os.Getpid()), int32(sink.PlayerPID())))
			d.note("net", internetDelay(upstream))
			if logging { // asking the system for the GPU costs more than the rest
				d.note("gpu", gpuLoad())
			}
		}
	}
}

func (d *debugLog) note(source, measure string) {
	if measure != "" {
		d.Line(source, measure)
	}
}

func obey(typed string, sink *Sink, debug *debugLog) {
	switch typed = strings.TrimSpace(typed); strings.ToLower(typed) {
	case "":
		sink.Reopen()
	case "d":
		debug.Toggle()
	default:
		log.Printf("note: %s", typed)
	}
}
