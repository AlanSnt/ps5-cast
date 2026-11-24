package main

import (
	"net"
	"strings"
	"testing"
)

func TestIPFlagMustBeAnIPv4Address(t *testing.T) {
	for _, invalid := range []string{"192.168.1.300", "eth0", "::1"} {
		if ip, err := localIP(invalid); err == nil {
			t.Errorf("localIP(%q) = %v, want an error", invalid, ip)
		}
	}

	ip, err := localIP("192.168.1.20")
	if err != nil {
		t.Fatal(err)
	}
	if got := ip.String(); got != "192.168.1.20" {
		t.Errorf("localIP = %s, want 192.168.1.20", got)
	}
}

func TestHTTPURLsFollowTheListeningAddress(t *testing.T) {
	lanIP := net.IPv4(192, 168, 1, 20)
	for _, tc := range []struct {
		name       string
		bound      net.TCPAddr
		local, lan string
	}{
		{"all interfaces", net.TCPAddr{IP: net.IPv4zero, Port: 8080}, "http://localhost:8080", "http://192.168.1.20:8080"},
		{"loopback only", net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 8080}, "http://127.0.0.1:8080", ""},
		{"one interface", net.TCPAddr{IP: net.IPv4(10, 0, 0, 5), Port: 9000}, "http://10.0.0.5:9000", "http://10.0.0.5:9000"},
	} {
		local, lan := httpURLs(&tc.bound, lanIP)
		if local != tc.local || lan != tc.lan {
			t.Errorf("%s: httpURLs = (%q, %q), want (%q, %q)", tc.name, local, lan, tc.local, tc.lan)
		}
	}
}

func TestStartHTTPReportsThePortItReallyGot(t *testing.T) {
	srv, bound, err := StartHTTP("127.0.0.1:0", NewSink("none", ""))
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	if bound.Port == 0 {
		t.Error("bound port = 0, want the port chosen by the system")
	}
}

func TestBannerDescribesWhatWillDisplayTheStream(t *testing.T) {
	ip := net.IPv4(192, 168, 1, 20)
	const local, lan = "http://localhost:8080", "http://192.168.1.20:8080"

	for _, tc := range []struct {
		name                   string
		player, local, lan     string
		want, mustNotMentionIt []string
	}{
		{"ffplay", "ffplay", local, lan,
			[]string{`window (ffplay) opens`, "Enter = restart the ffplay window", "d + Enter = debug log", "(or " + lan + " from another device)", lan + "/live.flv"},
			[]string{"mpv"}},
		{"browser only", "", local, lan,
			[]string{"the stream plays in the browser", "Browser   : " + local, "d + Enter = debug log"},
			[]string{"mpv", "Enter = restart"}},
		{"loopback only", "", "http://127.0.0.1:8080", "",
			[]string{"http://127.0.0.1:8080/live.flv"},
			[]string{"another device", "192.168.1.20:8080"}},
		{"nothing to display", "", "", "",
			[]string{"nothing displays the stream"},
			[]string{"Browser", "OBS", "in the browser"}},
	} {
		got := banner(ip, tc.player, tc.local, tc.lan)
		for _, s := range tc.want {
			if !strings.Contains(got, s) {
				t.Errorf("%s: banner misses %q:\n%s", tc.name, s, got)
			}
		}
		for _, s := range tc.mustNotMentionIt {
			if strings.Contains(got, s) {
				t.Errorf("%s: banner mentions %q:\n%s", tc.name, s, got)
			}
		}
	}
}
