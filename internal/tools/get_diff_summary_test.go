package tools

import (
	"strings"
	"testing"
)

func TestFormatDiffSummary(t *testing.T) {
	diff := `diff --git a/a.go b/a.go
index 111..222 100644
--- a/a.go
+++ b/a.go
@@ -1 +1 @@
-old
+new
diff --git a/b.py b/b.py
index 333..444 100644
--- a/b.py
+++ b/b.py
@@ -1 +1 @@
-x
+y
`
	output := formatDiffSummary("1234567890", "abcdef1234", diff)
	for _, want := range []string{
		"Diff: 12345678..abcdef12",
		"Changed files:",
		"- a.go",
		"- b.py",
		"```diff",
		"+new",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q:\n%s", want, output)
		}
	}
}
