package audio

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"time"
	"unsafe"

	"github.com/go-ole/go-ole"
	"github.com/moutend/go-wca/pkg/wca"
)

// Microphones lists active capture endpoints with their Windows endpoint IDs.
func Microphones(ctx context.Context) ([]Device, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := ole.CoInitializeEx(0, ole.COINIT_APARTMENTTHREADED); err != nil {
		return nil, err
	}
	defer ole.CoUninitialize()
	e, err := enumerator()
	if err != nil {
		return nil, err
	}
	defer e.Release()
	var devices []Device
	err = eachMicrophone(ctx, e, func(device *wca.IMMDevice, id string) error {
		var properties *wca.IPropertyStore
		if err := device.OpenPropertyStore(wca.STGM_READ, &properties); err != nil {
			return err
		}
		defer properties.Release()
		var name wca.PROPVARIANT
		if err := properties.GetValue(&wca.PKEY_Device_FriendlyName, &name); err != nil {
			return err
		}
		// PROPVARIANT.String frees the string allocated by GetValue.
		devices = append(devices, Device{ID: id, Name: name.String()})
		return nil
	})
	return devices, err
}

func enumerator() (*wca.IMMDeviceEnumerator, error) {
	var e *wca.IMMDeviceEnumerator
	err := wca.CoCreateInstance(wca.CLSID_MMDeviceEnumerator, 0, wca.CLSCTX_ALL, wca.IID_IMMDeviceEnumerator, &e)
	return e, err
}

func eachMicrophone(ctx context.Context, e *wca.IMMDeviceEnumerator, visit func(*wca.IMMDevice, string) error) error {
	var collection *wca.IMMDeviceCollection
	if err := e.EnumAudioEndpoints(wca.ECapture, wca.DEVICE_STATE_ACTIVE, &collection); err != nil {
		return err
	}
	defer collection.Release()
	var count uint32
	if err := collection.GetCount(&count); err != nil {
		return err
	}
	for i := uint32(0); i < count; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		var device *wca.IMMDevice
		if err := collection.Item(i, &device); err != nil {
			return err
		}
		var id string
		err := device.GetId(&id)
		if err == nil {
			err = visit(device, id)
		}
		device.Release()
		if err != nil {
			return err
		}
	}
	return nil
}

func (m microphone) Capture(ctx context.Context, consume func([]float32) error) error {
	return captureQueued(ctx, consume, m.captureWASAPI)
}

func captureQueued(ctx context.Context, consume func([]float32) error, produce func(context.Context, func([]float32) error) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	queue := newCaptureQueue()
	done := make(chan struct{})
	go func() {
		defer close(done)
		queue.fail(produce(ctx, queue.push))
	}()
	defer func() { cancel(); <-done }()
	return queue.consume(ctx, consume)
}

// Keep COM and WASAPI on one thread, independent of consumer scheduling.
func (m microphone) captureWASAPI(ctx context.Context, enqueue func([]float32) error) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := ole.CoInitializeEx(0, ole.COINIT_APARTMENTTHREADED); err != nil {
		return err
	}
	defer ole.CoUninitialize()
	e, err := enumerator()
	if err != nil {
		return fmt.Errorf("enumerate microphones: %w", err)
	}
	defer e.Release()
	var device *wca.IMMDevice
	if m.id == "" {
		err = e.GetDefaultAudioEndpoint(wca.ECapture, wca.EConsole, &device)
	} else {
		err = eachMicrophone(ctx, e, func(candidate *wca.IMMDevice, id string) error {
			if id == m.id {
				candidate.AddRef()
				device = candidate
			}
			return nil
		})
	}
	if device != nil {
		defer device.Release()
	}
	if err != nil {
		return fmt.Errorf("find microphone: %w", err)
	}
	if device == nil {
		return fmt.Errorf("microphone %q is unavailable", m.id)
	}
	var client *wca.IAudioClient
	if err := device.Activate(wca.IID_IAudioClient, wca.CLSCTX_ALL, nil, &client); err != nil {
		return fmt.Errorf("activate microphone: %w", err)
	}
	defer client.Release()
	return readWASAPI(ctx, client, 0, nil, enqueue)
}

func readWASAPI(ctx context.Context, client *wca.IAudioClient, extraFlags uint32, check func() error, enqueue func([]float32) error) error {
	const waveFormatIEEEFloat = 3 // WAVE_FORMAT_IEEE_FLOAT
	format := wca.WAVEFORMATEX{
		WFormatTag: waveFormatIEEEFloat, NChannels: 1, NSamplesPerSec: SampleRate,
		NAvgBytesPerSec: SampleRate * 4, NBlockAlign: 4, WBitsPerSample: 32,
	}
	// WASAPI inserts channel and sample-rate conversion for the requested format.
	// https://learn.microsoft.com/en-us/windows/win32/coreaudio/audclnt-streamflags-xxx-constants
	flags := extraFlags | uint32(wca.AUDCLNT_STREAMFLAGS_AUTOCONVERTPCM|wca.AUDCLNT_STREAMFLAGS_SRC_DEFAULT_QUALITY)
	const bufferDuration = 10000000 // One second in WASAPI's 100-ns units.
	if err := client.Initialize(wca.AUDCLNT_SHAREMODE_SHARED, flags, bufferDuration, 0, &format, nil); err != nil {
		return fmt.Errorf("initialize 16 kHz mono float32 capture: %w", err)
	}
	var capture *wca.IAudioCaptureClient
	if err := client.GetService(wca.IID_IAudioCaptureClient, &capture); err != nil {
		return err
	}
	defer capture.Release()
	if err := client.Start(); err != nil {
		return fmt.Errorf("start audio capture: %w", err)
	}
	defer client.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if check != nil {
			if err := check(); err != nil {
				return err
			}
		}
		var available uint32
		if err := capture.GetNextPacketSize(&available); err != nil {
			return fmt.Errorf("read audio capture: %w", err)
		}
		if available == 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-ticker.C:
				continue
			}
		}
		var data *byte
		var frames, flags uint32
		if err := capture.GetBuffer(&data, &frames, &flags, nil, nil); err != nil {
			return fmt.Errorf("get audio buffer: %w", err)
		}
		if frames == 0 {
			_ = capture.ReleaseBuffer(0)
			continue
		}
		// Discontinuity flags can report device timing glitches.
		// Pass their audio through. The app's queue detects a slow consumer.
		// https://learn.microsoft.com/en-us/windows/win32/api/audioclient/ne-audioclient-_audclnt_bufferflags
		samples := make([]float32, frames)
		if flags&wca.AUDCLNT_BUFFERFLAGS_SILENT == 0 {
			if data == nil && frames != 0 {
				_ = capture.ReleaseBuffer(frames)
				return errors.New("WASAPI returned a nil audio buffer")
			}
			copy(samples, unsafe.Slice((*float32)(unsafe.Pointer(data)), int(frames)))
		}
		if err := capture.ReleaseBuffer(frames); err != nil {
			return fmt.Errorf("release audio buffer: %w", err)
		}
		if err := enqueue(samples); err != nil {
			return err
		}
	}
}
