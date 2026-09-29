package helper

import (
	"bytes"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestApplyImageResolutionModelMappingOnlyRoutes1K(t *testing.T) {
	saved := ratio_setting.ImageResolutionModelMap2JSONString()
	savedPrices := ratio_setting.ImageResolutionPrice2JSONString()
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateImageResolutionModelMapByJSONString(saved)) })
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateImageResolutionPriceByJSONString(savedPrices)) })
	require.NoError(t, ratio_setting.UpdateImageResolutionPriceByJSONString(`{"public-image":{"1K":0.1,"2K":0.2,"4K":0.3}}`))
	require.NoError(t, ratio_setting.UpdateImageResolutionModelMapByJSONString(`{"public-image":"cheap-image"}`))

	tests := []struct {
		name         string
		size         string
		billedTier   string
		wantUpstream string
		wantMapped   bool
		channelMap   string
	}{
		{name: "one k", size: "1024x1024", billedTier: "1K", wantUpstream: "cheap-image", wantMapped: true},
		{name: "two k", size: "2048x2048", billedTier: "2K", wantUpstream: "public-image"},
		{name: "four k", size: "4096x4096", billedTier: "4K", wantUpstream: "public-image"},
		{name: "default one k", billedTier: "1K", wantUpstream: "cheap-image", wantMapped: true},
		{name: "channel alias", billedTier: "1K", channelMap: `{"cheap-image":"provider-image"}`, wantUpstream: "provider-image", wantMapped: true},
		{name: "nested two k with empty top-level size", billedTier: "2K", wantUpstream: "public-image"},
		{name: "expression price without resolution tier", wantUpstream: "public-image"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := &relaycommon.RelayInfo{
				OriginModelName: "public-image",
				ChannelMeta:     &relaycommon.ChannelMeta{UpstreamModelName: "public-image"},
				PriceData:       types.PriceData{ImageResolutionTier: tt.billedTier},
			}
			request := &dto.ImageRequest{Model: "public-image", Size: tt.size}
			require.NoError(t, ApplyImageResolutionModelMapping(info, request, tt.channelMap))
			require.Equal(t, "public-image", info.OriginModelName)
			require.Equal(t, tt.wantUpstream, info.UpstreamModelName)
			require.Equal(t, tt.wantMapped, info.IsModelMapped)
			require.Equal(t, tt.wantUpstream, request.Model)
		})
	}
}

func TestImageResolutionRoutingModelUsesPricedTierForChannel(t *testing.T) {
	saved := ratio_setting.ImageResolutionModelMap2JSONString()
	savedPrices := ratio_setting.ImageResolutionPrice2JSONString()
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateImageResolutionModelMapByJSONString(saved)) })
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateImageResolutionPriceByJSONString(savedPrices)) })
	require.NoError(t, ratio_setting.UpdateImageResolutionPriceByJSONString(`{"public-image":{"1K":0.1,"2K":0.2,"4K":0.3}}`))
	require.NoError(t, ratio_setting.UpdateImageResolutionModelMapByJSONString(`{"public-image":"cheap-image"}`))

	for _, tc := range []struct {
		name, body, want string
	}{
		{"default", `{"model":"public-image","prompt":"test"}`, "cheap-image"},
		{"one k", `{"model":"public-image","size":"1024x1024"}`, "cheap-image"},
		{"nested two k", `{"model":"public-image","parameters":{"image_size":"2K"}}`, "public-image"},
		{"four k", `{"model":"public-image","size":"4K"}`, "public-image"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewBufferString(tc.body))
			c.Request.Header.Set("Content-Type", "application/json")
			actual, err := ImageResolutionRoutingModel(c, "public-image")
			require.NoError(t, err)
			require.Equal(t, tc.want, actual)
		})
	}
}

