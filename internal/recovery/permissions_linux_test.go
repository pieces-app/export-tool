package recovery

import (
	"context"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestWorkspaceLinuxACLMask(t *testing.T) {
	w, o := createFixture(t)
	w.Close()
	key := filepath.Join(o.KeyDirectory, w.ID()+".key")
	other := uint32(65534)
	if uint32(os.Geteuid()) == other {
		other--
	}
	acl := func(mask uint16) []byte {
		// Linux POSIX ACL xattr version 2, followed by tag/permissions/ID.
		body := binary.LittleEndian.AppendUint32(nil, 2)
		for _, entry := range []struct {
			tag, permissions uint16
			id               uint32
		}{{1, 6, ^uint32(0)}, {2, 4, other}, {4, 0, ^uint32(0)}, {16, mask, ^uint32(0)}, {32, 0, ^uint32(0)}} {
			body = binary.LittleEndian.AppendUint16(body, entry.tag)
			body = binary.LittleEndian.AppendUint16(body, entry.permissions)
			body = binary.LittleEndian.AppendUint32(body, entry.id)
		}
		return body
	}
	if err := unix.Setxattr(key, "system.posix_acl_access", acl(0), 0); err != nil {
		if errors.Is(err, unix.ENOTSUP) {
			t.Skip("test filesystem does not support POSIX ACLs")
		}
		t.Fatal(err)
	}
	w, err := Open(context.Background(), o)
	if err != nil {
		t.Fatal("fully masked named-user ACL was treated as an effective grant")
	}
	w.Close()
	if err := unix.Setxattr(key, "system.posix_acl_access", acl(4), 0); err != nil {
		t.Fatal(err)
	}
	opened, err := Open(context.Background(), o)
	if !errors.Is(err, ErrPermissions) {
		if opened != nil {
			opened.Close()
		}
		t.Fatal("effective named-user ACL grant was accepted")
	}
}
