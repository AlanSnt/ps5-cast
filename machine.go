package main

import (
	"cmp"
	"fmt"
	"net"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/miekg/dns"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/mem"
	psnet "github.com/shirou/gopsutil/v4/net"
)

// Two readings further apart than maxInterval are not compared: the new one
// only starts the next measure.
const maxInterval = 15 * time.Second

type machine struct {
	nic     string // network interface the PS5 reaches this PC through
	readAt  time.Time
	traffic psnet.IOCountersStat
}

func describeMachine() string {
	system := runtime.GOOS + " " + runtime.GOARCH
	if info, err := host.Info(); err == nil {
		system = fmt.Sprintf("%s %s %s", info.Platform, info.PlatformVersion, info.KernelArch)
	}
	processor := "unknown CPU"
	if models, err := cpu.Info(); err == nil && len(models) > 0 {
		processor = models[0].ModelName
	}
	memory := "unknown RAM"
	if ram, err := mem.VirtualMemory(); err == nil {
		memory = fmt.Sprintf("%d GB of RAM", ram.Total>>30)
	}
	return fmt.Sprintf("%s, %s (%d threads), %s", system, processor, runtime.NumCPU(), memory)
}

func interfaceOf(ip net.IP) string {
	interfaces, _ := net.Interfaces()
	for _, candidate := range interfaces {
		addrs, _ := candidate.Addrs()
		for _, addr := range addrs {
			if network, ok := addr.(*net.IPNet); ok && network.IP.Equal(ip) {
				return candidate.Name
			}
		}
	}
	return ""
}

func (m *machine) load(now time.Time) string {
	elapsed := now.Sub(m.readAt)
	m.readAt = now
	var measures []string
	if cores, err := cpu.Percent(0, true); err == nil && len(cores) > 0 {
		var total float64
		for _, core := range cores {
			total += core
		}
		average := total / float64(len(cores))
		measures = append(measures, fmt.Sprintf("cpu=%.0f%% busiest_core=%.0f%%", average, slices.Max(cores)))
	}
	if ram, err := mem.VirtualMemory(); err == nil {
		measures = append(measures, fmt.Sprintf("ram=%.0f%% ram_free=%dMB", ram.UsedPercent, ram.Available>>20))
	}
	if now, err := m.counters(); err == nil {
		if before := m.traffic; before.Name == now.Name {
			down := mbps(before.BytesRecv, now.BytesRecv, elapsed)
			up := mbps(before.BytesSent, now.BytesSent, elapsed)
			failed := max(failures(now), failures(before)) - failures(before) // 0 when the counters went back
			measures = append(measures, fmt.Sprintf("nic=%q down=%.1fMbps up=%.1fMbps nic_errors=%d", now.Name, down, up, failed))
		}
		m.traffic = now
	}
	if elapsed > maxInterval {
		return ""
	}
	return strings.Join(measures, " ")
}

// counters adds up all the interfaces when the PS5's one is unknown.
func (m *machine) counters() (psnet.IOCountersStat, error) {
	perInterface, err := psnet.IOCounters(true)
	if err != nil {
		return psnet.IOCountersStat{}, err
	}
	all := psnet.IOCountersStat{Name: "all"}
	for _, counters := range perInterface {
		if counters.Name == m.nic {
			return counters, nil
		}
		all.BytesRecv += counters.BytesRecv
		all.BytesSent += counters.BytesSent
		all.Errin += counters.Errin
		all.Errout += counters.Errout
		all.Dropin += counters.Dropin
		all.Dropout += counters.Dropout
	}
	return all, nil
}

func failures(counters psnet.IOCountersStat) uint64 {
	return counters.Errin + counters.Errout + counters.Dropin + counters.Dropout
}

func mbps(before, now uint64, elapsed time.Duration) float64 {
	if now < before || elapsed <= 0 { // the interface restarted
		return 0
	}
	megabits := float64(now-before) * 8 / 1e6
	return megabits / elapsed.Seconds()
}

const busiestCount = 5

// A figure is a share of one core: 250% keeps two cores and a half busy.
// Only names are read: a command line may hold a secret.
func (p *processes) busiest(now time.Time, always ...int32) string {
	shares, nameOf := p.measure(now)
	var named []string
	for _, pid := range worthNaming(shares, always) {
		named = append(named, fmt.Sprintf("%q=%.0f%%", nameOf(pid), shares[pid]))
	}
	return strings.Join(named, " ")
}

func worthNaming(shares map[int32]float64, always []int32) []int32 {
	pids := make([]int32, 0, len(shares))
	for pid := range shares {
		pids = append(pids, pid)
	}
	slices.SortFunc(pids, func(a, b int32) int {
		return cmp.Or(cmp.Compare(shares[b], shares[a]), cmp.Compare(a, b))
	})
	var worth []int32
	for rank, pid := range pids {
		if rank < busiestCount || slices.Contains(always, pid) {
			worth = append(worth, pid)
		}
	}
	return worth
}

// An answer of the public DNS that slows down or does not come tells of a
// saturated Internet link, which is what Discord sends the picture through.
func internetDelay(upstream string) string {
	query := new(dns.Msg).SetQuestion(".", dns.TypeNS)
	client := dns.Client{Timeout: 2 * time.Second}
	_, delay, err := client.Exchange(query, upstream)
	if err != nil {
		return "internet=no_answer"
	}
	return fmt.Sprintf("internet=%dms", delay.Milliseconds())
}
