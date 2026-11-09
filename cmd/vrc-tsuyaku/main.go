// Command vrc-tsuyaku runs both translation flows and a local web UI.
// The record subcommand captures microphone or application audio for manual checks.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/motoki317/vrc-tsuyaku/internal/download"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, out, errout io.Writer) (err error) {
	if len(args) > 0 && args[0] == "record" {
		return record(ctx, args[1:], out, errout)
	}
	defer func() {
		err = withDataDirHint(err)
	}()
	flags := flag.NewFlagSet("vrc-tsuyaku", flag.ContinueOnError)
	flags.SetOutput(errout)
	override := flags.String("data-dir", "", "directory for settings and downloads (default: beside the executable)")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("usage: vrc-tsuyaku [-data-dir DIRECTORY]")
	}
	dir, err := dataDirectory(*override)
	if err != nil {
		return err
	}
	a, err := newApp(ctx, dir)
	if err != nil {
		return err
	}
	defer a.close()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return err
	}
	server := &http.Server{Handler: a.handler(), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	url := "http://" + listener.Addr().String() + "/#" + a.token
	fmt.Fprintln(out, url)
	if err := openBrowser(url); err != nil {
		fmt.Fprintf(errout, "Open the printed URL in a browser: %v\n", err)
	}
	a.startEnabled()
	select {
	case err = <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		// Interrupt event streams before the deferred a.close waits for model calls.
		return server.Close()
	}
}

func dataDirectory(override string) (string, error) {
	if override != "" {
		return filepath.Abs(override)
	}
	path, err := os.Executable()
	if err != nil {
		return "", err
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	return filepath.Dir(path), nil
}

func openBrowser(url string) error {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", url)
	} else {
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

func withDataDirHint(err error) error {
	if err == nil {
		return nil
	}
	var path *os.PathError
	var link *os.LinkError
	if !errors.Is(err, download.ErrCleanup) && (errors.As(err, &path) || errors.As(err, &link)) && (errors.Is(err, os.ErrPermission) || errors.Is(err, syscall.EROFS)) {
		return fmt.Errorf("%w (choose a writable directory with -data-dir)", err)
	}
	return err
}
