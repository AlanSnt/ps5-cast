package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// getJSON fetches the JSON document at url and decodes it as a T. It gives up
// after 3 s, the longest startup may wait for the ingest list, and reads at
// most 1 MB.
func getJSON[T any](ctx context.Context, url string) (T, error) {
	var doc T
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return doc, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return doc, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return doc, fmt.Errorf("HTTP %s", resp.Status)
	}
	err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&doc)
	return doc, err
}
