package main

import (
	"io"
	"log"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/yutopp/go-rtmp"
)

// logLines receives every log line, so that a test can wait for one.
type logLines chan string

func (l logLines) Write(p []byte) (int, error) {
	l <- string(p)
	return len(p), nil
}

// When the PS5 connects and then gives up, the console must say how far it got.
func TestClientLeavingBeforePublishingIsReportedWithItsSteps(t *testing.T) {
	free, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := free.Addr().String()
	free.Close()
	srv, err := StartRTMP(addr, NewSink("none", ""))
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	lines := make(logLines, 16)
	log.SetOutput(lines)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	quiet := logrus.New()
	quiet.SetOutput(io.Discard)
	client, err := rtmp.Dial("rtmp", addr, &rtmp.ConnConfig{Logger: quiet})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Connect(nil); err != nil {
		t.Fatal(err)
	}
	if _, err := client.CreateStream(nil, 128); err != nil {
		t.Fatal(err)
	}
	client.Close()

	const want = "left without sending a stream after: connect, createStream"
	timeout := time.After(2 * time.Second)
	for {
		select {
		case line := <-lines:
			if strings.Contains(line, want) {
				return
			}
		case <-timeout:
			t.Fatalf("no log line containing %q", want)
		}
	}
}
