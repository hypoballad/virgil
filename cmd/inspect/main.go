package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/hypoballad/virgil/internal/db"
	"github.com/hypoballad/virgil/internal/repository"
)

func main() {
	dbPath := flag.String("db", "", "Path to Virgil SQLite database")
	port := flag.Int("port", 8080, "HTTP server port")
	exportDogfood := flag.Bool("export-dogfood", false, "Export sanitized dogfooding report and exit")
	exportSession := flag.String("session", "latest", "Session id to export, or latest")
	exportExchange := flag.String("exchange", "latest", "Exchange id to export, or latest")
	exportOut := flag.String("out", "work/dogfood", "Dogfood export output directory")
	exportLog := flag.String("log", "", "Debug log path for dogfood export")
	exportLogLines := flag.Int("log-lines", 300, "Debug log tail lines to include in dogfood export")
	allowPatterns := flag.String("allow-patterns", "", "Optional newline-delimited regex allowlist for dogfood findings")
	denyPatterns := flag.String("deny-patterns", "", "Optional newline-delimited regex denylist for dogfood findings")
	failOnFindings := flag.Bool("fail-on-findings", false, "Exit non-zero when dogfood secret scan has findings")
	flag.Parse()

	// DB パス解決
	path := *dbPath
	if path == "" {
		path = os.Getenv("VIRGIL_DB_PATH")
	}
	if path == "" {
		path = "/home/agent/data/virgil.db"
	}

	// DB が存在するか確認
	if _, err := os.Stat(path); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "Database not found: %s\n", path)
		fmt.Fprintf(os.Stderr, "Usage: inspect --db /path/to/virgil.db\n")
		os.Exit(1)
	}

	database, err := db.New(path)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer database.Close()

	repo := repository.New(database)

	if *exportDogfood {
		logPath := *exportLog
		if logPath == "" {
			candidate := filepath.Join(filepath.Dir(path), "debug.log")
			if _, err := os.Stat(candidate); err == nil {
				logPath = candidate
			}
		}
		result, err := exportDogfoodReport(repo, dogfoodExportOptions{
			Session:        *exportSession,
			Exchange:       *exportExchange,
			OutDir:         *exportOut,
			LogPath:        logPath,
			LogLines:       *exportLogLines,
			AllowPatterns:  *allowPatterns,
			DenyPatterns:   *denyPatterns,
			FailOnFindings: *failOnFindings,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "dogfood export failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Dogfood export written to %s\n", result.OutputDir)
		fmt.Printf("Secret scan findings: %d\n", result.Scan.FindingCount)
		if *failOnFindings && result.Scan.FindingCount > 0 {
			os.Exit(2)
		}
		return
	}

	mux := http.NewServeMux()
	h := &Handler{repo: repo}

	// ページ
	mux.HandleFunc("/", h.indexPage)

	// API エンドポイント
	mux.HandleFunc("/api/sessions", h.listSessions)
	mux.HandleFunc("/api/sessions/", h.getSession)        // /api/sessions/{id}
	mux.HandleFunc("/api/exchanges/", h.listExchanges)    // /api/exchanges/{session_id}
	mux.HandleFunc("/api/exchange/", h.getExchange)       // /api/exchange/{id}
	mux.HandleFunc("/api/stats/", h.getStats)             // /api/stats/{session_id}
	mux.HandleFunc("/api/context/", h.getContextAnalysis) // /api/context/{session_id}

	addr := fmt.Sprintf(":%d", *port)
	fmt.Printf("🔍 Virgil Inspector running at http://localhost:%d\n", *port)
	fmt.Printf("📂 Database: %s\n", path)
	log.Fatal(http.ListenAndServe(addr, mux))
}
