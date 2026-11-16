package main

import "testing"

func TestDefaultSpoofMatchesIngestDomains(t *testing.T) {
	h := &dnsHandler{patterns: defaultSpoof}
	cases := map[string]bool{
		"cdg10.contribute.live-video.net.":         true,
		"ingest.global-contribute.live-video.net.": true,
		"live-cdg.twitch.tv.":                      true,
		"ingest.twitch.tv.":                        false,
		"api.twitch.tv.":                           false,
		"video-edge.live-video.net.":               false,
	}
	for name, want := range cases {
		if got := h.match(name); got != want {
			t.Errorf("match(%q) = %v, want %v", name, got, want)
		}
	}
}
