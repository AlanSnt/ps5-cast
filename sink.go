package main

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	tagAudio  byte = 8
	tagVideo  byte = 9
	tagScript byte = 18
)

var flvHeader = []byte{'F', 'L', 'V', 1, 0x05, 0, 0, 0, 9, 0, 0, 0, 0}

// Sink receives the FLV tags of the RTMP stream and fans them out to several
// outputs: the local player, a file, and the HTTP-FLV clients (browser, OBS, VLC).
// It caches the metadata and the H.264/AAC sequence headers so an output can
// join mid-stream: it starts on the next keyframe.
type Sink struct {
	playerMode string
	recordPath string

	mu       sync.Mutex
	active   bool
	meta     []byte
	videoSeq []byte
	audioSeq []byte

	player  *output
	record  *output
	clients map[*output]struct{}
}

// output is one FLV destination.
type output struct {
	w      *bufio.Writer
	closer io.Closer
	cmd    *exec.Cmd
	synced bool   // received a keyframe
	base   uint32 // timestamp of the first keyframe → rebased to 0
	err    error  // first failed write: the output is dead
}

func NewSink(playerMode, recordPath string) *Sink {
	return &Sink{playerMode: playerMode, recordPath: recordPath, clients: map[*output]struct{}{}}
}

func (s *Sink) BeginStream() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active {
		return false
	}
	s.active = true
	s.meta, s.videoSeq, s.audioSeq = nil, nil, nil
	s.openPlayerLocked()
	if s.recordPath != "" && s.record == nil {
		if f, err := createRecord(s.recordPath); err != nil {
			log.Printf("cannot record: %v", err)
		} else {
			s.record = newOutput(f, f, nil)
			log.Printf("recording → %s", f.Name())
		}
	}
	return true
}

// createRecord never overwrites an earlier recording: when path already exists
// (a previous run, or the PS5 reconnecting) it creates game-2.flv, game-3.flv…
func createRecord(path string) (*os.File, error) {
	ext := filepath.Ext(path)
	stem := strings.TrimSuffix(path, ext)
	name := path
	for n := 2; ; n++ {
		f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if !errors.Is(err, fs.ErrExist) {
			return f, err
		}
		name = fmt.Sprintf("%s-%d%s", stem, n, ext)
	}
}

func (s *Sink) EndStream() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active = false
	s.closeOutput(&s.player)
	s.closeOutput(&s.record)
	for c := range s.clients {
		s.dropClientLocked(c)
	}
}

func (s *Sink) Active() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.active
}

// Reopen restarts the player if the stream is live and the window was closed.
func (s *Sink) Reopen() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.active {
		log.Println("no stream in progress")
		return
	}
	if s.player != nil && s.player.err == nil {
		return
	}
	s.closeOutput(&s.player)
	s.openPlayerLocked()
	if s.player != nil {
		s.primeLocked(s.player)
	}
}

func (s *Sink) Close() { s.EndStream() }

// AddClient attaches an extra output (HTTP client) to the current stream.
func (s *Sink) AddClient(w io.Writer, c io.Closer) (*output, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.active {
		return nil, false
	}
	o := newOutput(w, c, nil)
	s.clients[o] = struct{}{}
	s.primeLocked(o)
	return o, true
}

func (s *Sink) RemoveClient(o *output) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dropClientLocked(o)
}

func (s *Sink) dropClientLocked(o *output) {
	if _, ok := s.clients[o]; ok {
		delete(s.clients, o)
		o.closer.Close()
	}
}

