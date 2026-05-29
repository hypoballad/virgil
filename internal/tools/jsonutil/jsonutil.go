package jsonutil

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

type OutlineEntry struct {
	Path        string
	Type        string
	Description string
}

func AnalyzeStructure(data interface{}, maxDepth int) []OutlineEntry {
	if maxDepth <= 0 {
		maxDepth = 2
	}
	var entries []OutlineEntry
	walkOutline(data, "$", 0, maxDepth, &entries)
	return entries
}

func EstimateTokens(byteSize int) int {
	if byteSize <= 0 {
		return 0
	}
	return (byteSize + 3) / 4
}

func QueryJSONPath(data interface{}, expression string) (interface{}, error) {
	tokens, err := parseJSONPath(expression)
	if err != nil {
		return nil, err
	}
	current := []interface{}{data}
	for _, tok := range tokens {
		var next []interface{}
		for _, item := range current {
			values, err := applyToken(item, tok)
			if err != nil {
				return nil, err
			}
			next = append(next, values...)
		}
		if len(next) == 0 {
			return nil, ErrPathNotFound
		}
		current = next
	}
	if len(current) == 1 {
		return current[0], nil
	}
	return current, nil
}

var ErrPathNotFound = fmt.Errorf("path not found")

func walkOutline(value interface{}, path string, depth int, maxDepth int, entries *[]OutlineEntry) {
	if depth >= maxDepth {
		return
	}
	switch v := value.(type) {
	case map[string]interface{}:
		keys := sortedKeys(v)
		for _, key := range keys {
			childPath := joinPath(path, key)
			*entries = append(*entries, OutlineEntry{
				Path:        childPath,
				Type:        jsonType(v[key]),
				Description: describeValue(v[key]),
			})
			walkOutline(v[key], childPath, depth+1, maxDepth, entries)
		}
	case []interface{}:
		if len(v) > 0 {
			childPath := path + "[*]"
			*entries = append(*entries, OutlineEntry{
				Path:        childPath,
				Type:        jsonType(v[0]),
				Description: describeValue(v[0]),
			})
			walkOutline(v[0], childPath, depth, maxDepth, entries)
		}
	default:
		if path == "$" {
			*entries = append(*entries, OutlineEntry{
				Path:        path,
				Type:        jsonType(value),
				Description: describeValue(value),
			})
		}
	}
}

