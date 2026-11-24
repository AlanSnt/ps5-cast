package main

import (
	"fmt"
	"time"
)

type streamStats struct {
	// Since the previous report.
	bytes, pictures, keyframes, sounds int
	longestGap                         time.Duration
	late                               time.Duration

	// Since the start of the stream.
	began       time.Time
	lastPicture time.Time
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

func (s *streamStats) report(now time.Time, elapsed time.Duration) string {
	gap := s.longestGap
	if !s.lastPicture.IsZero() {
		gap = max(gap, now.Sub(s.lastPicture)) // the picture that has still not arrived
	}
	kbps := float64(s.bytes) * 8 / 1000 / elapsed.Seconds()
	line := fmt.Sprintf("bitrate=%.0fkbps pictures=%d keyframes=%d sounds=%d longest_gap=%dms late=%dms",
		kbps, s.pictures, s.keyframes, s.sounds, gap.Milliseconds(), s.late.Milliseconds())
	// The clocks of the PS5 and of the PC drift apart: without forgetting the
	// fastest picture little by little, every picture would look later over hours.
	forgiven := elapsed / 1000
	*s = streamStats{began: s.began, lastPicture: s.lastPicture, fastest: s.fastest + forgiven}
	return line
}

type streamReport struct {
	arrival string
	readers []string
}
