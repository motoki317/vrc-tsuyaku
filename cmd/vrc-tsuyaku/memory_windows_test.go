package main

import (
	"fmt"
	"os"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

func peakMemory() (uint64, uint64, error) {
	// PeakWorkingSetSize is the peak resident working set in bytes.
	// https://learn.microsoft.com/windows/win32/api/psapi/ns-psapi-process_memory_counters
	memory := windows.NewLazySystemDLL("psapi.dll").NewProc("GetProcessMemoryInfo")
	read := func(pid uint32) (uint64, error) {
		process, err := windows.OpenProcess(windows.PROCESS_QUERY_INFORMATION|windows.PROCESS_VM_READ, false, pid)
		if err != nil {
			return 0, err
		}
		defer windows.CloseHandle(process)
		var counters struct {
			Size, PageFaults                                                                                             uint32
			PeakWorkingSet, WorkingSet, PeakPagedPool, PagedPool, PeakNonpagedPool, NonpagedPool, Pagefile, PeakPagefile uintptr
		}
		counters.Size = uint32(unsafe.Sizeof(counters))
		ok, _, err := memory.Call(uintptr(process), uintptr(unsafe.Pointer(&counters)), uintptr(counters.Size))
		if ok == 0 {
			return 0, err
		}
		return uint64(counters.PeakWorkingSet), nil
	}
	own, err := read(uint32(os.Getpid()))
	if err != nil {
		return 0, 0, err
	}
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return 0, 0, err
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	for err = windows.Process32First(snapshot, &entry); err == nil; err = windows.Process32Next(snapshot, &entry) {
		if entry.ParentProcessID == uint32(os.Getpid()) && strings.EqualFold(windows.UTF16ToString(entry.ExeFile[:]), "llama-server.exe") {
			peak, err := read(entry.ProcessID)
			return own, peak, err
		}
	}
	return 0, 0, fmt.Errorf("llama-server child not found: %w", err)
}
