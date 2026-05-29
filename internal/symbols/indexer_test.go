package symbols

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

type fakeSymbolStore struct {
	mtimes      map[string]int64
	upsertPaths []string
	version     string
}

func (s *fakeSymbolStore) UpsertFile(filePath string, outline *FileOutline, fileMtime int64) error {
	if s.mtimes == nil {
		s.mtimes = make(map[string]int64)
	}
	s.upsertPaths = append(s.upsertPaths, filePath)
	s.mtimes[filePath] = fileMtime
	return nil
}

func (s *fakeSymbolStore) DeleteByFilePath(filePath string) error {
	return nil
}

func (s *fakeSymbolStore) GetFileMtime(filePath string) (int64, error) {
	if s.mtimes == nil {
		return 0, nil
	}
	return s.mtimes[filePath], nil
}

func (s *fakeSymbolStore) GetIndexVersion() (string, error) {
	return s.version, nil
}

func (s *fakeSymbolStore) SetIndexVersion(version string) error {
	s.version = version
	return nil
}

type fakeCallStore struct {
	upsertCount int
	deleteCount int
}

func (s *fakeCallStore) UpsertFile(filePath string, graph *FileCallGraph) error {
	s.upsertCount++
	return nil
}

func (s *fakeCallStore) DeleteByFilePath(filePath string) error {
	s.deleteCount++
	return nil
}

type fakeImportStore struct {
	upserts []*FileImports
	deletes []string
}

func (s *fakeImportStore) UpsertFile(filePath string, imports *FileImports) error {
	s.upserts = append(s.upserts, imports)
	return nil
}

func (s *fakeImportStore) DeleteByFilePath(filePath string) error {
	s.deletes = append(s.deletes, filePath)
	return nil
}

func TestIndexFileWithForceIgnoresMtime(t *testing.T) {
	path := filepath.Join("testdata", "sample_calls.go")
	store := &fakeSymbolStore{}
	callStore := &fakeCallStore{}
	idx := NewIndexer(".", store, callStore)

	if err := idx.indexFileWithForce(path, true); err != nil {
		t.Fatalf("initial forced index failed: %v", err)
	}
	if len(store.upsertPaths) != 1 {
		t.Fatalf("symbol upserts after first index = %d, want 1", len(store.upsertPaths))
	}
	if callStore.upsertCount != 1 {
		t.Fatalf("call upserts after first index = %d, want 1", callStore.upsertCount)
	}

	if err := idx.indexFileWithForce(path, false); err != nil {
		t.Fatalf("mtime-based index failed: %v", err)
	}
	if len(store.upsertPaths) != 1 {
		t.Fatalf("symbol upserts after unchanged index = %d, want 1", len(store.upsertPaths))
	}
	if callStore.upsertCount != 1 {
		t.Fatalf("call upserts after unchanged index = %d, want 1", callStore.upsertCount)
	}

	if err := idx.indexFileWithForce(path, true); err != nil {
		t.Fatalf("second forced index failed: %v", err)
	}
	if len(store.upsertPaths) != 2 {
		t.Fatalf("symbol upserts after second forced index = %d, want 2", len(store.upsertPaths))
	}
	if callStore.upsertCount != 2 {
		t.Fatalf("call upserts after second forced index = %d, want 2", callStore.upsertCount)
	}
}

func TestIndexFileStoresNanosecondMtime(t *testing.T) {
	path := filepath.Join("testdata", "sample_calls.go")
	store := &fakeSymbolStore{}
	idx := NewIndexer(".", store)

	if err := idx.IndexFileForce(path); err != nil {
		t.Fatalf("forced index failed: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat failed: %v", err)
	}
	if got, want := store.mtimes[path], info.ModTime().UnixNano(); got != want {
		t.Fatalf("stored mtime = %d, want UnixNano %d", got, want)
	}
}

