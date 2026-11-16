package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"strings"

	"github.com/sirupsen/logrus"
	"github.com/yutopp/go-rtmp"
	rtmpmsg "github.com/yutopp/go-rtmp/message"
)

// StartRTMP listens on addr and returns the server with the address it is
// really bound to, which differs from addr when the port is 0.
func StartRTMP(addr string, sink *Sink) (*rtmp.Server, net.Addr, error) {
	l, err := net.Listen("tcp4", addr)
	if err != nil {
		return nil, nil, err
	}
	logger := logrus.New()
	// Errors only: the library's warnings print the arguments of the commands
	// it does not know, and those may hold the stream key.
	logger.SetLevel(logrus.ErrorLevel)

	srv := rtmp.NewServer(&rtmp.ServerConfig{
		OnConnect: func(conn net.Conn) (io.ReadWriteCloser, *rtmp.ConnConfig) {
			if tc, ok := conn.(*net.TCPConn); ok {
				tc.SetNoDelay(true)
				tc.SetReadBuffer(4 << 20)
			}
			return conn, &rtmp.ConnConfig{
				Handler: &rtmpHandler{sink: sink, remote: conn.RemoteAddr().String()},
				ControlState: rtmp.StreamControlStateConfig{
					DefaultBandwidthWindowSize: 6 * 1024 * 1024 / 8,
				},
				ReaderBufferSize: 64 << 10,
				Logger:           logger,
			}
		},
	})
	go srv.Serve(l)
	log.Printf("RTMP listening on %s", addr)
	return srv, l.Addr(), nil
}

// maxSteps bounds the trace kept for a client that never publishes.
const maxSteps = 20

// moreSteps replaces the last step when the trace is full.
const moreSteps = "…"

type rtmpHandler struct {
	rtmp.DefaultHandler
	sink       *Sink
	remote     string
	publishing bool
	// steps are the RTMP commands received so far, reported when the
	// connection closes without a stream. Only their names: the arguments hold
	// the Twitch stream key, which is never logged. Commands the RTMP library
	// does not know never reach the handler, so they are not listed.
	steps []string
}

func (h *rtmpHandler) step(name string) {
	switch {
	case len(h.steps) < maxSteps:
		h.steps = append(h.steps, name)
	case len(h.steps) == maxSteps:
		h.steps[maxSteps-1] = moreSteps
	}
}

func (h *rtmpHandler) OnConnect(_ uint32, cmd *rtmpmsg.NetConnectionConnect) error {
	h.step("connect")
	log.Printf("RTMP: connection from %s (app=%q)", h.remote, cmd.Command.App)
	return nil
}

func (h *rtmpHandler) OnReleaseStream(uint32, *rtmpmsg.NetConnectionReleaseStream) error {
	h.step("releaseStream")
	return nil
}

func (h *rtmpHandler) OnFCPublish(uint32, *rtmpmsg.NetStreamFCPublish) error {
	h.step("FCPublish")
	return nil
}

func (h *rtmpHandler) OnCreateStream(uint32, *rtmpmsg.NetConnectionCreateStream) error {
	h.step("createStream")
	return nil
}

// A command or data message the library knows, received when it is not
// expected.
func (h *rtmpHandler) OnUnknownCommandMessage(_ uint32, cmd *rtmpmsg.CommandMessage) error {
	h.step(fmt.Sprintf("unexpected command %q", cmd.CommandName))
	return nil
}

func (h *rtmpHandler) OnUnknownDataMessage(_ uint32, data *rtmpmsg.DataMessage) error {
	h.step(fmt.Sprintf("unexpected data %q", data.Name))
	return nil
}

func (h *rtmpHandler) OnPublish(_ *rtmp.StreamContext, _ uint32, _ *rtmpmsg.NetStreamPublish) error {
	h.step("publish")
	if !h.sink.BeginStream() {
		log.Printf("RTMP: stream from %s refused, another one is already in progress", h.remote)
		return errors.New("a stream is already in progress")
	}
	h.publishing = true
	log.Printf("▶ stream received from %s", h.remote)
	return nil
}

func (h *rtmpHandler) OnSetDataFrame(ts uint32, data *rtmpmsg.NetStreamSetDataFrame) error {
	if h.publishing {
		h.sink.Write(tagScript, ts, data.Payload)
	}
	return nil
}

func (h *rtmpHandler) OnAudio(ts uint32, payload io.Reader) error {
	return h.forward(tagAudio, ts, payload)
}

func (h *rtmpHandler) OnVideo(ts uint32, payload io.Reader) error {
	return h.forward(tagVideo, ts, payload)
}

func (h *rtmpHandler) forward(kind byte, ts uint32, payload io.Reader) error {
	if !h.publishing {
		return nil
	}
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(payload); err != nil {
		return err
	}
	h.sink.Write(kind, ts, buf.Bytes())
	return nil
}

func (h *rtmpHandler) OnClose() {
	if !h.publishing {
		// Without a single command it is not an RTMP client: stay silent.
		if len(h.steps) > 0 {
			log.Printf("RTMP: connection from %s closed without a stream after: %s", h.remote, strings.Join(h.steps, ", "))
		}
		return
	}
	h.publishing = false
	h.sink.EndStream()
	log.Printf("■ stream ended (%s)", h.remote)
}
