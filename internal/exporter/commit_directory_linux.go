package exporter

import (
	"errors"

	"golang.org/x/sys/unix"
)

func renameDirectoryExclusive(from, to string) error {
	err := unix.Renameat2(unix.AT_FDCWD, from, unix.AT_FDCWD, to, unix.RENAME_NOREPLACE)
	if errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EINVAL) {
		return errors.Join(errors.ErrUnsupported, err)
	}
	return err
}
