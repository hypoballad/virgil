package tools

import (
	"regexp"
	"strings"
)

var (
	goFailLineRE      = regexp.MustCompile(`^\s*([^\s:]+_test\.go):(\d+):\s*(.+)$`)
	goFailTestRE      = regexp.MustCompile(`^--- FAIL: ([^\s]+)`)
	pythonFailLineRE  = regexp.MustCompile(`^\s*(/\S+\.py|\S+\.py):(\d+):\s*(.+)$`)
	pytestSectionRE   = regexp.MustCompile(`^_{2,}\s+(.+?)\s+_{2,}$`)
	nodeFailLineRE    = regexp.MustCompile(`^\s*(\S+\.(?:js|jsx|ts|tsx)):(\d+):(\d+)\s+(.+)$`)
	rustFailLineRE    = regexp.MustCompile(`^\s*-->\s+(\S+\.rs):(\d+):(\d+)`)
	testSummaryLineRE = regexp.MustCompile(`(?i)(FAIL|Error|panic|AssertionError|expected|undefined|cannot find|Traceback|FAILED|error\[E\d+\])`)
)

func summarizeTestFailure(output string, language string, maxItems int) string {
	if maxItems <= 0 {
		maxItems = 8
	}
	lines := strings.Split(output, "\n")
	items := make([]string, 0, maxItems)
	seen := make(map[string]bool)

	add := func(item string) {
		item = strings.TrimSpace(item)
		if item == "" || seen[item] || len(items) >= maxItems {
			return
		}
		seen[item] = true
		items = append(items, item)
	}

	for _, line := range lines {
		switch language {
		case "go":
			if m := goFailTestRE.FindStringSubmatch(line); len(m) == 2 {
				add("failed test: " + m[1])
				continue
			}
			if m := goFailLineRE.FindStringSubmatch(line); len(m) == 4 {
				add(m[1] + ":" + m[2] + ": " + compactFailureText(m[3]))
				continue
			}
		case "python":
			if m := pytestSectionRE.FindStringSubmatch(line); len(m) == 2 {
				add("pytest case: " + compactFailureText(m[1]))
				continue
			}
			if m := pythonFailLineRE.FindStringSubmatch(line); len(m) == 4 {
				add(m[1] + ":" + m[2] + ": " + compactFailureText(m[3]))
				continue
			}
		case "node":
			if m := nodeFailLineRE.FindStringSubmatch(line); len(m) == 5 {
				add(m[1] + ":" + m[2] + ": " + compactFailureText(m[4]))
				continue
			}
		case "rust":
			if m := rustFailLineRE.FindStringSubmatch(line); len(m) == 4 {
				add(m[1] + ":" + m[2] + ": rust compiler error location")
				continue
			}
		}
	}

	for _, line := range lines {
		if len(items) >= maxItems {
			break
		}
		if testSummaryLineRE.MatchString(line) {
			add(compactFailureText(line))
		}
	}

	if len(items) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("Failure summary:\n")
	for _, item := range items {
		sb.WriteString("- ")
		sb.WriteString(item)
		sb.WriteString("\n")
	}
	sb.WriteString("\nRecommended next step: inspect only the failing file/function above, then make the smallest fix and rerun the same test command.")
	return sb.String()
}

func compactFailureText(s string) string {
	s = strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
	const max = 180
	if len([]rune(s)) <= max {
		return s
	}
	runes := []rune(s)
	return string(runes[:max]) + "..."
}
