package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// latestReleaseServer answers like GitHub's "latest release" endpoint.
func latestReleaseServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestNewerStableVersionIsReported(t *testing.T) {
	for _, tc := range []struct {
		name, running, latestTag, want string
	}{
		{"older release", "0.1.0", "v0.2.0", "0.2.0"},
		{"older patch", "0.1.9", "v0.1.10", "0.1.10"},
		{"pre-release of the latest stable", "0.2.0-alpha", "v0.2.0", "0.2.0"},
		{"latest release", "0.2.0", "v0.2.0", ""},
		{"pre-release ahead of the latest stable", "0.3.0-alpha", "v0.2.0", ""},
		{"newer than the latest stable", "0.3.0", "v0.2.0", ""},
	} {
		srv := latestReleaseServer(t, http.StatusOK, `{"tag_name": "`+tc.latestTag+`"}`)
		got, err := newerVersion(context.Background(), tc.running, srv.URL)
		if err != nil || got != tc.want {
			t.Errorf("%s: newerVersion(%q) with latest %s = %q, %v, want %q",
				tc.name, tc.running, tc.latestTag, got, err, tc.want)
		}
	}
}

func TestBuildWithoutAVersionDoesNotAskGitHub(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("the latest release was fetched for a development build")
	}))
	defer srv.Close()

	if got, err := newerVersion(context.Background(), "dev", srv.URL); got != "" || err != nil {
		t.Errorf("newerVersion(dev) = %q, %v, want nothing", got, err)
	}
}

func TestUnusableLatestReleaseIsAnError(t *testing.T) {
	for name, srv := range map[string]*httptest.Server{
		"rate limited":     latestReleaseServer(t, http.StatusForbidden, `{"tag_name": "v9.9.9"}`),
		"not JSON":         latestReleaseServer(t, http.StatusOK, "<html>"),
		"no tag":           latestReleaseServer(t, http.StatusOK, `{}`),
		"tag not a semver": latestReleaseServer(t, http.StatusOK, `{"tag_name": "v9.9.9\nhello"}`),
	} {
		if got, err := newerVersion(context.Background(), "0.1.0", srv.URL); got != "" || err == nil {
			t.Errorf("%s: newerVersion = %q, %v, want an error", name, got, err)
		}
	}
}
