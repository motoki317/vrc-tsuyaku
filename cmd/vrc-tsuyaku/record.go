package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/motoki317/vrc-tsuyaku/internal/audio"
)

func record(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("record", flag.ContinueOnError)
	flags.SetOutput(stderr)
	seconds := flags.Float64("seconds", 3, "recording length in seconds")
	microphone := flags.String("microphone", "", "microphone ID (empty selects the default)")
	application := flags.String("app", "", "capture the named application's playback instead of a microphone")
	list := flags.Bool("list", false, "list microphone IDs and names without recording")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *application != "" && (*microphone != "" || *list) {
		return errors.New("-app cannot be combined with -microphone or -list")
	}
	if *list {
		if flags.NArg() != 0 {
			return errors.New("usage: record -list")
		}
		devices, err := audio.Microphones(ctx)
		if err != nil {
			return err
		}
		for _, device := range devices {
			if _, err := fmt.Fprintf(stdout, "%s\t%s\n", device.ID, device.Name); err != nil {
				return err
			}
		}
		return nil
	}
	if flags.NArg() != 1 {
		return errors.New("usage: record -list | record [-seconds N] [-microphone ID | -app NAME] out.wav")
	}
	// ParseDuration rejects NaN, infinity, and overflow before conversion to
	// time.Duration; a direct float-to-integer conversion would not reject them.
	duration, err := time.ParseDuration(strconv.FormatFloat(*seconds, 'f', -1, 64) + "s")
	if err != nil {
		return fmt.Errorf("invalid -seconds: %w", err)
	}
	source := audio.Microphone(*microphone)
	if *application != "" {
		source = audio.Application(*application)
	}
	return recordFile(ctx, source, duration, flags.Arg(0))
}

func recordFile(ctx context.Context, source audio.Source, duration time.Duration, path string) (err error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create recording: %w", err)
	}
	defer func() {
		err = errors.Join(err, f.Close())
		if err != nil {
			err = errors.Join(err, os.Remove(path))
		}
	}()
	return audio.RecordWAV(ctx, source, duration, f)
}
