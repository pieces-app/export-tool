package filemeta

import (
	"bytes"
	"golang.org/x/sys/unix"
	"strings"
)

func Write(path, title, description string, tags []string) string {
	normalized := make([]string, len(tags))
	for i, tag := range tags {
		normalized[i] = strings.ReplaceAll(tag, ",", "_")
	}
	for name, value := range map[string]string{"user.xdg.tags": strings.Join(normalized, ","), "user.xdg.comment": description} {
		b := []byte(value)
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
