package recovery

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Files created without an explicit security descriptor take the token's
// default owner. For an elevated administrator that is the Administrators
// group, so SQLite's rollback journal, which the database driver creates, was
// owned by Administrators and failed checkPrivate's owner test while the
// tool's own files passed, because finishPrivateFile sets their owner
// (issue #20). Adopting the user as the default owner makes every file this
// process creates pass the same test.
var defaultOwnerErr = adoptUserAsDefaultOwner()

func adoptUserAsDefaultOwner() error {
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY|windows.TOKEN_ADJUST_DEFAULT, &token); err != nil {
		return err
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		return err
	}
	owner := struct{ Owner *windows.SID }{user.User.Sid}
	err = windows.SetTokenInformation(token, windows.TokenOwner, (*byte)(unsafe.Pointer(&owner)), uint32(unsafe.Sizeof(owner)))
	runtime.KeepAlive(user)
	return err
}

// ownerReason explains an owner mismatch. The common cause is an elevated
// window whose default owner could not be changed.
func ownerReason() error {
	if windows.GetCurrentProcessToken().IsElevated() {
		if defaultOwnerErr != nil {
			return fmt.Errorf("%w; this window is elevated and the process could not adopt the user as default owner (%v); run the export from a window that is not run as administrator", errOwnedByOther, defaultOwnerErr)
		}
		return fmt.Errorf("%w; this window is elevated", errOwnedByOther)
	}
	return errOwnedByOther
}

func extendedPath(path string) string {
	if strings.HasPrefix(path, `\\?\`) {
		return path
	}
	if strings.HasPrefix(path, `\\`) {
		return `\\?\UNC\` + strings.TrimPrefix(path, `\\`)
	}
	return `\\?\` + path
}

func makePrivateDirectory(path string) error {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	// Apply a protected ACL at creation, before any key or state can exist.
	sid := user.User.Sid.String()
	sd, err := windows.SecurityDescriptorFromString("O:" + sid + "D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;" + sid + ")")
	if err != nil {
		return err
	}
	name, err := windows.UTF16PtrFromString(extendedPath(path))
	if err != nil {
		return err
	}
	sa := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	err = windows.CreateDirectory(name, &sa)
	runtime.KeepAlive(sd)
	return err
}

func finishPrivateDirectory(context.Context, *os.File) error { return nil }

func finishPrivateFile(ctx context.Context, f *os.File) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// Elevated tokens can default to Administrators as the owner. Set the
	// actual user's ownership only on this exclusively created, still-empty
	// file. Its inherited DACL already grants access only to user/SYSTEM.
	name, err := windows.UTF16PtrFromString(extendedPath(f.Name()))
	if err != nil {
		return err
	}
	h, err := windows.CreateFile(name, windows.READ_CONTROL|windows.WRITE_OWNER|windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return err
	}
	other := os.NewFile(uintptr(h), f.Name())
	defer other.Close()
	before, err := f.Stat()
	if err != nil {
		return err
	}
	after, err := other.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(before, after) {
		return errIdentityChanged
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	return windows.SetSecurityInfo(h, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION, user.User.Sid, nil, nil, nil)
}

func checkPrivate(ctx context.Context, f *os.File, directory bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	h := windows.Handle(f.Fd())
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		return err
	}
	switch {
	case info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0:
		return errReparsePoint
	case directory && info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0:
		return errNotDirectory
	case !directory && info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0:
		return errNotRegular
	case !directory && info.NumberOfLinks != 1:
		return errMultiplyLinked
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	sd, err := windows.GetSecurityInfo(h, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	defer runtime.KeepAlive(sd)
	owner, _, err := sd.Owner()
	if err != nil || owner == nil || !owner.Equals(user.User.Sid) {
		return ownerReason()
	}
	acl, _, err := sd.DACL()
	if err != nil || acl == nil || acl.AceCount == 0 {
		return errOtherPrincipal
	}
	if directory {
		control, _, err := sd.Control()
		if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
			return errInheritsDACL
		}
	}
	userAllowed := false
	for i := uint32(0); i < uint32(acl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(acl, i, &ace); err != nil || ace == nil || ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Header.AceSize < uint16(unsafe.Sizeof(windows.ACCESS_ALLOWED_ACE{})) {
			return errOtherPrincipal
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if sid.Equals(user.User.Sid) {
			userAllowed = true
		} else if !sid.IsWellKnown(windows.WinLocalSystemSid) {
			return errOtherPrincipal
		}
	}
	if !userAllowed {
		return errOtherPrincipal
	}
	return nil
}

func lockFile(f *os.File) error {
	overlap := &windows.Overlapped{}
	err := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, overlap)
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return ErrBusy
	}
	if err != nil {
		return fail(ErrStorage, err)
	}
	return nil
}
