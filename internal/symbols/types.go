package symbols

// SymbolType はシンボルの種類
type SymbolType string

const (
	SymbolFunction  SymbolType = "function"
	SymbolMethod    SymbolType = "method"
	SymbolClass     SymbolType = "class"
	SymbolStruct    SymbolType = "struct"
	SymbolInterface SymbolType = "interface"
	SymbolType_     SymbolType = "type" // type alias / type definition
	SymbolConst     SymbolType = "const"
	SymbolVar       SymbolType = "var"
)

// Symbol はソースコードから抽出された1つのシンボル
type Symbol struct {
	Name       string     // シンボル名
	Type       SymbolType // 種類
	Receiver   string     // メソッドのレシーバ型（メソッド以外は空）
	Signature  string     // 関数シグネチャ全体 ("func (c *Calculator) Add(x, y int) int")
	Doc        string     // docstring または直前コメントから抽出した説明
	StartLine  int        // 開始行（1-indexed）
	EndLine    int        // 終了行（inclusive）
	FilePath   string     // ファイルパス（呼び出し元が設定）
	IsFallback bool       // Tree-sitter で取れず行ベース fallback で補完したシンボル
}

// FileOutline はファイル単位のシンボル一覧
type FileOutline struct {
	FilePath string
	Language string // "go", "python", "luau", etc.
	Symbols  []Symbol
}

// CallEdge は関数呼び出し関係を表す
// "caller_file 内の caller_name が callee_name を call_line で呼んでいる"
type CallEdge struct {
	CallerFile     string // 呼び出し元のファイルパス
	CallerName     string // 呼び出し元の関数/メソッド名
	CallerReceiver string // 呼び出し元のレシーバ型（メソッドの場合のみ）
	CalleeName     string // 呼び出される関数/メソッド名
	CalleeReceiver string // 呼び出される側のレシーバ型（試行ベースで取得、不明なら空）
	CallLine       int    // 呼び出しの行番号（1-indexed）
	Language       string // 言語
}

// FileCallGraph はファイル単位の呼び出しグラフ
type FileCallGraph struct {
	FilePath string
	Language string
	Calls    []CallEdge
}

// Import は Python import 文から抽出した1つの import 対象を表す。
type Import struct {
	FilePath      string
	LineNumber    int
	Kind          string // "import" or "from_import"
	Module        string
	ImportedName  string
	Alias         string
	IsRelative    bool
	RelativeLevel int
	IsWildcard    bool
	Scope         string // "module", "function", "class", "conditional"
}

// FileImports はファイル単位の import 一覧。
type FileImports struct {
	FilePath string
	Language string
	Imports  []Import
}