func TestImageResolutionRoutingModelMultipartPreservesBody(t *testing.T) {
	saved := ratio_setting.ImageResolutionModelMap2JSONString()
	savedPrices := ratio_setting.ImageResolutionPrice2JSONString()
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateImageResolutionModelMapByJSONString(saved)) })
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateImageResolutionPriceByJSONString(savedPrices)) })
	require.NoError(t, ratio_setting.UpdateImageResolutionPriceByJSONString(`{"public-image":{"1K":0.1,"2K":0.2,"4K":0.3}}`))
	require.NoError(t, ratio_setting.UpdateImageResolutionModelMapByJSONString(`{"public-image":"cheap-image"}`))
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	require.NoError(t, writer.WriteField("model", "public-image"))
	require.NoError(t, writer.WriteField("size", "1024x1024"))
	file, err := writer.CreateFormFile("image", "test.png")
	require.NoError(t, err)
	_, err = file.Write([]byte("image-bytes"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", bytes.NewReader(body.Bytes()))
	c.Request.Header.Set("Content-Type", writer.FormDataContentType())
	actual, err := ImageResolutionRoutingModel(c, "public-image")
	require.NoError(t, err)
	require.Equal(t, "cheap-image", actual)
	require.Equal(t, "public-image", c.Request.PostForm.Get("model"))
}

func TestApplyImageResolutionModelMappingSupportsQuality1K(t *testing.T) {
	saved := ratio_setting.ImageResolutionModelMap2JSONString()
	savedPrices := ratio_setting.ImageResolutionPrice2JSONString()
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateImageResolutionModelMapByJSONString(saved)) })
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateImageResolutionPriceByJSONString(savedPrices)) })
	require.NoError(t, ratio_setting.UpdateImageResolutionPriceByJSONString(`{"public-image":{"1K":0.1,"2K":0.2,"4K":0.3}}`))
	require.NoError(t, ratio_setting.UpdateImageResolutionModelMapByJSONString(`{"public-image":"cheap-image"}`))

	info := &relaycommon.RelayInfo{
		OriginModelName: "public-image",
		ChannelMeta:     &relaycommon.ChannelMeta{UpstreamModelName: "public-image"},
	}
	info.PriceData.ImageResolutionTier = "1K"
	request := &dto.ImageRequest{Model: "public-image", Quality: "1K"}
	require.NoError(t, ApplyImageResolutionModelMapping(info, request, ""))
	require.Equal(t, "cheap-image", info.UpstreamModelName)
}

func TestRewriteImageRequestModelPreservesUnknownFields(t *testing.T) {
	input := []byte(`{"model":"public-image","prompt":"draw a moon","provider_extension":{"seed":7}}`)
	output, err := RewriteImageRequestModel(input, "cheap-image")
	require.NoError(t, err)
	var decoded map[string]any
	require.NoError(t, common.Unmarshal(output, &decoded))
	require.Equal(t, "cheap-image", decoded["model"])
	require.Equal(t, "draw a moon", decoded["prompt"])
	require.Equal(t, map[string]any{"seed": float64(7)}, decoded["provider_extension"])
}

func TestRewriteImageMultipartModelPreservesImageAndExtension(t *testing.T) {
	var input bytes.Buffer
	writer := multipart.NewWriter(&input)
	require.NoError(t, writer.WriteField("model", "public-image"))
	require.NoError(t, writer.WriteField("seed", "7"))
	file, err := writer.CreateFormFile("image", "test.png")
	require.NoError(t, err)
	_, err = file.Write([]byte("image bytes"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	output, contentType, err := RewriteImageMultipartModel(&input, writer.FormDataContentType(), "cheap-image")
	require.NoError(t, err)
	_, params, err := mime.ParseMediaType(contentType)
	require.NoError(t, err)
	request := multipart.NewReader(output, params["boundary"])
	form, err := request.ReadForm(1024)
	require.NoError(t, err)
	defer form.RemoveAll()
	require.Equal(t, []string{"cheap-image"}, form.Value["model"])
	require.Equal(t, []string{"7"}, form.Value["seed"])
	require.Len(t, form.File["image"], 1)
	f, err := form.File["image"][0].Open()
	require.NoError(t, err)
	defer f.Close()
	data, err := io.ReadAll(f)
	require.NoError(t, err)
	require.Equal(t, []byte("image bytes"), data)
}

func TestRewriteImageRequestModelRejectsNonObjectJSON(t *testing.T) {
	_, err := RewriteImageRequestModel([]byte(`[]`), "cheap-image")
	require.Error(t, err)
}
