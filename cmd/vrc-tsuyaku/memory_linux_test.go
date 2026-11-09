package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func peakMemory() (uint64, uint64, error) {
	read := func(pid string) (uint64, error) {
		b, err := os.ReadFile(filepath.Join("/proc", pid, "status"))
		if err != nil {
			return 0, err
		}
		for line := range strings.SplitSeq(string(b), "\n") {
			if strings.HasPrefix(line, "VmHWM:") {
				fields := strings.Fields(line)
				n, err := strconv.ParseUint(fields[1], 10, 64)
				return n * 1024, err
			}
		}
		return 0, fmt.Errorf("no VmHWM for %s", pid)
	}
	own, err := read("self")
	if err != nil {
		return 0, 0, err
	}
	tasks, err := os.ReadDir("/proc/self/task")
	if err != nil {
		return 0, 0, err
	}
	for _, task := range tasks {
		b, err := os.ReadFile(filepath.Join("/proc/self/task", task.Name(), "children"))
		if err != nil {
			continue
		}
		for _, pid := range strings.Fields(string(b)) {
			name, _ := os.ReadFile(filepath.Join("/proc", pid, "comm"))
			if strings.TrimSpace(string(name)) == "llama-server" {
				peak, err := read(pid)
				return own, peak, err
			}
		}
	}
	return 0, 0, fmt.Errorf("llama-server child not found")
}
