package exporter

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/zricethezav/gitleaks/v8/detect"
	"github.com/zricethezav/gitleaks/v8/report"
)

// secretScanBudget bounds one detector call. Tests lower it.
var secretScanBudget = 10 * time.Second

// errSecretScanIncomplete marks a scan that timed out twice. The final audit
// adds the output file's name to it.
var errSecretScanIncomplete = errors.New("secret scan did not finish after one full retry")

// Gitleaks runs every rule whose keyword appears anywhere in a fragment over
// that whole fragment, then rescans it in decoding passes, so one call's cost
// grows with the fragment. A 1 MiB note that mentions many services took over
// 10 s twice, failing the export. Scanning bounded windows keeps every call far
// below the budget, and fewer rules run in each window.
//
// Windows overlap by more than any single secret the rules match, including
// PEM and PGP private key blocks, so each secret lies whole in one window.
// Cuts fall just after a line break near the limit, else after a space, so
// rules see the same neighboring text as in the whole value.
const secretScanWindowBytes = 64 << 10
const secretScanOverlapBytes = 16 << 10
const secretScanCutSearchBytes = 8 << 10

func secretScanWindows(text string) [][2]int {
	windows := [][2]int{}
	for start := 0; ; {
		end := start + secretScanWindowBytes
		if end >= len(text) {
			return append(windows, [2]int{start, len(text)})
		}
		end = cutAfter(text, end-secretScanCutSearchBytes, end)
		windows = append(windows, [2]int{start, end})
		// The next window starts between 24 and 16 KiB before this one ends.
		start = cutAfter(text, end-secretScanOverlapBytes-secretScanCutSearchBytes, end-secretScanOverlapBytes)
	}
}

// cutAfter returns a cut in (lo, hi]: just after the last line break in
// text[lo:hi], else after its last space or tab, else hi moved back to the
// start of a UTF-8 character.
func cutAfter(text string, lo, hi int) int {
	if i := strings.LastIndexByte(text[lo:hi], '\n'); i >= 0 {
		return lo + i + 1
	}
	if i := strings.LastIndexAny(text[lo:hi], " \t"); i >= 0 {
		return lo + i + 1
	}
	for hi > lo && !utf8.RuneStart(text[hi]) {
		hi--
	}
	return hi
}

// detectSecrets runs the detector over each window of text. Every window gets
// the full budget and one fresh retry, and any incomplete window fails closed.
func (s *Scanner) detectSecrets(ctx context.Context, text string, stats *ScanResult) ([]report.Finding, error) {
	var all []report.Finding
	for _, w := range secretScanWindows(text) {
		fragment := detect.Fragment{Raw: text[w[0]:w[1]]}
		findings, retried, err := completeSecretScan(ctx, secretScanBudget, func(deadline context.Context, attempt int) []report.Finding {
			detector := s.detector
			if attempt > 0 {
				// Never carry partial detector state into the complete retry.
				detector = s.forkForAudit().detector
			}
			return detector.DetectContext(deadline, fragment)
		})
		if retried {
			stats.TimeoutRetries++
		}
		if err != nil {
			return nil, err
		}
		all = append(all, findings...)
	}
	return all, nil
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