func TestFullScanForcesWhenIndexVersionChanges(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "app.go")
	if err := os.WriteFile(path, []byte("package main\n\nfunc First() {}\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	store := &fakeSymbolStore{version: "old-version"}
	idx := NewIndexer(root, store)

	if err := idx.indexFileWithForce(path, true); err != nil {
		t.Fatalf("initial index failed: %v", err)
	}
	if len(store.upsertPaths) != 1 {
		t.Fatalf("initial upserts = %d, want 1", len(store.upsertPaths))
	}

	if err := idx.fullScan(context.Background(), false); err != nil {
		t.Fatalf("full scan failed: %v", err)
	}
	if len(store.upsertPaths) != 2 {
		t.Fatalf("upserts after version-mismatched full scan = %d, want 2", len(store.upsertPaths))
	}
	if store.version != CurrentIndexVersion {
		t.Fatalf("index version = %q, want %q", store.version, CurrentIndexVersion)
	}
}

func TestFullScanSkipsPythonVirtualenvAndCacheDirs(t *testing.T) {
	root := t.TempDir()
	store := &fakeSymbolStore{}
	idx := NewIndexer(root, store)

	files := map[string]string{
		"app.py": "def app():\n    return 1\n",
		filepath.Join("venv", "lib", "ignored.py"):   "def ignored_venv():\n    return 1\n",
		filepath.Join(".venv", "lib", "ignored.py"):  "def ignored_dot_venv():\n    return 1\n",
		filepath.Join("env", "lib", "ignored.py"):    "def ignored_env():\n    return 1\n",
		filepath.Join("__pycache__", "ignored.py"):   "def ignored_pycache():\n    return 1\n",
		filepath.Join(".pytest_cache", "ignored.py"): "def ignored_pytest():\n    return 1\n",
		filepath.Join(".mypy_cache", "ignored.py"):   "def ignored_mypy():\n    return 1\n",
		filepath.Join(".ruff_cache", "ignored.py"):   "def ignored_ruff():\n    return 1\n",
		filepath.Join("pkg.egg-info", "ignored.py"):  "def ignored_egg_info():\n    return 1\n",
		filepath.Join("node_modules", "ignored.py"):  "def ignored_node_modules():\n    return 1\n",
		filepath.Join(".git", "hooks", "ignored.py"): "def ignored_git():\n    return 1\n",
	}
	for rel, content := range files {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	if err := idx.fullScan(context.Background(), true); err != nil {
		t.Fatalf("full scan failed: %v", err)
	}

	if len(store.upsertPaths) != 1 {
		t.Fatalf("indexed paths = %v, want only app.py", store.upsertPaths)
	}
	if got, want := filepath.Base(store.upsertPaths[0]), "app.py"; got != want {
		t.Fatalf("indexed file = %s, want %s", got, want)
	}
}

func TestIndexFileStoresPythonImports(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "app.py")
	if err := os.WriteFile(path, []byte("import os\nfrom .helper import foo\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	store := &fakeSymbolStore{}
	importStore := &fakeImportStore{}
	idx := NewIndexer(root, store)
	idx.SetImportStore(importStore)

	if err := idx.IndexFileForce(path); err != nil {
		t.Fatalf("IndexFileForce() error = %v", err)
	}

	if got, want := len(importStore.upserts), 1; got != want {
		t.Fatalf("import upserts = %d, want %d", got, want)
	}
	imports := importStore.upserts[0].Imports
	if got, want := len(imports), 2; got != want {
		t.Fatalf("len(imports) = %d, want %d", got, want)
	}
	if imports[0].Module != "os" || imports[1].Module != ".helper" || imports[1].ImportedName != "foo" {
		t.Fatalf("imports = %+v", imports)
	}

	if err := idx.RemoveFile(path); err != nil {
		t.Fatalf("RemoveFile() error = %v", err)
	}
	if got, want := len(importStore.deletes), 1; got != want {
		t.Fatalf("import deletes = %d, want %d", got, want)
	}
}
