package main

import (
	"log"
	"net"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/miekg/dns"
)

// Twitch ingest domains. ingest.twitch.tv is deliberately NOT redirected:
// it is an HTTPS discovery endpoint, intercepting it would break the stream.
var defaultSpoof = []string{
	"*.contribute.live-video.net",
	"live*.twitch.tv",
}

type dnsServer struct {
	udp, tcp *dns.Server
}

func (s *dnsServer) Shutdown() {
	s.udp.Shutdown()
	s.tcp.Shutdown()
}

type dnsHandler struct {
	ip       net.IP
	upstream string
	patterns []string
	verbose  bool
	udp, tcp *dns.Client

	mu   sync.Mutex
	seen map[string]bool
}

func StartDNS(ip net.IP, upstream string, patterns []string, verbose bool) (*dnsServer, error) {
	h := &dnsHandler{
		ip: ip, upstream: upstream, patterns: patterns, verbose: verbose,
		udp:  &dns.Client{Net: "udp", Timeout: 3 * time.Second},
		tcp:  &dns.Client{Net: "tcp", Timeout: 5 * time.Second},
		seen: map[string]bool{},
	}
	s := &dnsServer{
		udp: &dns.Server{Addr: ":53", Net: "udp", Handler: h, UDPSize: 65535},
		tcp: &dns.Server{Addr: ":53", Net: "tcp", Handler: h},
	}
	if err := listenAndServe(s.udp); err != nil {
		return nil, err
	}
	if err := listenAndServe(s.tcp); err != nil {
		s.udp.Shutdown()
		return nil, err
	}
	log.Printf("DNS listening on :53 (relay → %s, redirecting %v)", upstream, patterns)
	return s, nil
}

// listenAndServe starts the server and waits until it is really listening,
// so a bind error (port 53 in use) is reported right away.
func listenAndServe(s *dns.Server) error {
	started := make(chan struct{})
	errc := make(chan error, 1)
	s.NotifyStartedFunc = func() { close(started) }
	go func() { errc <- s.ListenAndServe() }()
	select {
	case <-started:
		return nil
	case err := <-errc:
		return err
	}
}

func (h *dnsHandler) match(name string) bool {
	name = strings.TrimSuffix(strings.ToLower(name), ".")
	for _, p := range h.patterns {
		if ok, _ := path.Match(p, name); ok {
			return true
		}
	}
	return false
}

func (h *dnsHandler) ServeDNS(w dns.ResponseWriter, req *dns.Msg) {
	if len(req.Question) == 1 {
		q := req.Question[0]
		if h.match(q.Name) {
			h.answerSpoofed(w, req, q)
			return
		}
		h.logQuery(w, q.Name)
	}
	h.forward(w, req)
}

func (h *dnsHandler) answerSpoofed(w dns.ResponseWriter, req *dns.Msg, q dns.Question) {
	resp := new(dns.Msg)
	resp.SetReply(req)
	resp.Authoritative = true
	switch q.Qtype {
	case dns.TypeA:
		resp.Answer = []dns.RR{&dns.A{
			Hdr: dns.RR_Header{Name: q.Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 30},
			A:   h.ip,
		}}
		log.Printf("→ %s redirected to this PC (%s)", strings.TrimSuffix(q.Name, "."), h.ip)
	default:
		// AAAA and others: empty answer to force IPv4.
	}
	w.WriteMsg(resp)
}

func (h *dnsHandler) forward(w dns.ResponseWriter, req *dns.Msg) {
	c := h.udp
	if _, isTCP := w.RemoteAddr().(*net.TCPAddr); isTCP {
		c = h.tcp
	}
	resp, _, err := c.Exchange(req, h.upstream)
	if err == nil && resp.Truncated && c == h.udp {
		resp, _, err = h.tcp.Exchange(req, h.upstream)
	}
	if err != nil {
		fail := new(dns.Msg)
		fail.SetRcode(req, dns.RcodeServerFailure)
		w.WriteMsg(fail)
		return
	}
	resp.Id = req.Id
	w.WriteMsg(resp)
}

// logQuery logs each domain only once (all of them with -v): handy to spot a
// new ingest domain without flooding the console.
func (h *dnsHandler) logQuery(w dns.ResponseWriter, name string) {
	name = strings.TrimSuffix(name, ".")
	if !h.verbose {
		h.mu.Lock()
		dup := h.seen[name]
		h.seen[name] = true
		h.mu.Unlock()
		if dup {
			return
		}
	}
	log.Printf("  dns %s ← %s", name, hostOnly(w.RemoteAddr()))
}

func hostOnly(a net.Addr) string {
	h, _, err := net.SplitHostPort(a.String())
	if err != nil {
		return a.String()
	}
	return h
}
