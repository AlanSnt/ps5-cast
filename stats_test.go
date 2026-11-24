package main

import (
	"strings"
	"testing"
	"time"
)

// arrive feeds the pictures of a 50 pictures/s stream, each one arriving the
// given number of milliseconds after the start.
func arrive(stats *streamStats, start time.Time, arrivalsMS ...int) {
	for i, ms := range arrivalsMS {
		tag := flvTag{tagVideo, uint32(20 * i), picture(i == 0, byte(i))}
		stats.add(tag, start.Add(time.Duration(ms)*time.Millisecond))
	}
}

func TestStreamStatsOfASteadyStream(t *testing.T) {
	var stats streamStats
	start := time.Now()

	arrive(&stats, start, 0, 20, 40, 60)
	stats.add(flvTag{tagAudio, 60, sound}, start.Add(60*time.Millisecond))
	got := stats.report(start.Add(60*time.Millisecond), 100*time.Millisecond)

	// 4 pictures of 6 bytes and a sound of 3 bytes in a tenth of a second.
	want := "bitrate=2kbps pictures=4 keyframes=1 sounds=1 longest_gap=20ms late=0ms"
	if got != want {
		t.Errorf("report = %q, want %q", got, want)
	}
}

// A picture held back by the network arrives after the moment its timestamp
// calls for: that delay is what the viewer sees as a freeze.
func TestStreamStatsTellHowLateAPictureArrived(t *testing.T) {
	var stats streamStats
	start := time.Now()

	arrive(&stats, start, 0, 20, 340, 345)
	got := stats.report(start.Add(345*time.Millisecond), time.Second)

	for _, want := range []string{"longest_gap=320ms", "late=300ms"} {
		if !strings.Contains(got, want) {
			t.Errorf("report = %q, want it to hold %q", got, want)
		}
	}
}

// The first pictures of a stream may come slower than the following ones (the
// PS5 sends what it had buffered): lateness is measured against the fastest
// picture seen, not against the first one.
func TestStreamStatsMeasureLatenessAgainstTheFastestPicture(t *testing.T) {
	var stats streamStats
	start := time.Now()

	arrive(&stats, start, 0, 5, 10, 15, 335)
	got := stats.report(start.Add(335*time.Millisecond), time.Second)

	// The 4th picture came 45 ms ahead of the first one's pace, the 5th 255 ms after it.
	if want := "late=300ms"; !strings.Contains(got, want) {
		t.Errorf("report = %q, want it to hold %q", got, want)
	}
}

// The PS5's clock and the PC's do not tick at the same rate. Over an evening
// that must not read as pictures arriving later and later.
func TestStreamStatsDoNotMistakeClockDriftForLateness(t *testing.T) {
	var stats streamStats
	start := time.Now()
	const hour = 3600

	var report string
	for second := range hour {
		sent := time.Duration(second) * time.Second
		drift := sent / 10_000 // the PC's clock runs 100 ppm faster: 360 ms in an hour
		stats.add(flvTag{tagVideo, uint32(second * 1000), picture(true, 0)}, start.Add(sent+drift))
		report = stats.report(start.Add(sent+drift), time.Second)
	}

	if !strings.Contains(report, "late=0ms") {
		t.Errorf("report after an hour of drift = %q, want late=0ms", report)
	}
}

func TestStreamStatsStartAgainAfterEachReport(t *testing.T) {
	var stats streamStats
	start := time.Now()
	arrive(&stats, start, 0, 20, 340)
	stats.report(start.Add(340*time.Millisecond), time.Second)

	got := stats.report(start.Add(1340*time.Millisecond), time.Second)

	// Nothing arrived: the wait since the last picture is the gap.
	want := "bitrate=0kbps pictures=0 keyframes=0 sounds=0 longest_gap=1000ms late=0ms"
	if got != want {
		t.Errorf("second report = %q, want %q", got, want)
	}
}

func TestQueueDescribesWhatTheReaderHasNotRead(t *testing.T) {
	queue := newLiveQueue("the player")
	for _, tag := range []flvTag{
		{tagVideo, 0, videoConfig},
		{tagVideo, 1000, picture(true, 1)},
		{tagVideo, 1400, picture(false, 2)},
	} {
		if err := queue.writeTag(tag); err != nil {
			t.Fatal(err)
		}
	}

	want := `"the player" waiting=400ms/0KB sent=1400ms dropped=0ms state=not started`
	if got := queue.describe(); got != want {
		t.Errorf("report = %q, want %q", got, want)
	}

	readAll(queue)
	queue.writeTag(flvTag{tagVideo, 3000, picture(true, 3)})
	queue.writeTag(flvTag{tagVideo, 4000, picture(true, 4)})

	want = `"the player" waiting=0ms/0KB sent=3000ms dropped=1000ms state=behind`
	if got := queue.describe(); got != want {
		t.Errorf("report after a drop = %q, want %q", got, want)
	}
}

func TestSinkReportsTheStreamAndItsReaders(t *testing.T) {
	sink := NewSink("none", "")
	if _, live := sink.Report(time.Now(), time.Second); live {
		t.Fatal("a report says a stream is in progress before any stream")
	}
	sink.BeginStream()
	t.Cleanup(sink.Close)
	for _, viewer := range []string{"HTTP viewer 10.0.0.9:5000", "HTTP viewer 10.0.0.2:5000"} {
		if _, ok := sink.AddClient(viewer); !ok {
			t.Fatalf("%s was refused", viewer)
		}
	}

	sink.Write(tagVideo, 0, keyframe)
	report, live := sink.Report(time.Now(), time.Second)

	if !live {
		t.Fatal("the report says no stream is in progress")
	}
	if !strings.Contains(report.arrival, "pictures=1 keyframes=1") {
		t.Errorf("arrival = %q, want the picture received", report.arrival)
	}
	if len(report.readers) != 2 ||
		!strings.HasPrefix(report.readers[0], `"HTTP viewer 10.0.0.2:5000" waiting=0ms/1KB`) ||
		!strings.HasPrefix(report.readers[1], `"HTTP viewer 10.0.0.9:5000" waiting=0ms/1KB`) {
		t.Errorf("readers = %q, want the two viewers by name, each with the picture it has not read", report.readers)
	}
}
