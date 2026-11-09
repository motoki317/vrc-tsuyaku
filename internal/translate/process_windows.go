package translate

import (
	"fmt"
	"io"
	"os"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

func launch(path string, args []string) (_ *child, err error) {
	app, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	command, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(append([]string{path}, args...)))
	if err != nil {
		return nil, err
	}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			windows.CloseHandle(job)
		}
	}()
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		return nil, err
	}
	attrs, err := windows.NewProcThreadAttributeList(2)
	if err != nil {
		return nil, err
	}
	defer attrs.Delete()
	// Assign at creation: a crash between CreateProcess and AssignProcessToJobObject
	// would otherwise leave an orphan. This attribute is available on Windows 10+.
	// https://learn.microsoft.com/windows/win32/api/processthreadsapi/nf-processthreadsapi-updateprocthreadattribute
	const jobList = 0x0002000d // PROC_THREAD_ATTRIBUTE_JOB_LIST
	if err := attrs.Update(jobList, unsafe.Pointer(&job), unsafe.Sizeof(job)); err != nil {
		return nil, err
	}
	input, err := os.Open(os.DevNull)
	if err != nil {
		return nil, err
	}
	defer input.Close()
	reader, writer, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	defer writer.Close()
	defer func() {
		if err != nil {
			reader.Close()
		}
	}()
	var handles [2]windows.Handle
	for i, file := range []*os.File{input, writer} {
		if err := windows.DuplicateHandle(windows.CurrentProcess(), windows.Handle(file.Fd()), windows.CurrentProcess(), &handles[i], 0, true, windows.DUPLICATE_SAME_ACCESS); err != nil {
			return nil, err
		}
		defer windows.CloseHandle(handles[i])
	}
	// The child inherits only stdio handles. Inheriting the job handle keeps
	// the job open after the app dies.
	if err := attrs.Update(windows.PROC_THREAD_ATTRIBUTE_HANDLE_LIST, unsafe.Pointer(&handles[0]), unsafe.Sizeof(handles)); err != nil {
		return nil, err
	}
	info := windows.StartupInfoEx{ProcThreadAttributeList: attrs.List()}
	info.Cb = uint32(unsafe.Sizeof(info))
	info.Flags = windows.STARTF_USESTDHANDLES
	info.StdInput, info.StdOutput, info.StdErr = handles[0], handles[1], handles[1]
	var pi windows.ProcessInformation
	if err := windows.CreateProcess(app, command, nil, nil, true, windows.EXTENDED_STARTUPINFO_PRESENT|windows.CREATE_NO_WINDOW, nil, nil, &info.StartupInfo, &pi); err != nil {
		return nil, err
	}
	windows.CloseHandle(pi.Thread)
	p := &child{done: make(chan struct{})}
	logged := make(chan struct{})
	go func() { _, _ = io.Copy(&p.log, reader); reader.Close(); close(logged) }()
	var mu sync.Mutex
	finished := false
	p.kill = func() error {
		mu.Lock()
		defer mu.Unlock()
		if finished {
			return nil
		}
		return windows.TerminateJobObject(job, 1)
	}
	go func() {
		_, waitErr := windows.WaitForSingleObject(pi.Process, windows.INFINITE)
		var code uint32
		if waitErr == nil {
			waitErr = windows.GetExitCodeProcess(pi.Process, &code)
		}
		if waitErr == nil && code != 0 {
			waitErr = fmt.Errorf("exit status %d", code)
		}
		mu.Lock()
		windows.CloseHandle(pi.Process)
		windows.CloseHandle(job)
		finished = true
		mu.Unlock()
		<-logged
		p.err = waitErr
		close(p.done)
	}()
	return p, nil
}
