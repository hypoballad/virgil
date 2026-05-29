package symbols

import (
	"strings"
)

const maxSymbolDocLength = 500

// AttachSymbolDocs fills Symbol.Doc from language comments/docstrings.
func AttachSymbolDocs(symbolList []Symbol, sourceCode []byte, language string) {
	lines := strings.Split(string(sourceCode), "\n")
	for i := range symbolList {
		doc := ""
		if language == "python" {
			doc = extractPythonDocstring(lines, symbolList[i])
		}
		if doc == "" {
			doc = extractLeadingCommentDoc(lines, symbolList[i], language)
		}
		symbolList[i].Doc = truncateSymbolDoc(doc)
	}
}

func extractLeadingCommentDoc(lines []string, sym Symbol, language string) string {
	lineIndex := sym.StartLine - 2
	if lineIndex < 0 || lineIndex >= len(lines) {
		return ""
	}

	var comments []string
	for i := lineIndex; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" {
			break
		}
		text, ok := stripDocLineComment(trimmed, language)
		if !ok {
			break
		}
		comments = append(comments, text)
	}
	if len(comments) == 0 {
		return ""
	}

	for i, j := 0, len(comments)-1; i < j; i, j = i+1, j-1 {
		comments[i], comments[j] = comments[j], comments[i]
	}
	return normalizeDoc(strings.Join(comments, "\n"))
}

func stripDocLineComment(trimmed, language string) (string, bool) {
	switch language {
	case "python":
		if strings.HasPrefix(trimmed, "#") {
			return strings.TrimSpace(strings.TrimPrefix(trimmed, "#")), true
		}
	case "go", "javascript", "typescript", "rust":
		if strings.HasPrefix(trimmed, "//") {
			return strings.TrimSpace(strings.TrimPrefix(trimmed, "//")), true
		}
	}
	return "", false
}

func extractPythonDocstring(lines []string, sym Symbol) string {
	if sym.Type != SymbolClass && sym.Type != SymbolFunction && sym.Type != SymbolMethod {
		return ""
	}
	start := sym.StartLine - 1
	if start < 0 || start >= len(lines) {
		return ""
	}
	defIndent := pythonFallbackIndent(lines[start])

	headerEnd := start
	for headerEnd < len(lines) {
		if pythonDefinitionHeaderClosed(strings.TrimSpace(lines[headerEnd])) {
			break
		}
		headerEnd++
	}
	if headerEnd >= len(lines)-1 {
		return ""
	}

	for i := headerEnd + 1; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if pythonFallbackIndent(line) <= defIndent {
			return ""
		}
		quote, content, ok := parsePythonDocstringStart(trimmed)
		if !ok {
			return ""
		}
		return collectPythonDocstring(lines, i, quote, content)
	}
	return ""
}

func parsePythonDocstringStart(trimmed string) (quote string, content string, ok bool) {
	lowered := strings.ToLower(trimmed)
	prefixes := []string{"", "r", "u", "f", "b", "fr", "rf", "br", "rb"}
	for _, prefix := range prefixes {
		if strings.HasPrefix(lowered, prefix+`"""`) {
			return `"""`, trimmed[len(prefix)+3:], true
		}
		if strings.HasPrefix(lowered, prefix+`'''`) {
			return `'''`, trimmed[len(prefix)+3:], true
		}
	}
	return "", "", false
}

func collectPythonDocstring(lines []string, startLine int, quote string, firstContent string) string {
	if end := strings.Index(firstContent, quote); end >= 0 {
		return normalizeDoc(firstContent[:end])
	}

	parts := []string{firstContent}
	for i := startLine + 1; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if end := strings.Index(line, quote); end >= 0 {
			parts = append(parts, line[:end])
			break
		}
		parts = append(parts, line)
	}
	return normalizeDoc(strings.Join(parts, "\n"))
}

func normalizeDoc(doc string) string {
	doc = strings.ReplaceAll(doc, "\r\n", "\n")
	lines := strings.Split(doc, "\n")
	for i := range lines {
		lines[i] = strings.TrimSpace(lines[i])
	}
	for len(lines) > 0 && lines[0] == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(strings.Fields(strings.Join(lines, " ")), " ")
}

func truncateSymbolDoc(doc string) string {
	doc = strings.TrimSpace(doc)
	if len([]rune(doc)) <= maxSymbolDocLength {
		return doc
	}
	runes := []rune(doc)
	return string(runes[:maxSymbolDocLength]) + "... [truncated]"
}
