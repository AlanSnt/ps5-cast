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
	// recentLines is how many lines are kept while the debug log is off: about
	// two minutes of a stream shown by mpv.
	recentLines = 500
	// measureEvery spaces the measures of the stream and of the machine.
	measureEvery = time.Second
	// probeEvery spaces the measures that cost more to take.
	probeEvery = 5 * time.Second
)

// debugLog is the debug log of a session: one file that receives the console
// messages, the measures taken while the stream plays and what mpv reports.
//
// While it is off, the latest lines are kept in memory, so turning it on in
// the middle of a lag also records what led to it.
type debugLog struct {
	path  string
	intro []string // what the session runs on, written every time the log is turned on

	mu     sync.Mutex
	file   *os.File // nil while the log is off
	recent []string // the latest lines while it is off
}

// newDebugLog prepares the debug log of a session started at the given time.
// The file is only created once the log is turned on.
func newDebugLog(dir string, started time.Time, intro []string) *debugLog {
	path := filepath.Join(dir, "ps5cast-debug-"+started.Format("20060102-150405")+".log")
	if absolute, err := filepath.Abs(path); err == nil {
		path = absolute // the console tells the user where to find the file
	}
	return &debugLog{path: path, intro: intro}
}

func (d *debugLog) Active() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.file != nil
}

// Toggle turns the log on or off and tells the console where the file is.
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

// open turns the log on: it continues the file of the session, starting with
// the lines kept while the log was off.
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

// Line records one line. The source tells where it comes from: log, stream,
// reader, pc, mpv…
func (d *debugLog) Line(source, text string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.put(debugLine(source, text))
}

func debugLine(source, text string) string {
	return fmt.Sprintf("%s %-6s %s\n", time.Now().Format("2006-01-02 15:04:05.000"), source, text)
}

// put writes the line to the file, or keeps it while the log is off. The
// caller holds d.mu.
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

// Writer returns a writer that records every line it receives under the given
// source.
func (d *debugLog) Writer(source string) io.Writer {
	return &lineWriter{record: func(line string) { d.Line(source, line) }}
}

// lineWriter cuts what it receives into lines, whatever the size of the writes.
type lineWriter struct {
	record func(line string)

	mu      sync.Mutex
	partial []byte // the start of a line whose end has not arrived yet
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

// measure records every second how the stream arrives, what each reader has
// not read yet and the load of the machine. It also does while the log is off,
// as long as a stream is in progress: these are the lines kept for the moment
// the log is turned on.
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

// probe records every five seconds the measures that take longer: the busiest
// programs and how fast the Internet answers, while a stream is in progress or
// the log is on. The GPU is only measured while the log is on, as asking the
// system for it costs more than the rest.
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
			if logging {
				d.note("gpu", gpuLoad())
			}
		}
	}
}

// note records a measure, unless the system did not give it.
func (d *debugLog) note(source, measure string) {
	if measure != "" {
		d.Line(source, measure)
	}
}

// obey runs what was typed in the console: Enter alone restarts the player,
// "d" turns the debug log on or off, and any other text is a note that lands
// in the debug log, to tell when a lag was seen.
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
