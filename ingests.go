package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

// twitchIngestsURL is the public list of Twitch ingest servers, the one the
// PS5 reads before it broadcasts.
const twitchIngestsURL = "https://ingest.twitch.tv/ingests"

type ingestList struct {
	Ingests []struct {
		URLTemplate string `json:"url_template"`
	} `json:"ingests"`
}

// spoofPatterns returns the domains to redirect: exactly the -spoof list when
// it is set, otherwise the built-in patterns plus every published ingest they
// do not cover yet. Without the list, the built-in patterns are used alone.
func spoofPatterns(ctx context.Context, spoofFlag, ingestsURL string) []string {
	if spoofFlag != "" {
		return splitPatterns(spoofFlag)
	}
	patterns := slices.Clone(defaultSpoof)
	hosts, err := fetchIngestHosts(ctx, ingestsURL)
	if err != nil {
		log.Printf("Twitch ingest list unavailable (%v): using the built-in domains only", err)
		return patterns
	}
	for _, host := range hosts {
		if !matchAny(patterns, host) {
			log.Printf("new Twitch ingest domain: %s", host)
			patterns = append(patterns, host)
		}
	}
	return patterns
}

// fetchIngestHosts returns the host of every RTMP ingest in the list.
func fetchIngestHosts(ctx context.Context, ingestsURL string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ingestsURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %s", resp.Status)
	}

	var list ingestList
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&list); err != nil {
		return nil, err
	}
	var hosts []string
	for _, ingest := range list.Ingests {
		if host, ok := rtmpHost(ingest.URLTemplate); ok {
			hosts = append(hosts, host)
		}
	}
	if len(hosts) == 0 {
		return nil, errors.New("no RTMP ingest in the list")
	}
	return hosts, nil
}

// rtmpHost extracts the host of an "rtmp://host/app/{stream_key}" template.
// Anything that is not a plain host name is refused: the host becomes a
// redirect pattern, so it must not carry wildcards.
func rtmpHost(urlTemplate string) (string, bool) {
	u, err := url.Parse(urlTemplate)
	if err != nil || u.Scheme != "rtmp" {
		return "", false
	}
	host := strings.ToLower(u.Hostname())
	isHostChar := func(r rune) bool {
		return r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '.'
	}
	if host == "" || strings.ContainsFunc(host, func(r rune) bool { return !isHostChar(r) }) {
		return "", false
	}
	return host, true
}
