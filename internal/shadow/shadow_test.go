package shadow

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestShadowRepoBasic(t *testing.T) {
	// 一時ディレクトリでテスト
	tmpDir, err := os.MkdirTemp("", "virgil-shadow-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// テスト用のファイルを作成
	testFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("initial content"), 0644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	repo, err := New(tmpDir)
	if err != nil {
		t.Fatal(err)
	}

	// Init
	if err := repo.Init(ctx); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// 初期コミットhash取得
	initialHash, err := repo.HeadCommit(ctx)
	if err != nil {
		t.Fatalf("HeadCommit failed: %v", err)
	}
	if initialHash == "" {
		t.Fatal("expected non-empty initial commit hash")
	}

	// ファイル変更してpre/postコミット
	preHash, err := repo.CommitPre(ctx, "test_tool")
	if err != nil {
		t.Fatalf("CommitPre failed: %v", err)
	}

	// ファイル変更
	if err := os.WriteFile(testFile, []byte("modified content"), 0644); err != nil {
		t.Fatal(err)
	}

	postHash, err := repo.CommitPost(ctx, "test_tool")
	if err != nil {
		t.Fatalf("CommitPost failed: %v", err)
	}

	if preHash == postHash {
		t.Errorf("expected different hashes for pre/post, got same: %s", preHash)
	}

	// Rewind して内容が戻るか確認
	if err := repo.Rewind(ctx, preHash); err != nil {
		t.Fatalf("Rewind failed: %v", err)
	}

	content, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatal(err)
	}

	if string(content) != "initial content" {
		t.Errorf("expected 'initial content' after rewind, got %q", string(content))
	}
}

func TestShadowRepoNoChange(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "virgil-shadow-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	if err := os.WriteFile(filepath.Join(tmpDir, "test.txt"), []byte("content"), 0644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	repo, err := New(tmpDir)
	if err != nil {
		t.Fatal(err)
	}

	if err := repo.Init(ctx); err != nil {
		t.Fatal(err)
	}

	// 変更なしでpre/postコミット → 同じhashが返る
	preHash, _ := repo.CommitPre(ctx, "noop_tool")
	postHash, _ := repo.CommitPost(ctx, "noop_tool")

	if preHash != postHash {
		t.Errorf("expected same hash for noop, got pre=%s post=%s", preHash, postHash)
	}
}

func TestDiffFromCurrent(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "shadow-diff-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	ctx := context.Background()
	repo, err := New(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Init(ctx); err != nil {
		t.Fatal(err)
	}

	// 初期状態のhash取得
	initialHash, err := repo.HeadCommit(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// ファイル追加してコミット
	testFile := filepath.Join(tmpDir, "added.txt")
	if err := os.WriteFile(testFile, []byte("new content"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CommitPost(ctx, "test"); err != nil {
		t.Fatal(err)
	}

	// ファイル変更してコミット
	if err := os.WriteFile(testFile, []byte("modified content"), 0644); err != nil {
		t.Fatal(err)
	}
	secondHash, err := repo.CommitPost(ctx, "test2")
	if err != nil {
		t.Fatal(err)
	}

	// initial（ファイル追加前）に戻す場合の差分
	summary, err := repo.DiffFromCurrent(ctx, initialHash)
	if err != nil {
		t.Fatalf("DiffFromCurrent failed: %v", err)
	}

	// added.txt が削除対象として検出されること
	if len(summary.AddedFiles) != 1 {
		t.Errorf("expected 1 added file, got %d: %v", len(summary.AddedFiles), summary.AddedFiles)
	}
	if len(summary.AddedFiles) > 0 && summary.AddedFiles[0] != "added.txt" {
		t.Errorf("expected added.txt, got %s", summary.AddedFiles[0])
	}

	// 1つ前（ファイル変更前）に戻す場合の差分
	if err := os.WriteFile(testFile, []byte("modified content"), 0644); err != nil {
		t.Fatal(err)
	}
	summary2, err := repo.DiffFromCurrent(ctx, secondHash)
	if err != nil {
		t.Fatalf("DiffFromCurrent failed: %v", err)
	}

	// ワーキングツリーが secondHash と同じなら差分なし
	if len(summary2.AddedFiles) != 0 || len(summary2.DeletedFiles) != 0 || len(summary2.ModifiedFiles) != 0 {
		t.Errorf("expected no differences, got: %+v", summary2)
	}
}

func TestDiff(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "shadow-diff-unit-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	ctx := context.Background()
	repo, err := New(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Init(ctx); err != nil {
		t.Fatal(err)
	}

	testFile := filepath.Join(tmpDir, "diff.txt")

	// Pre-commit: line 1 to 20
	var initialBuilder strings.Builder
	for i := 1; i <= 20; i++ {
		initialBuilder.WriteString(fmt.Sprintf("line %d\n", i))
	}
	if err := os.WriteFile(testFile, []byte(initialBuilder.String()), 0644); err != nil {
		t.Fatal(err)
	}
	preHash, _ := repo.CommitPost(ctx, "pre")

	// Post-commit: change line 10 and 11
	var modifiedBuilder strings.Builder
	for i := 1; i <= 20; i++ {
		if i == 10 || i == 11 {
			modifiedBuilder.WriteString(fmt.Sprintf("modified line %d\n", i))
		} else {
			modifiedBuilder.WriteString(fmt.Sprintf("line %d\n", i))
		}
	}
	if err := os.WriteFile(testFile, []byte(modifiedBuilder.String()), 0644); err != nil {
		t.Fatal(err)
	}
	postHash, _ := repo.CommitPost(ctx, "post")

	// Test 1: Full diff
	diff, err := repo.Diff(ctx, preHash, postHash, 0)
	if err != nil {
		t.Fatalf("Diff failed: %v", err)
	}
	if !strings.Contains(diff, "-line 10") || !strings.Contains(diff, "+modified line 10") {
		t.Errorf("diff doesn't contain expected changes:\n%s", diff)
	}
	if !strings.Contains(diff, "@@") {
		t.Errorf("diff missing hunk header:\n%s", diff)
	}

	// Test 2: Truncation
	// This diff should be around 10-15 lines. Truncate at 5.
	truncated, _ := repo.Diff(ctx, preHash, postHash, 5)
	if !strings.Contains(truncated, "[Diff truncated.") {
		t.Errorf("expected truncation message, got:\n%s", truncated)
	}
	lines := strings.Split(truncated, "\n")
	if len(lines) > 10 { // 5 lines + message + padding + possibly original truncated lines
		t.Errorf("too many lines in truncated diff: %d", len(lines))
	}
}
