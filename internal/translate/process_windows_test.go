package translate

import (
	"testing"

	"golang.org/x/sys/windows"
)

func watchProcess(t *testing.T, pid int) func() {
	t.Helper()
	h, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = windows.TerminateProcess(h, 1); windows.CloseHandle(h) })
	return func() {
		t.Helper()
		status, err := windows.WaitForSingleObject(h, 10000)
		if err != nil || status != windows.WAIT_OBJECT_0 {
			t.Fatalf("child survived app exit: wait=%d, error=%v", status, err)
		}
	}
}
