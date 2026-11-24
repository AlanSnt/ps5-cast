package main

import (
	"bufio"
	"embed"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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
	playerLog  io.Writer // what mpv prints; nil discards it

	mu       sync.Mutex
	active   bool
	meta     []byte
	videoSeq []byte
	audioSeq []byte
	stats    streamStats

	player  *output
	record  *output
	clients map[*liveQueue]*output
}

// flvTag is one piece of the stream: a picture, a few milliseconds of sound,
// or the stream's metadata.
type flvTag struct {
	kind byte
	ts   uint32 // milliseconds
	data []byte
}

// isConfig reports whether the tag is what a decoder needs before any frame:
// the metadata and the H.264/AAC sequence headers.
func (t flvTag) isConfig() bool {
	return t.kind == tagScript ||
		(t.kind == tagVideo && isVideoSeqHeader(t.data)) ||
		(t.kind == tagAudio && isAudioSeqHeader(t.data))
}

// isKeyframe reports whether a reader can start decoding at this tag.
func (t flvTag) isKeyframe() bool {
	return t.kind == tagVideo && isKeyframe(t.data) && !isVideoSeqHeader(t.data)
}

// output is one FLV destination.
type output struct {
	dst    tagWriter
	cmd    *exec.Cmd
	opened time.Time // when the player was started
	synced bool      // received a keyframe
	base   uint32    // timestamp of the first keyframe → rebased to 0
	err    error     // first failed write: the output is dead
}

// tagWriter is where an output sends its tags: the recording, or the queue of
// a live reader.
type tagWriter interface {
	writeTag(flvTag) error
	io.Closer
}

func NewSink(playerMode, recordPath string) *Sink {
	return &Sink{playerMode: playerMode, recordPath: recordPath, clients: map[*liveQueue]*output{}}
}

func (s *Sink) BeginStream() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active {
		return false
	}
	s.active = true
	s.meta, s.videoSeq, s.audioSeq = nil, nil, nil
	s.stats = streamStats{}
	s.openPlayerLocked()
	if s.recordPath != "" && s.record == nil {
		if f, err := createRecord(s.recordPath); err != nil {
			log.Printf("cannot record: %v", err)
		} else {
			s.record = newRecordOutput(f)
			log.Printf("recording → %s", f.Name())
		}
	}
	return true
}

// createRecord never overwrites an earlier recording: when path already exists
// (a previous run, or the PS5 reconnecting) it creates game-2.flv, game-3.flv…
func createRecord(path string) (*os.File, error) {
	if st, err := os.Stat(path); err == nil && st.IsDir() {
		return nil, fmt.Errorf("%s is a directory, not a file", path)
	}
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
	if err := s.closeOutput(&s.record); err != nil {
		log.Printf("recording may be incomplete: %v", err)
	}
	for queue := range s.clients {
		s.dropClientLocked(queue)
	}
}

func (s *Sink) Active() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.active
}

// restartGuard is how long a player that just started is left alone.
var restartGuard = 2 * time.Second

// Reopen restarts the player if the stream is live: it brings back a closed
// window, and replaces one that shows a late or frozen picture.
func (s *Sink) Reopen() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.active {
		log.Println("no stream in progress")
		return
	}
	if s.player != nil && time.Since(s.player.opened) < restartGuard {
		return // Enter pressed twice: keep the window the first press opened
	}
	s.closeOutput(&s.player)
	s.openPlayerLocked()
	if s.player != nil {
		s.primeLocked(s.player)
	}
}

func (s *Sink) Close() { s.EndStream() }

func (s *Sink) Report(now time.Time, elapsed time.Duration) (streamReport, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.active {
		return streamReport{}, false
	}
	report := streamReport{arrival: s.stats.report(now, elapsed)}
	for queue := range s.clients {
		report.readers = append(report.readers, queue.describe())
	}
	slices.Sort(report.readers) // a map has no order: keep the viewers in the same one
	if s.player != nil {
		if queue, ok := s.player.dst.(*liveQueue); ok {
			report.readers = append([]string{queue.describe()}, report.readers...)
		}
	}
	return report, true
}

func (s *Sink) PlayerPID() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.player == nil || s.player.cmd == nil || s.player.cmd.Process == nil {
		return 0
	}
	return s.player.cmd.Process.Pid
}

// AddClient attaches an extra reader (HTTP client) to the current stream and
// returns the queue it reads the stream from. The name is for the console.
func (s *Sink) AddClient(name string) (*liveQueue, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.active {
		return nil, false
	}
	queue := newLiveQueue(name)
	o := &output{dst: queue}
	s.clients[queue] = o
	s.primeLocked(o)
	return queue, true
}

func (s *Sink) RemoveClient(queue *liveQueue) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dropClientLocked(queue)
}

func (s *Sink) dropClientLocked(queue *liveQueue) {
	delete(s.clients, queue)
	queue.Close()
}

func (s *Sink) Write(kind byte, ts uint32, data []byte) {
	if len(data) == 0 {
		return
	}
	tag := flvTag{kind, ts, data}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stats.add(tag, time.Now())

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
			s.writeTo(o, tag)
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
	for queue, o := range s.clients {
		s.writeTo(o, tag)
		if o.err != nil {
			s.dropClientLocked(queue) // client too slow or gone: drop it
		}
	}
}

func (s *Sink) writeTo(o *output, tag flvTag) {
	if !o.synced && !tag.isConfig() {
		if !tag.isKeyframe() {
			return // wait for a keyframe to start cleanly
		}
		o.synced, o.base = true, tag.ts
	}
	if o.synced {
		tag.ts = msSince(o.base, tag.ts)
	} else {
		tag.ts = 0
	}
	o.err = o.dst.writeTag(tag)
}

