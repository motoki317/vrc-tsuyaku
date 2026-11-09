// Command ortsetup downloads the CPU runtimes used by gated tests. Run it through just ort.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"

	"github.com/motoki317/vrc-tsuyaku/internal/catalog"
	"github.com/motoki317/vrc-tsuyaku/internal/download"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, out, errout io.Writer) error {
	flags := flag.NewFlagSet("ortsetup", flag.ContinueOnError)
	flags.SetOutput(errout)
	dir := flags.String("data-dir", "", "directory for the Linux and Windows test runtimes")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *dir == "" || flags.NArg() != 0 {
		return errors.New("usage: ortsetup -data-dir DIRECTORY")
	}
	for _, goos := range []string{"linux", "windows"} {
		m, err := download.New(filepath.Join(*dir, goos))
		if err != nil {
			return err
		}
		ok, err := m.CheckInstalled(ctx, catalog.ONNXRuntime, goos)
		if err != nil {
			return err
		}
		if !ok {
			if err = m.Download(ctx, catalog.ONNXRuntime, goos, nil); err != nil {
				return err
			}
		}
		installed, err := m.Directory(catalog.ONNXRuntime, goos)
		if err != nil {
			return err
		}
		a, err := catalog.Lookup(catalog.ONNXRuntime, goos)
		if err != nil {
			return err
		}
		library, err := a.Path(installed, "runtime")
		if err != nil {
			return err
		}
		if _, err = fmt.Fprintf(out, "%s: %s\n", goos, library); err != nil {
			return err
		}
	}
	return nil
}
