package repository

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/hypoballad/virgil/internal/db"
	"github.com/hypoballad/virgil/internal/symbols"
)

func TestImportRepositoryRoundTrip(t *testing.T) {
	repo := newTestImportRepository(t)
	filePath := "/workspace/pkg/module.py"

	err := repo.UpsertFile(filePath, &symbols.FileImports{
		FilePath: filePath,
		Language: "python",
		Imports: []symbols.Import{
			{LineNumber: 1, Kind: "import", Module: "os", Scope: "module"},
			{LineNumber: 2, Kind: "import", Module: "numpy", Alias: "np", Scope: "module"},
			{LineNumber: 3, Kind: "from_import", Module: ".utils", ImportedName: "foo", IsRelative: true, RelativeLevel: 1, Scope: "module"},
		},
	})
	if err != nil {
		t.Fatalf("UpsertFile() error = %v", err)
	}

	records, err := repo.ListByFilePath(filePath)
	if err != nil {
		t.Fatalf("ListByFilePath() error = %v", err)
	}
	if got, want := len(records), 3; got != want {
		t.Fatalf("len(records) = %d, want %d", got, want)
	}
	if records[1].Module != "numpy" || records[1].Alias != "np" {
		t.Fatalf("alias record = %+v", records[1])
	}
	if !records[2].IsRelative || records[2].RelativeLevel != 1 || records[2].ImportedName != "foo" {
		t.Fatalf("relative record = %+v", records[2])
	}

	if err := repo.DeleteByFilePath(filePath); err != nil {
		t.Fatalf("DeleteByFilePath() error = %v", err)
	}
	records, err = repo.ListByFilePath(filePath)
	if err != nil {
		t.Fatalf("ListByFilePath() after delete error = %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("records after delete = %+v", records)
	}
}

func TestImportRepositoryUpsertMany(t *testing.T) {
	repo := newTestImportRepository(t)
	filePath := "/workspace/many.py"

	imports := make([]symbols.Import, 0, 125)
	for i := 0; i < 125; i++ {
		imports = append(imports, symbols.Import{
			LineNumber: i + 1,
			Kind:       "import",
			Module:     fmt.Sprintf("module%d", i),
			Scope:      "module",
		})
	}

	if err := repo.UpsertFile(filePath, &symbols.FileImports{FilePath: filePath, Language: "python", Imports: imports}); err != nil {
		t.Fatalf("UpsertFile() error = %v", err)
	}
	records, err := repo.ListByFilePath(filePath)
	if err != nil {
		t.Fatalf("ListByFilePath() error = %v", err)
	}
	if got, want := len(records), 125; got != want {
		t.Fatalf("len(records) = %d, want %d", got, want)
	}
}

func TestImportRepositoryFindDependents(t *testing.T) {
	repo := newTestImportRepository(t)

	files := map[string][]symbols.Import{
		"/workspace/a.py": {
			{LineNumber: 2, Kind: "import", Module: "numpy", Alias: "np", Scope: "module"},
			{LineNumber: 5, Kind: "from_import", Module: "typing", ImportedName: "List", Scope: "module"},
		},
		"/workspace/b.py": {
			{LineNumber: 1, Kind: "import", Module: "numpy.linalg", Scope: "module"},
		},
		"/workspace/c.py": {
			{LineNumber: 3, Kind: "from_import", Module: "numpy", ImportedName: "ndarray", Scope: "module"},
		},
		"/workspace/d.py": {
			{LineNumber: 4, Kind: "from_import", Module: ".numpy", ImportedName: "local_numpy", IsRelative: true, RelativeLevel: 1, Scope: "module"},
		},
	}
	for filePath, imports := range files {
		if err := repo.UpsertFile(filePath, &symbols.FileImports{FilePath: filePath, Language: "python", Imports: imports}); err != nil {
			t.Fatalf("UpsertFile(%s) error = %v", filePath, err)
		}
	}

	entries, err := repo.FindDependents("numpy", false, 0)
	if err != nil {
		t.Fatalf("FindDependents() error = %v", err)
	}
	if got, want := len(entries), 3; got != want {
		t.Fatalf("len(entries) = %d, want %d: %+v", got, want, entries)
	}
	if entries[0].FilePath != "/workspace/a.py" || entries[1].FilePath != "/workspace/b.py" || entries[2].ImportedName != "ndarray" {
		t.Fatalf("unexpected sorted entries: %+v", entries)
	}

	entries, err = repo.FindDependents(".numpy", true, 0)
	if err != nil {
		t.Fatalf("FindDependents(relative) error = %v", err)
	}
	if got, want := len(entries), 1; got != want {
		t.Fatalf("len(relative entries) = %d, want %d: %+v", got, want, entries)
	}
	if !entries[0].IsRelative {
		t.Fatalf("relative entry missing flag: %+v", entries[0])
	}

	entries, err = repo.FindDependents("numpy", false, 2)
	if err != nil {
		t.Fatalf("FindDependents(limit) error = %v", err)
	}
	if got, want := len(entries), 2; got != want {
		t.Fatalf("len(limited entries) = %d, want %d", got, want)
	}

	entries, err = repo.FindDependents("pandas", false, 0)
	if err != nil {
		t.Fatalf("FindDependents(no match) error = %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("no-match entries = %+v", entries)
	}
}

func TestImportRepositoryFindDependentsWithOptions(t *testing.T) {
	repo := newTestImportRepository(t)

	files := map[string][]symbols.Import{
		"/workspace/pkg/a.py": {
			{LineNumber: 2, Kind: "import", Module: "numpy", Alias: "np", Scope: "module"},
			{LineNumber: 3, Kind: "import", Module: "numpy.linalg", Alias: "linalg", Scope: "function"},
		},
		"/workspace/pkg/b.py": {
			{LineNumber: 4, Kind: "from_import", Module: "numpy", ImportedName: "ndarray", Scope: "module"},
			{LineNumber: 5, Kind: "from_import", Module: "typing", IsWildcard: true, Scope: "conditional"},
		},
	}
	for filePath, imports := range files {
		if err := repo.UpsertFile(filePath, &symbols.FileImports{FilePath: filePath, Language: "python", Imports: imports}); err != nil {
			t.Fatalf("UpsertFile(%s) error = %v", filePath, err)
		}
	}

	entries, err := repo.FindDependentsWithOptions(DependentSearchOptions{
		Module:       "numpy",
		ExactModule:  true,
		ImportKind:   "from_import",
		ImportedName: "ndarray",
		FilePath:     "pkg/b.py",
		MaxResults:   10,
	})
	if err != nil {
		t.Fatalf("FindDependentsWithOptions() error = %v", err)
	}
	if got, want := len(entries), 1; got != want {
		t.Fatalf("len(entries) = %d, want %d: %+v", got, want, entries)
	}
	if entries[0].ImportedName != "ndarray" || entries[0].FilePath != "/workspace/pkg/b.py" {
		t.Fatalf("unexpected filtered entry: %+v", entries[0])
	}

	entries, err = repo.FindDependentsWithOptions(DependentSearchOptions{
		Module:     "numpy",
		Alias:      "np",
		MaxResults: 10,
	})
	if err != nil {
		t.Fatalf("FindDependentsWithOptions(alias) error = %v", err)
	}
	if got, want := len(entries), 1; got != want {
		t.Fatalf("len(alias entries) = %d, want %d: %+v", got, want, entries)
	}
	if entries[0].Alias != "np" {
		t.Fatalf("alias entry = %+v", entries[0])
	}

	entries, err = repo.FindDependentsWithOptions(DependentSearchOptions{
		Module:       "typing",
		Scope:        "conditional",
		WildcardOnly: true,
		MaxResults:   10,
	})
	if err != nil {
		t.Fatalf("FindDependentsWithOptions(wildcard) error = %v", err)
	}
	if got, want := len(entries), 1; got != want {
		t.Fatalf("len(wildcard entries) = %d, want %d: %+v", got, want, entries)
	}
	if !entries[0].IsWildcard || entries[0].Scope != "conditional" {
		t.Fatalf("wildcard entry = %+v", entries[0])
	}
}

func newTestImportRepository(t *testing.T) *ImportRepository {
	t.Helper()
	database, err := db.New(filepath.Join(t.TempDir(), "virgil.db"))
	if err != nil {
		t.Fatalf("db.New() error = %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Fatalf("database.Close() error = %v", err)
		}
	})
	return NewImportRepository(database)
}
