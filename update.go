package main

import (
	"context"
	"fmt"
	"log"

	"golang.org/x/mod/semver"
)

const (
	// GitHub's "latest" release is the most recent one that is neither a
	// pre-release nor a draft: the latest stable version.
	latestReleaseURL = "https://api.github.com/repos/AlanSnt/ps5-cast/releases/latest"
	downloadPage     = "https://github.com/AlanSnt/ps5-cast/releases/latest"
)

type latestRelease struct {
	TagName string `json:"tag_name"`
}

// newerVersion returns the latest stable version when it is newer than the
// running one, "" otherwise. A build without a version (a local "dev" build)
// is not compared, and GitHub is not asked.
func newerVersion(ctx context.Context, running, releaseURL string) (string, error) {
	runningTag := "v" + running
	if !semver.IsValid(runningTag) {
		return "", nil
	}
	latest, err := getJSON[latestRelease](ctx, releaseURL)
	if err != nil {
		return "", err
	}
	if !semver.IsValid(latest.TagName) {
		return "", fmt.Errorf("latest release has an unexpected tag %q", latest.TagName)
	}
	if semver.Compare(latest.TagName, runningTag) <= 0 {
		return "", nil
	}
	return latest.TagName[1:], nil
}

// announceNewerVersion tells in the console when a newer stable version can be
// downloaded. ps5cast works the same when the check fails, so the failure is
// only reported in verbose mode.
func announceNewerVersion(ctx context.Context, running string, verbose bool) {
	latest, err := newerVersion(ctx, running, latestReleaseURL)
	switch {
	case err != nil && verbose:
		log.Printf("cannot check for a newer version: %v", err)
	case latest != "":
		log.Printf("⬆ ps5cast %s is available (this is %s): %s", latest, running, downloadPage)
	}
}
