package main

import (
	"net"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestMachineLoadTellsCPUMemoryAndTraffic(t *testing.T) {
	pc := machine{}
	start := time.Now()
	if got := pc.load(start); got != "" {
		t.Errorf("first load = %q, want nothing: there is no earlier reading to compare with", got)
	}

	got := pc.load(start.Add(time.Second))

	shape := regexp.MustCompile(`^cpu=\d+% busiest_core=\d+% ram=\d+% ram_free=\d+MB nic="all" down=\d+\.\dMbps up=\d+\.\dMbps nic_errors=\d+$`)
	if !shape.MatchString(got) {
		t.Errorf("load = %q, want it shaped like %s", got, shape)
	}

	// Compared with a reading taken long ago, the traffic of an hour would
	// count as the traffic of a second.
	if got := pc.load(start.Add(time.Hour)); got != "" {
		t.Errorf("load after an hour without a reading = %q, want nothing", got)
	}
}

func TestTrafficIsCountedInMegabitsPerSecond(t *testing.T) {
	if got := mbps(1_000_000, 2_000_000, 2*time.Second); got != 4 {
		t.Errorf("1 MB in 2 s = %v Mbps, want 4", got)
	}
	if got := mbps(2_000_000, 1_000_000, time.Second); got != 0 {
		t.Errorf("a counter that went back = %v Mbps, want 0", got)
	}
}

func TestInterfaceOfFindsTheInterfaceOfAnAddress(t *testing.T) {
	if got := interfaceOf(net.IPv4(127, 0, 0, 1)); got == "" {
		t.Error("no interface found for 127.0.0.1, want the loopback one")
	}
	if got := interfaceOf(net.IPv4(203, 0, 113, 7)); got != "" {
		t.Errorf("interface of an address this machine does not have = %q, want none", got)
	}
}

// ps5cast and the player are always named, even when five other programs are
// busier.
func TestProcessesWorthNaming(t *testing.T) {
	shares := map[int32]float64{1: 90, 2: 80, 3: 70, 4: 60, 5: 50, 6: 40, 7: 3, 8: 0}

	got := worthNaming(shares, []int32{7, 0})

	if want := []int32{1, 2, 3, 4, 5, 7}; !slices.Equal(got, want) {
		t.Errorf("worthNaming = %v, want %v", got, want)
	}
}

func TestBusiestProcessesAreNamedWithoutTheirCommandLine(t *testing.T) {
	var programs processes
	start := time.Now()
	programs.busiest(start) // some systems only tell what was used between two calls

	got := programs.busiest(start.Add(time.Second), int32(os.Getpid()))

	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	name := self[strings.LastIndexAny(self, `/\\`)+1:]
	// The system cuts long process names: compare their first characters.
	if !strings.Contains(got, `"`+name[:min(len(name), 12)]) {
		t.Errorf("busiest = %q, want it to name this test (%s)", got, name)
	}
	if strings.Contains(got, "-test.") {
		t.Errorf("busiest = %q, want names only, never a command line", got)
	}
}

// A laptop often has two GPUs: adding them up would hide which one is full.
func TestEngineLoadIsToldPerGPUAndKindOfWork(t *testing.T) {
	got := engineLoad(map[string]uint64{
		"pid_4_luid_0x00000000_0x0000C5D1_phys_0_eng_0_engtype_3D":          30,
		"pid_9_luid_0x00000000_0x0000C5D1_phys_0_eng_0_engtype_3D":          12,
		"pid_9_luid_0x00000000_0x0000C5D1_phys_0_eng_5_engtype_3D":          25,
		"pid_9_luid_0x00000000_0x0000C5D1_phys_0_eng_1_engtype_VideoDecode": 8,
		"pid_7_luid_0x00000000_0x0000D2A0_phys_0_eng_3_engtype_VideoEncode": 20,
		"pid_7_luid_0x00000000_0x0000D2A0_phys_0_eng_2_engtype_Copy":        0,
		"not an engine": 99,
	})

	// 3D on the first GPU: its busiest engine, used by two programs.
	if want := "gpu0_3D=42% gpu0_VideoDecode=8% gpu1_VideoEncode=20%"; got != want {
		t.Errorf("engineLoad = %q, want %q", got, want)
	}
	if got := engineLoad(nil); got != "idle" {
		t.Errorf("engineLoad without any load = %q, want idle", got)
	}
}
