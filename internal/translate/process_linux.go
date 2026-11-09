package translate

import (
	"os/exec"
	"runtime"
	"syscall"
)

func launch(path string, args []string) (*child, error) {
	p := &child{done: make(chan struct{})}
	cmd := exec.Command(path, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	cmd.Stdout, cmd.Stderr = &p.log, &p.log
	started := make(chan error, 1)
	go func() {
		// Pdeathsig follows the creating thread, not the whole Go process.
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		err := cmd.Start()
		started <- err
		if err != nil {
			return
		}
		p.err = cmd.Wait()
		close(p.done)
	}()
	if err := <-started; err != nil {
		return nil, err
	}
	p.kill = cmd.Process.Kill
	return p, nil
}
