package main

import (
	"bytes"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// keyframe is a minimal H.264 keyframe tag body (frame type 1, AVC, NALU).
var keyframe = append([]byte{0x17, 1, 0, 0, 0}, make([]byte, 1024)...)

// stalledPlayer never reads its input, like a paused or frozen mpv.
type stalledPlayer struct{ release chan struct{} }

func (p stalledPlayer) Write([]byte) (int, error) {
	<-p.release
	return 0, io.ErrClosedPipe
}

func (p stalledPlayer) Close() error { return nil }

func TestStalledPlayerDoesNotBlockTheStream(t *testing.T) {
	sink := liveSink(t)
	player := stalledPlayer{release: make(chan struct{})}
	defer close(player.release)

	sink.mu.Lock()
	sink.player = newPlayerOutput(player, nil)
	sink.mu.Unlock()

	written := make(chan struct{})
	go func() {
		defer close(written)
		for ts := uint32(0); ts < 4*outputQueue; ts++ {
			sink.Write(tagVideo, ts, keyframe)
		}
	}()

	select {
	case <-written:
	case <-time.After(5 * time.Second):
		t.Fatal("Sink.Write is blocked by a player that stopped reading")
	}

	sink.mu.Lock()
	dropped := sink.player == nil
	sink.mu.Unlock()
	if !dropped {
		t.Error("the stalled player is still attached, want it dropped")
	}
}

func TestRecordingNeverOverwritesAnExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "game.flv")

	for _, want := range []string{"game.flv", "game-2.flv", "game-3.flv"} {
		f, err := createRecord(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := filepath.Base(f.Name()); got != want {
			t.Errorf("recording file = %q, want %q", got, want)
		}
		if _, err := f.WriteString("stream"); err != nil {
			t.Fatal(err)
		}
		f.Close()
	}

	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != "stream" {
		t.Errorf("first recording = %q, want it left untouched", first)
	}
}

// failingFile fails every write, like a full disk.
type failingFile struct{}

func (failingFile) Write([]byte) (int, error) { return 0, errors.New("no space left on device") }
func (failingFile) Close() error              { return nil }

func TestFailedRecordingIsReportedAndClosed(t *testing.T) {
	sink := liveSink(t)
	sink.mu.Lock()
	sink.record = newOutput(failingFile{}, failingFile{}, nil)
	sink.mu.Unlock()

	var logged bytes.Buffer
	log.SetOutput(&logged)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	sink.Write(tagVideo, 0, keyframe)

	sink.mu.Lock()
	closed := sink.record == nil
	sink.mu.Unlock()
	if !closed {
		t.Error("the failed recording is still attached, want it closed")
	}
	if !strings.Contains(logged.String(), "no space left on device") {
		t.Errorf("log = %q, want the write error reported", logged.String())
	}
}
