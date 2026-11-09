package main

import (
	"errors"
	"path/filepath"

	"golang.org/x/sys/windows"
)

func checkTemporaryFolder(dir string) error {
	temp, err := readWindowsPath(func(buf *uint16, size uint32) (uint32, error) {
		return windows.GetTempPath(size, buf)
	})
	if err != nil {
		// An unresolved temporary folder cannot identify this as a temporary copy.
		return nil
	}
	temp, err = filepath.EvalSymlinks(temp)
	if err != nil {
		// An unresolved temporary folder cannot identify this as a temporary copy.
		return nil
	}
	temp, err = longWindowsPath(temp)
	if err != nil {
		// An unresolved temporary folder cannot identify this as a temporary copy.
		return nil
	}
	dir, err = longWindowsPath(dir)
	if err != nil {
		return err
	}
	if !insideWindowsTemp(dir, temp) {
		return nil
	}
	languages, _ := windows.GetUserPreferredUILanguages(windows.MUI_LANGUAGE_NAME)
	message := temporaryFolderMessage(languages)
	text, err := windows.UTF16PtrFromString(message)
	if err != nil {
		return err
	}
	title, _ := windows.UTF16PtrFromString("VRC Tsuyaku")
	_, _ = windows.MessageBox(0, text, title, windows.MB_OK|windows.MB_ICONWARNING)
	return errors.New(message)
}

func longWindowsPath(name string) (string, error) {
	name16, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return "", err
	}
	return readWindowsPath(func(buf *uint16, size uint32) (uint32, error) {
		return windows.GetLongPathName(name16, buf, size)
	})
}

func readWindowsPath(read func(*uint16, uint32) (uint32, error)) (string, error) {
	for size := uint32(windows.MAX_PATH + 1); ; {
		buf := make([]uint16, size)
		n, err := read(&buf[0], size)
		if err != nil {
			return "", err
		}
		if n < size {
			return windows.UTF16ToString(buf[:n]), nil
		}
		size = n + 1
	}
}
