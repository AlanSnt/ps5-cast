package main

import (
	"bytes"
	"log"
	"os"
	"reflect"
	"strings"
	"testing"
)

var (
	videoConfig = []byte{0x17, 0, 0, 0, 0, 0xC0}
	sound       = []byte{0xAF, 1, 0x50}
)

// picture returns the body of a video tag. The mark tells the pictures apart.
func picture(keyframe bool, mark byte) []byte {
	if keyframe {
		return []byte{0x17, 1, 0, 0, 0, mark}
	}
	return []byte{0x27, 1, 0, 0, 0, mark}
}

func readAll(queue *liveQueue) []flvTag {
	var tags []flvTag
	for tag, ok := queue.pop(); ok; tag, ok = queue.pop() {
		tags = append(tags, tag)
	}
	return tags
}

func TestLiveReaderAfterAStall(t *testing.T) {
	const key, inter = true, false

	for _, tc := range []struct {
		name   string
		stream []flvTag // received while the reader reads nothing
		want   []flvTag
	}{
		{
			name: "left more than maxLiveLag behind, it resumes at the latest keyframe",
			stream: []flvTag{
				{tagVideo, 1000, picture(key, 1)},
				{tagAudio, 1010, sound},
				{tagVideo, 1500, picture(inter, 2)},
				{tagVideo, 3000, picture(key, 3)},
				{tagVideo, 3016, picture(inter, 4)},
			},
			want: []flvTag{
				{tagVideo, 0, videoConfig},
				{tagVideo, 0, picture(key, 3)}, // takes the timestamp of the first tag dropped
				{tagVideo, 16, picture(inter, 4)},
			},
		},
		{
			name: "left behind twice, its timestamps still have no gap",
			stream: []flvTag{
				{tagVideo, 1000, picture(key, 1)},
				{tagVideo, 2000, picture(key, 2)},
				{tagVideo, 2100, picture(inter, 3)},
				{tagVideo, 3000, picture(key, 4)},
				{tagAudio, 3020, sound},
			},
			want: []flvTag{
				{tagVideo, 0, videoConfig},
				{tagVideo, 0, picture(key, 4)},
				{tagAudio, 20, sound},
			},
		},
		{
			name: "one millisecond past maxLiveLag, it resumes at the keyframe",
			stream: []flvTag{
				{tagVideo, 1000, picture(key, 1)},
				{tagVideo, 1001 + maxLiveLag, picture(key, 2)},
			},
			want: []flvTag{
				{tagVideo, 0, videoConfig},
				{tagVideo, 0, picture(key, 2)},
			},
		},
		{
			name: "however late, it only resumes at a keyframe",
			stream: []flvTag{
				{tagVideo, 1000, picture(key, 1)},
				{tagVideo, 3000, picture(inter, 2)},
			},
			want: []flvTag{
				{tagVideo, 0, videoConfig},
				{tagVideo, 0, picture(key, 1)},
				{tagVideo, 2000, picture(inter, 2)},
			},
		},
		{
			name: "a decoder configuration sent again is not a keyframe to resume at",
			stream: []flvTag{
				{tagVideo, 1000, picture(key, 1)},
				{tagVideo, 3000, videoConfig},
			},
			want: []flvTag{
				{tagVideo, 0, videoConfig},
				{tagVideo, 0, picture(key, 1)},
				{tagVideo, 2000, videoConfig},
			},
		},
		{
			name: "a decoder configuration sent while it was behind moves back with the keyframe",
			stream: []flvTag{
				{tagVideo, 1000, picture(key, 1)},
				{tagVideo, 3000, videoConfig},
				{tagVideo, 4000, picture(key, 2)},
			},
			want: []flvTag{
				{tagVideo, 0, videoConfig},
				{tagVideo, 0, videoConfig},
				{tagVideo, 0, picture(key, 2)},
			},
		},
		{
			name: "within maxLiveLag, it loses nothing",
			stream: []flvTag{
				{tagVideo, 1000, picture(key, 1)},
				{tagVideo, 1200, picture(inter, 2)},
				{tagVideo, 1000 + maxLiveLag, picture(key, 3)},
			},
			want: []flvTag{
				{tagVideo, 0, videoConfig},
				{tagVideo, 0, picture(key, 1)},
				{tagVideo, 200, picture(inter, 2)},
				{tagVideo, maxLiveLag, picture(key, 3)},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sink := liveSink(t)
			sink.Write(tagVideo, 0, videoConfig)
			reader, ok := sink.AddClient("viewer")
			if !ok {
				t.Fatal("AddClient refused a reader during a stream")
			}

			for _, tag := range tc.stream {
				sink.Write(tag.kind, tag.ts, tag.data)
			}

			if got := readAll(reader); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("the reader got\n%v\nwant\n%v", got, tc.want)
			}
		})
	}
}

// consoleDuring returns what the console receives until the test ends.
func consoleDuring(t *testing.T) *bytes.Buffer {
	t.Helper()
	var logged bytes.Buffer
	log.SetOutput(&logged)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	return &logged
}

// The console says why the picture jumped. When the reader then reads nothing
// at all (a paused or frozen window), it says so once and stays quiet.
func TestReaderThatStopsReadingIsReportedOnce(t *testing.T) {
	sink := liveSink(t)
	reader, _ := sink.AddClient("the window")
	logged := consoleDuring(t)

	sink.Write(tagVideo, 0, picture(true, 1))
	reader.pop()
	for ts := uint32(2000); ts <= 8000; ts += 2000 {
		sink.Write(tagVideo, ts, picture(true, 2))
	}

	for message, want := range map[string]int{
		"the window was 2.0 s behind: back to the live picture": 1,
		"the window has stopped reading":                        1,
	} {
		if got := strings.Count(logged.String(), message); got != want {
			t.Errorf("%q logged %d times, want %d:\n%s", message, got, want, logged.String())
		}
	}
}

// A reader that never keeps up (weak Wi-Fi, slow PC) is brought back at every
// keyframe: the console must not get a line each time.
func TestReaderOftenBehindDoesNotFloodTheConsole(t *testing.T) {
	sink := liveSink(t)
	reader, _ := sink.AddClient("the window")
	logged := consoleDuring(t)

	for ts := uint32(0); ts <= 8000; ts += 2000 {
		sink.Write(tagVideo, ts, picture(true, 1))
		sink.Write(tagVideo, ts+100, picture(false, 2))
		reader.pop() // reads, but never the whole of it
	}

	if got := strings.Count(logged.String(), "behind"); got != 1 {
		t.Errorf("the reader was reported behind %d times in a row, want once:\n%s", got, logged.String())
	}
	if strings.Contains(logged.String(), "stopped reading") {
		t.Errorf("a reader that still reads was reported as stopped:\n%s", logged.String())
	}
}

// A reader that reads as the stream comes is never brought forward, however
// long the stream lasts.
func TestLiveReaderThatKeepsUpLosesNothing(t *testing.T) {
	sink := liveSink(t)
	reader, _ := sink.AddClient("viewer")

	var got, want []flvTag
	for ts := uint32(0); ts < 10*maxLiveLag; ts += 100 {
		sink.Write(tagVideo, ts, picture(true, byte(ts/100)))
		want = append(want, flvTag{tagVideo, ts, picture(true, byte(ts/100))})
		got = append(got, readAll(reader)...)
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("the reader got\n%v\nwant\n%v", got, want)
	}
}
