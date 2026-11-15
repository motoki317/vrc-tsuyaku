package instance

import (
	"os"
	"syscall"
)

func lockFile(file *os.File, wait bool) (bool, error) {
	flags := syscall.LOCK_EX
	if !wait {
		flags |= syscall.LOCK_NB
	}
	for {
		err := syscall.Flock(int(file.Fd()), flags)
		if err == syscall.EINTR {
			continue
		}
		if err == syscall.EWOULDBLOCK {
			return false, nil
		}
		if err != nil {
			return false, &os.PathError{Op: "lock", Path: file.Name(), Err: err}
		}
		return true, nil
	}
}

func unlockFile(file *os.File) error {
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_UN); err != nil {
		return &os.PathError{Op: "unlock", Path: file.Name(), Err: err}
	}
	return nil
}
