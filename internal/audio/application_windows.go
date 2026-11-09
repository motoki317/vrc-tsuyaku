// Adapts process-loopback activation code from whispering-ui (MIT):
// https://github.com/Sharrnah/whispering-ui/blob/00b7533b148d0feec959e8ddf8c7b72b6ca9c329/Utilities/ProcessCapture_windows.go
// Copyright (c) 2022 Sharrnah. See ../../THIRD_PARTY_NOTICES.md for the MIT license.

package audio

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"github.com/go-ole/go-ole"
	"github.com/moutend/go-wca/pkg/wca"
	"golang.org/x/sys/windows"
)

const (
	ePointer                      = 0x80004003
	eNoInterface                  = 0x80004002
	getActivateResultSlot         = 3 // IActivateAudioInterfaceAsyncOperation, after IUnknown.
	audioClientActivationLoopback = 1
	processLoopbackIncludeTree    = 0
	vtBlob                        = 65
)

func (a application) Capture(ctx context.Context, consume func([]float32) error) error {
	return captureQueued(ctx, consume, a.captureProcess)
}

func (a application) captureProcess(ctx context.Context, enqueue func([]float32) error) error {
	// OBS enables process filtering from Windows 10 2004 despite Microsoft's 20348 minimum.
	// https://github.com/obsproject/obs-studio/blob/7a25c3fd8b5eb87c2d2e0606cae36e32014bc6fa/plugins/win-wasapi/plugin-main.cpp
	if windows.RtlGetVersion().BuildNumber < 19041 {
		return errors.New("application audio capture requires Windows build 19041 or newer")
	}
	pid, err := applicationPID(a.executable)
	if err != nil {
		return err
	}
	process, err := windows.OpenProcess(windows.SYNCHRONIZE, false, pid)
	if err != nil {
		return fmt.Errorf("open %s: %w", a.executable, err)
	}
	defer windows.CloseHandle(process)
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	// Activation completes on an MTA worker. Keep its owner and this capture
	// thread in the same MTA to pass the audio client without COM marshaling.
	// https://learn.microsoft.com/en-us/windows/win32/api/mmdeviceapi/nf-mmdeviceapi-activateaudiointerfaceasync
	if err := ole.CoInitializeEx(0, ole.COINIT_MULTITHREADED); err != nil {
		return err
	}
	defer ole.CoUninitialize()
	client, err := activateLoopback(ctx, pid)
	if err != nil {
		return err
	}
	defer client.Release()
	return readWASAPI(ctx, client, wca.AUDCLNT_STREAMFLAGS_LOOPBACK, func() error {
		status, err := windows.WaitForSingleObject(process, 0)
		if err != nil {
			return fmt.Errorf("check %s: %w", a.executable, err)
		}
		if status == windows.WAIT_OBJECT_0 {
			return fmt.Errorf("%s exited", a.executable)
		}
		return nil
	}, enqueue)
}

func applicationPID(name string) (uint32, error) {
	if strings.TrimSpace(name) == "" {
		return 0, errors.New("application executable name is empty")
	}
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return 0, err
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	var pid uint32
	for err = windows.Process32First(snapshot, &entry); err == nil; err = windows.Process32Next(snapshot, &entry) {
		if strings.EqualFold(windows.UTF16ToString(entry.ExeFile[:]), name) {
			if pid != 0 {
				return 0, fmt.Errorf("multiple processes named %s are running", name)
			}
			pid = entry.ProcessID
		}
	}
	if !errors.Is(err, windows.ERROR_NO_MORE_FILES) {
		return 0, err
	}
	if pid == 0 {
		return 0, fmt.Errorf("%s is not running", name)
	}
	return pid, nil
}

type activationResult struct {
	client *wca.IAudioClient
	err    error
}
type loopbackCompletion struct {
	table *[4]uintptr
	refs  atomic.Int32
	done  chan activationResult
}

