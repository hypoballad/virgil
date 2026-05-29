package symbols

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

// ExtractImportsFromFile は Python ファイルから import 文を抽出する。
func (e *Extractor) ExtractImportsFromFile(path string) (*FileImports, error) {
	if strings.ToLower(filepath.Ext(path)) != ".py" {
		return nil, fmt.Errorf("unsupported file type for imports: %s", filepath.Ext(path))
	}

	src, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}

	imports, err := e.ExtractImportsFromSource(src, "python")
	if err != nil {
		return nil, err
	}
	for i := range imports {
		imports[i].FilePath = path
	}

	return &FileImports{
		FilePath: path,
		Language: "python",
		Imports:  imports,
	}, nil
}

// ExtractImportsFromSource は Python source から import 文を抽出する。
func (e *Extractor) ExtractImportsFromSource(src []byte, language string) ([]Import, error) {
	if language != "python" && language != "py" {
		return nil, fmt.Errorf("unsupported language for imports: %s", language)
	}

	lang := grammars.PythonLanguage()
	parser := gotreesitter.NewParser(lang)
	tree, err := parser.Parse(src)
	if err != nil {
		return nil, fmt.Errorf("parse python: %w", err)
	}
	if tree == nil || tree.RootNode() == nil {
		return nil, fmt.Errorf("parse python: empty tree")
	}

	var imports []Import
	gotreesitter.Walk(tree.RootNode(), func(node *gotreesitter.Node, depth int) gotreesitter.WalkAction {
		nodeType := node.Type(lang)
		if nodeType != "import_statement" && nodeType != "import_from_statement" {
			return gotreesitter.WalkContinue
		}

		stmt := node.Text(src)
		line := int(node.StartPoint().Row) + 1
		scope := pythonImportScope(node, lang)

		parsed := parsePythonImportStatement(stmt, line, scope)
		imports = append(imports, parsed...)
		return gotreesitter.WalkSkipChildren
	})

	return imports, nil
}

func pythonImportScope(node *gotreesitter.Node, lang *gotreesitter.Language) string {
	scope := "module"
	for p := node.Parent(); p != nil; p = p.Parent() {
		switch p.Type(lang) {
		case "function_definition":
			return "function"
		case "class_definition":
			return "class"
		case "try_statement", "if_statement", "match_statement", "with_statement":
			scope = "conditional"
		}
	}
	return scope
}

func parsePythonImportStatement(stmt string, line int, scope string) []Import {
	stmt = stripPythonImportComment(stmt)
	stmt = normalizePythonImportStatement(stmt)

	if strings.HasPrefix(stmt, "import ") {
		return parsePythonSimpleImport(strings.TrimSpace(strings.TrimPrefix(stmt, "import ")), line, scope)
	}
	if strings.HasPrefix(stmt, "from ") {
		return parsePythonFromImport(strings.TrimSpace(strings.TrimPrefix(stmt, "from ")), line, scope)
	}
	return nil
}

func stripPythonImportComment(stmt string) string {
	lines := strings.Split(stmt, "\n")
	for i, line := range lines {
		if idx := strings.Index(line, "#"); idx >= 0 {
			lines[i] = line[:idx]
		}
	}
	return strings.Join(lines, "\n")
}

func normalizePythonImportStatement(stmt string) string {
	replacer := strings.NewReplacer("(", " ", ")", " ", "\\\n", " ", "\n", " ", "\t", " ")
	stmt = replacer.Replace(stmt)
	for strings.Contains(stmt, "  ") {
		stmt = strings.ReplaceAll(stmt, "  ", " ")
	}
	return strings.TrimSpace(stmt)
}

func parsePythonSimpleImport(rest string, line int, scope string) []Import {
	var imports []Import
	for _, item := range splitPythonImportList(rest) {
		name, alias := splitPythonAlias(item)
		if name == "" {
			continue
		}
		imports = append(imports, Import{
			LineNumber: line,
			Kind:       "import",
			Module:     name,
			Alias:      alias,
			Scope:      scope,
		})
	}
	return imports
}

func parsePythonFromImport(rest string, line int, scope string) []Import {
	modulePart, namesPart, ok := strings.Cut(rest, " import ")
	if !ok {
		return nil
	}
	modulePart = strings.TrimSpace(modulePart)
	namesPart = strings.TrimSpace(namesPart)
	if modulePart == "" || namesPart == "" {
		return nil
	}

	relativeLevel := countLeadingDots(modulePart)
	isRelative := relativeLevel > 0

	if namesPart == "*" {
		return []Import{{
			LineNumber:    line,
			Kind:          "from_import",
			Module:        modulePart,
			ImportedName:  "*",
			IsRelative:    isRelative,
			RelativeLevel: relativeLevel,
			IsWildcard:    true,
			Scope:         scope,
		}}
	}

	var imports []Import
	for _, item := range splitPythonImportList(namesPart) {
		name, alias := splitPythonAlias(item)
		if name == "" {
			continue
		}
		imports = append(imports, Import{
			LineNumber:    line,
			Kind:          "from_import",
			Module:        modulePart,
			ImportedName:  name,
			Alias:         alias,
			IsRelative:    isRelative,
			RelativeLevel: relativeLevel,
			Scope:         scope,
		})
	}
	return imports
}

func splitPythonImportList(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func splitPythonAlias(s string) (name string, alias string) {
	fields := strings.Fields(strings.TrimSpace(s))
	if len(fields) >= 3 && fields[len(fields)-2] == "as" {
		return strings.Join(fields[:len(fields)-2], ""), fields[len(fields)-1]
	}
	return strings.Join(fields, ""), ""
}

func countLeadingDots(s string) int {
	count := 0
	for _, r := range s {
		if r != '.' {
			break
		}
		count++
	}
	return count
}
