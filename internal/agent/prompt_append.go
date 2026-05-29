package agent

import (
	"log"
	"os"
	"strings"
)

const promptAppendSectionTitle = "# Local Environment Instructions"

func SystemPromptWithAppend(base, extra string) string {
	extra = strings.TrimSpace(extra)
	if extra == "" {
		return base
	}
	return strings.TrimRight(base, "\n") + "\n\n" + promptAppendSectionTitle + "\n\n" + extra
}

// ExtractPromptAppend returns the local instruction section added by SystemPromptWithAppend.
func ExtractPromptAppend(prompt string) string {
	marker := "\n\n" + promptAppendSectionTitle + "\n\n"
	idx := strings.LastIndex(prompt, marker)
	if idx < 0 {
		return ""
	}
	return strings.TrimSpace(prompt[idx+len(marker):])
}

func SystemPromptWithAppendFromEnv(base string) string {
	path := strings.TrimSpace(os.Getenv("VIRGIL_PROMPT_APPEND"))
	if path == "" {
		return base
	}

	content, err := os.ReadFile(path)
	if err != nil {
		log.Printf("warning: failed to read VIRGIL_PROMPT_APPEND %q: %v", path, err)
		return base
	}
	return SystemPromptWithAppend(base, string(content))
}
