package tools

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	md "github.com/JohannesKaufmann/html-to-markdown"
	"github.com/JohannesKaufmann/html-to-markdown/plugin"
	readability "github.com/go-shiori/go-readability"
)

const (
	fetchDocsMaxOutputChars = 15000
	fetchDocsMaxHTMLBytes   = 5 * 1024 * 1024
	fetchDocsTimeout        = 30 * time.Second
	fetchDocsUserAgent      = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0 Safari/537.36"
)

type FetchDocsTool struct {
	client    *http.Client
	userAgent string
}

type fetchDocsArgs struct {
	URL string `json:"url"`
}

type FetchDocsConfig struct {
	CABundlePath       string
	UserAgent          string
	InsecureSkipVerify bool
}

func NewFetchDocsTool() *FetchDocsTool {
	return NewFetchDocsToolWithConfig(FetchDocsConfigFromEnv())
}

func NewFetchDocsToolWithConfig(config FetchDocsConfig) *FetchDocsTool {
	userAgent := strings.TrimSpace(config.UserAgent)
	if userAgent == "" {
		userAgent = fetchDocsUserAgent
	}
	return &FetchDocsTool{
		client: &http.Client{
			Timeout:   fetchDocsTimeout,
			Transport: newFetchDocsTransport(config),
		},
		userAgent: userAgent,
	}
}

func FetchDocsConfigFromEnv() FetchDocsConfig {
	return FetchDocsConfig{
		CABundlePath:       strings.TrimSpace(os.Getenv("VIRGIL_FETCH_CA_BUNDLE")),
		UserAgent:          strings.TrimSpace(os.Getenv("VIRGIL_FETCH_USER_AGENT")),
		InsecureSkipVerify: strings.EqualFold(strings.TrimSpace(os.Getenv("VIRGIL_FETCH_INSECURE_SKIP_VERIFY")), "true"),
	}
}

func newFetchDocsTransport(config FetchDocsConfig) http.RoundTripper {
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return http.DefaultTransport
	}

	cloned := transport.Clone()
	cloned.Proxy = http.ProxyFromEnvironment

	tlsConfig := &tls.Config{
		InsecureSkipVerify: config.InsecureSkipVerify,
	}
	if config.InsecureSkipVerify {
		log.Printf("warning: VIRGIL_FETCH_INSECURE_SKIP_VERIFY=true; fetch_docs TLS verification is disabled")
	}

	if strings.TrimSpace(config.CABundlePath) != "" {
		if pool := loadFetchDocsCertPool(config.CABundlePath); pool != nil {
			tlsConfig.RootCAs = pool
		}
	}

	cloned.TLSClientConfig = tlsConfig
	return cloned
}

func loadFetchDocsCertPool(path string) *x509.CertPool {
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		if err != nil {
			log.Printf("warning: failed to load system cert pool for fetch_docs: %v", err)
		}
		pool = x509.NewCertPool()
	}

	pemBytes, err := os.ReadFile(path)
	if err != nil {
		log.Printf("warning: failed to read VIRGIL_FETCH_CA_BUNDLE %q: %v", path, err)
		return pool
	}
	if ok := pool.AppendCertsFromPEM(pemBytes); !ok {
		log.Printf("warning: VIRGIL_FETCH_CA_BUNDLE %q did not contain valid PEM certificates", path)
	}
	return pool
}

func (t *FetchDocsTool) Name() string {
	return "fetch_docs"
}

func (t *FetchDocsTool) IsMutating() bool {
	return false
}

func (t *FetchDocsTool) Definition() ToolDefinition {
	return ToolDefinition{
		Type: "function",
		Function: FunctionDefinition{
			Name:        "fetch_docs",
			Description: "Fetch the content of a web page and return it as Markdown. Useful for reading documentation, GitHub readmes, or technical articles. Optimized for local LLMs with content extraction and length limits.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"url": map[string]interface{}{
						"type":        "string",
						"description": "URL of the web page to fetch.",
					},
				},
				"required": []string{"url"},
			},
		},
	}
}

