package recovery

import (
	"context"
	"encoding/binary"
	"os"
	"os/exec"
	"runtime"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Darwin inherited ACLs can grant access independently of mode 0700/0600.
// Operate on the held descriptor, never an unchecked pathname. These are
// system utilities; command output never goes to the user or logs.
func descriptorCommand(ctx context.Context, f *os.File, program string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, program, args...)
	cmd.ExtraFiles = []*os.File{f}
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	output, err := cmd.Output()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return output, err
}

func finishPrivateDirectory(ctx context.Context, f *os.File) error {
	_, err := descriptorCommand(ctx, f, "/bin/chmod", "-N", "/dev/fd/3")
	return err
}

func checkPlatformACL(ctx context.Context, f *os.File) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// sys/attr.h: a 32-bit returned length followed by attrreference_t
	// (signed 32-bit offset, unsigned 32-bit length). Request only extended
	// security. A missing ACL has an empty reference; any present ACL is
	// rejected, so its variable-length body need not be allocated or parsed.
	// Path-based ls on /dev/fd does NOT expose the underlying file's ACL.
	attrs := unix.Attrlist{Bitmapcount: 5, Commonattr: unix.ATTR_CMN_EXTENDED_SECURITY}
	var buffer [12]byte
	_, _, errno := unix.Syscall6(unix.SYS_FGETATTRLIST, f.Fd(), uintptr(unsafe.Pointer(&attrs)), uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)), 0, 0)
	runtime.KeepAlive(f)
	if errno != 0 {
		return errno
	}
	if binary.NativeEndian.Uint32(buffer[0:4]) != 12 || binary.NativeEndian.Uint32(buffer[4:8]) != 8 || binary.NativeEndian.Uint32(buffer[8:12]) != 0 {
		return ErrPermissions
	}
	return nil
}
