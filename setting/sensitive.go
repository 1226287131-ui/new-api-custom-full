package setting

import "strings"

// These options are retained as a compatibility layer for installations that
// still have the retired sensitive-word settings in their option table. New
// request-policy instances are migrated by model.MigrateSensitiveWords.
var CheckSensitiveEnabled = false
var CheckSensitiveOnPromptEnabled = false

// StopOnSensitiveEnabled controls whether a detected word stops generation.
var StopOnSensitiveEnabled = true

// StreamCacheQueueLength is the legacy stream cache queue length; zero means
// no cache. It remains readable so old option rows can be migrated safely.
var StreamCacheQueueLength = 0

// SensitiveWords is retained for legacy option serialization and migration.
var SensitiveWords []string

func SensitiveWordsToString() string {
	return strings.Join(SensitiveWords, "\n")
}

func SensitiveWordsFromString(value string) {
	words := make([]string, 0)
	for word := range strings.SplitSeq(value, "\n") {
		word = strings.TrimSpace(word)
		if word != "" {
			words = append(words, word)
		}
	}
	SensitiveWords = words
}

func ShouldCheckPromptSensitive() bool {
	return CheckSensitiveEnabled && CheckSensitiveOnPromptEnabled
}
