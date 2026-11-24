package main

import (
	"os/exec"
	"regexp"
	"strings"
)

var gpuUtilization = regexp.MustCompile(`"Device Utilization %"=(\d+)`)

func gpuLoad() string {
	registry, err := exec.Command("ioreg", "-r", "-d", "1", "-w", "0", "-c", "IOAccelerator").Output()
	if err != nil {
		return ""
	}
	var loads []string
	for _, match := range gpuUtilization.FindAllSubmatch(registry, -1) {
		loads = append(loads, "usage="+string(match[1])+"%")
	}
	return strings.Join(loads, " ")
}
