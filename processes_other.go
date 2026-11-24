//go:build !darwin

package main

import (
	"fmt"
	"time"

	"github.com/shirou/gopsutil/v4/process"
)

type processes struct {
	spent  map[int32]float64 // CPU seconds per process at the previous reading
	readAt time.Time
}

func (p *processes) measure(now time.Time) (shares map[int32]float64, nameOf func(pid int32) string) {
	running, _ := process.Processes()
	elapsed := now.Sub(p.readAt)
	shares = map[int32]float64{}
	byPID := make(map[int32]*process.Process, len(running))
	spent := make(map[int32]float64, len(running))
	for _, proc := range running {
		times, err := proc.Times()
		if err != nil {
			continue // a system process that cannot be read
		}
		byPID[proc.Pid] = proc
		spent[proc.Pid] = times.User + times.System
		before, known := p.spent[proc.Pid]
		// A process that used less than before is another one that took its ID.
		if known && spent[proc.Pid] >= before && elapsed <= maxInterval {
			shares[proc.Pid] = 100 * (spent[proc.Pid] - before) / elapsed.Seconds()
		}
	}
	p.spent, p.readAt = spent, now

	return shares, func(pid int32) string {
		name, err := byPID[pid].Name()
		if err != nil {
			return fmt.Sprintf("pid %d", pid)
		}
		return name
	}
}
