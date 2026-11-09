// Package download installs verified catalog artifacts in the caller's data directory.
package download

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/motoki317/vrc-tsuyaku/internal/catalog"
	"github.com/motoki317/vrc-tsuyaku/internal/config"
)

// ErrLicenseRequired means saved settings lack acceptance for the pinned revision.
var ErrLicenseRequired = errors.New("license acceptance required")

// ErrCleanup means the new installation is active, but an old installation or
// staging directory could not be removed. The error includes the remaining path.
var ErrCleanup = errors.New("artifact installed but cleanup failed")

// Progress reports downloaded source bytes across the artifact. The download and
// verify phases repeat for each source. Extraction runs in verify without increasing
// Downloaded or Total. The installed phase follows publication.
type Progress struct {
	ArtifactID, Phase string
	Downloaded, Total int64
}

// Manager serializes operations within one data directory. Reuse one Manager for
// that directory; callers must not install through separate managers concurrently.
// Creating a manager makes no network requests and creates no files.
type Manager struct {
	dataDir string
	client  *http.Client
	turn    chan struct{}
}

func New(dataDir string) (*Manager, error) {
	if dataDir == "" {
		return nil, errors.New("download data directory must not be empty")
	}
	dir, err := filepath.Abs(dataDir)
	if err != nil {
		return nil, err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 30 * time.Second
	return &Manager{dataDir: dir, turn: make(chan struct{}, 1), client: &http.Client{
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("too many download redirects")
			}
			return httpsURL(req.URL)
		},
	}}, nil
}

func httpsURL(u *url.URL) error {
	if u.Scheme != "https" || u.Host == "" {
		return fmt.Errorf("download URL must use HTTPS: %s", u)
	}
	return nil
}

func (m *Manager) acquire(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case m.turn <- struct{}{}:
		return nil
	}
}

// Directory returns the artifact directory without checking files or accessing the network.
// goos validates the catalog lookup but does not affect the path. One data directory
// holds one OS's artifacts.
func (m *Manager) Directory(id, goos string) (string, error) {
	if _, err := catalog.Lookup(id, goos); err != nil {
		return "", err
	}
	return filepath.Join(m.dataDir, "downloads", id), nil
}

// Download fetches and verifies every source and selected archive member before
// replacing an installation. Before publication, failure or cancellation removes
// staging files and preserves an existing installation. Each retry starts from
// the beginning. A nil result means the artifact is installed.
// On ErrCleanup, keep the installed state, report the error, and ask the user to
// remove the leftover directory named in the error. An installed event is sent.
// An artifact with an AcceptanceKey requires that key in settings.yaml before
// any network request. Missing acceptance returns ErrLicenseRequired.
// The caller must invoke Download only in response to the user's download action.
// notify, if nonnil, runs synchronously and must not call this Manager's methods.
func (m *Manager) Download(ctx context.Context, id, goos string, notify func(Progress)) error {
	a, err := catalog.Lookup(id, goos)
	if err != nil {
		return err
	}
	if err = m.acquire(ctx); err != nil {
		return err
	}
	defer func() { <-m.turn }()
	if a.AcceptanceKey != "" {
		settings, err := config.Load(m.dataDir)
		if err != nil {
			return err
		}
		if _, ok := settings.LicensesAccepted[a.AcceptanceKey]; !ok {
			return fmt.Errorf("%w: %s", ErrLicenseRequired, a.AcceptanceKey)
		}
	}
	return m.install(ctx, a, notify)
}

