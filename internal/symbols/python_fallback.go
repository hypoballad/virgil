package symbols

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// FallbackPythonSymbol represents a symbol detected without AST help.
type FallbackPythonSymbol struct {
	Name      string
	Kind      string
	Receiver  string
	StartLine int
	EndLine   int
	Signature string
}

var (
	pythonFallbackClassRE    = regexp.MustCompile(`^class\s+([A-Za-z_][A-Za-z0-9_]*)\s*(?:[\(:]|$)`)
	pythonFallbackDefRE      = regexp.MustCompile(`^def\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
	pythonFallbackAsyncDefRE = regexp.MustCompile(`^async\s+def\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
)

// ExtractPythonFallbackSymbols scans Python top-level class/def/async def and direct class methods line by line.
func ExtractPythonFallbackSymbols(sourceCode []byte) []FallbackPythonSymbol {
	lines := strings.Split(string(sourceCode), "\n")
	lineCount := len(lines)
	if lineCount > 0 && lines[lineCount-1] == "" {
		lineCount--
	}

	type candidate struct {
		symbol FallbackPythonSymbol
		indent int
	}

	var candidates []candidate
	inTripleString := false
	var tripleQuote string
	currentClass := ""
	currentClassIndent := -1
	currentClassBodyIndent := -1
	currentClassHeaderOpen := false

	for i := 0; i < lineCount; i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			updatePythonTripleStringState(line, &inTripleString, &tripleQuote)
			continue
		}
		if inTripleString {
			updatePythonTripleStringState(line, &inTripleString, &tripleQuote)
			continue
		}
		indent := pythonFallbackIndent(line)

		if currentClassHeaderOpen && currentClass != "" && indent == currentClassIndent && pythonDefinitionHeaderClosed(trimmed) {
			currentClassHeaderOpen = false
			updatePythonTripleStringState(line, &inTripleString, &tripleQuote)
			continue
		}

		if indent == 0 {
			currentClass = ""
			currentClassIndent = -1
			currentClassBodyIndent = -1
			currentClassHeaderOpen = false
			if match := pythonFallbackClassRE.FindStringSubmatch(trimmed); len(match) == 2 {
				candidates = append(candidates, candidate{symbol: FallbackPythonSymbol{
					Name:      match[1],
					Kind:      "class",
					StartLine: i + 1,
					Signature: trimmed,
				}, indent: indent})
				currentClass = match[1]
				currentClassIndent = indent
				currentClassBodyIndent = -1
				currentClassHeaderOpen = !pythonDefinitionHeaderClosed(trimmed)
			} else if match := pythonFallbackAsyncDefRE.FindStringSubmatch(trimmed); len(match) == 2 {
				candidates = append(candidates, candidate{symbol: FallbackPythonSymbol{
					Name:      match[1],
					Kind:      "async_function",
					StartLine: i + 1,
					Signature: trimmed,
				}, indent: indent})
			} else if match := pythonFallbackDefRE.FindStringSubmatch(trimmed); len(match) == 2 {
				candidates = append(candidates, candidate{symbol: FallbackPythonSymbol{
					Name:      match[1],
					Kind:      "function",
					StartLine: i + 1,
					Signature: trimmed,
				}, indent: indent})
			}

			updatePythonTripleStringState(line, &inTripleString, &tripleQuote)
			continue
		}

		if currentClass != "" && indent > currentClassIndent {
			if currentClassHeaderOpen {
				if pythonDefinitionHeaderClosed(trimmed) {
					currentClassHeaderOpen = false
				}
				updatePythonTripleStringState(line, &inTripleString, &tripleQuote)
				continue
			}
			if currentClassBodyIndent < 0 {
				currentClassBodyIndent = indent
			}
			if indent == currentClassBodyIndent {
				if match := pythonFallbackAsyncDefRE.FindStringSubmatch(trimmed); len(match) == 2 {
					candidates = append(candidates, candidate{symbol: FallbackPythonSymbol{
						Name:      match[1],
						Kind:      "method",
						Receiver:  currentClass,
						StartLine: i + 1,
						Signature: trimmed,
					}, indent: indent})
				} else if match := pythonFallbackDefRE.FindStringSubmatch(trimmed); len(match) == 2 {
					candidates = append(candidates, candidate{symbol: FallbackPythonSymbol{
						Name:      match[1],
						Kind:      "method",
						Receiver:  currentClass,
						StartLine: i + 1,
						Signature: trimmed,
					}, indent: indent})
				}
			}
		}

		updatePythonTripleStringState(line, &inTripleString, &tripleQuote)
	}

	symbols := make([]FallbackPythonSymbol, len(candidates))
	for i := range candidates {
		symbols[i] = candidates[i].symbol
	}
	setPythonFallbackEndLines(symbols, lineCount)
	return symbols
}

