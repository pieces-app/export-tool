package exporter

import "errors"

// Generated paths can contain private summary titles or person names. Keep
// those out of terminal errors while retaining errno for programmatic checks.
type outputFilesystemError struct{ cause error }

func (e *outputFilesystemError) Error() string {
	if errors.Is(e.cause, errors.ErrUnsupported) {
		return "destination filesystem does not support safe non-overwriting finalization; choose another filesystem; partial output was not finalized"
	}
	return "export filesystem operation failed; check available space and file permissions; output was not finalized"
}

func (e *outputFilesystemError) Unwrap() error { return e.cause }
