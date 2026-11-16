package main

import (
	"bytes"
	"errors"
	"io"
	"log"
	"net"

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

type rtmpHandler struct {
	rtmp.DefaultHandler
	sink       *Sink
	remote     string
	publishing bool
}

func (h *rtmpHandler) OnConnect(_ uint32, cmd *rtmpmsg.NetConnectionConnect) error {
	log.Printf("RTMP: connection from %s (app=%q)", h.remote, cmd.Command.App)
	return nil
}

func (h *rtmpHandler) OnPublish(_ *rtmp.StreamContext, _ uint32, _ *rtmpmsg.NetStreamPublish) error {
	// The Twitch stream key is never logged.
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
	if h.publishing {
		h.publishing = false
		h.sink.EndStream()
		log.Printf("■ stream ended (%s)", h.remote)
	}
}
