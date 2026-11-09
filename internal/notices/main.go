package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
)

func main() {
	if err := generate("go.mod", "cmd/vrc-tsuyaku/notices", runtime.Version(), moduleDirectory); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func moduleDirectory(mod module.Version) (string, error) {
	cmd := exec.Command("go", "mod", "download", "-json", mod.String())
	data, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("download %s: %w\n%s", mod, err, data)
	}
	var downloaded struct{ Dir string }
	if err := json.Unmarshal(data, &downloaded); err != nil {
		return "", err
	}
	if downloaded.Dir == "" {
		return "", fmt.Errorf("no module cache directory for %s", mod)
	}
	return downloaded.Dir, nil
}

func generate(modPath, dest, goVersion string, directory func(module.Version) (string, error)) error {
	data, err := os.ReadFile(modPath)
	if err != nil {
		return err
	}
	mods, err := modfile.Parse(modPath, data, nil)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dest, 0755); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(dest, ".generate-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	if err := os.MkdirAll(filepath.Join(stage, "modules"), 0755); err != nil {
		return err
	}
	for _, req := range mods.Require {
		dir, err := directory(req.Mod)
		if err != nil {
			return err
		}
		if err := copyLicenses(dir, filepath.Join(stage, "modules", filepath.FromSlash(req.Mod.String()))); err != nil {
			return fmt.Errorf("%s: %w", req.Mod, err)
		}
	}
	toolchain := module.Version{Path: "golang.org/toolchain", Version: "v0.0.1-" + goVersion + "." + runtime.GOOS + "-" + runtime.GOARCH}
	goDir, err := directory(toolchain)
	if err != nil {
		return err
	}
	if err := copyFile(filepath.Join(goDir, "LICENSE"), filepath.Join(stage, "go", "LICENSE")); err != nil {
		return err
	}
	for _, name := range []string{"modules", "go"} {
		if err := os.RemoveAll(filepath.Join(dest, name)); err != nil {
			return err
		}
		if err := os.Rename(filepath.Join(stage, name), filepath.Join(dest, name)); err != nil {
			return err
		}
	}
	return nil
}

func copyLicenses(source, dest string) error {
	entries, err := os.ReadDir(source)
	if err != nil {
		return err
	}
	found := false
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := strings.ToUpper(entry.Name())
		base := name
		if i := strings.IndexAny(name, ".-_"); i >= 0 {
			base = name[:i]
		}
		if base != "LICENSE" && base != "LICENCE" && base != "NOTICE" {
			continue
		}
		if err := copyFile(filepath.Join(source, entry.Name()), filepath.Join(dest, entry.Name())); err != nil {
			return err
		}
		found = found || base != "NOTICE"
	}
	if !found {
		return fmt.Errorf("no LICENSE file in module cache directory")
	}
	return nil
}

func copyFile(source, dest string) error {
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	if len(data) == 0 {
		return fmt.Errorf("empty license text: %s", source)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return err
	}
	return os.WriteFile(dest, data, 0644)
}