var (
	activateAudio = windows.NewLazySystemDLL("mmdevapi.dll").NewProc("ActivateAudioInterfaceAsync")
	completionIID = ole.NewGUID("{41D949AB-9862-444A-80F6-C261334DA5EB}")
	agileIID      = ole.NewGUID("{94EA2B94-E9CC-49E0-C0FF-EE64CA8F5B90}")
	// COM owns these objects beyond a cancelled caller's lifetime.
	completions sync.Map
	// NewCallback has a process-wide allocation limit. All activations share this table.
	completionTable = [4]uintptr{
		windows.NewCallback(func(c *loopbackCompletion, iid *ole.GUID, out **loopbackCompletion) uintptr {
			if out == nil {
				return ePointer
			}
			*out = nil
			if iid == nil || (*iid != *ole.IID_IUnknown && *iid != *completionIID && *iid != *agileIID) {
				return eNoInterface
			}
			c.refs.Add(1)
			*out = c
			return 0
		}),
		windows.NewCallback(func(c *loopbackCompletion) uintptr { return uintptr(c.refs.Add(1)) }),
		windows.NewCallback(func(c *loopbackCompletion) uintptr { return c.release() }),
		windows.NewCallback(func(c *loopbackCompletion, operation *ole.IUnknown) uintptr {
			table := (*[4]uintptr)(unsafe.Pointer(operation.RawVTable))
			var status uint32
			var client *wca.IAudioClient
			hr, _, _ := syscall.SyscallN(table[getActivateResultSlot], uintptr(unsafe.Pointer(operation)), uintptr(unsafe.Pointer(&status)), uintptr(unsafe.Pointer(&client)))
			err := hresult("get application audio activation", hr)
			if err == nil {
				err = hresult("activate application audio", uintptr(status))
			}
			c.done <- activationResult{client, err}
			return 0
		}),
	}
)

func (c *loopbackCompletion) release() uintptr {
	n := c.refs.Add(-1)
	if n == 0 {
		completions.Delete(c)
	}
	return uintptr(n)
}

func hresult(action string, value uintptr) error {
	if int32(value) < 0 {
		return fmt.Errorf("%s: HRESULT 0x%08x", action, uint32(value))
	}
	return nil
}

func activateLoopback(ctx context.Context, pid uint32) (*wca.IAudioClient, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	result := make(chan activationResult)
	// Activation cannot be cancelled. Its owner keeps COM initialized through
	// completion, even when the capture caller stops waiting.
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		err := ole.CoInitializeEx(0, ole.COINIT_MULTITHREADED)
		var client *wca.IAudioClient
		if err == nil {
			defer ole.CoUninitialize()
			client, err = activateLoopbackClient(pid)
		}
		select {
		case result <- activationResult{client, err}:
		case <-ctx.Done():
			if client != nil {
				client.Release()
			}
		}
	}()
	select {
	case completed := <-result:
		return completed.client, completed.err
	case <-ctx.Done():
		return nil, fmt.Errorf("activate application audio: %w", ctx.Err())
	}
}

func activateLoopbackClient(pid uint32) (*wca.IAudioClient, error) {
	if err := activateAudio.Find(); err != nil {
		return nil, err
	}
	// AUDIOCLIENT_ACTIVATION_PARAMS and VT_BLOB select only the target process tree.
	params := struct{ kind, pid, mode uint32 }{audioClientActivationLoopback, pid, processLoopbackIncludeTree}
	variant := struct {
		kind     uint16
		reserved [3]uint16
		size     uint32
		data     *struct{ kind, pid, mode uint32 }
	}{kind: vtBlob, size: uint32(unsafe.Sizeof(params)), data: &params}
	c := &loopbackCompletion{table: &completionTable, done: make(chan activationResult, 1)}
	c.refs.Store(1)
	completions.Store(c, true)
	defer c.release()
	name, _ := windows.UTF16PtrFromString(`VAD\Process_Loopback`)
	var operation *ole.IUnknown
	hr, _, _ := activateAudio.Call(uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(wca.IID_IAudioClient)), uintptr(unsafe.Pointer(&variant)), uintptr(unsafe.Pointer(c)), uintptr(unsafe.Pointer(&operation)))
	if err := hresult("activate application audio", hr); err != nil {
		if operation != nil {
			operation.Release()
		}
		return nil, err
	}
	defer operation.Release()
	result := <-c.done
	runtime.KeepAlive(&params)
	runtime.KeepAlive(&variant)
	if result.err != nil {
		if result.client != nil {
			result.client.Release()
		}
		return nil, result.err
	}
	if result.client == nil {
		return nil, errors.New("application audio activation returned no client")
	}
	return result.client, nil
}
