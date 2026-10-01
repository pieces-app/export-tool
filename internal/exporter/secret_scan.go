package exporter

import (
	"context"
	"errors"
	"time"

	"github.com/zricethezav/gitleaks/v8/report"
)

// A suspend/resume or transient scheduling stall can expire a local scan's
// deadline. Discard its partial findings and permit one complete fresh attempt.
// Cancellation and repeated timeouts still fail closed; no scan is skipped.
func completeSecretScan(ctx context.Context, budget time.Duration, scan func(context.Context, int) []report.Finding) ([]report.Finding, bool, error) {
	retried := false
	for attempt := 0; attempt < 2; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, retried, err
		}
		if attempt > 0 {
			retried = true
		}
		deadline, cancel := context.WithTimeout(ctx, budget)
		findings := scan(deadline, attempt)
		err := deadline.Err()
		cancel()
		if parentErr := ctx.Err(); parentErr != nil {
			return nil, retried, parentErr
		}
		if err == nil {
			return findings, retried, nil
		}
		if !errors.Is(err, context.DeadlineExceeded) {
			return nil, retried, err
		}
	}
	return nil, retried, errConfig("secret scan did not finish after one full retry")
}
