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

// maxInterval is how far apart two readings may be taken and still be
// compared. Past it (no stream for a while), the new reading only serves as
// the start of the next measure.
const maxInterval = 15 * time.Second

// machine measures the load of this PC between two calls.
type machine struct {
	nic     string               // network interface the PS5 reaches this PC through, "" when unknown
	at      time.Time            // when the previous call was made
	traffic psnet.IOCountersStat // the counters of the interface at that time
}

// describeMachine tells the debug log, once, what ps5cast runs on.
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

// interfaceOf returns the name of the network interface that holds ip, such as
// "Wi-Fi", "Ethernet" or "en0", and "" when none does.
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

// load describes the CPU, the memory and the network traffic of the PC since
// the previous call, and nothing when that call is too old. A measure the
// system refuses to give is left out.
func (m *machine) load(now time.Time) string {
	elapsed := now.Sub(m.at)
	m.at = now
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

// counters returns the traffic counters of the PS5's interface, or of all the
// interfaces together when it is unknown.
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

// failures counts the packets an interface lost or received damaged.
func failures(counters psnet.IOCountersStat) uint64 {
	return counters.Errin + counters.Errout + counters.Dropin + counters.Dropout
}

// mbps converts a byte counter read twice into megabits per second. A counter
// that went back (interface restarted) gives 0.
func mbps(before, now uint64, elapsed time.Duration) float64 {
	if now < before || elapsed <= 0 {
		return 0
	}
	megabits := float64(now-before) * 8 / 1e6
	return megabits / elapsed.Seconds()
}

// busiestCount is how many programs the debug log names every time.
const busiestCount = 5

// busiest names the programs that used the most CPU since the previous call,
// and always the ones whose process ID is given (ps5cast, the player). A
// figure is a share of one core: 250% keeps two cores and a half busy.
// Only names are read: a command line may hold a secret.
func (p *processes) busiest(now time.Time, always ...int32) string {
	shares, nameOf := p.measure(now)
	var named []string
	for _, pid := range worthNaming(shares, always) {
		named = append(named, fmt.Sprintf("%q=%.0f%%", nameOf(pid), shares[pid]))
	}
	return strings.Join(named, " ")
}

// worthNaming returns the processes to name, busiest first: the busiestCount
// ones that used the most CPU, and the ones always wanted.
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

// internetDelay measures how long the public DNS takes to answer. An answer
// that slows down or does not come tells of a saturated or failing Internet
// link, which is what Discord sends the picture through.
func internetDelay(upstream string) string {
	query := new(dns.Msg).SetQuestion(".", dns.TypeNS)
	client := dns.Client{Timeout: 2 * time.Second}
	_, delay, err := client.Exchange(query, upstream)
	if err != nil {
		return "internet=no_answer"
	}
	return fmt.Sprintf("internet=%dms", delay.Milliseconds())
}
