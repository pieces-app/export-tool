package exporter

import (
	"errors"

	"golang.org/x/sys/unix"
)

func renameDirectoryExclusive(from, to string) error {
	err := unix.RenamexNp(from, to, unix.RENAME_EXCL)
	if errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EINVAL) {
		return errors.Join(errors.ErrUnsupported, err)
	}
	return err
}
