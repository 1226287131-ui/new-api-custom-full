package helper

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

// RewriteImageRequestModel updates only the top-level model field in a JSON
// image request. Unlike marshaling dto.ImageRequest, this preserves provider-
// specific fields that are intentionally unknown to the public DTO.
func RewriteImageRequestModel(body []byte, modelName string) ([]byte, error) {
	modelName = strings.TrimSpace(modelName)
	if modelName == "" {
		return body, nil
	}

	var payload map[string]json.RawMessage
	if err := common.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode image request for model mapping: %w", err)
	}
	if payload == nil {
		return nil, fmt.Errorf("image request body must be a JSON object")
	}

	encodedModel, err := common.Marshal(modelName)
	if err != nil {
		return nil, fmt.Errorf("encode mapped image model: %w", err)
	}
	payload["model"] = encodedModel

	rewritten, err := common.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode mapped image request: %w", err)
	}
	return rewritten, nil
}

// RewriteImageMultipartModel preserves file and extension parts while replacing
// the model field for pass-through image edit requests.
func RewriteImageMultipartModel(body io.Reader, contentType, modelName string) (*bytes.Buffer, string, error) {
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil || params["boundary"] == "" {
		return nil, "", fmt.Errorf("invalid image multipart content type")
	}

	result := new(bytes.Buffer)
	writer := multipart.NewWriter(result)
	reader := multipart.NewReader(body, params["boundary"])
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, "", fmt.Errorf("read image multipart part: %w", err)
		}
		if part.FormName() == "model" {
			part.Close()
			continue
		}
		out, err := writer.CreatePart(part.Header)
		if err != nil {
			part.Close()
			return nil, "", fmt.Errorf("write image multipart part: %w", err)
		}
		_, err = io.Copy(out, part)
		part.Close()
		if err != nil {
			return nil, "", fmt.Errorf("copy image multipart part: %w", err)
		}
	}
	if err := writer.WriteField("model", modelName); err != nil {
		return nil, "", fmt.Errorf("write mapped image model: %w", err)
	}
	if err := writer.Close(); err != nil {
		return nil, "", fmt.Errorf("finish image multipart body: %w", err)
	}
	return result, writer.FormDataContentType(), nil
}
