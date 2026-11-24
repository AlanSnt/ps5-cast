package main

import (
	"fmt"
	"time"
)

// streamStats measures how the PS5's stream arrives between two reports.
type streamStats struct {
	// Since the previous report.
	bytes, pictures, keyframes, sounds int
	longestGap                         time.Duration // longest wait between two pictures
	late                               time.Duration // how late the latest-coming picture arrived

	// Since the start of the stream.
	began       time.Time     // arrival of the first picture
	lastPicture time.Time     // arrival of the latest one
	fastest     time.Duration // shortest trip of a picture: the others are late compared to it
}

func (s *streamStats) add(tag flvTag, now time.Time) {
	s.bytes += len(tag.data)
	switch {
	case tag.kind == tagAudio:
		s.sounds++
	case tag.kind == tagVideo && !tag.isConfig():
		s.addPicture(tag, now)
	}
}

// addPicture compares when the picture arrived with when its timestamp says it
// was made. Clocks differ, so only the trips can be compared with each other:
// a picture is late by how much longer its trip was than the shortest one.
func (s *streamStats) addPicture(tag flvTag, now time.Time) {
	first := s.lastPicture.IsZero()
	if first {
		s.began = now
	}
	trip := now.Sub(s.began) - time.Duration(tag.ts)*time.Millisecond
	if first || trip < s.fastest {
		s.fastest = trip
	}
	s.late = max(s.late, trip-s.fastest)
	if !first {
		s.longestGap = max(s.longestGap, now.Sub(s.lastPicture))
	}
	s.lastPicture = now

	s.pictures++
	if tag.isKeyframe() {
		s.keyframes++
	}
}

// report describes what arrived during the time elapsed since the previous
// report, and starts a new count.
func (s *streamStats) report(now time.Time, elapsed time.Duration) string {
	gap := s.longestGap
	if !s.lastPicture.IsZero() {
		gap = max(gap, now.Sub(s.lastPicture)) // the picture that has still not arrived
	}
	kbps := float64(s.bytes) * 8 / 1000 / elapsed.Seconds()
	line := fmt.Sprintf("bitrate=%.0fkbps pictures=%d keyframes=%d sounds=%d longest_gap=%dms late=%dms",
		kbps, s.pictures, s.keyframes, s.sounds, gap.Milliseconds(), s.late.Milliseconds())
	// The clocks of the PS5 and of the PC do not tick at the same rate: over
	// hours, every picture would look later and later than a fastest one seen
	// long ago. Forgetting it by a thousandth of the time elapsed is far more
	// than two clocks drift apart, and leaves a real delay visible for minutes.
	forgiven := elapsed / 1000
	*s = streamStats{began: s.began, lastPicture: s.lastPicture, fastest: s.fastest + forgiven}
	return line
}

// streamReport is what the debug log records about a stream in progress.
type streamReport struct {
	arrival string   // how the PS5's stream arrived since the previous report
	readers []string // what the player and each HTTP viewer have not read yet
}
