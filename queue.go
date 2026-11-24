package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log"
	"slices"
	"sync"
	"time"
)

const (
	// maxLiveLag is how far behind the stream, in milliseconds, a live reader
	// may be when a keyframe arrives. Further than that, it jumps to the keyframe.
	maxLiveLag = 500
	// maxQueueBytes bounds what is kept for a reader that stopped reading a
	// stream without keyframes, where it has nowhere to jump to.
	maxQueueBytes = 32 << 20
	// behindReportEvery spaces the console lines about a reader that keeps
	// falling behind: it is brought back at every keyframe.
	behindReportEvery = 10 * time.Second
)

// readerState is what the queue knows of its reader, to tell the console once.
type readerState int

const (
	notStarted readerState = iota // has read nothing yet: a player takes a moment to open
	reading                       // took a tag since the last drop
	behind                        // had its backlog dropped, and has read nothing since
	stopped                       // had it dropped again: paused, frozen or gone
)

func (s readerState) String() string {
	return [...]string{"not started", "reading", "behind", "stopped"}[s]
}

var errSlowClient = errors.New("client too slow")

// liveQueue holds the tags a live reader (the local player or an HTTP viewer)
// has not read yet, so a slow reader never slows the PS5's stream down.
//
// A reader plays at normal speed: what waits here is delay it would never make
// up. So when a keyframe arrives and the reader is more than maxLiveLag
// behind, the waiting frames are dropped and the reader resumes at that
// keyframe. Later timestamps are moved back by what was dropped: the reader
// sees a cut in the picture, not a gap in time.
type liveQueue struct {
	reader string // who reads, for the console

	mu      sync.Mutex
	tags    []flvTag
	bytes   int
	dropped uint32 // milliseconds dropped so far, taken off the later timestamps
	newest  uint32 // timestamp of the latest tag queued

	state          readerState
	reportedBehind time.Time

	ready chan struct{} // holds a token when tags may be waiting
	done  chan struct{}
	once  sync.Once
}

func newLiveQueue(reader string) *liveQueue {
	return &liveQueue{reader: reader, ready: make(chan struct{}, 1), done: make(chan struct{})}
}

// writeTag never blocks. It fails once the queue is closed or full: the reader
// is then gone or stalled, and its output is dropped.
func (q *liveQueue) writeTag(tag flvTag) error {
	select {
	case <-q.done:
		return io.ErrClosedPipe
	default:
	}
	q.mu.Lock()
	defer q.mu.Unlock()

	tag.ts = msSince(q.dropped, tag.ts)
	if tag.isKeyframe() {
		lag := q.dropBacklog(tag.ts)
		q.dropped += lag
		tag.ts -= lag
	}
	if q.bytes+len(tag.data) > maxQueueBytes {
		return errSlowClient
	}
	q.tags = append(q.tags, tag)
	q.bytes += len(tag.data)
	q.newest = tag.ts
	select {
	case q.ready <- struct{}{}:
	default:
	}
	return nil
}

// dropBacklog drops the waiting frames when the oldest one is more than
// maxLiveLag older than the keyframe arriving at keyframeTS, and returns how
// many milliseconds of stream were dropped. The decoder configuration is kept:
// the reader may not have read it yet.
func (q *liveQueue) dropBacklog(keyframeTS uint32) uint32 {
	oldest := slices.IndexFunc(q.tags, func(tag flvTag) bool { return !tag.isConfig() })
	if oldest < 0 {
		return 0
	}
	lag := msSince(q.tags[oldest].ts, keyframeTS)
	if lag <= maxLiveLag {
		return 0
	}
	q.report(lag)
	resume := q.tags[oldest].ts // the timestamp the keyframe takes
	q.tags = slices.DeleteFunc(q.tags, func(tag flvTag) bool { return !tag.isConfig() })
	q.bytes = 0
	for i := range q.tags {
		q.tags[i].ts = min(q.tags[i].ts, resume) // timestamps never go back
		q.bytes += len(q.tags[i].data)
	}
	return lag
}

// report tells the console that the reader's backlog was dropped, without
// flooding it: a reader often behind is reported every behindReportEvery, and
// one that reads nothing at all is reported once.
func (q *liveQueue) report(lag uint32) {
	switch q.state {
	case reading:
		if time.Since(q.reportedBehind) >= behindReportEvery {
			log.Printf("%s was %.1f s behind: back to the live picture", q.reader, float64(lag)/1000)
			q.reportedBehind = time.Now()
		}
		q.state = behind
	case behind:
		log.Printf("%s has stopped reading the stream (paused or frozen)", q.reader)
		q.state = stopped
	}
}

// describe tells the debug log what the reader has not read yet (as stream
// time and as size), the timestamp of the latest tag it was sent, and how much
// stream was dropped for it so far.
func (q *liveQueue) describe() string {
	q.mu.Lock()
	defer q.mu.Unlock()
	var waiting uint32
	if oldest := slices.IndexFunc(q.tags, func(tag flvTag) bool { return !tag.isConfig() }); oldest >= 0 {
		waiting = msSince(q.tags[oldest].ts, q.newest)
	}
	return fmt.Sprintf("%q waiting=%dms/%dKB sent=%dms dropped=%dms state=%s",
		q.reader, waiting, q.bytes>>10, q.newest, q.dropped, q.state)
}

// pop returns the oldest waiting tag, if any.
func (q *liveQueue) pop() (flvTag, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.tags) == 0 {
		return flvTag{}, false
	}
	tag := q.tags[0]
	q.tags[0] = flvTag{} // lets the frame go once it is written
	q.tags = q.tags[1:]
	q.bytes -= len(tag.data)
	q.state = reading
	return tag, true
}

// copyTo writes the stream to w as FLV until the queue is closed, stop is
// closed or a write fails. It takes one tag at a time, so that what a blocked
// reader has not read stays in the queue, where it can be dropped.
func (q *liveQueue) copyTo(w io.Writer, stop <-chan struct{}) error {
	stream := bufio.NewWriterSize(w, 64<<10)
	stream.Write(flvHeader) // a failure is reported by the first flush
	for {
		select {
		case <-q.ready:
		case <-q.done:
			return nil
		case <-stop:
			return nil
		}
		for tag, ok := q.pop(); ok; tag, ok = q.pop() {
			if err := encodeTag(stream, tag); err != nil {
				return err
			}
		}
		if err := stream.Flush(); err != nil { // the reader is up to date: minimal latency
			return err
		}
	}
}

func (q *liveQueue) Close() error {
	q.once.Do(func() { close(q.done) })
	return nil
}

// msSince returns the milliseconds from origin to ts, 0 when ts is earlier.
func msSince(origin, ts uint32) uint32 {
	if ts < origin {
		return 0
	}
	return ts - origin
}
