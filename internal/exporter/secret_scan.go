package exporter

import (
	"context"
	"errors"
	"time"

	"github.com/zricethezav/gitleaks/v8/detect"
	"github.com/zricethezav/gitleaks/v8/report"
)

// secretScanBudget is the time for scanning up to secretScanBudgetBytes, and
// for each further secretScanBudgetBytes. Tests lower it.
var secretScanBudget = 10 * time.Second

const secretScanBudgetBytes = 64 << 10

// errSecretScanIncomplete marks a scan that timed out twice. The final audit
// adds the output file's name to it.
var errSecretScanIncomplete = errors.New("secret scan did not finish after one full retry")

// detectFragment runs one detector call. Tests replace it to observe or stall
// individual calls.
var detectFragment = func(ctx context.Context, d *detect.Detector, f detect.Fragment) []report.Finding {
	return d.DetectContext(ctx, f)
}

// scanBudget is the time allowed to scan n bytes. Gitleaks runs every rule
// whose keyword appears anywhere in a fragment over that whole fragment, then
// rescans it in decoding passes, so one call's time grows with its size. Real
// 1 MiB notes took 3.6 to 19 s, and a fixed 10 s limit failed exports with
// errSecretScanIncomplete. Scaling keeps a stalled scan bounded without
// splitting the text, so detection is the same as one whole scan.
func scanBudget(n int) time.Duration {
	return secretScanBudget * time.Duration(1+n/secretScanBudgetBytes)
}

// detectSecrets scans text whole, with a budget that grows with its size and
// one fresh retry.
func (s *Scanner) detectSecrets(ctx context.Context, text string, stats *ScanResult) ([]report.Finding, error) {
	fragment := detect.Fragment{Raw: text}
	findings, retried, err := completeSecretScan(ctx, scanBudget(len(text)), func(deadline context.Context, attempt int) []report.Finding {
		detector := s.detector
		if attempt > 0 {
			// Never carry partial detector state into the complete retry.
			detector = s.forkForAudit().detector
		}
		return detectFragment(deadline, detector, fragment)
	})
	if retried {
		stats.TimeoutRetries++
	}
	return findings, err
}

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
	return nil, retried, errSecretScanIncomplete
}
