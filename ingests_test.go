package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

// ingestListServer serves body as Twitch's ingest list.
func ingestListServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestPublishedIngestsAreRedirected(t *testing.T) {
	srv := ingestListServer(t, http.StatusOK, `{"ingests": [
		{"url_template": "rtmp://cdg10.contribute.live-video.net/app/{stream_key}"},
		{"url_template": "rtmp://Ingest.New-Network.example.net/app/{stream_key}"},
		{"url_template": "rtmp://ingest.new-network.example.net/app/{stream_key}"},
		{"url_template": "rtmp://*.example.org/app/{stream_key}"},
		{"url_template": "rtmps://secure-only.example.net:443/app/{stream_key}"},
		{"url_template": ""}
	]}`)

	got := spoofPatterns(context.Background(), "", srv.URL)

	want := append(slices.Clone(defaultSpoof), "ingest.new-network.example.net")
	if !slices.Equal(got, want) {
		t.Errorf("spoofPatterns = %v, want %v", got, want)
	}
}

func TestBuiltInDomainsAreUsedWhenTheIngestListIsUnusable(t *testing.T) {
	for name, srv := range map[string]*httptest.Server{
		"server error": ingestListServer(t, http.StatusInternalServerError, `{"ingests": [{"url_template": "rtmp://new.example.net/app/{stream_key}"}]}`),
		"not JSON":     ingestListServer(t, http.StatusOK, "<html>"),
		"empty list":   ingestListServer(t, http.StatusOK, `{"ingests": []}`),
	} {
		if got := spoofPatterns(context.Background(), "", srv.URL); !slices.Equal(got, defaultSpoof) {
			t.Errorf("%s: spoofPatterns = %v, want the built-in %v", name, got, defaultSpoof)
		}
	}
}

func TestSpoofFlagIsUsedAsTheExactList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("the ingest list was fetched although -spoof is set")
	}))
	defer srv.Close()

	got := spoofPatterns(context.Background(), " A.example , b.example. ", srv.URL)

	if want := []string{"a.example", "b.example"}; !slices.Equal(got, want) {
		t.Errorf("spoofPatterns = %v, want %v", got, want)
	}
}
