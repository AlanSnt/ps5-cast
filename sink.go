package main

import (
	"bufio"
	"encoding/binary"
	"errors"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

const (
	tagAudio  byte = 8
	tagVideo  byte = 9
	tagScript byte = 18
)

var flvHeader = []byte{'F', 'L', 'V', 1, 0x05, 0, 0, 0, 9, 0, 0, 0, 0}

// Sink reçoit les tags FLV du flux RTMP et les distribue à plusieurs sorties :
// le lecteur local, un fichier, et les clients HTTP-FLV (navigateur, OBS, VLC).
// Il garde en cache les métadonnées et les "sequence headers" H.264/AAC pour
// brancher une sortie en cours de flux : elle démarre sur la prochaine image clé.
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

// output = une destination FLV.
type output struct {
	w      *bufio.Writer
	closer io.Closer
	cmd    *exec.Cmd
	synced bool   // a reçu une image clé
	base   uint32 // timestamp de la 1re image clé → rebase à 0
	broken bool
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
		if f, err := os.Create(s.recordPath); err != nil {
			log.Printf("enregistrement impossible : %v", err)
		} else {
			s.record = newOutput(f, f, nil)
			log.Printf("enregistrement → %s", s.recordPath)
		}
	}
	return true
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

// Reopen relance le lecteur si le flux tourne et que la fenêtre a été fermée.
func (s *Sink) Reopen() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.active {
		log.Println("aucun flux en cours")
		return
	}
	if s.player != nil && !s.player.broken {
		return
	}
	s.closeOutput(&s.player)
	s.openPlayerLocked()
	if s.player != nil {
		s.primeLocked(s.player)
	}
}

func (s *Sink) Close() { s.EndStream() }

// AddClient branche une sortie supplémentaire (client HTTP) sur le flux en cours.
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
		if o != nil && !o.broken {
			s.writeTo(o, kind, ts, data)
		}
	}
	if s.player != nil && s.player.broken {
		log.Println("fenêtre fermée — appuie sur Entrée pour la rouvrir")
		s.closeOutput(&s.player)
	}
	for c := range s.clients {
		s.writeTo(c, kind, ts, data)
		if c.broken {
			s.dropClientLocked(c) // client trop lent ou parti : on le coupe
		}
	}
}

func (s *Sink) writeTo(o *output, kind byte, ts uint32, data []byte) {
	isConfig := kind == tagScript ||
		(kind == tagVideo && isVideoSeqHeader(data)) ||
		(kind == tagAudio && isAudioSeqHeader(data))

	if !o.synced && !isConfig {
		if kind != tagVideo || !isKeyframe(data) {
			return // on attend une image clé pour démarrer net
		}
		o.synced, o.base = true, ts
	}
	var rel uint32
	if o.synced && ts >= o.base {
		rel = ts - o.base
	}
	if err := writeTag(o.w, kind, rel, data); err != nil {
		o.broken = true
		return
	}
	if kind == tagVideo {
		if err := o.w.Flush(); err != nil { // 1 flush par image : latence minimale
			o.broken = true
		}
	}
}

// primeLocked envoie à une sortie fraîchement branchée ce qu'il faut pour décoder.
func (s *Sink) primeLocked(o *output) {
	for _, t := range []struct {
		kind byte
		data []byte
	}{{tagScript, s.meta}, {tagVideo, s.videoSeq}, {tagAudio, s.audioSeq}} {
		if t.data != nil {
			s.writeTo(o, t.kind, 0, t.data)
		}
	}
	if o.w.Flush() != nil {
		o.broken = true
	}
}

func (s *Sink) openPlayerLocked() {
	if s.player != nil || s.playerMode == "none" {
		return
	}
	name, args := playerCommand(s.playerMode)
	if name == "" {
		return // pas de lecteur local : la page web fait l'affaire
	}
	cmd := exec.Command(name, args...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		log.Printf("lecteur : %v", err)
		return
	}
	if err := cmd.Start(); err != nil {
		log.Printf("lecteur : %v", err)
		return
	}
	go cmd.Wait() // évite les zombies ; une écriture échouera si la fenêtre est fermée
	s.player = newOutput(stdin, stdin, cmd)
	log.Printf("fenêtre ouverte avec %s", filepath.Base(name))
}

func (s *Sink) closeOutput(p **output) {
	o := *p
	if o == nil {
		return
	}
	o.w.Flush()
	o.closer.Close() // EOF → le lecteur se ferme tout seul
	if o.cmd != nil && o.cmd.Process != nil {
		proc := o.cmd.Process
		time.AfterFunc(3*time.Second, func() { proc.Kill() }) // filet de sécurité
	}
	*p = nil
}

func newOutput(w io.Writer, c io.Closer, cmd *exec.Cmd) *output {
	o := &output{w: bufio.NewWriterSize(w, 256<<10), closer: c, cmd: cmd}
	if _, err := o.w.Write(flvHeader); err != nil {
		o.broken = true
	}
	return o
}

// writeTag écrit un tag FLV : en-tête 11 octets + données + taille précédente.
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

// H.264 FLV : octet 0 = frameType<<4 | codecID(7), octet 1 = AVCPacketType.
func isVideoSeqHeader(d []byte) bool { return len(d) > 1 && d[0]&0x0f == 7 && d[1] == 0 }
func isKeyframe(d []byte) bool       { return len(d) > 0 && d[0]>>4 == 1 }

// AAC FLV : octet 0 = soundFormat(10)<<4 | …, octet 1 = AACPacketType.
func isAudioSeqHeader(d []byte) bool { return len(d) > 1 && d[0]>>4 == 10 && d[1] == 0 }

// chanWriter : Write non bloquant vers un canal. Si le client HTTP ne suit pas,
// le canal se remplit, Write échoue et le client est coupé — le flux RTMP de
// la PS5 n'est jamais ralenti par un spectateur lent.
type chanWriter struct {
	ch   chan []byte
	done chan struct{}
	once sync.Once
}

var errSlowClient = errors.New("client trop lent")

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

// playerCommand cherche mpv puis ffplay, d'abord à côté de l'exécutable.
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

func hasLocalPlayer(mode string) bool {
	if mode == "none" {
		return false
	}
	p, _ := playerCommand(mode)
	return p != ""
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
