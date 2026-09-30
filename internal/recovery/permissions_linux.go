package recovery

import (
	"context"
	"os"
)

func finishPrivateDirectory(context.Context, *os.File) error { return nil }

// Linux POSIX ACL named-user/group access is bounded by the group class mask,
// reflected in mode bits. checkPrivate requires that mask to grant no access.
func checkPlatformACL(context.Context, *os.File) error { return nil }
