package tools

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

func RejectSerializedLineListForCode(path string, content string) error {
	if !isCodePath(path) {
		return nil
	}
	if !looksLikeSerializedCodeLineList(content) {
		return nil
	}
	return fmt.Errorf("content looks like a serialized list of code lines, not source code; refusing to write it to %s. Pass raw source lines instead of a quoted [] list", path)
}

func isCodePath(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".py", ".go", ".js", ".jsx", ".ts", ".tsx", ".rs", ".java", ".c", ".cc", ".cpp", ".h", ".hpp", ".cs", ".rb", ".php", ".sh", ".lua":
		return true
	default:
		return false
	}
}

func looksLikeSerializedCodeLineList(content string) bool {
	trimmed := strings.TrimSpace(content)
	if !strings.HasPrefix(trimmed, "[") {
		return false
	}

	var jsonLines []string
	if err := json.Unmarshal([]byte(trimmed), &jsonLines); err == nil {
		return looksLikeCodeLinePayload(jsonLines)
	}

	lines := strings.Split(trimmed, "\n")
	if len(lines) < 4 || strings.TrimSpace(lines[0]) != "[" {
		return false
	}

	items := make([]string, 0, len(lines)-2)
	for _, line := range lines[1:] {
		text := strings.TrimSpace(line)
		if text == "" || text == "]" {
			continue
		}
		text = strings.TrimSuffix(text, ",")
		if len(text) < 2 {
			continue
		}
		if (strings.HasPrefix(text, `"`) && strings.HasSuffix(text, `"`)) ||
			(strings.HasPrefix(text, `'`) && strings.HasSuffix(text, `'`)) {
			if unquoted, err := strconv.Unquote(text); err == nil {
				items = append(items, unquoted)
			} else {
				items = append(items, text[1:len(text)-1])
			}
		}
		if len(items) >= 20 {
			break
		}
	}

	return looksLikeCodeLinePayload(items)
}

func looksLikeCodeLinePayload(lines []string) bool {
	if len(lines) < 3 {
		return false
	}
	codeLike := 0
	nonEmpty := 0
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		nonEmpty++
		if isCodeLikeLine(line) {
			codeLike++
		}
	}
	return nonEmpty >= 3 && codeLike >= 2
}

func isCodeLikeLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "\t") {
		return true
	}
	prefixes := []string{
		"class ", "def ", "async def ", "import ", "from ", "return ", "if ", "elif ", "else:", "for ", "while ", "try:", "except ", "finally:", "with ", "@", "#", `"""`, "'''", "self.",
		"func ", "type ", "package ", "const ", "var ", "let ", "export ", "public ", "private ",
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(trimmed, prefix) {
			return true
		}
	}
	return strings.HasSuffix(trimmed, ":")
}