func (t *FetchDocsTool) Execute(ctx context.Context, rawArgs json.RawMessage) (*Result, error) {
	var args fetchDocsArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return ErrorResult(fmt.Sprintf("invalid arguments: %v", err)), nil
	}

	pageURL, err := normalizeFetchURL(args.URL)
	if err != nil {
		return ErrorResult(err.Error()), nil
	}

	htmlBytes, finalURL, err := t.fetchHTML(ctx, pageURL)
	if err != nil {
		return ErrorResult(fmt.Sprintf("fetch failed: %v", err)), nil
	}

	markdown, usedReadability, err := htmlToMarkdown(htmlBytes, finalURL)
	if err != nil {
		return ErrorResult(fmt.Sprintf("convert failed: %v", err)), nil
	}

	markdown, truncated := truncateFetchDocs(markdown, fetchDocsMaxOutputChars)
	content := fmt.Sprintf("# Fetched document\n\nSource: %s\n\n%s", finalURL.String(), markdown)

	res := SuccessResult(content)
	res.Metadata = map[string]interface{}{
		"url":              finalURL.String(),
		"used_readability": usedReadability,
		"truncated":        truncated,
		"content_chars":    len([]rune(markdown)),
	}
	return res, nil
}

func (t *FetchDocsTool) fetchHTML(ctx context.Context, pageURL *url.URL) ([]byte, *url.URL, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL.String(), nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("User-Agent", t.userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,text/plain;q=0.8,*/*;q=0.7")

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, nil, fmt.Errorf("unexpected HTTP status: %s", resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, fetchDocsMaxHTMLBytes+1))
	if err != nil {
		return nil, nil, err
	}
	if len(body) > fetchDocsMaxHTMLBytes {
		return nil, nil, fmt.Errorf("response too large: exceeds %d bytes", fetchDocsMaxHTMLBytes)
	}

	return body, resp.Request.URL, nil
}

func normalizeFetchURL(rawURL string) (*url.URL, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, fmt.Errorf("url is required")
	}

	pageURL, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("invalid url: %v", err)
	}
	if pageURL.Scheme != "http" && pageURL.Scheme != "https" {
		return nil, fmt.Errorf("url must use http or https")
	}
	if pageURL.Host == "" {
		return nil, fmt.Errorf("url host is required")
	}

	return pageURL, nil
}

func htmlToMarkdown(htmlBytes []byte, pageURL *url.URL) (string, bool, error) {
	article, err := readability.FromReader(bytes.NewReader(htmlBytes), pageURL)
	htmlContent := ""
	usedReadability := false
	if err == nil && strings.TrimSpace(article.Content) != "" {
		htmlContent = article.Content
		usedReadability = true
	} else {
		htmlContent = extractBodyHTML(string(htmlBytes))
	}

	converter := md.NewConverter(md.DomainFromURL(pageURL.String()), true, nil)
	converter.Use(plugin.GitHubFlavored())
	markdown, err := converter.ConvertString(htmlContent)
	if err != nil {
		return "", usedReadability, err
	}

	markdown = strings.TrimSpace(markdown)
	if article.Title != "" && !strings.HasPrefix(markdown, "# ") {
		markdown = "# " + strings.TrimSpace(article.Title) + "\n\n" + markdown
	}
	return markdown, usedReadability, nil
}

func extractBodyHTML(html string) string {
	bodyRe := regexp.MustCompile(`(?is)<body\b[^>]*>(.*)</body>`)
	matches := bodyRe.FindStringSubmatch(html)
	if len(matches) >= 2 {
		return matches[1]
	}
	return html
}

func truncateFetchDocs(content string, maxChars int) (string, bool) {
	runes := []rune(content)
	if len(runes) <= maxChars {
		return content, false
	}
	return string(runes[:maxChars]) + "\n\n... [Truncated due to context limit]", true
}
