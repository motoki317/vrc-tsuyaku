package asr

import (
	"sync"

	"github.com/ebitengine/purego"
)

var nativeHeapTrim = sync.OnceValue(func() func(uintptr) int32 {
	symbol, err := purego.Dlsym(purego.RTLD_DEFAULT, "malloc_trim")
	if err != nil {
		return nil
	}
	var trim func(uintptr) int32
	purego.RegisterFunc(&trim, symbol)
	return trim
})

func releaseNativeMemory() {
	// glibc can keep freed session arenas resident until an explicit trim.
	// Only free pages are reclaimed, so another flow's live allocations stay valid.
	if trim := nativeHeapTrim(); trim != nil {
		trim(0)
	}
}
