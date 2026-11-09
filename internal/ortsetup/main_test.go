package main

import (
	"context"
	"io"
	"testing"
)

func TestArguments(t *testing.T) {
	for _, args := range [][]string{nil, {"-data-dir", ""}, {"-data-dir", "directory", "extra"}, {"-unknown"}} {
		if err := run(context.Background(), args, io.Discard, io.Discard); err == nil {
			t.Fatalf("accepted %q", args)
		}
	}
}
