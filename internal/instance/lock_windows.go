package instance

import (
	"os"

	"golang.org/x/sys/windows"
)

// Windows locks deny reads through other handles. Lock byte 2^32 so the JSON
// remains readable, including while its owner holds the slot.
const lockOffsetHigh = 1

func lockFile(file *os.File, wait bool) (bool, error) {
	flags := uint32(windows.LOCKFILE_EXCLUSIVE_LOCK)
	if !wait {
		flags |= windows.LOCKFILE_FAIL_IMMEDIATELY
	}
	overlap := windows.Overlapped{OffsetHigh: lockOffsetHigh}
	err := windows.LockFileEx(windows.Handle(file.Fd()), flags, 0, 1, 0, &overlap)
	if err == windows.ERROR_LOCK_VIOLATION {
		return false, nil
	}
	if err != nil {
		return false, &os.PathError{Op: "lock", Path: file.Name(), Err: err}
	}
	return true, nil
}

func unlockFile(file *os.File) error {
	overlap := windows.Overlapped{OffsetHigh: lockOffsetHigh}
	if err := windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, &overlap); err != nil {
		return &os.PathError{Op: "unlock", Path: file.Name(), Err: err}
	}
	return nil
}
