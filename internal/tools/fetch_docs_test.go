package tools

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFetchDocsToolExecute(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("User-Agent"); got == "" {
			t.Fatal("User-Agent header is empty")
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html>
<html>
  <head><title>Example Docs</title></head>
  <body>
    <nav>Navigation noise</nav>
    <main>
      <article>
        <h1>Example Docs</h1>
        <p>This is the useful documentation body.</p>
        <pre><code>go test ./...</code></pre>
      </article>
    </main>
  </body>
</html>`))
	}))
	defer server.Close()

	tool := NewFetchDocsTool()
	args, _ := json.Marshal(fetchDocsArgs{URL: server.URL})
	res, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if res.IsError {
		t.Fatalf("Execute returned tool error: %s", res.Content)
	}
	if !strings.Contains(res.Content, "Source: "+server.URL) {
		t.Fatalf("result missing source URL:\n%s", res.Content)
	}
	if !strings.Contains(res.Content, "useful documentation body") {
		t.Fatalf("result missing article body:\n%s", res.Content)
	}
	if strings.Contains(res.Content, "Navigation noise") {
		t.Fatalf("result should not include navigation noise:\n%s", res.Content)
	}
}

func TestFetchDocsRejectsNonHTTPURL(t *testing.T) {
	tool := NewFetchDocsTool()
	args, _ := json.Marshal(fetchDocsArgs{URL: "file:///etc/passwd"})
	res, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("Execute succeeded, want tool error")
	}
}

func TestFetchDocsConfigFromEnv(t *testing.T) {
	t.Setenv("VIRGIL_FETCH_CA_BUNDLE", "/tmp/company.pem")
	t.Setenv("VIRGIL_FETCH_USER_AGENT", "Mozilla/5.0")
	t.Setenv("VIRGIL_FETCH_INSECURE_SKIP_VERIFY", "TRUE")

	config := FetchDocsConfigFromEnv()
	if config.CABundlePath != "/tmp/company.pem" {
		t.Fatalf("CABundlePath = %q", config.CABundlePath)
	}
	if config.UserAgent != "Mozilla/5.0" {
		t.Fatalf("UserAgent = %q", config.UserAgent)
	}
	if !config.InsecureSkipVerify {
		t.Fatal("InsecureSkipVerify = false, want true")
	}
}

func TestFetchDocsUsesConfiguredUserAgent(t *testing.T) {
	const userAgent = "VirgilTest/1.0"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("User-Agent"); got != userAgent {
			t.Fatalf("User-Agent = %q, want %q", got, userAgent)
		}
		_, _ = w.Write([]byte(`<html><body><main><p>Hello docs.</p></main></body></html>`))
	}))
	defer server.Close()

	tool := NewFetchDocsToolWithConfig(FetchDocsConfig{UserAgent: userAgent})
	args, _ := json.Marshal(fetchDocsArgs{URL: server.URL})
	res, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if res.IsError {
		t.Fatalf("Execute returned tool error: %s", res.Content)
	}
}

func TestFetchDocsWithCustomCABundle(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html><body><main><p>Secure docs.</p></main></body></html>`))
	}))
	defer server.Close()

	certPath := writeServerCertificatePEM(t, server)
	tool := NewFetchDocsToolWithConfig(FetchDocsConfig{CABundlePath: certPath})
	args, _ := json.Marshal(fetchDocsArgs{URL: server.URL})
	res, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if res.IsError {
		t.Fatalf("Execute returned tool error: %s", res.Content)
	}
	if !strings.Contains(res.Content, "Secure docs") {
		t.Fatalf("result missing secure content:\n%s", res.Content)
	}
}

func TestFetchDocsWithInsecureSkipVerify(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html><body><main><p>Insecure docs.</p></main></body></html>`))
	}))
	defer server.Close()

	tool := NewFetchDocsToolWithConfig(FetchDocsConfig{InsecureSkipVerify: true})
	args, _ := json.Marshal(fetchDocsArgs{URL: server.URL})
	res, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if res.IsError {
		t.Fatalf("Execute returned tool error: %s", res.Content)
	}
}

func TestFetchDocsInvalidCABundleDoesNotPanic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "invalid.pem")
	if err := os.WriteFile(path, []byte("not a certificate"), 0600); err != nil {
		t.Fatal(err)
	}

	tool := NewFetchDocsToolWithConfig(FetchDocsConfig{CABundlePath: path})
	if tool == nil || tool.client == nil {
		t.Fatal("expected tool with client")
	}
}

func TestTruncateFetchDocs(t *testing.T) {
	content, truncated := truncateFetchDocs("abcdef", 3)
	if !truncated {
		t.Fatal("truncated = false, want true")
	}
	if !strings.Contains(content, "... [Truncated due to context limit]") {
		t.Fatalf("missing truncation marker: %q", content)
	}
}

func writeServerCertificatePEM(t *testing.T, server *httptest.Server) string {
	t.Helper()
	if len(server.TLS.Certificates) == 0 || len(server.TLS.Certificates[0].Certificate) == 0 {
		t.Fatal("test TLS server has no certificate")
	}

	pemBytes := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: server.TLS.Certificates[0].Certificate[0],
	})
	path := filepath.Join(t.TempDir(), "server.pem")
	if err := os.WriteFile(path, pemBytes, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
