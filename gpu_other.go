//go:build !darwin && !windows

package main

import (
	"os"
	"path/filepath"
	"strings"
)

// gpuLoad reads the load of each GPU whose driver publishes it (AMD). It
// returns "" for the others.
func gpuLoad() string {
	files, _ := filepath.Glob("/sys/class/drm/card*/device/gpu_busy_percent")
	var loads []string
	for _, file := range files {
		if percent, err := os.ReadFile(file); err == nil {
			loads = append(loads, "usage="+strings.TrimSpace(string(percent))+"%")
		}
	}
	return strings.Join(loads, " ")
}
