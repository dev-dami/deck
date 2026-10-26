package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
)

// startProfiles starts a CPU profile in dir when it is set (DECK_PPROF) and
// returns the function that stops it and writes a heap profile next to it.
// The files are named after the process id, so a TUI and a server running
// at once do not overwrite each other's:
//
//	DECK_PPROF=/tmp/deckprof deck
//	go tool pprof /tmp/deckprof/deck-cpu-1234.pprof
func startProfiles(dir string) (stop func()) {
	if dir == "" {
		return func() {}
	}
	fail := func(err error) func() {
		fmt.Fprintln(os.Stderr, "deck: DECK_PPROF:", err)
		return func() {}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fail(err)
	}
	pid := os.Getpid()
	cpu, err := os.Create(filepath.Join(dir, fmt.Sprintf("deck-cpu-%d.pprof", pid)))
	if err != nil {
		return fail(err)
	}
	if err := pprof.StartCPUProfile(cpu); err != nil {
		cpu.Close()
		return fail(err)
	}
	return func() {
		pprof.StopCPUProfile()
		if err := cpu.Close(); err != nil {
			fmt.Fprintln(os.Stderr, "deck: DECK_PPROF:", err)
		}
		heap, err := os.Create(filepath.Join(dir, fmt.Sprintf("deck-heap-%d.pprof", pid)))
		if err != nil {
			fmt.Fprintln(os.Stderr, "deck: DECK_PPROF:", err)
			return
		}
		defer heap.Close()
		runtime.GC() // up-to-date in-use figures
		if err := pprof.WriteHeapProfile(heap); err != nil {
			fmt.Fprintln(os.Stderr, "deck: DECK_PPROF:", err)
		}
	}
}