func sortedKeys(obj map[string]interface{}) []string {
	keys := make([]string, 0, len(obj))
	for key := range obj {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func joinPath(base, key string) string {
	if isSimpleJSONPathKey(key) {
		return base + "." + key
	}
	escaped, _ := json.Marshal(key)
	return base + "[" + string(escaped) + "]"
}

func isSimpleJSONPathKey(key string) bool {
	if key == "" {
		return false
	}
	for i, r := range key {
		if i == 0 {
			if r != '_' && (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') {
				return false
			}
			continue
		}
		if r != '_' && (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

func jsonType(value interface{}) string {
	switch value.(type) {
	case map[string]interface{}:
		return "object"
	case []interface{}:
		return "array"
	case string:
		return "string"
	case float64, json.Number:
		return "number"
	case bool:
		return "boolean"
	case nil:
		return "null"
	default:
		return "unknown"
	}
}

func describeValue(value interface{}) string {
	switch v := value.(type) {
	case map[string]interface{}:
		return describeObject(sortedKeys(v))
	case []interface{}:
		return describeArray(v)
	case string:
		return "string"
	case float64, json.Number:
		return "number"
	case bool:
		return "boolean"
	case nil:
		return "null"
	default:
		return "unknown"
	}
}

func describeObject(keys []string) string {
	if len(keys) == 0 {
		return "object (0 keys)"
	}
	visible := keys
	suffix := ""
	if len(visible) > 8 {
		visible = visible[:8]
		suffix = ", ..."
	}
	return fmt.Sprintf("object (%d keys: %s%s)", len(keys), strings.Join(visible, ", "), suffix)
}

func describeArray(values []interface{}) string {
	if len(values) == 0 {
		return "array[0 items]"
	}
	return fmt.Sprintf("array[%d items] of %s", len(values), jsonType(values[0]))
}

type pathToken struct {
	kind  string
	key   string
	index int
	start int
	end   int
}

func parseJSONPath(expr string) ([]pathToken, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return nil, fmt.Errorf("invalid JSONPath: empty expression")
	}
	if expr[0] != '$' {
		return nil, fmt.Errorf("invalid JSONPath: expression must start with $")
	}
	var tokens []pathToken
	for i := 1; i < len(expr); {
		switch expr[i] {
		case '.':
			i++
			if i >= len(expr) {
				return nil, fmt.Errorf("invalid JSONPath: trailing dot")
			}
			start := i
			for i < len(expr) && expr[i] != '.' && expr[i] != '[' {
				i++
			}
			if start == i {
				return nil, fmt.Errorf("invalid JSONPath: empty key")
			}
			tokens = append(tokens, pathToken{kind: "key", key: expr[start:i]})
		case '[':
			end := strings.IndexByte(expr[i:], ']')
			if end < 0 {
				return nil, fmt.Errorf("invalid JSONPath: missing ]")
			}
			content := strings.TrimSpace(expr[i+1 : i+end])
			tok, err := parseBracketToken(content)
			if err != nil {
				return nil, err
			}
			tokens = append(tokens, tok)
			i += end + 1
		default:
			return nil, fmt.Errorf("invalid JSONPath: unexpected character %q", expr[i])
		}
	}
	return tokens, nil
}

func parseBracketToken(content string) (pathToken, error) {
	if content == "*" {
		return pathToken{kind: "wildcard"}, nil
	}
	if strings.Contains(content, ":") {
		parts := strings.Split(content, ":")
		if len(parts) != 2 {
			return pathToken{}, fmt.Errorf("invalid JSONPath slice: [%s]", content)
		}
		start, err := parseOptionalIndex(parts[0], 0)
		if err != nil {
			return pathToken{}, err
		}
		end, err := parseOptionalIndex(parts[1], -1)
		if err != nil {
			return pathToken{}, err
		}
		return pathToken{kind: "slice", start: start, end: end}, nil
	}
	if strings.HasPrefix(content, "'") || strings.HasPrefix(content, `"`) {
		var key string
		if err := json.Unmarshal([]byte(content), &key); err != nil {
			return pathToken{}, fmt.Errorf("invalid JSONPath quoted key: %v", err)
		}
		return pathToken{kind: "key", key: key}, nil
	}
	index, err := strconv.Atoi(content)
	if err != nil {
		return pathToken{}, fmt.Errorf("invalid JSONPath bracket token: [%s]", content)
	}
	return pathToken{kind: "index", index: index}, nil
}

func parseOptionalIndex(value string, defaultValue int) (int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return defaultValue, nil
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("invalid JSONPath slice index %q", value)
	}
	return n, nil
}

func applyToken(value interface{}, tok pathToken) ([]interface{}, error) {
	switch tok.kind {
	case "key":
		obj, ok := value.(map[string]interface{})
		if !ok {
			return nil, nil
		}
		child, ok := obj[tok.key]
		if !ok {
			return nil, nil
		}
		return []interface{}{child}, nil
	case "index":
		arr, ok := value.([]interface{})
		if !ok {
			return nil, nil
		}
		idx := tok.index
		if idx < 0 {
			idx = len(arr) + idx
		}
		if idx < 0 || idx >= len(arr) {
			return nil, nil
		}
		return []interface{}{arr[idx]}, nil
	case "wildcard":
		arr, ok := value.([]interface{})
		if !ok {
			return nil, nil
		}
		return arr, nil
	case "slice":
		arr, ok := value.([]interface{})
		if !ok {
			return nil, nil
		}
		start := tok.start
		end := tok.end
		if start < 0 {
			start = len(arr) + start
		}
		if end < 0 {
			end = len(arr)
		}
		if end < 0 {
			end = len(arr) + end
		}
		if start < 0 {
			start = 0
		}
		if end > len(arr) {
			end = len(arr)
		}
		if start > end {
			return []interface{}{}, nil
		}
		return arr[start:end], nil
	default:
		return nil, fmt.Errorf("invalid JSONPath token kind: %s", tok.kind)
	}
}
