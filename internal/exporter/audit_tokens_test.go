package exporter

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuditTokenCacheRequiresCompleteExactApprovals(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var cache auditTokenCache
	calls := map[string]int{}
	failed := errors.New("incomplete scan")
	scan := func(value string) error {
		calls[value]++
		if value == "fails" {
			return failed
		}
		return nil
	}
	for range 3 {
		for _, value := range []string{"approved", "Approved", "", "1"} {
			if err := cache.check(ctx, value, scan); err != nil {
				t.Fatal(err)
			}
		}
		if err := cache.check(ctx, "fails", scan); !errors.Is(err, failed) {
			t.Fatal("an incomplete scan was accepted")
		}
	}
	if calls["approved"] != 1 || calls["Approved"] != 1 || calls[""] != 1 || calls["1"] != 1 || calls["fails"] != 3 {
		t.Fatal("exact approvals or failed scans were reused incorrectly")
	}
	cancel()
	if err := cache.check(ctx, "approved", scan); !errors.Is(err, context.Canceled) {
		t.Fatal("approval bypassed cancellation")
	}
}

func TestAuditTokenCacheBounds(t *testing.T) {
	for _, size := range []int{1, auditTokenMaxValue - 8, auditTokenMaxValue + 1} {
		var cache auditTokenCache
		calls := 0
		scan := func(string) error { calls++; return nil }
		for i := range auditTokenEntries + 3 {
			value := fmt.Sprintf("%08d", i) + strings.Repeat("x", size)
			for range 2 {
				if err := cache.check(context.Background(), value, scan); err != nil {
					t.Fatal(err)
				}
			}
		}
		if len(cache.approved) > auditTokenEntries || cache.bytes > auditTokenBytes || calls <= auditTokenEntries {
			t.Fatal("cache bounds discarded mandatory scanning")
		}
		if size == auditTokenMaxValue-8 && cache.bytes != auditTokenBytes {
			t.Fatal("fixture did not exercise the byte limit")
		}
	}
}

func TestJSONTokenApprovalsStayWithinOneFileAndPolicy(t *testing.T) {
	r := &run{ctx: context.Background(), stage: t.TempDir(), opts: Options{Scanner: scanner(t, DefaultPolicy())}}
	auditor := outputAuditor{run: r}
	first, second := filepath.Join(r.stage, "first.jsonl"), filepath.Join(r.stage, "second.jsonl")
	text := strings.Repeat("{\"label\":\"initially ordinary label\",\"count\":1}\n", 100)
	for _, file := range []string{first, second} {
		if err := os.WriteFile(file, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := auditor.file(first); err != nil {
		t.Fatal(err)
	}
	r.opts.Scanner.remember("initially ordinary label")
	if err := auditor.file(second); err == nil {
		t.Fatal("approval from a prior file bypassed a learned credential")
	}
	// A secret after many repeated approved tokens must still fail the file.
	r.opts.Scanner = scanner(t, DefaultPolicy())
	if err := os.WriteFile(second, []byte(text+"{\"label\":\""+fakeSecret()+"\"}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := auditor.file(second); err == nil {
		t.Fatal("repeated approvals hid a new credential")
	}
	if err := os.WriteFile(second, []byte(text+"{\"label\":"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := auditor.file(second); err == nil {
		t.Fatal("repeated approvals hid truncated JSON")
	}
}

func BenchmarkRepeatedAuditTokens(b *testing.B) {
	for _, cached := range []bool{false, true} {
		b.Run(fmt.Sprintf("cached=%t", cached), func(b *testing.B) {
			scanner, err := NewScanner(DefaultPolicy(), "")
			if err != nil {
				b.Fatal(err)
			}
			r := &run{ctx: context.Background(), opts: Options{Scanner: scanner}}
			values := []string{"source_ref", "target_ref", "Relation", "association_record", "annotations", "0", "1", "markdown/summaries/ordinary-summary.md"}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				var cache auditTokenCache
				for range 2000 {
					for _, value := range values {
						var err error
						if cached {
							err = cache.check(r.ctx, value, r.auditText)
						} else {
							err = r.auditText(value)
						}
						if err != nil {
							b.Fatal(err)
						}
					}
				}
			}
		})
	}
}
