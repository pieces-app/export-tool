package exporter

import "os"

// Preflight is not a lock: another process can create the destination while
// records are being exported. A normal POSIX rename can replace an empty
// directory, so finalization must atomically refuse every existing target.
func commitDirectory(from, to string) error {
	if err := renameDirectoryExclusive(from, to); err != nil {
		return &os.LinkError{Op: "finalize", Old: from, New: to, Err: err}
	}
	return nil
}
