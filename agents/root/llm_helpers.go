// Package root - LLM-only helpers. Non-LLM logic is untouched.
package root

import (
	"strings"

	"github.com/sage-x-project/sage-multi-agent/llm"
)

// extractFirstJSON returns the outermost {...} region from a string.
func extractFirstJSON(s string) []byte {
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start >= 0 && end > start {
		return []byte(s[start : end+1])
	}
	return nil
}

func langOrDefault(lang string) string {
	lang = strings.ToLower(strings.TrimSpace(lang))
	if lang == "ko" {
		return "ko"
	}
	return "en"
}

// containsAny does a space-insensitive, case-insensitive substring search.

func containsAny(s string, kws ...string) bool {
	for _, kw := range kws {
		if strings.Contains(s, strings.ToLower(kw)) {
			return true
		}
	}
	return false
}

// detectLang is a thin wrapper.
func detectLang(s string) string { return llm.DetectLang(s) }
