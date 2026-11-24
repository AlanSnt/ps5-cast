package main

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type processes struct{}

// measure asks ps: the system calls open to a program only read the processes
// of its own user, which leaves out WindowServer, the one that draws the windows.
func (*processes) measure(time.Time) (shares map[int32]float64, nameOf func(pid int32) string) {
	ps := exec.Command("ps", "-Aco", "pid=,pcpu=,comm=")
	ps.Env = append(os.Environ(), "LC_ALL=C") // a dot in the figures, whatever the language of the system
	listing, _ := ps.Output()

	shares = map[int32]float64{}
	names := map[int32]string{}
	for _, line := range strings.Split(string(listing), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		pid, pidErr := strconv.Atoi(fields[0])
		share, shareErr := strconv.ParseFloat(fields[1], 64)
		if pidErr != nil || shareErr != nil {
			continue
		}
		shares[int32(pid)] = share
		names[int32(pid)] = strings.Join(fields[2:], " ")
	}
	return shares, func(pid int32) string { return names[pid] }
}
