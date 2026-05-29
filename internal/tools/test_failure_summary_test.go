package tools

import (
	"strings"
	"testing"
)

func TestSummarizeTestFailureGo(t *testing.T) {
	output := `=== RUN   TestThing
    thing_test.go:42: got 1, want 2
--- FAIL: TestThing (0.00s)
FAIL
`
	summary := summarizeTestFailure(output, "go", 8)
	for _, want := range []string{
		"Failure summary:",
		"failed test: TestThing",
		"thing_test.go:42: got 1, want 2",
		"Recommended next step",
	} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary missing %q:\n%s", want, summary)
		}
	}
}

func TestSummarizeTestFailurePython(t *testing.T) {
	output := `________ test_parse ________
pkg/test_parser.py:12: AssertionError: expected parsed value
E   AssertionError: expected parsed value
`
	summary := summarizeTestFailure(output, "python", 8)
	for _, want := range []string{
		"pytest case: test_parse",
		"pkg/test_parser.py:12: AssertionError: expected parsed value",
	} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary missing %q:\n%s", want, summary)
		}
	}
}
