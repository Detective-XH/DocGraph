package main

import (
	"fmt"
	"os"
	"runtime"
	"runtime/pprof"
)

// startProfiling wires optional pprof output, gated entirely by env vars so it
// is a zero-cost no-op in normal runs:
//
//	DOCGRAPH_CPUPROFILE=<path>  write a CPU profile covering the whole run
//	DOCGRAPH_MEMPROFILE=<path>  write a heap profile at exit
//
// It returns a stop func meant to be deferred. Profiles are flushed by that
// stop func, so it only captures commands that return normally (e.g. index);
// paths that call os.Exit skip the deferred flush by design.
func startProfiling() func() {
	var stops []func()

	if path := os.Getenv("DOCGRAPH_CPUPROFILE"); path != "" {
		f, err := os.Create(path) // #nosec G703 -- path from operator-only DOCGRAPH_CPUPROFILE env var
		if err != nil {
			fmt.Fprintf(os.Stderr, "cpuprofile: %v\n", err)
		} else if err := pprof.StartCPUProfile(f); err != nil {
			fmt.Fprintf(os.Stderr, "cpuprofile: %v\n", err)
			f.Close()
		} else {
			stops = append(stops, func() {
				pprof.StopCPUProfile()
				f.Close()
			})
		}
	}

	if path := os.Getenv("DOCGRAPH_MEMPROFILE"); path != "" {
		stops = append(stops, func() {
			f, err := os.Create(path) // #nosec G703 -- path from operator-only DOCGRAPH_MEMPROFILE env var
			if err != nil {
				fmt.Fprintf(os.Stderr, "memprofile: %v\n", err)
				return
			}
			defer f.Close()
			runtime.GC() // materialize up-to-date heap stats
			if err := pprof.WriteHeapProfile(f); err != nil {
				fmt.Fprintf(os.Stderr, "memprofile: %v\n", err)
			}
		})
	}

	return func() {
		for _, stop := range stops {
			stop()
		}
	}
}
