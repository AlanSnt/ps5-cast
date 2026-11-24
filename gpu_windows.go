package main

import "github.com/yusufpapurcu/wmi"

// gpuEngine is one row of the performance counters Task Manager shows as the
// GPU load: one engine of one GPU, used by one process.
type gpuEngine struct {
	Name                  string
	UtilizationPercentage uint64
}

// gpuLoad reads the load of the GPU by kind of work. The counters are asked by
// their class name, which is the same in every language of Windows.
func gpuLoad() string {
	var rows []gpuEngine
	query := "SELECT Name, UtilizationPercentage FROM Win32_PerfFormattedData_GPUPerformanceCounters_GPUEngine"
	if err := wmi.Query(query, &rows); err != nil {
		return "unavailable: " + err.Error()
	}
	engines := make(map[string]uint64, len(rows))
	for _, row := range rows {
		engines[row.Name] += row.UtilizationPercentage
	}
	return engineLoad(engines)
}
