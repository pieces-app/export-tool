package recovery

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWorkspaceWindowsRejectsAdditionalACLPrincipal(t *testing.T) {
	w, o := createFixture(t)
	w.Close()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;SY)(A;;FA;;;" + user.User.Sid.String() + ")(A;;FR;;;WD)")
	if err != nil {
		t.Fatal(err)
	}
	acl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(o.KeyDirectory, w.ID()+".key")
	if err := windows.SetNamedSecurityInfo(key, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		t.Fatal(err)
	}
	opened, err := Open(context.Background(), o)
	if !errors.Is(err, ErrPermissions) {
		if opened != nil {
			opened.Close()
		}
		t.Fatal("additional Windows ACL principal was accepted")
	}
}