func (m *Manager) install(ctx context.Context, a catalog.Artifact, notify func(Progress)) (err error) {
	defer func() {
		if err != nil {
			err = fmt.Errorf("install %s in %s: %w", a.ID, m.dataDir, err)
		}
	}()
	committed := false
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = os.MkdirAll(m.dataDir, 0o755); err != nil {
		return err
	}
	data, err := os.OpenRoot(m.dataDir)
	if err != nil {
		return err
	}
	defer data.Close()
	if err = data.MkdirAll("downloads", 0o755); err != nil {
		return err
	}
	root, err := data.OpenRoot("downloads")
	if err != nil {
		return err
	}
	defer root.Close()
	stage := ".download-" + rand.Text()
	if err = root.Mkdir(stage, 0o700); err != nil {
		return err
	}
	defer func() {
		if cleanupErr := root.RemoveAll(stage); cleanupErr != nil {
			cleanupErr = fmt.Errorf("remove staging directory %s: %w", filepath.Join(root.Name(), stage), cleanupErr)
			if committed {
				cleanupErr = errors.Join(ErrCleanup, cleanupErr)
			}
			err = errors.Join(err, cleanupErr)
		}
	}()
	work, err := root.OpenRoot(stage)
	if err != nil {
		return err
	}
	// Windows refuses to rename a directory while these handles remain open.
	err = m.prepare(ctx, work, a, notify)
	err = errors.Join(err, work.Close())
	if err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = publish(root, filepath.Join(stage, "files"), a.ID); err != nil && !errors.Is(err, ErrCleanup) {
		return err
	}
	committed = true
	if notify != nil {
		var total int64
		for _, s := range a.Sources {
			total += s.Size
		}
		notify(Progress{ArtifactID: a.ID, Phase: "installed", Downloaded: total, Total: total})
	}
	return err
}

func publish(root *os.Root, stage, id string) error {
	backup := ".previous-" + rand.Text()
	exists := false
	if _, err := root.Lstat(id); err == nil {
		exists = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if exists {
		if err := root.Rename(id, backup); err != nil {
			return fmt.Errorf("move existing %s aside: %w", id, err)
		}
	}
	if err := root.Rename(stage, id); err != nil {
		if exists {
			err = errors.Join(err, root.Rename(backup, id))
		}
		return fmt.Errorf("publish %s: %w", id, err)
	}
	if exists {
		if err := root.RemoveAll(backup); err != nil {
			return fmt.Errorf("%w: remove old files in %s: %w", ErrCleanup, filepath.Join(root.Name(), backup), err)
		}
	}
	return nil
}

// CheckInstalled hashes every required file without network access. Call it at
// startup and cache the result for the UI and flow starts. A nil Download result
// already establishes the installed state without another hash check.
// Missing or corrupt files and Linux executables without execute permission return
// false. I/O and cancellation errors are reported.
func (m *Manager) CheckInstalled(ctx context.Context, id, goos string) (bool, error) {
	a, err := catalog.Lookup(id, goos)
	if err != nil {
		return false, err
	}
	if err = m.acquire(ctx); err != nil {
		return false, err
	}
	defer func() { <-m.turn }()
	return m.check(ctx, a)
}

func (m *Manager) check(ctx context.Context, a catalog.Artifact) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	data, err := os.OpenRoot(m.dataDir)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer data.Close()
	root, err := data.OpenRoot(filepath.Join("downloads", a.ID))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer root.Close()
	for _, s := range a.Sources {
		for _, f := range s.Files {
			file, err := root.Open(f.Path)
			if errors.Is(err, os.ErrNotExist) {
				return false, nil
			}
			if err != nil {
				return false, err
			}
			info, statErr := file.Stat()
			if statErr != nil {
				file.Close()
				return false, statErr
			}
			if !info.Mode().IsRegular() || info.Size() != f.Size ||
				(runtime.GOOS == "linux" && f.Executable && info.Mode().Perm()&0o111 == 0) {
				file.Close()
				return false, nil
			}
			err = verifyCopy(ctx, nil, file, f.Size, f.SHA256)
			closeErr := file.Close()
			if errors.Is(err, errIntegrity) {
				return false, closeErr
			}
			if err != nil || closeErr != nil {
				return false, errors.Join(err, closeErr)
			}
		}
	}
	return true, nil
}
