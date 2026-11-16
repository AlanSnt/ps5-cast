package main

import (
	"io"
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
