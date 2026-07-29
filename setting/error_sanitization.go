package setting

import (
	"strings"
	"sync"
)

var (
	errorSanitizationMu       sync.RWMutex
	errorSanitizationKeywords []string
)

func ErrorSanitizationKeywordsToString() string {
	errorSanitizationMu.RLock()
	defer errorSanitizationMu.RUnlock()

	return strings.Join(errorSanitizationKeywords, "\n")
}

func ErrorSanitizationKeywordsFromString(value string) {
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == '\n' || r == '\r' || r == ',' || r == '，' || r == ';' || r == '；'
	})

	keywords := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		keyword := strings.TrimSpace(part)
		if keyword == "" {
			continue
		}
		if _, exists := seen[keyword]; exists {
			continue
		}
		seen[keyword] = struct{}{}
		keywords = append(keywords, keyword)
	}

	errorSanitizationMu.Lock()
	errorSanitizationKeywords = keywords
	errorSanitizationMu.Unlock()
}

func GetErrorSanitizationKeywords() []string {
	errorSanitizationMu.RLock()
	defer errorSanitizationMu.RUnlock()

	return append([]string(nil), errorSanitizationKeywords...)
}
