package translate

import (
	"errors"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func watchProcess(t *testing.T, pid int) func() {
	t.Helper()
	fd, err := unix.PidfdOpen(pid, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = unix.PidfdSendSignal(fd, unix.SIGKILL, nil, 0); _ = unix.Close(fd) })
	return func() {
		t.Helper()
		// A pidfd stays tied to this child even if its PID is later reused.
		fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		deadline := time.Now().Add(10 * time.Second)
		for {
			remaining := time.Until(deadline)
			if remaining <= 0 {
				t.Fatal("child survived app exit: deadline exceeded")
			}
			n, err := unix.Poll(fds, int((remaining+time.Millisecond-1)/time.Millisecond))
			if errors.Is(err, unix.EINTR) {
				continue
			}
			if err != nil || n != 1 || fds[0].Revents&unix.POLLIN == 0 {
				t.Fatalf("child survived app exit: poll=%d, error=%v", n, err)
			}
			return
		}
	}
}
