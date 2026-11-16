package main

import (
	"bytes"
	"io"
	"log"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/yutopp/go-rtmp"
	"github.com/yutopp/go-rtmp/handshake"
)

// startTestRTMP starts the RTMP server on a free local port.
func startTestRTMP(t *testing.T) (addr string) {
	t.Helper()
	free, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr = free.Addr().String()
	free.Close()
	srv, err := StartRTMP(addr, NewSink("none", ""))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close() })
	return addr
}

// The PS5 sends "connect" with transaction ID 0 and only accepts an answer
// carrying the same ID: with any other it waits 10 s and refuses to broadcast.
func TestConnectIsAnsweredWithTheClientTransactionID(t *testing.T) {
	conn, err := net.Dial("tcp4", startTestRTMP(t))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := handshake.HandshakeWithServer(conn, conn, &handshake.Config{}); err != nil {
		t.Fatal(err)
	}

	const transactionID = "\x00\x00\x00\x00\x00\x00\x00\x00" // the number 0
	connect := "\x02\x00\x07connect" + "\x00" + transactionID +
		"\x03\x00\x03app\x02\x00\x03app\x00\x00\x09" // {app: "app"}
	// One chunk on chunk stream 3: a command message (0x14) on stream 0.
	header := []byte{0x03, 0, 0, 0, 0, 0, byte(len(connect)), 0x14, 0, 0, 0, 0}
	if _, err := conn.Write(append(header, connect...)); err != nil {
		t.Fatal(err)
	}

	result := []byte("\x02\x00\x07_result\x00")
	var answer []byte
	buf := make([]byte, 1024)
	for {
		n, err := conn.Read(buf)
		answer = append(answer, buf[:n]...)
		if i := bytes.Index(answer, result); i >= 0 && len(answer) >= i+len(result)+8 {
			if got := string(answer[i+len(result):][:8]); got != transactionID {
				t.Errorf("_result carries transaction ID %x, want %x", got, transactionID)
			}
			return
		}
		if err != nil {
			t.Fatalf("no _result in the answer to connect: %v", err)
		}
	}
}

// logLines receives every log line, so that a test can wait for one.
type logLines chan string

func (l logLines) Write(p []byte) (int, error) {
	l <- string(p)
	return len(p), nil
}

// When the PS5 connects and then gives up, the console must say how far it got.
func TestClientLeavingBeforePublishingIsReportedWithItsSteps(t *testing.T) {
	addr := startTestRTMP(t)

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
