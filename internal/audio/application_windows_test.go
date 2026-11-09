package audio

import (
	"syscall"
	"testing"
	"unsafe"

	"github.com/go-ole/go-ole"
)

func TestCompletionOwnership(t *testing.T) {
	c := &loopbackCompletion{table: &completionTable, done: make(chan activationResult, 1)}
	c.refs.Store(1)
	completions.Store(c, true)
	var out *loopbackCompletion
	query := func(iid *ole.GUID) uintptr {
		hr, _, _ := syscall.SyscallN(completionTable[0], uintptr(unsafe.Pointer(c)), uintptr(unsafe.Pointer(iid)), uintptr(unsafe.Pointer(&out)))
		return hr
	}
	for _, iid := range []*ole.GUID{ole.IID_IUnknown, completionIID, agileIID} {
		if hr := query(iid); hr != 0 || out != c {
			t.Fatalf("query: 0x%x, %p", hr, out)
		}
	}
	if hr := query(ole.NewGUID("{00000001-0000-0000-0000-000000000000}")); hr != 0x80004002 || out != nil {
		t.Fatalf("unsupported query: 0x%x, %p", hr, out)
	}
	for i := 3; i >= 0; i-- {
		count, _, _ := syscall.SyscallN(completionTable[2], uintptr(unsafe.Pointer(c)))
		_, held := completions.Load(c)
		if count != uintptr(i) || held != (i != 0) {
			t.Fatalf("release: count=%d, retained=%v", count, held)
		}
	}
}
