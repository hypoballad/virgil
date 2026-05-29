package symbols

import (
	"context"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// SymbolStore はシンボルの永続化先（DBインターフェース）
type SymbolStore interface {
	UpsertFile(filePath string, outline *FileOutline, fileMtime int64) error
	DeleteByFilePath(filePath string) error
	GetFileMtime(filePath string) (int64, error)
	GetIndexVersion() (string, error)
	SetIndexVersion(version string) error
}

const CurrentIndexVersion = "symbols-v3-nanotime-python-imports-fallback-methods-docs-20260518"

// CallStore は呼び出しグラフの永続化先。
type CallStore interface {
	UpsertFile(filePath string, graph *FileCallGraph) error
	DeleteByFilePath(filePath string) error
}

// ImportStore は import 一覧の永続化先。
type ImportStore interface {
	UpsertFile(filePath string, imports *FileImports) error
	DeleteByFilePath(filePath string) error
}

// Indexer はワークスペース全体をスキャンしてシンボルを抽出・保存する
type Indexer struct {
	workspaceRoot string
	store         SymbolStore
	callStore     CallStore
	importStore   ImportStore
	extractor     *Extractor

	mu             sync.Mutex
	indexingActive bool
	lastIndexedAt  time.Time
	totalFiles     int
	indexedFiles   int
}

func NewIndexer(workspaceRoot string, store SymbolStore, callStore ...CallStore) *Indexer {
	var calls CallStore
	if len(callStore) > 0 {
		calls = callStore[0]
	}
	return &Indexer{
		workspaceRoot: workspaceRoot,
		store:         store,
		callStore:     calls,
		extractor:     NewExtractor(),
	}
}

func (idx *Indexer) SetImportStore(store ImportStore) {
	idx.importStore = store
}

// Status は現在のインデックス状態を返す
type IndexStatus struct {
	Active        bool
	LastIndexedAt time.Time
	TotalFiles    int
	IndexedFiles  int
}

func (idx *Indexer) Status() IndexStatus {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	return IndexStatus{
		Active:        idx.indexingActive,
		LastIndexedAt: idx.lastIndexedAt,
		TotalFiles:    idx.totalFiles,
		IndexedFiles:  idx.indexedFiles,
	}
}

// StartFullScan はワークスペース全体のフルスキャンを別 goroutine で実行する
func (idx *Indexer) StartFullScan(ctx context.Context) {
	idx.StartFullScanWithForce(ctx, false)
}

// StartFullScanWithForce は強制再インデックスのオプション付きでフルスキャンを開始する。
func (idx *Indexer) StartFullScanWithForce(ctx context.Context, force bool) {
	idx.mu.Lock()
	if idx.indexingActive {
		idx.mu.Unlock()
		return
	}
	idx.indexingActive = true
	idx.indexedFiles = 0
	idx.totalFiles = 0
	idx.mu.Unlock()

	go func() {
		defer func() {
			idx.mu.Lock()
			idx.indexingActive = false
			idx.lastIndexedAt = time.Now()
			idx.mu.Unlock()
		}()

		if err := idx.fullScan(ctx, force); err != nil {
			log.Printf("indexer: full scan error: %v", err)
		}
	}()
}

func (idx *Indexer) fullScan(ctx context.Context, force bool) error {
	if !force {
		version, err := idx.store.GetIndexVersion()
		if err != nil {
			return fmt.Errorf("get index version: %w", err)
		}
		if version != CurrentIndexVersion {
			log.Printf("indexer: index version mismatch (%q != %q), forcing rescan", version, CurrentIndexVersion)
			force = true
		}
	}

	// 1. 対象ファイルを収集
	var files []string
	err := filepath.WalkDir(idx.workspaceRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil // エラーは無視して続行
		}

		// ディレクトリのスキップ判定
		if d.IsDir() {
			name := d.Name()
			// 隠しディレクトリ、依存関係、ビルド成果物をスキップ
			if strings.HasPrefix(name, ".") ||
				name == "node_modules" ||
				name == "vendor" ||
				name == "dist" ||
				name == "build" ||
				name == "venv" ||
				name == "env" ||
				strings.HasSuffix(name, ".egg-info") ||
				name == "__pycache__" {
				return filepath.SkipDir
			}
			return nil
		}

		if IsSupportedFile(path) {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("walk: %w", err)
	}

	idx.mu.Lock()
	idx.totalFiles = len(files)
	idx.mu.Unlock()

	if force {
		log.Printf("indexer: force rescan, ignoring mtime cache")
	}
	log.Printf("indexer: found %d supported source files to scan", len(files))

	// 2. 各ファイルを処理
	for _, path := range files {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if err := idx.indexFileWithForce(path, force); err != nil {
			log.Printf("indexer: failed to index %s: %v", path, err)
		}

		idx.mu.Lock()
		idx.indexedFiles++
		idx.mu.Unlock()
	}

	log.Printf("indexer: scan complete: %d files indexed", idx.indexedFiles)
	if err := idx.store.SetIndexVersion(CurrentIndexVersion); err != nil {
		return fmt.Errorf("set index version: %w", err)
	}
	return nil
}

// indexFile は1ファイルをインデックスする
func (idx *Indexer) indexFile(path string) error {
	return idx.indexFileWithForce(path, false)
}

func (idx *Indexer) indexFileWithForce(path string, force bool) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat: %w", err)
	}
	mtime := info.ModTime().UnixNano()

	if !force {
		// 差分更新: 既存の mtime と比較
		existingMtime, err := idx.store.GetFileMtime(path)
		if err != nil {
			return fmt.Errorf("get mtime: %w", err)
		}
		if existingMtime != 0 && existingMtime == mtime {
			return nil
		}
	}

	// シンボル抽出
	outline, err := idx.extractor.ExtractFromFile(path)
	if err != nil {
		return fmt.Errorf("extract: %w", err)
	}

	if err := idx.store.UpsertFile(path, outline, mtime); err != nil {
		return err
	}

	if idx.callStore != nil {
		graph, err := idx.extractor.ExtractCallsFromFile(path)
		if err == nil {
			if err := idx.callStore.UpsertFile(path, graph); err != nil {
				log.Printf("indexer: upsert calls failed for %s: %v", path, err)
			}
		}
	}

	if idx.importStore != nil && strings.ToLower(filepath.Ext(path)) == ".py" {
		imports, err := idx.extractor.ExtractImportsFromFile(path)
		if err == nil {
			if err := idx.importStore.UpsertFile(path, imports); err != nil {
				log.Printf("indexer: upsert imports failed for %s: %v", path, err)
			}
		}
	}

	return nil
}

// IndexFile は単一ファイルを即座にインデックスする
func (idx *Indexer) IndexFile(path string) error {
	return idx.IndexFileWithForce(path, false)
}

// IndexFileWithForce は単一ファイルを即座にインデックスする。
// force=true の場合は mtime キャッシュを無視する。
func (idx *Indexer) IndexFileWithForce(path string, force bool) error {
	ext := filepath.Ext(path)
	if _, ok := langConfigs[strings.ToLower(ext)]; !ok {
		return nil
	}
	return idx.indexFileWithForce(path, force)
}

// IndexFileForce は単一ファイルを mtime キャッシュを無視してインデックスする。
func (idx *Indexer) IndexFileForce(path string) error {
	return idx.IndexFileWithForce(path, true)
}

// RemoveFile はファイルがワークスペースから削除された時に呼ぶ
func (idx *Indexer) RemoveFile(path string) error {
	if err := idx.store.DeleteByFilePath(path); err != nil {
		return err
	}
	if idx.callStore != nil {
		if err := idx.callStore.DeleteByFilePath(path); err != nil {
			return err
		}
	}
	if idx.importStore != nil {
		return idx.importStore.DeleteByFilePath(path)
	}
	return nil
}
