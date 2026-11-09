// Package audio captures microphones and application audio in 16 kHz mono float32 format.
package audio

import "context"

const SampleRate = 16000

// Source captures until ctx ends, the consumer returns an error, or capture fails.
// Capture calls the consumer serially with chunks that the consumer can retain,
// and returns the consumer's error unwrapped.
// Cancellation interrupts capture waits, but cannot interrupt a blocked consumer.
// Device loss returns an error. A source never reconnects or switches devices.
type Source interface {
	Capture(ctx context.Context, consume func([]float32) error) error
}

// Device identifies a microphone. ID is opaque and Name is for display.
type Device struct {
	ID   string
	Name string
}

// Microphone returns a source whose Capture resolves id from Microphones.
// An empty ID selects the default microphone when Capture starts.
// Each Capture call opens and closes its own stream.
func Microphone(id string) Source { return microphone{id: id} }

type microphone struct{ id string }

// Application captures an executable's playback. Capture resolves the name at
// start and fails if the process or its playback stream is unavailable or ends.
// Windows includes the process's children. Linux matches PulseAudio stream properties.
// applicationNames supplies additional Linux application.name matches.
// The source never waits for an application to start.
func Application(executable string, applicationNames ...string) Source {
	return application{executable: executable, names: append([]string(nil), applicationNames...)}
}

type application struct {
	executable string
	names      []string
}
