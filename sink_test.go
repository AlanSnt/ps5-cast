package main

import (
	"bytes"
	"errors"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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

// Without keyframes the player cannot be brought back to the live picture:
// what it does not read is kept up to maxQueueBytes, then it is dropped.
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
		sink.Write(tagVideo, 0, keyframe)
		picture := append([]byte{0x27, 1, 0, 0, 0}, make([]byte, 1<<20)...)
		// Twice the limit: the player may have taken a few pictures before it stalled.
		for sent := 0; sent <= 2*maxQueueBytes; sent += len(picture) {
			sink.Write(tagVideo, 1, picture)
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

// standInMpv makes the given shell commands the only mpv ps5cast can find.
func standInMpv(t *testing.T, commands string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in player is a shell script")
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "mpv"), []byte("#!/bin/sh\n"+commands+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Setenv("HOME", t.TempDir()) // the scripts given to mpv go to the user's cache
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
}

// streamWithStandInPlayer starts a stream shown by a stand-in mpv and returns
// the sink with a way to tell which player process is running.
func streamWithStandInPlayer(t *testing.T) (*Sink, func() *exec.Cmd) {
	t.Helper()
	standInMpv(t, "exec /bin/cat >/dev/null")

	sink := NewSink("mpv", "")
	if !sink.BeginStream() {
		t.Fatal("BeginStream refused a first stream")
	}
	t.Cleanup(sink.Close)
	running := func() *exec.Cmd {
		sink.mu.Lock()
		defer sink.mu.Unlock()
		if sink.player == nil {
			return nil
		}
		return sink.player.cmd
	}
	if running() == nil {
		t.Fatal("no player was started with the stream")
	}
	return sink, running
}

// Enter is the way out when the window is open but shows a late or frozen
// picture: the player is restarted, not only reopened once closed.
func TestReopenRestartsARunningPlayer(t *testing.T) {
	previous := restartGuard
	restartGuard = 0
	t.Cleanup(func() { restartGuard = previous })
	sink, running := streamWithStandInPlayer(t)
	first := running()

	sink.Reopen()

	if second := running(); second == nil || second == first {
		t.Errorf("after Reopen the player is %p, want a new one replacing %p", second, first)
	}
}

// Enter pressed twice, or held, must not close the window that the first
// press just opened.
func TestReopenLeavesAPlayerThatJustStarted(t *testing.T) {
	sink, running := streamWithStandInPlayer(t)
	first := running()

	sink.Reopen()

	if second := running(); second != first {
		t.Errorf("Reopen replaced a player started a moment ago (%p, was %p)", second, first)
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
	sink.record = newRecordOutput(failingFile{})
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

func TestEmptyPayloadsAreNotWritten(t *testing.T) {
	sink := liveSink(t)
	file, err := os.Create(filepath.Join(t.TempDir(), "game.flv"))
	if err != nil {
		t.Fatal(err)
	}
	sink.mu.Lock()
	sink.record = newRecordOutput(file)
	sink.mu.Unlock()
	sink.Write(tagVideo, 0, keyframe)
	before, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}

	sink.Write(tagAudio, 10, nil)
	sink.Write(tagVideo, 20, []byte{})

	after, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if after.Size() != before.Size() {
		t.Errorf("recording grew from %d to %d bytes, want empty payloads dropped", before.Size(), after.Size())
	}
}

func TestRecordingToADirectoryIsAnError(t *testing.T) {
	dir := t.TempDir()

	if f, err := createRecord(dir); err == nil {
		f.Close()
		t.Errorf("createRecord(directory) created %q, want an error", f.Name())
	}
}

// lateFailingFile accepts writes but fails on Close, like a network share that
// reports a deferred write error.
type lateFailingFile struct{}

func (lateFailingFile) Write(p []byte) (int, error) { return len(p), nil }
func (lateFailingFile) Close() error                { return errors.New("deferred write failed") }

func TestRecordingThatFailsOnCloseIsReported(t *testing.T) {
	sink := liveSink(t)
	sink.mu.Lock()
	sink.record = newRecordOutput(lateFailingFile{})
	sink.mu.Unlock()

	var logged bytes.Buffer
	log.SetOutput(&logged)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	sink.EndStream()

	if !strings.Contains(logged.String(), "deferred write failed") {
		t.Errorf("log = %q, want the close error reported", logged.String())
	}
}

func TestLocalPlayerNameIsTheLogicalName(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "ffplay"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Setenv("ProgramFiles", t.TempDir()) // no mpv installed by winget

	for mode, want := range map[string]string{"auto": "ffplay", "ffplay": "ffplay", "mpv": "", "none": ""} {
		if got := localPlayerName(mode); got != want {
			t.Errorf("localPlayerName(%q) = %q, want %q", mode, got, want)
		}
	}
}

// winget installs mpv in "Program Files\MPV Player" and leaves the PATH alone.
func TestMpvInstalledByWingetIsFound(t *testing.T) {
	programFiles := t.TempDir()
	mpv := filepath.Join(programFiles, "MPV Player", "mpv.exe")
	if err := os.MkdirAll(filepath.Dir(mpv), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mpv, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ProgramFiles", programFiles)
	t.Setenv("PATH", t.TempDir())

	if got := findExe("mpv"); got != mpv {
		t.Errorf("findExe(mpv) = %q, want %q", got, mpv)
	}
	if got := findExe("ffplay"); got != "" {
		t.Errorf("findExe(ffplay) = %q, want none", got)
	}
}
