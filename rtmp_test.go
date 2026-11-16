package main

import (
	"bytes"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
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
	srv, bound, err := StartRTMP("127.0.0.1:0", NewSink("none", ""))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close() })
	return bound.String()
}

// Transaction IDs as AMF0 encodes them: 8 bytes of a float64.
const (
	idZero  = "\x00\x00\x00\x00\x00\x00\x00\x00"
	idTwo   = "\x40\x00\x00\x00\x00\x00\x00\x00"
	idThree = "\x40\x08\x00\x00\x00\x00\x00\x00"
)

// rtmpCommand builds one RTMP chunk holding an AMF0 command: its name, its
// transaction ID, then its arguments, already encoded.
func rtmpCommand(name, transactionID, args string) []byte {
	body := "\x02\x00" + string([]byte{byte(len(name))}) + name + "\x00" + transactionID + args
	// Chunk stream 3: a command message (0x14) on message stream 0.
	header := []byte{0x03, 0, 0, 0, 0, 0, byte(len(body)), 0x14, 0, 0, 0, 0}
	return append(header, body...)
}

// rawRTMPClient is a client past the handshake, that writes its own commands.
type rawRTMPClient struct {
	net.Conn
	t        *testing.T
	received []byte
}

func dialRawRTMP(t *testing.T, addr string) *rawRTMPClient {
	t.Helper()
	conn, err := net.Dial("tcp4", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := handshake.HandshakeWithServer(conn, conn, &handshake.Config{}); err != nil {
		t.Fatal(err)
	}
	return &rawRTMPClient{Conn: conn, t: t}
}

func (c *rawRTMPClient) send(name, transactionID, args string) {
	c.t.Helper()
	if _, err := c.Write(rtmpCommand(name, transactionID, args)); err != nil {
		c.t.Fatal(err)
	}
}

// awaitResult reads until the server has sent count "_result" answers and
// returns the transaction ID of the last one.
func (c *rawRTMPClient) awaitResult(count int) (transactionID string) {
	c.t.Helper()
	result := []byte("\x02\x00\x07_result\x00")
	buf := make([]byte, 1024)
	for {
		last := bytes.LastIndex(c.received, result)
		if bytes.Count(c.received, result) >= count && len(c.received) >= last+len(result)+8 {
			return string(c.received[last+len(result):][:8])
		}
		n, err := c.Read(buf)
		c.received = append(c.received, buf[:n]...)
		if err != nil && n == 0 {
			c.t.Fatalf("answer %d never came: %v", count, err)
		}
	}
}

const connectArgs = "\x03\x00\x03app\x02\x00\x03app\x00\x00\x09" // {app: "app"}

// The PS5 sends "connect" with transaction ID 0 and only accepts an answer
// carrying the same ID: with any other it waits 10 s and refuses to broadcast.
func TestConnectIsAnsweredWithTheClientTransactionID(t *testing.T) {
	client := dialRawRTMP(t, startTestRTMP(t))

	client.send("connect", idZero, connectArgs)

	if got := client.awaitResult(1); got != idZero {
		t.Errorf("_result carries transaction ID %x, want %x", got, idZero)
	}
}

// The console is what users paste when they report a problem: a command the
// RTMP library does not know must not put its arguments there.
func TestArgumentsOfUnknownCommandsAreNotPrinted(t *testing.T) {
	console, err := os.Create(filepath.Join(t.TempDir(), "console"))
	if err != nil {
		t.Fatal(err)
	}
	stderr := os.Stderr
	os.Stderr = console
	log.SetOutput(console)
	t.Cleanup(func() { os.Stderr = stderr; log.SetOutput(stderr); console.Close() })

	client := dialRawRTMP(t, startTestRTMP(t))
	client.send("connect", idZero, connectArgs)
	client.awaitResult(1)
	client.send("_checkbw", idTwo, "\x05\x02\x00\x0elive_SECRETKEY")
	client.send("createStream", idThree, "\x05")
	client.awaitResult(2) // answered after _checkbw was handled

	printed, err := os.ReadFile(console.Name())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(printed), "SECRETKEY") {
		t.Errorf("the console shows a command argument:\n%s", printed)
	}
}

// logLines receives every log line, so that a test can wait for one.
type logLines chan string

func (l logLines) Write(p []byte) (int, error) {
	l <- string(p)
	return len(p), nil
}

// When a client connects and no stream follows, the console must say how far
// the exchange went.
func TestConnectionClosedBeforePublishingIsReportedWithItsSteps(t *testing.T) {
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

	const want = "closed without a stream after: connect, createStream"
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
