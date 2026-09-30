package exporter

import (
	"errors"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

func renameDirectoryExclusive(from, to string) error {
	from, err := extendedWindowsPath(from)
	if err != nil {
		return err
	}
	to, err = extendedWindowsPath(to)
	if err != nil {
		return err
	}
	source, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	destination, err := windows.UTF16PtrFromString(to)
	if err != nil {
		return err
	}
	// Omitting MOVEFILE_REPLACE_EXISTING makes an existing destination fail.
	err = windows.MoveFileEx(source, destination, 0)
	if errors.Is(err, windows.ERROR_NOT_SUPPORTED) || errors.Is(err, windows.ERROR_CALL_NOT_IMPLEMENTED) {
		return errors.Join(errors.ErrUnsupported, err)
	}
	return err
}

func extendedWindowsPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(abs, `\\?\`) {
		return abs, nil
	}
	if strings.HasPrefix(abs, `\\`) {
		return `\\?\UNC\` + abs[2:], nil
	}
	return `\\?\` + abs, nil
}
