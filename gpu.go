package main

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// An engine of a GPU as Windows names it, with the process that uses it:
// "pid_4_luid_0x00000000_0x0000C5D1_phys_0_eng_3_engtype_3D".
var engineName = regexp.MustCompile(`luid_(0x[0-9A-Fa-f]+_0x[0-9A-Fa-f]+)_phys_\d+_eng_(\d+)_engtype_(.+)$`)

// engineLoad turns the load of the GPU engines of Windows, given per process,
// into one figure per GPU and kind of work: 3D for drawing the windows,
// VideoDecode for the player, VideoEncode for what Discord sends. As in the
// Task Manager, a kind of work is as loaded as its busiest engine. GPUs are
// numbered in the order of their identifiers; kinds without load are left out.
func engineLoad(engines map[string]uint64) string {
	type engine struct{ gpu, index, kind string }
	perEngine := map[engine]uint64{}
	for name, load := range engines {
		if parts := engineName.FindStringSubmatch(name); parts != nil {
			perEngine[engine{gpu: parts[1], index: parts[2], kind: parts[3]}] += load
		}
	}

	type work struct{ gpu, kind string }
	busiest := map[work]uint64{}
	var gpus []string
	for engine, load := range perEngine {
		kind := work{engine.gpu, engine.kind}
		busiest[kind] = max(busiest[kind], load)
		if !slices.Contains(gpus, engine.gpu) {
			gpus = append(gpus, engine.gpu)
		}
	}
	slices.Sort(gpus)

	var measures []string
	for kind, load := range busiest {
		if load > 0 {
			measures = append(measures, fmt.Sprintf("gpu%d_%s=%d%%", slices.Index(gpus, kind.gpu), kind.kind, load))
		}
	}
	slices.Sort(measures)
	if len(measures) == 0 {
		return "idle"
	}
	return strings.Join(measures, " ")
}
