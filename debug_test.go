package main

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestDebugLog(t *testing.T) *debugLog {
	t.Helper()
	started := time.Date(2026, 11, 24, 21, 30, 5, 0, time.Local)
	debug := newDebugLog(t.TempDir(), started, []string{"ps5cast test on a test machine"})
	t.Cleanup(debug.Stop)
	return debug
}

func readDebugLog(t *testing.T, debug *debugLog) string {
	t.Helper()
	content, err := os.ReadFile(debug.path)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

// The lag has often begun when the log is turned on: what was measured just
// before must be in the file too.
func TestDebugLogStartsWithWhatHappenedBeforeItWasTurnedOn(t *testing.T) {
	debug := newTestDebugLog(t)

	debug.Line("stream", "bitrate=6000kbps")
	if _, err := os.Stat(debug.path); err == nil {
		t.Fatal("the debug file exists before the log is turned on")
	}
	if err := debug.open(); err != nil {
		t.Fatal(err)
	}
	debug.Line("stream", "bitrate=200kbps")

	content := readDebugLog(t, debug)
	for _, want := range []string{"info   ps5cast test on a test machine", "stream bitrate=6000kbps", "stream bitrate=200kbps"} {
		if !strings.Contains(content, want) {
			t.Errorf("debug log misses %q:\n%s", want, content)
		}
	}
	if before, after := strings.Index(content, "6000kbps"), strings.Index(content, "200kbps"); before > after {
		t.Errorf("the line recorded before the log was turned on comes after the later one:\n%s", content)
	}
	if name := filepath.Base(debug.path); name != "ps5cast-debug-20261124-213005.log" {
		t.Errorf("debug file = %q, want it named after the start of the session", name)
	}
}

func TestDebugLogKeepsOnlyTheLatestLinesWhileOff(t *testing.T) {
	debug := newTestDebugLog(t)

	debug.Line("stream", "the oldest line")
	for range recentLines {
		debug.Line("stream", "a recent line")
	}
	if err := debug.open(); err != nil {
		t.Fatal(err)
	}

	content := readDebugLog(t, debug)
	if strings.Contains(content, "the oldest line") {
		t.Error("the debug log holds a line older than what it keeps while off")
	}
	if got := strings.Count(content, "a recent line"); got != recentLines {
		t.Errorf("debug log holds %d recent lines, want %d", got, recentLines)
	}
}

// One file per session: turning the log off and on again continues it.
func TestDebugLogTurnedOffAndOnAgainContinuesTheSameFile(t *testing.T) {
	debug := newTestDebugLog(t)

	debug.Toggle()
	debug.Line("stream", "first part")
	debug.Toggle()
	if debug.Active() {
		t.Fatal("the debug log is still on after it was turned off")
	}
	debug.Line("stream", "while off")
	debug.Toggle()
	debug.Line("stream", "second part")

	content := readDebugLog(t, debug)
	for _, want := range []string{"first part", "while off", "second part"} {
		if !strings.Contains(content, want) {
			t.Errorf("debug log misses %q:\n%s", want, content)
		}
	}
}

func TestEveryDebugLineIsDatedToTheMillisecond(t *testing.T) {
	debug := newTestDebugLog(t)
	if err := debug.open(); err != nil {
		t.Fatal(err)
	}
	debug.Line("pc", "cpu=12%")

	for _, line := range strings.Split(strings.TrimSpace(readDebugLog(t, debug)), "\n") {
		date, _, _ := strings.Cut(line, " info ")
		date, _, _ = strings.Cut(date, " pc ")
		if _, err := time.Parse("2006-01-02 15:04:05.000", date); err != nil {
			t.Errorf("line %q does not start with a date to the millisecond", line)
		}
	}
}

// mpv writes its output in pieces that do not stop at the end of a line.
func TestDebugWriterRecordsWholeLines(t *testing.T) {
	debug := newTestDebugLog(t)
	if err := debug.open(); err != nil {
		t.Fatal(err)
	}
	mpv := debug.Writer("mpv")

	for _, piece := range []string{"pos=1.0 dro", "pped=3\r\npos=2.0", " dropped=4\n", "\n"} {
		if _, err := mpv.Write([]byte(piece)); err != nil {
			t.Fatal(err)
		}
	}

	content := readDebugLog(t, debug)
	for _, want := range []string{"mpv    pos=1.0 dropped=3\n", "mpv    pos=2.0 dropped=4\n"} {
		if !strings.Contains(content, want) {
			t.Errorf("debug log misses %q:\n%s", want, content)
		}
	}
	if got := strings.Count(content, " mpv "); got != 2 {
		t.Errorf("debug log holds %d mpv lines, want 2 (no empty line)", got)
	}
}

func TestConsoleCommands(t *testing.T) {
	debug := newTestDebugLog(t)
	sink := NewSink("none", "")
	log.SetOutput(debug.Writer("log"))
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	obey("d", sink, debug)
	if !debug.Active() {
		t.Fatal("d did not turn the debug log on")
	}
	obey("big lag in the menu", sink, debug)
	obey(" d ", sink, debug)
	if debug.Active() {
		t.Error("d did not turn the debug log off")
	}
	if content := readDebugLog(t, debug); !strings.Contains(content, "note: big lag in the menu") {
		t.Errorf("debug log misses the note:\n%s", content)
	}
}

// mpv's warnings and the measures of mpv-stats.lua are what tells whether the
// window itself is what lags.
func TestWhatMpvPrintsLandsInTheDebugLog(t *testing.T) {
	standInMpv(t, "echo '[mpv_stats] pos=1.000 dropped=7'\nexec /bin/cat >/dev/null")
	debug := newTestDebugLog(t)
	if err := debug.open(); err != nil {
		t.Fatal(err)
	}
	sink := NewSink("mpv", "")
	sink.playerLog = debug.Writer("mpv")

	sink.BeginStream()
	t.Cleanup(sink.Close)

	const want = "mpv    [mpv_stats] pos=1.000 dropped=7\n"
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(readDebugLog(t, debug), want) {
		if time.Now().After(deadline) {
			t.Fatalf("debug log misses %q:\n%s", want, readDebugLog(t, debug))
		}
		time.Sleep(10 * time.Millisecond)
	}
}
