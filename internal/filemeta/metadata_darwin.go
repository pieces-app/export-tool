package filemeta

import (
	"bytes"
	"golang.org/x/sys/unix"
	"howett.net/plist"
)

func Write(path, title, description string, tags []string) string {
	for name, value := range map[string]any{"com.apple.metadata:_kMDItemUserTags": tags, "com.apple.metadata:kMDItemFinderComment": description} {
		b, err := plist.Marshal(value, plist.BinaryFormat)
		if err != nil {
			return "encoding_failed"
		}
		if unix.Setxattr(path, name, b, 0) != nil {
			return "unsupported_or_write_failed"
		}
		n, err := unix.Getxattr(path, name, nil)
		if err != nil {
			return "readback_failed"
		}
		actual := make([]byte, n)
		if _, err = unix.Getxattr(path, name, actual); err != nil || !bytes.Equal(b, actual) {
			return "readback_failed"
		}
	}
	return "applied"
}
