package relay

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relay/requestplugin"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/model_setting"

	"github.com/gin-gonic/gin"
)

// outboundImageCounter is an image adaptor whose upstream body carries the
// image count in a member other than n. OutboundImageCount reads the count
// from that body, nil when the member is absent.
type outboundImageCounter interface {
	OutboundImageCount(body []byte) (*uint, error)
}

func ImageHelper(c *gin.Context, info *relaycommon.RelayInfo) (newAPIError *types.NewAPIError) {
	info.InitChannelMeta(c)
	info.BillingImageCount = nil
	info.ImageRequestCount = 0

	imageReq, ok := info.Request.(*dto.ImageRequest)
	if !ok {
		return types.NewErrorWithStatusCode(fmt.Errorf("invalid request type, expected dto.ImageRequest, got %T", info.Request), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}

	request, err := common.DeepCopy(imageReq)
	if err != nil {
		return types.NewError(fmt.Errorf("failed to copy request to ImageRequest: %w", err), types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
	}

	err = helper.ModelMappedHelper(c, info, request)
	if err != nil {
		return types.NewError(err, types.ErrorCodeChannelModelMappedError, types.ErrOptionWithSkipRetry())
	}
	if err = helper.ApplyImageResolutionModelMapping(info, request, c.GetString("model_mapping")); err != nil {
		return types.NewErrorWithStatusCode(err, types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}

	adaptor := GetAdaptor(info.ApiType)
	if adaptor == nil {
		return types.NewError(fmt.Errorf("invalid api type: %d", info.ApiType), types.ErrorCodeInvalidApiType, types.ErrOptionWithSkipRetry())
	}
	adaptor.Init(info)
	imageCount, err := request.ImageCount(false)
	if err != nil {
		return types.NewErrorWithStatusCode(err, types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}

	var requestBody io.Reader
	var jsonData []byte
	var outbound struct {
		N *uint `json:"n"`
	}
	validateQuantity := false

	if model_setting.GetGlobalSettings().PassThroughRequestEnabled || info.ChannelSetting.PassThroughBodyEnabled {
		storage, err := common.GetBodyStorage(c)
		if err != nil {
			return types.NewErrorWithStatusCode(err, types.ErrorCodeReadRequestBodyFailed, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
		}
		if strings.Contains(c.Request.Header.Get("Content-Type"), "multipart/form-data") {
			requestBody = common.NewReplayableBodyReader(storage)
			if info.IsImageResolutionModelMapped {
				var contentType string
				originalContentType := c.Request.Header.Get("Content-Type")
				requestBody, contentType, err = helper.RewriteImageMultipartModel(requestBody, originalContentType, request.Model)
				if err != nil {
					return types.NewErrorWithStatusCode(err, types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
				}
				c.Request.Header.Set("Content-Type", contentType)
				defer c.Request.Header.Set("Content-Type", originalContentType)
			}
		} else {
			jsonData, err = storage.Bytes()
			if err != nil {
				return types.NewErrorWithStatusCode(fmt.Errorf("invalid image billing parameters: %w", err), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
			}
			validateQuantity = true
			if common.DebugEnabled {
				if raw, readErr := storage.Bytes(); readErr == nil {
					logger.LogDebug(c, "image request body: %s", raw)
				}
			}
			if info.IsImageResolutionModelMapped {
				jsonData, err = helper.RewriteImageRequestModel(jsonData, request.Model)
				if err != nil {
					return types.NewErrorWithStatusCode(err, types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
				}
			}
		}
		requestBody = common.NewReplayableBodyReader(storage)
	} else {
		convertedRequest, err := adaptor.ConvertImageRequest(c, info, *request)
		if err != nil {
			// An adaptor that already classified its rejection (status code
			// and retry policy) keeps that classification instead of being
			// downgraded to a retryable conversion failure.
			var apiErr *types.NewAPIError
			if errors.As(err, &apiErr) {
				return apiErr
			}
			return types.NewError(err, types.ErrorCodeConvertRequestFailed)
		}
		relaycommon.AppendRequestConversionFromRequest(info, convertedRequest)

		switch convertedRequest.(type) {
		case *bytes.Buffer:
			if newAPIError = requestplugin.PassThrough(c, info, nil); newAPIError != nil {
				return newAPIError
			}
			requestBody = convertedRequest.(io.Reader)
		default:
			jsonData, err = common.Marshal(convertedRequest)
			if err != nil {
				return types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
			}

			if jsonData, newAPIError = requestplugin.Outbound(c, info, jsonData); newAPIError != nil {
				return newAPIError
			}
			// apply param override
			if len(info.ParamOverride) > 0 {
				jsonData, err = relaycommon.ApplyParamOverrideWithRelayInfo(jsonData, info)
				if err != nil {
					return newAPIErrorFromParamOverride(err)
				}
			}
			if billingErr := validateOutboundImageBilling(jsonData, info); billingErr != nil {
				return billingErr
			}

		}
	}
	if jsonData != nil {
		// This is a different trust boundary from ingress: channel overrides
		// and request plugins can change the quantity actually submitted, in
		// n or in the member the adaptor sends it in.
		err := common.Unmarshal(jsonData, &outbound)
		if counter, ok := adaptor.(outboundImageCounter); ok && err == nil {
			outbound.N, err = counter.OutboundImageCount(jsonData)
		}
		if err != nil {
			return types.NewErrorWithStatusCode(fmt.Errorf("invalid image billing parameters: %w", err), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
		}
		validateQuantity = true
	}
	if validateQuantity {
		// Validate the count in the body sent upstream, including channel overrides.
		quantityRequest := dto.ImageRequest{N: outbound.N}
		if quantityRequest.N == nil {
			quantityRequest.N = common.GetPointer(uint(imageCount))
		}
		imageCount, err = quantityRequest.ImageCount(false)
		if err != nil {
			return types.NewErrorWithStatusCode(err, types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
		}
	}
	if jsonData != nil {
		logger.LogDebug(c, "image request body: %s", jsonData)
		body, closer, err := relaycommon.NewOutboundJSONBody(jsonData)
		if err != nil {
			return types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
		}
		defer closer.Close()
		requestBody = body
	}

	if billingErr := service.PrepareImageBillingForRequest(c, info, imageCount); billingErr != nil {
		return billingErr
	}

	statusCodeMappingStr := c.GetString("status_code_mapping")

	resp, err := adaptor.DoRequest(c, info, requestBody)
	if err != nil {
		return types.NewOpenAIError(err, types.ErrorCodeDoRequestFailed, http.StatusInternalServerError)
	}
	var httpResp *http.Response
	if resp != nil {
		httpResp = resp.(*http.Response)
		info.IsStream = info.IsStream || strings.HasPrefix(httpResp.Header.Get("Content-Type"), "text/event-stream")
		if httpResp.StatusCode != http.StatusOK {
			if httpResp.StatusCode == http.StatusCreated && info.ApiType == constant.APITypeReplicate {
				// replicate channel returns 201 Created when using Prefer: wait, treat it as success.
				httpResp.StatusCode = http.StatusOK
			} else {
				newAPIError = service.RelayErrorHandler(c.Request.Context(), httpResp, false)
				// reset status code 重置状态码
				service.ResetStatusCode(newAPIError, statusCodeMappingStr)
				return newAPIError
			}
		}
	}

	originalWriter := c.Writer
	captureWriter := &imageResponseCaptureWriter{ResponseWriter: originalWriter}
	c.Writer = captureWriter
	defer func() { c.Writer = originalWriter }()
	usage, newAPIError := adaptor.DoResponse(c, httpResp, info)
	c.Writer = originalWriter
	if newAPIError != nil {
		// reset status code 重置状态码
		service.ResetStatusCode(newAPIError, statusCodeMappingStr)
		return newAPIError
	}

	imageCacheJob := prepareImageCacheJob(c, info, captureWriter.Bytes())
	if imageCacheJob != nil {
		service.SetImageCacheInfo(c, service.ImageCacheInfo{
			TotalCount: len(imageCacheJob.Sources),
			Status:     "pending",
		})
	}

	imageN := uint(1)
	if request.N != nil {
		imageN = *request.N
	}

	if usage.(*dto.Usage).TotalTokens == 0 {
		usage.(*dto.Usage).TotalTokens = 1
	}
	if usage.(*dto.Usage).PromptTokens == 0 {
		usage.(*dto.Usage).PromptTokens = 1
	}

	quality := request.Quality
	if quality == "" {
		quality = "standard"
	}

	var logContent []*common.Message

	if len(request.Size) > 0 {
		logContent = append(logContent, common.NewMessage("Size {{size}}", map[string]any{"size": request.Size}))
	}
	if len(quality) > 0 {
		logContent = append(logContent, common.NewMessage("Quality {{quality}}", map[string]any{"quality": quality}))
	}
	if imageN > 0 {
		logContent = append(logContent, common.NewMessage("Image count {{count}}", map[string]any{"count": imageN}))
	}

	service.PostTextConsumeQuota(c, info, usage.(*dto.Usage), logContent)
	service.StartImageCacheJob(imageCacheJob)
	return nil
}

// imageResponseCaptureWriter forwards every byte to the client while keeping
// a copy for local image recovery. The embedded gin writer preserves Flush,
// Hijack, CloseNotify and the other response-writer capabilities used by SSE.
type imageResponseCaptureWriter struct {
	gin.ResponseWriter
	body bytes.Buffer
}

func (w *imageResponseCaptureWriter) Write(data []byte) (int, error) {
	_, _ = w.body.Write(data)
	return w.ResponseWriter.Write(data)
}

func (w *imageResponseCaptureWriter) WriteString(value string) (int, error) {
	_, _ = w.body.WriteString(value)
	return w.ResponseWriter.WriteString(value)
}

func (w *imageResponseCaptureWriter) Bytes() []byte {
	return w.body.Bytes()
}

func prepareImageCacheJob(c *gin.Context, info *relaycommon.RelayInfo, body []byte) *service.ImageCacheJob {
	contentType := c.Writer.Header().Get("Content-Type")
	sources := service.ExtractImageSources(body, contentType)
	requestID := strings.TrimSpace(c.GetString(common.RequestIdKey))
	if len(sources) == 0 || requestID == "" {
		return nil
	}

	headers := imageCacheAuthHeaders(info)
	for index := range sources {
		sources[index].Headers = headers
		if info != nil {
			sources[index].Proxy = service.ResolveChannelProxy(info.ChannelSetting)
		}
	}

	requestSnapshot := &http.Request{Host: c.Request.Host, Header: c.Request.Header.Clone()}
	if c.Request.TLS != nil {
		requestSnapshot.TLS = c.Request.TLS
	}
	userID := 0
	if info != nil {
		userID = info.UserId
	}
	return &service.ImageCacheJob{
		RequestID: requestID,
		UserID:    userID,
		Request:   requestSnapshot,
		Sources:   sources,
		MediaDeliveryNode: func() string {
			if service.MediaDeliveryEnabled() && info != nil && info.UserSetting.MediaDeliveryNode == dto.MediaDeliveryNodeHK {
				return dto.MediaDeliveryNodeHK
			}
			return dto.MediaDeliveryNodeUS
		}(),
	}
}

func imageCacheAuthHeaders(info *relaycommon.RelayInfo) http.Header {
	headers := make(http.Header)
	if info == nil || strings.TrimSpace(info.ApiKey) == "" {
		return headers
	}
	key := strings.TrimSpace(info.ApiKey)
	switch info.ChannelType {
	case constant.ChannelTypeGemini:
		headers.Set("x-goog-api-key", key)
	case constant.ChannelTypeAnthropic:
		headers.Set("x-api-key", key)
	case constant.ChannelTypeAzure:
		headers.Set("api-key", key)
	default:
		headers.Set("Authorization", "Bearer "+key)
	}
	return headers
}
