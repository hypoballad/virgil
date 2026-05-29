package main

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type secretFinding struct {
	File     string `json:"file"`
	Line     int    `json:"line"`
	Detector string `json:"detector"`
	Severity string `json:"severity"`
	Preview  string `json:"preview"`
}

type secretScanReport struct {
	FindingCount int             `json:"finding_count"`
	Findings     []secretFinding `json:"findings"`
}

type secretDetector struct {
	name     string
	severity string
	re       *regexp.Regexp
	mask     string
}

var dogfoodSecretDetectors = []secretDetector{
	{name: "github-token", severity: "high", re: regexp.MustCompile(`\b(?:ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9_]{20,}\b`), mask: "[secret]"},
	{name: "github-pat", severity: "high", re: regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{20,}\b`), mask: "[secret]"},
	{name: "openai-key", severity: "high", re: regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{20,}\b`), mask: "[secret]"},
	{name: "bearer-token", severity: "high", re: regexp.MustCompile(`(?i)Authorization:\s*Bearer\s+[A-Za-z0-9._~+/=-]{12,}`), mask: "Authorization: Bearer [secret]"},
	{name: "cookie", severity: "high", re: regexp.MustCompile(`(?i)\b(?:Cookie|Set-Cookie):\s*[^\n\r]+`), mask: "[cookie]"},
	{name: "generic-secret", severity: "high", re: regexp.MustCompile(`(?i)\b(?:api[_-]?key|access[_-]?token|refresh[_-]?token|secret|password|passwd|pwd)\s*[:=]\s*["']?[^"'\s,}]+`), mask: "[secret]"},
	{name: "email", severity: "medium", re: regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`), mask: "[email]"},
	{name: "private-ip", severity: "medium", re: regexp.MustCompile(`\b(?:10\.\d{1,3}\.\d{1,3}\.\d{1,3}|192\.168\.\d{1,3}\.\d{1,3}|172\.(?:1[6-9]|2\d|3[01])\.\d{1,3}\.\d{1,3})\b`), mask: "[private ip]"},
	{name: "home-path", severity: "medium", re: regexp.MustCompile(`(?:/home/[^/\s]+|/Users/[^/\s]+|[A-Za-z]:\\Users\\[^\\\s]+)(?:[/\\][^\s]*)?`), mask: "[sanitized path]"},
	{name: "internal-host", severity: "medium", re: regexp.MustCompile(`\b[A-Za-z0-9][A-Za-z0-9.-]*\.(?:local|internal|corp|intra|lan)\b`), mask: "[internal host]"},
	{name: "url-credentials", severity: "high", re: regexp.MustCompile(`https?://[^/\s:@]+:[^/\s@]+@[^\s]+`), mask: "https://[credentials]@[host]"},
}

func maskSensitiveText(text string) string {
	out := text
	for _, detector := range dogfoodSecretDetectors {
		out = detector.re.ReplaceAllString(out, detector.mask)
	}
	return out
}

func scanFilesForSecrets(baseDir string, files []string, allowPatterns []*regexp.Regexp, denyPatterns []*regexp.Regexp) secretScanReport {
	var findings []secretFinding
	for _, name := range files {
		path := filepath.Join(baseDir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		findings = append(findings, scanTextForSecrets(name, string(data), allowPatterns, denyPatterns)...)
	}
	return secretScanReport{FindingCount: len(findings), Findings: findings}
}

func scanTextForSecrets(fileName, text string, allowPatterns []*regexp.Regexp, denyPatterns []*regexp.Regexp) []secretFinding {
	lines := strings.Split(text, "\n")
	var findings []secretFinding
	for i, line := range lines {
		for _, detector := range dogfoodSecretDetectors {
			if detector.re.MatchString(line) {
				preview := detector.re.ReplaceAllString(line, detector.mask)
				finding := secretFinding{File: fileName, Line: i + 1, Detector: detector.name, Severity: detector.severity, Preview: truncateFindingPreview(preview)}
				if !allowedFinding(finding, allowPatterns) {
					findings = append(findings, finding)
				}
			}
		}
		for _, deny := range denyPatterns {
			if deny.MatchString(line) {
				finding := secretFinding{File: fileName, Line: i + 1, Detector: "custom-deny", Severity: "medium", Preview: truncateFindingPreview(maskSensitiveText(line))}
				if !allowedFinding(finding, allowPatterns) {
					findings = append(findings, finding)
				}
			}
		}
	}
	return findings
}

func allowedFinding(finding secretFinding, allowPatterns []*regexp.Regexp) bool {
	haystack := finding.File + "\n" + finding.Detector + "\n" + finding.Preview
	for _, allow := range allowPatterns {
		if allow.MatchString(haystack) {
			return true
		}
	}
	return false
}

func truncateFindingPreview(preview string) string {
	runes := []rune(preview)
	if len(runes) <= 160 {
		return preview
	}
	return string(runes[:160]) + "[cut]"
}

func loadRegexFile(path string) ([]*regexp.Regexp, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var patterns []*regexp.Regexp
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		re, err := regexp.Compile(line)
		if err != nil {
			return nil, err
		}
		patterns = append(patterns, re)
	}
	return patterns, scanner.Err()
}