func (s *Sink) Write(kind byte, ts uint32, data []byte) {
	if len(data) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	switch {
	case kind == tagScript:
		s.meta = data
	case kind == tagVideo && isVideoSeqHeader(data):
		s.videoSeq = data
	case kind == tagAudio && isAudioSeqHeader(data):
		s.audioSeq = data
	}

	for _, o := range []*output{s.player, s.record} {
		if o != nil && o.err == nil {
			s.writeTo(o, kind, ts, data)
		}
	}
	if s.player != nil && s.player.err != nil {
		log.Println("player closed or stalled — press Enter to reopen it")
		s.closeOutput(&s.player)
	}
	if s.record != nil && s.record.err != nil {
		log.Printf("recording stopped: %v", s.record.err)
		s.closeOutput(&s.record)
	}
	for c := range s.clients {
		s.writeTo(c, kind, ts, data)
		if c.err != nil {
			s.dropClientLocked(c) // client too slow or gone: drop it
		}
	}
}

func (s *Sink) writeTo(o *output, kind byte, ts uint32, data []byte) {
	isConfig := kind == tagScript ||
		(kind == tagVideo && isVideoSeqHeader(data)) ||
		(kind == tagAudio && isAudioSeqHeader(data))

	if !o.synced && !isConfig {
		if kind != tagVideo || !isKeyframe(data) {
			return // wait for a keyframe to start cleanly
		}
		o.synced, o.base = true, ts
	}
	var rel uint32
	if o.synced && ts >= o.base {
		rel = ts - o.base
	}
	if err := writeTag(o.w, kind, rel, data); err != nil {
		o.err = err
		return
	}
	if kind == tagVideo {
		if err := o.w.Flush(); err != nil { // one flush per frame: minimal latency
			o.err = err
		}
	}
}

// primeLocked sends a freshly attached output what it needs to decode.
func (s *Sink) primeLocked(o *output) {
	for _, t := range []struct {
		kind byte
		data []byte
	}{{tagScript, s.meta}, {tagVideo, s.videoSeq}, {tagAudio, s.audioSeq}} {
		if t.data != nil {
			s.writeTo(o, t.kind, 0, t.data)
		}
	}
	if err := o.w.Flush(); err != nil {
		o.err = err
	}
}

func (s *Sink) openPlayerLocked() {
	if s.player != nil || s.playerMode == "none" {
		return
	}
	name, args := playerCommand(s.playerMode)
	if name == "" {
		return // no local player: the web page does the job
	}
	cmd := exec.Command(name, args...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		log.Printf("player: %v", err)
		return
	}
	if err := cmd.Start(); err != nil {
		log.Printf("player: %v", err)
		return
	}
	go cmd.Wait() // avoids zombies
	s.player = newPlayerOutput(stdin, cmd)
	log.Printf("window opened with %s", filepath.Base(name))
}

func (s *Sink) closeOutput(p **output) {
	o := *p
	if o == nil {
		return
	}
	o.w.Flush()
	o.closer.Close()
	if o.cmd != nil && o.cmd.Process != nil {
		proc := o.cmd.Process
		time.AfterFunc(3*time.Second, func() { proc.Kill() }) // safety net
	}
	*p = nil
}

// newPlayerOutput feeds the player through a queue drained by its own
// goroutine: a player that stops reading (paused, frozen) fills the queue and
// is dropped, instead of blocking the sink and every other output with it.
func newPlayerOutput(stdin io.WriteCloser, cmd *exec.Cmd) *output {
	queue := newChanWriter(outputQueue)
	go func() {
		defer stdin.Close() // EOF → the player exits on its own
		for {
			select {
			case b := <-queue.ch:
				if _, err := stdin.Write(b); err != nil {
					queue.Close() // window closed: the next sink write fails and drops the output
					return
				}
			case <-queue.done:
				return
			}
		}
	}()
	return newOutput(queue, queue, cmd)
}

func newOutput(w io.Writer, c io.Closer, cmd *exec.Cmd) *output {
	o := &output{w: bufio.NewWriterSize(w, 256<<10), closer: c, cmd: cmd}
	if _, err := o.w.Write(flvHeader); err != nil {
		o.err = err
	}
	return o
}

