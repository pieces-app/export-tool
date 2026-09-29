package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfirmationAnswers(t *testing.T) {
	for _, test := range []struct {
		input    string
		want     bool
		hasError bool
	}{{"\n", true, false}, {"Y\n", true, false}, {"yes\n", true, false}, {"n\n", false, false}, {"NO\n", false, false}, {"bad\ny\n", true, false}, {"", false, true}, {"y", false, true}} {
		var output bytes.Buffer
		got, err := confirm(context.Background(), bufio.NewReader(strings.NewReader(test.input)), &output)
		if got != test.want || (err != nil) != test.hasError {
			t.Fatalf("confirmation %q returned %v, %v", test.input, got, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if approved, _ := confirm(ctx, bufio.NewReader(strings.NewReader("")), &bytes.Buffer{}); approved {
		t.Fatal("canceled prompt approved")
	}
}

func TestHelpVersionAndValidation(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"export", "--help"}, {"policy", "init", "--help"}, {"lists", "fetch", "--help"}, {"version"}} {
		var out, errs bytes.Buffer
		if code := Run(context.Background(), args, &out, &errs, "test-version"); code != 0 {
			t.Fatalf("%v exit %d", args, code)
		}
		if out.Len()+errs.Len() == 0 {
			t.Fatalf("%v printed no help", args)
		}
	}
	var out, errs bytes.Buffer
	if code := Run(context.Background(), []string{"export", "--base-url", "https://example.com"}, &out, &errs, "test"); code != 1 {
		t.Fatalf("unsafe URL exit %d", code)
	}
}

func TestScopeValidationBeforeConnection(t *testing.T) {
	for _, flags := range [][]string{{"--scope", "bad"}, {"--scope", "summaries", "--materials", "all"}, {"--scope", "all", "--materials", "TAGS"}} {
		var out, errs bytes.Buffer
		args := append([]string{"scan", "--launch-os=false", "--base-url", "http://127.0.0.1:1"}, flags...)
		if code := Run(context.Background(), args, &out, &errs, "test"); code != 1 || !strings.Contains(errs.String(), "scope") {
			t.Fatalf("scope validation did not precede OS discovery: %d %s", code, errs.String())
		}
	}
}

func TestPolicyInitDoesNotOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "policy.json")
	var out, errs bytes.Buffer
	args := []string{"policy", "init", "--output", path}
	if code := Run(context.Background(), args, &out, &errs, "test"); code != 0 {
		t.Fatal(errs.String())
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if code := Run(context.Background(), args, &out, &errs, "test"); code != 1 {
		t.Fatal("existing policy overwritten")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("existing policy changed")
	}
}

func TestCLIExportExitCodesAndPrivateErrors(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(map[bool]string{false: "complete", true: "missing"}[missing], func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				var value any
				switch r.URL.Path {
				case "/.well-known/health", "/.well-known/version":
					if r.URL.Path == "/.well-known/health" {
						value = "ok:macos"
					} else {
						value = "12.3.108"
					}
				case "/materials/metrics":
					value = map[string]any{"total_count": 1}
				case "/materials/identifiers":
					value = map[string]any{"identifiers": []string{"user"}}
				case "/users/batch/fetch":
					if missing {
						http.Error(w, "private server response should never be printed", http.StatusForbidden)
						return
					}
					value = map[string]any{"users": map[string]any{"iterable": []any{map[string]any{"id": "user", "name": "Fixture user"}}}}
				default:
					http.NotFound(w, r)
					return
				}
				_ = json.NewEncoder(w).Encode(value)
			}))
			defer srv.Close()
			outPath := filepath.Join(t.TempDir(), "export")
			var out, errs bytes.Buffer
			code := Run(context.Background(), []string{"export", "--yes", "--close-desktop=false", "--base-url", srv.URL, "--materials", "USERS", "--output", outPath}, &out, &errs, "test")
			want := 0
			if missing {
				want = 2
			}
			if code != want {
				t.Fatalf("exit=%d want=%d: %s", code, want, errs.String())
			}
			if strings.Contains(out.String()+errs.String(), "private server response") {
				t.Fatal("server content printed to terminal")
			}
			if _, err := os.Stat(filepath.Join(outPath, "manifest.json")); err != nil {
				t.Fatal(err)
			}
		})
	}
}
