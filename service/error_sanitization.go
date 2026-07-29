package service

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting"
)

// SanitizeErrorMessage removes configured provider names from a user-facing
// error while keeping the original error available to internal callers.
func SanitizeErrorMessage(message string) string {
	for _, keyword := range setting.GetErrorSanitizationKeywords() {
		message = strings.ReplaceAll(message, keyword, "")
	}
	return message
}

// SanitizeErrorResponseBody removes configured keywords only from common
// error-message fields in a JSON response. User prompts and result fields
// remain unchanged.
func SanitizeErrorResponseBody(body []byte) []byte {
	var root any
	if err := common.Unmarshal(body, &root); err != nil {
		return body
	}

	sanitizeErrorResponseNode(root, false)
	sanitized, err := common.Marshal(root)
	if err != nil {
		return body
	}
	return sanitized
}

func sanitizeErrorResponseNode(node any, errorContext bool) {
	switch value := node.(type) {
	case map[string]any:
		for key, child := range value {
			normalizedKey := strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(key))
			fieldIsError := isErrorMessageField(normalizedKey)
			childInErrorContext := errorContext || normalizedKey == "error" || normalizedKey == "errors"

			if message, ok := child.(string); ok {
				if fieldIsError || normalizedKey == "error" || normalizedKey == "errors" {
					value[key] = SanitizeErrorMessage(message)
				}
				continue
			}
			sanitizeErrorResponseNode(child, childInErrorContext || fieldIsError)
		}
	case []any:
		for _, child := range value {
			sanitizeErrorResponseNode(child, errorContext)
		}
	}
}

func isErrorMessageField(key string) bool {
	switch key {
	case "message", "reason", "failreason", "errormessage", "errmsg":
		return true
	default:
		return false
	}
}