// primeLocked sends a freshly attached output what it needs to decode.
func (s *Sink) primeLocked(o *output) {
	for _, config := range []flvTag{{kind: tagScript, data: s.meta}, {kind: tagVideo, data: s.videoSeq}, {kind: tagAudio, data: s.audioSeq}} {
		if config.data != nil && o.err == nil {
			s.writeTo(o, config)
		}
	}
}

func (s *Sink) openPlayerLocked() {
	if s.player != nil || s.playerMode == "none" {
		return
	}
	name, path, args := playerCommand(s.playerMode)
	if path == "" {
		return // no local player: the web page does the job
	}
	cmd := exec.Command(path, args...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr // ffplay prints its warnings in the console
	if name == "mpv" {
		scripts, err := installMpvScripts()
		if err != nil {
			log.Printf("mpv starts without all its scripts (slower to catch up after a stall, a frozen picture while the window is hidden, or no measures in the debug log): %v", err)
		}
		cmd = exec.Command(path, append(scripts, args...)...)
		cmd.Stdout, cmd.Stderr = s.playerLog, s.playerLog
	}
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
	log.Printf("window opened with %s", name)
}

func (s *Sink) closeOutput(p **output) error {
	o := *p
	if o == nil {
		return nil
	}
	err := o.dst.Close()
	if o.cmd != nil && o.cmd.Process != nil {
		proc := o.cmd.Process
		time.AfterFunc(3*time.Second, func() { proc.Kill() }) // safety net
	}
	*p = nil
	return err
}

// newPlayerOutput feeds the player through a queue drained by its own
// goroutine: a player that stops reading (paused, frozen) never blocks the
// sink and every other output with it.
func newPlayerOutput(stdin io.WriteCloser, cmd *exec.Cmd) *output {
	queue := newLiveQueue("the player")
	go func() {
		defer stdin.Close() // EOF → the player exits on its own
		defer queue.Close() // window closed: the next sink write fails and drops the output
		queue.copyTo(stdin, nil)
	}()
	return &output{dst: queue, cmd: cmd, opened: time.Now()}
}

// recordFile is the recording: every tag is written, none is ever dropped.
type recordFile struct {
	stream *bufio.Writer
	file   io.Closer
}

func newRecordOutput(file io.WriteCloser) *output {
	stream := bufio.NewWriterSize(file, 256<<10)
	stream.Write(flvHeader) // a failure is reported by the first flush
	return &output{dst: &recordFile{stream: stream, file: file}}
}

func (r *recordFile) writeTag(tag flvTag) error {
	if err := encodeTag(r.stream, tag); err != nil {
		return err
	}
	if tag.kind != tagVideo {
		return nil
	}
	return r.stream.Flush() // one flush per frame: a crash loses one frame at most
}

func (r *recordFile) Close() error {
	return errors.Join(r.stream.Flush(), r.file.Close())
}

// encodeTag writes one FLV tag: 11-byte header + data + previous tag size.
func encodeTag(w io.Writer, tag flvTag) error {
	var hdr [11]byte
	n, ts, data := len(tag.data), tag.ts, tag.data
	hdr[0] = tag.kind
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

// playerCommand looks for mpv then ffplay, next to the executable first. It
// returns the player's name, the path of its executable and its arguments.
func playerCommand(mode string) (name, path string, args []string) {
	mpvArgs := []string{
		"--profile=low-latency", "--no-cache",
		// The profile's single CPU decoding thread drifts behind the sound on a busy machine.
		// No --untimed: it turns off the dropping of late pictures, and a picture
		// that cannot skip stays behind the sound once the catch-up plays faster.
		"--hwdec=auto-safe", "--framedrop=decoder+vo",
		"--demuxer-lavf-format=flv", "--demuxer-lavf-probesize=32768",
		// What mpv may hold of the stream when it falls behind: the catch-up
		// script sees it and speeds up. The rest waits in ps5cast, which drops it.
		"--demuxer-readahead-secs=0.5",
		"--title=PS5", "--force-window=immediate", "--keep-open=no",
		// Warnings, and the measures of mpv-stats.lua: both go to the debug log.
		"--msg-level=all=warn,mpv_stats=info", "--no-input-terminal", "-",
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
			return name, p, candidates[name]
		}
	}
	return "", "", nil
}

//go:embed mpv-catchup.lua mpv-stats.lua
var mpvScripts embed.FS

// mpv only loads a script from a file.
func installMpvScripts() (options []string, err error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(cache, "ps5cast")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	var failed []error
	for _, name := range []string{"mpv-catchup.lua", "mpv-stats.lua"} {
		script, err := mpvScripts.ReadFile(name)
		if err != nil {
			return nil, err
		}
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, script, 0o600); err != nil {
			failed = append(failed, err) // the other script is still worth loading
			continue
		}
		options = append(options, "--script="+path)
	}
	return options, errors.Join(failed...)
}

// localPlayerName returns "mpv" or "ffplay", or "" when no local player will be used.
func localPlayerName(mode string) string {
	if mode == "none" {
		return ""
	}
	name, _, _ := playerCommand(mode)
	return name
}

func findExe(name string) string {
	if self, err := os.Executable(); err == nil {
		for _, n := range []string{name + ".exe", name} {
			if p := filepath.Join(filepath.Dir(self), n); isFile(p) {
				return p
			}
		}
	}
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	// winget installs mpv there and leaves the PATH alone.
	if dir := os.Getenv("ProgramFiles"); dir != "" {
		if p := filepath.Join(dir, "MPV Player", name+".exe"); isFile(p) {
			return p
		}
	}
	return ""
}

func isFile(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}