func setPythonFallbackEndLines(symbols []FallbackPythonSymbol, lineCount int) {
	topLevelIndexes := make([]int, 0)
	classEndByName := make(map[string]int)
	methodIndexesByClass := make(map[string][]int)

	for i, sym := range symbols {
		if sym.Receiver == "" {
			topLevelIndexes = append(topLevelIndexes, i)
		} else if sym.Kind == "method" {
			methodIndexesByClass[sym.Receiver] = append(methodIndexesByClass[sym.Receiver], i)
		}
	}

	for i, idx := range topLevelIndexes {
		endLine := lineCount
		if i+1 < len(topLevelIndexes) {
			endLine = symbols[topLevelIndexes[i+1]].StartLine - 1
		}
		symbols[idx].EndLine = endLine
		if symbols[idx].Kind == "class" {
			classEndByName[symbols[idx].Name] = endLine
		}
	}

	for className, indexes := range methodIndexesByClass {
		classEnd := classEndByName[className]
		if classEnd == 0 {
			classEnd = lineCount
		}
		for i, idx := range indexes {
			endLine := classEnd
			if i+1 < len(indexes) {
				endLine = symbols[indexes[i+1]].StartLine - 1
			}
			symbols[idx].EndLine = endLine
		}
	}
}

func fallbackKey(name, receiver string) string {
	if receiver != "" {
		return "member:" + receiver + ":" + name
	}
	return "top:" + name
}

func symbolFallbackKey(sym Symbol) string {
	return fallbackKey(sym.Name, sym.Receiver)
}

func pythonDefinitionHeaderClosed(trimmed string) bool {
	return strings.HasSuffix(trimmed, ":")
}

func pythonFallbackIndent(line string) int {
	indent := 0
	for _, r := range line {
		switch r {
		case ' ':
			indent++
		case '\t':
			indent += 4
		default:
			return indent
		}
	}
	return indent
}

// MergeWithFallback adds fallback symbols whose names were not found by AST extraction.
func MergeWithFallback(astSymbols []Symbol, fallbackSymbols []FallbackPythonSymbol) []Symbol {
	seen := make(map[string]bool, len(astSymbols))
	seenDBKey := make(map[string]bool, len(astSymbols))
	merged := make([]Symbol, 0, len(astSymbols)+len(fallbackSymbols))
	for _, sym := range astSymbols {
		seen[symbolFallbackKey(sym)] = true
		dbKey := symbolDBUniqueKey(sym)
		if seenDBKey[dbKey] {
			continue
		}
		seenDBKey[dbKey] = true
		merged = append(merged, sym)
	}

	for _, fallback := range fallbackSymbols {
		key := fallbackKey(fallback.Name, fallback.Receiver)
		if seen[key] {
			continue
		}
		symType := SymbolFunction
		switch fallback.Kind {
		case "class":
			symType = SymbolClass
		case "method":
			symType = SymbolMethod
		}
		merged = append(merged, Symbol{
			Name:       fallback.Name,
			Type:       symType,
			Receiver:   fallback.Receiver,
			Signature:  fallback.Signature,
			StartLine:  fallback.StartLine,
			EndLine:    fallback.EndLine,
			IsFallback: true,
		})
		dbKey := symbolDBUniqueKey(merged[len(merged)-1])
		if seenDBKey[dbKey] {
			merged = merged[:len(merged)-1]
			seen[key] = true
			continue
		}
		seenDBKey[dbKey] = true
		seen[key] = true
	}
	sort.Slice(merged, func(i, j int) bool {
		if merged[i].StartLine != merged[j].StartLine {
			return merged[i].StartLine < merged[j].StartLine
		}
		if merged[i].Receiver != merged[j].Receiver {
			return merged[i].Receiver < merged[j].Receiver
		}
		return merged[i].Name < merged[j].Name
	})
	return merged
}

func symbolDBUniqueKey(sym Symbol) string {
	return sym.Name + ":" + strconv.Itoa(sym.StartLine)
}

func updatePythonTripleStringState(line string, inTripleString *bool, tripleQuote *string) {
	for i := 0; i+2 < len(line); i++ {
		token := ""
		switch {
		case strings.HasPrefix(line[i:], `"""`):
			token = `"""`
		case strings.HasPrefix(line[i:], `'''`):
			token = `'''`
		default:
			continue
		}

		if *inTripleString {
			if token == *tripleQuote {
				*inTripleString = false
				*tripleQuote = ""
			}
		} else {
			*inTripleString = true
			*tripleQuote = token
		}
		i += 2
	}
}
