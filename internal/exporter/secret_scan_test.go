package exporter

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/zricethezav/gitleaks/v8/detect"
	"github.com/zricethezav/gitleaks/v8/report"
)

func TestCompleteSecretScanDiscardsInterruptedFindings(t *testing.T) {
	var first context.Context
	calls := 0
	findings, retried, err := completeSecretScan(context.Background(), 5*time.Millisecond, func(ctx context.Context, attempt int) []report.Finding {
		calls++
		if attempt == 0 {
			first = ctx
			<-ctx.Done()
			return []report.Finding{{Secret: "partial finding must not survive"}}
		}
		if ctx == first || ctx.Err() != nil {
			t.Fatal("retry did not receive a fresh active context")
		}
		return []report.Finding{{Secret: "complete finding"}}
	})
	if err != nil || !retried || calls != 2 || len(findings) != 1 || findings[0].Secret != "complete finding" {
		t.Fatal("incomplete scan results were accepted", err, retried, calls)
	}
}

func TestCompleteSecretScanRepeatedTimeoutFailsClosed(t *testing.T) {
	calls := 0
	findings, retried, err := completeSecretScan(context.Background(), time.Millisecond, func(ctx context.Context, _ int) []report.Finding {
		calls++
		<-ctx.Done()
		return []report.Finding{{Secret: "partial"}}
	})
	if err == nil || !retried || calls != 2 || findings != nil {
		t.Fatal("repeated timeout did not fail closed", err, retried, calls)
	}
}

func TestCompleteSecretScanParentCancellationNeverRetries(t *testing.T) {
	for _, when := range []string{"before", "during", "deadline"} {
		t.Run(when, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			wantCalls, wantError := 1, context.Canceled
			if when == "before" {
				cancel()
				wantCalls = 0
			} else if when == "deadline" {
				cancel()
				ctx, cancel = context.WithTimeout(context.Background(), 5*time.Millisecond)
				wantError = context.DeadlineExceeded
			}
			defer cancel()
			calls := 0
			findings, retried, err := completeSecretScan(ctx, time.Second, func(child context.Context, _ int) []report.Finding {
				calls++
				if when == "deadline" {
					<-child.Done()
				} else {
					cancel() // Even an otherwise successful last callback must fail.
				}
				return []report.Finding{{Secret: "incomplete"}}
			})
			if !errors.Is(err, wantError) || retried || calls != wantCalls || findings != nil {
				t.Fatal("parent cancellation became success or triggered retry", err, retried, calls)
			}
		})
	}
}

func TestCompleteSecretScanRetryRetainsSecretDetection(t *testing.T) {
	s := scanner(t, DefaultPolicy())
	secret := fakeSecret()
	fragment := detect.Fragment{Raw: "body=token: " + secret}
	calls := 0
	findings, retried, err := completeSecretScan(context.Background(), time.Second, func(ctx context.Context, attempt int) []report.Finding {
		calls++
		if attempt == 0 {
			<-ctx.Done()
			return nil
		}
		fork := s.forkForAudit()
		if fork.detector == s.detector || fork.detector.IgnoreGitleaksAllow != s.detector.IgnoreGitleaksAllow || fork.detector.MaxDecodeDepth != s.detector.MaxDecodeDepth || fork.detector.MaxTargetMegaBytes != s.detector.MaxTargetMegaBytes {
			t.Fatal("retry detector lost its configured scan policy")
		}
		return fork.detector.DetectContext(ctx, fragment)
	})
	if err != nil || !retried || calls != 2 {
		t.Fatal("complete retry failed", err, retried, calls)
	}
	for _, finding := range findings {
		if finding.Secret == secret {
			return
		}
	}
	t.Fatal("fresh complete scan did not detect the synthetic credential")
}

func TestCompleteSecretScanSuccessfulFirstAttemptDoesNotRetry(t *testing.T) {
	calls := 0
	findings, retried, err := completeSecretScan(context.Background(), time.Second, func(context.Context, int) []report.Finding {
		calls++
		return nil
	})
	if err != nil || retried || calls != 1 || len(findings) != 0 {
		t.Fatal("complete first scan was retried", err, retried, calls)
	}
}