// writeTag writes one FLV tag: 11-byte header + data + previous tag size.
func writeTag(w *bufio.Writer, kind byte, ts uint32, data []byte) error {
	var hdr [11]byte
	n := len(data)
	hdr[0] = kind
	hdr[1], hdr[2], hdr[3] = byte(n>>16), byte(n>>8), byte(n)
	hdr[4], hdr[5], hdr[6], hdr[7] = byte(ts>>16), byte(ts>>8), byte(ts), byte(ts>>24)
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	if _, err := w.Write(data); err != nil {
		return err
	}
	var prev [4]byte
	binary.BigEndian.PutUint32(prev[:], uint32(11+n))
	_, err := w.Write(prev[:])
	return err
}

// H.264 FLV: byte 0 = frameType<<4 | codecID(7), byte 1 = AVCPacketType.
func isVideoSeqHeader(d []byte) bool { return len(d) > 1 && d[0]&0x0f == 7 && d[1] == 0 }
func isKeyframe(d []byte) bool       { return len(d) > 0 && d[0]>>4 == 1 }

// AAC FLV: byte 0 = soundFormat(10)<<4 | …, byte 1 = AACPacketType.
func isAudioSeqHeader(d []byte) bool { return len(d) > 1 && d[0]>>4 == 10 && d[1] == 0 }

// chanWriter is a non-blocking Write to a channel. If the reader (HTTP client
// or local player) cannot keep up, the channel fills, Write fails and the
// output is dropped: the PS5's RTMP stream is never slowed down by a slow reader.
type chanWriter struct {
	ch   chan []byte
	done chan struct{}
	once sync.Once
}

var errSlowClient = errors.New("client too slow")

// outputQueue is how many video frames a reader may fall behind (~4 s at
// 60 fps) before it is dropped.
const outputQueue = 240

func newChanWriter(depth int) *chanWriter {
	return &chanWriter{ch: make(chan []byte, depth), done: make(chan struct{})}
}

func (c *chanWriter) Write(p []byte) (int, error) {
	select {
	case <-c.done:
		return 0, io.ErrClosedPipe
	default:
	}
	b := make([]byte, len(p))
	copy(b, p)
	select {
	case c.ch <- b:
		return len(p), nil
	default:
		return 0, errSlowClient
	}
}

func (c *chanWriter) Close() error {
	c.once.Do(func() { close(c.done) })
	return nil
}

// playerCommand looks for mpv then ffplay, next to the executable first.
func playerCommand(mode string) (string, []string) {
	mpvArgs := []string{
		"--profile=low-latency", "--no-cache", "--untimed",
		"--demuxer-lavf-format=flv", "--demuxer-lavf-probesize=32768",
		"--title=PS5", "--force-window=immediate", "--keep-open=no",
		"--msg-level=all=warn", "--no-terminal", "-",
	}
	ffArgs := []string{
		"-hide_banner", "-loglevel", "warning",
		"-fflags", "nobuffer", "-flags", "low_delay", "-framedrop",
		"-probesize", "32768", "-f", "flv", "-window_title", "PS5", "-i", "pipe:0",
	}
	candidates := map[string][]string{"mpv": mpvArgs, "ffplay": ffArgs}
	order := []string{"mpv", "ffplay"}
	if mode == "mpv" || mode == "ffplay" {
		order = []string{mode}
	}
	for _, name := range order {
		if p := findExe(name); p != "" {
			return p, candidates[name]
		}
	}
	return "", nil
}

// localPlayerName returns "mpv" or "ffplay", or "" when no local player will be used.
func localPlayerName(mode string) string {
	if mode == "none" {
		return ""
	}
	path, _ := playerCommand(mode)
	if path == "" {
		return ""
	}
	return strings.TrimSuffix(filepath.Base(path), ".exe")
}

func findExe(name string) string {
	if self, err := os.Executable(); err == nil {
		for _, n := range []string{name + ".exe", name} {
			p := filepath.Join(filepath.Dir(self), n)
			if st, err := os.Stat(p); err == nil && !st.IsDir() {
				return p
			}
		}
	}
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	return ""
}
