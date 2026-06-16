package tools

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

const lineHashPrefix = "h:"

func lineHash(line string) string {
	normalized := strings.TrimSuffix(line, "\r")
	sum := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(sum[:4])
}

func formatReadLine(lineNum int, line string) string {
	return fmt.Sprintf("%4d | [%s%s] %s\n", lineNum, lineHashPrefix, lineHash(line), line)
}

func normalizeExpectedLineHash(value string) string {
	hash := strings.TrimSpace(strings.ToLower(value))
	hash = strings.TrimPrefix(hash, "[")
	hash = strings.TrimSuffix(hash, "]")
	hash = strings.TrimPrefix(hash, lineHashPrefix)
	return hash
}

func validateExpectedLineHash(path string, lineNum int, line string, expected string) error {
	normalized := normalizeExpectedLineHash(expected)
	if normalized == "" {
		return nil
	}
	actual := lineHash(line)
	if normalized != actual {
		return fmt.Errorf("line hash mismatch for %s:%d: expected %s%s, got %s%s. The file may have changed since read_file; re-read a narrow range before editing", path, lineNum, lineHashPrefix, normalized, lineHashPrefix, actual)
	}
	return nil
}
