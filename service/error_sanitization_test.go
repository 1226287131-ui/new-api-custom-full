package service

import (
	"errors"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func withErrorSanitizationKeywords(t *testing.T, value string) {
	t.Helper()
	original := setting.ErrorSanitizationKeywordsToString()
	setting.ErrorSanitizationKeywordsFromString(value)
	t.Cleanup(func() {
		setting.ErrorSanitizationKeywordsFromString(original)
	})
}

func TestSanitizeErrorMessageRemovesConfiguredKeywords(t *testing.T) {
	withErrorSanitizationKeywords(t, "灵动\nprovider, provider；灵动")

	assert.Equal(t, "生成失败：请重试", SanitizeErrorMessage("灵动生成失败：请重试"))
	assert.Equal(t, "请求失败", SanitizeErrorMessage("provider请求失败"))
}

func TestTaskErrorWrapperKeepsOriginalError(t *testing.T) {
	withErrorSanitizationKeywords(t, "灵动")
	original := errors.New("灵动生成失败")

	taskError := TaskErrorWrapper(original, "task_failed", 502)

	assert.Equal(t, "生成失败", taskError.Message)
	assert.Same(t, original, taskError.Error)
}

func TestSanitizeErrorResponseBodyOnlyChangesErrorFields(t *testing.T) {
	withErrorSanitizationKeywords(t, "灵动")
	body := []byte(`{"message":"灵动生成失败","prompt":"请保留灵动这个词","data":{"result":"灵动作品"},"error":{"message":"灵动上游错误"}}`)

	sanitized := SanitizeErrorResponseBody(body)
	var decoded map[string]any
	require.NoError(t, common.Unmarshal(sanitized, &decoded))

	assert.Equal(t, "生成失败", decoded["message"])
	assert.Equal(t, "请保留灵动这个词", decoded["prompt"])
	assert.Equal(t, "灵动作品", decoded["data"].(map[string]any)["result"])
	assert.Equal(t, "上游错误", decoded["error"].(map[string]any)["message"])
}
