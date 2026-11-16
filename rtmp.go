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

func StartRTMP(addr string, sink *Sink) (*rtmp.Server, error) {
	l, err := net.Listen("tcp4", addr)
	if err != nil {
		return nil, err
	}
	logger := logrus.New()
	logger.SetLevel(logrus.WarnLevel)

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
	return srv, nil
}

// maxSteps bounds the trace kept for a client that never publishes.
const maxSteps = 20

type rtmpHandler struct {
	rtmp.DefaultHandler
	sink       *Sink
	remote     string
	publishing bool
	// steps are the RTMP commands received so far, reported when the client
	// leaves without publishing. Only their names: the arguments hold the
	// Twitch stream key, which is never logged.
	steps []string
}

func (h *rtmpHandler) step(name string) {
	if len(h.steps) < maxSteps {
		h.steps = append(h.steps, name)
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

func (h *rtmpHandler) OnUnknownCommandMessage(_ uint32, cmd *rtmpmsg.CommandMessage) error {
	h.step(fmt.Sprintf("unknown command %q", cmd.CommandName))
	return nil
}

func (h *rtmpHandler) OnUnknownDataMessage(_ uint32, data *rtmpmsg.DataMessage) error {
	h.step(fmt.Sprintf("unknown data %q", data.Name))
	return nil
}

func (h *rtmpHandler) OnUnknownMessage(_ uint32, msg rtmpmsg.Message) error {
	h.step(fmt.Sprintf("unknown message %T", msg))
	return nil
}

func (h *rtmpHandler) OnPublish(_ *rtmp.StreamContext, _ uint32, _ *rtmpmsg.NetStreamPublish) error {
	h.step("publish")
	if !h.sink.BeginStream() {
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
		log.Printf("RTMP: %s left without sending a stream after: %s", h.remote, strings.Join(h.steps, ", "))
		return
	}
	h.publishing = false
	h.sink.EndStream()
	log.Printf("■ stream ended (%s)", h.remote)
}
