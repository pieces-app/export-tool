//go:build darwin || linux

package recovery

import (
	"context"
	"errors"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

func makePrivateDirectory(path string) error { return os.Mkdir(path, 0700) }

func finishPrivateFile(context.Context, *os.File) error { return nil }

func checkPrivate(ctx context.Context, f *os.File, directory bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	switch {
	case !ok:
		return ErrPermissions
	case stat.Uid != uint32(os.Geteuid()):
		return errOwnedByOther
	case info.Mode().Perm()&0077 != 0:
		return errLoosePermissions
	case directory && !info.IsDir():
		return errNotDirectory
	case !directory && !info.Mode().IsRegular():
		return errNotRegular
	case !directory && stat.Nlink != 1:
		return errMultiplyLinked
	}
	return checkPlatformACL(ctx, f)
}

func lockFile(f *os.File) error {
	err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
		return ErrBusy
	}
	if err != nil {
		return fail(ErrStorage, err)
	}
	return nil
}
