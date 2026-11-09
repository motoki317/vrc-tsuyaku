package download

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/motoki317/vrc-tsuyaku/internal/catalog"
	"github.com/motoki317/vrc-tsuyaku/internal/errcode"
)

var errIntegrity = errcode.With(errcode.DownloadIntegrity, errors.New("size or SHA-256 mismatch"))

type codedReader struct {
	io.Reader
	code errcode.Code
}

func (r codedReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		err = errcode.With(r.code, err)
	}
	return n, err
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}

func verifyCopy(ctx context.Context, w io.Writer, r io.Reader, size int64, digest string) error {
	hash := sha256.New()
	if w == nil {
		w = io.Discard
	}
	n, err := io.Copy(io.MultiWriter(w, hash), io.LimitReader(contextReader{ctx, r}, size+1))
	if err != nil {
		return err
	}
	if n != size || hex.EncodeToString(hash.Sum(nil)) != digest {
		return errIntegrity
	}
	return ctx.Err()
}

type progressReader struct {
	r      io.Reader
	p      *Progress
	notify func(Progress)
	last   int64
}

func (r *progressReader) Read(b []byte) (int, error) {
	n, err := r.r.Read(b)
	r.p.Downloaded += int64(n)
	if r.notify != nil && (r.p.Downloaded-r.last >= 1<<20 || err != nil) {
		r.notify(*r.p)
		r.last = r.p.Downloaded
	}
	return n, err
}

func (m *Manager) prepare(ctx context.Context, work *os.Root, a catalog.Artifact, notify func(Progress)) error {
	if err := work.Mkdir("files", 0o755); err != nil {
		return err
	}
	files, err := work.OpenRoot("files")
	if err != nil {
		return err
	}
	defer files.Close()
	p := Progress{ArtifactID: a.ID, Phase: "download"}
	for _, s := range a.Sources {
		p.Total += s.Size
	}
	for i, s := range a.Sources {
		p.Phase = "download"
		if notify != nil {
			notify(p)
		}
		name := fmt.Sprintf("source-%d", i)
		if err = m.fetch(ctx, work, name, s, &p, notify); err != nil {
			return fmt.Errorf("download %s: %w", s.URL, err)
		}
		p.Phase = "verify"
		if notify != nil {
			notify(p)
		}
		if err = extract(ctx, work, files, name, s); err != nil {
			return fmt.Errorf("extract %s: %w", s.URL, err)
		}
		if s.Format != "file" {
			if err = work.Remove(name); err != nil {
				return err
			}
		}
	}
	return nil
}

func (m *Manager) fetch(ctx context.Context, root *os.Root, name string, s catalog.Source, p *Progress, notify func(Progress)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.URL, nil)
	if err != nil {
		return err
	}
	if err = httpsURL(req.URL); err != nil {
		return err
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return errcode.With(errcode.DownloadHTTP, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errcode.With(errcode.DownloadHTTP, fmt.Errorf("HTTP %s", resp.Status))
	}
	if resp.ContentLength >= 0 && resp.ContentLength != s.Size {
		return errIntegrity
	}
	f, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	r := &progressReader{r: codedReader{resp.Body, errcode.DownloadHTTP}, p: p, notify: notify, last: p.Downloaded}
	copyErr := verifyCopy(ctx, f, r, s.Size, s.SHA256)
	if copyErr == nil {
		copyErr = f.Sync()
	}
	return errors.Join(copyErr, f.Close())
}

func extract(ctx context.Context, work, files *os.Root, name string, s catalog.Source) error {
	if s.Format == "file" {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := work.Chmod(name, installMode(s.Files[0])); err != nil {
			return err
		}
		return work.Rename(name, filepath.Join("files", s.Files[0].Path))
	}
	f, err := work.Open(name)
	if err != nil {
		return err
	}
	defer f.Close()
	switch s.Format {
	case "zip":
		z, err := zip.NewReader(f, s.Size)
		if err != nil {
			return errcode.With(errcode.DownloadIntegrity, err)
		}
		for _, wanted := range s.Files {
			var match *zip.File
			for _, member := range z.File {
				if member.Name == wanted.Member {
					if match != nil {
						return errcode.With(errcode.DownloadIntegrity, fmt.Errorf("duplicate member %s", wanted.Member))
					}
					match = member
				}
			}
			if match == nil {
				return errcode.With(errcode.DownloadIntegrity, fmt.Errorf("missing member %s", wanted.Member))
			}
			if !match.Mode().IsRegular() || match.UncompressedSize64 != uint64(wanted.Size) {
				return errcode.With(errcode.DownloadIntegrity, fmt.Errorf("invalid member %s", wanted.Member))
			}
			r, err := match.Open()
			if err != nil {
				return errcode.With(errcode.DownloadIntegrity, err)
			}
			err = writeMember(ctx, files, wanted, codedReader{r, errcode.DownloadIntegrity})
			err = errors.Join(err, r.Close())
			if err != nil {
				return err
			}
		}
		return nil
	case "tar.gz":
		gz, err := gzip.NewReader(contextReader{ctx, f})
		if err != nil {
			return errcode.With(errcode.DownloadIntegrity, err)
		}
		defer gz.Close()
		tr := tar.NewReader(gz)
		seen := make(map[string]bool)
		for {
			h, err := tr.Next()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return errcode.With(errcode.DownloadIntegrity, err)
			}
			for _, wanted := range s.Files {
				if h.Name == wanted.Member {
					if seen[h.Name] {
						return errcode.With(errcode.DownloadIntegrity, fmt.Errorf("duplicate member %s", h.Name))
					}
					seen[h.Name] = true
					if h.Typeflag != tar.TypeReg || h.Size != wanted.Size {
						return errcode.With(errcode.DownloadIntegrity, fmt.Errorf("invalid member %s", h.Name))
					}
					if err = writeMember(ctx, files, wanted, codedReader{tr, errcode.DownloadIntegrity}); err != nil {
						return err
					}
				}
			}
		}
		for _, wanted := range s.Files {
			if !seen[wanted.Member] {
				return errcode.With(errcode.DownloadIntegrity, fmt.Errorf("missing member %s", wanted.Member))
			}
		}
		return nil
	default:
		return fmt.Errorf("unsupported archive format %q", s.Format)
	}
}

func writeMember(ctx context.Context, root *os.Root, f catalog.File, r io.Reader) error {
	out, err := root.OpenFile(f.Path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, installMode(f))
	if err != nil {
		return err
	}
	err = verifyCopy(ctx, out, r, f.Size, f.SHA256)
	if err == nil {
		err = out.Sync()
	}
	return errors.Join(err, out.Close())
}

func installMode(f catalog.File) os.FileMode {
	if f.Executable {
		return 0o755
	}
	return 0o644
}
