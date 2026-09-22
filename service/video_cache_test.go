package service

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestExtractVideoResultURLPrefersResultPayload(t *testing.T) {
	body := []byte(`{
		"data": {
			"input": {"url": "https://images.example/input.png"},
			"result": {"data": [{"url": "https://upstream.example/video.mp4"}]}
		}
	}`)

	assert.Equal(t, "https://upstream.example/video.mp4", ExtractVideoResultURL(body))
}

func TestResolveVideoResultURL(t *testing.T) {
	assert.Equal(t,
		"https://upstream.example/api/video.mp4",
		ResolveVideoResultURL("https://upstream.example", "/api/video.mp4"),
	)
	assert.Equal(t,
		"https://cdn.example/video.mp4",
		ResolveVideoResultURL("https://upstream.example", "https://cdn.example/video.mp4"),
	)
	assert.Equal(t, "/api/video.mp4", ResolveVideoResultURL("not-a-url", "/api/video.mp4"))
}

func TestVideoCacheProxyPrefersDedicatedDownloadProxy(t *testing.T) {
	t.Setenv(videoCacheProxyEnv, "socks5h://video-egress:1080")

	assert.Equal(t, "socks5h://video-egress:1080", videoCacheProxy(VideoCacheSource{
		Proxy:             "http://channel-proxy.example:8080",
		UseDedicatedProxy: true,
	}))
}

func TestVideoCacheProxyKeepsUnselectedChannelOnItsOwnProxy(t *testing.T) {
	t.Setenv(videoCacheProxyEnv, "socks5h://video-egress:1080")

	assert.Equal(t, "http://channel-proxy.example:8080", videoCacheProxy(VideoCacheSource{
		Proxy: " http://channel-proxy.example:8080 ",
	}))
}

func TestVideoCacheProxyFallsBackToChannelProxyWhenDedicatedEgressIsUnavailable(t *testing.T) {
	t.Setenv(videoCacheProxyEnv, "")

	assert.Equal(t, "http://channel-proxy.example:8080", videoCacheProxy(VideoCacheSource{
		Proxy:             " http://channel-proxy.example:8080 ",
		UseDedicatedProxy: true,
	}))
}

func TestVideoResultURLFailureReason(t *testing.T) {
	assert.Equal(t,
		"upstream video generation returned no final video URL",
		VideoResultURLFailureReason("https://upstream.example/Video%20generation%20returned%20no%20final%20video%20URL"),
	)
	assert.Empty(t, VideoResultURLFailureReason("https://upstream.example/video/result"))
}

func TestNormalizeNewAPIVideoTaskResultPromotesUsableURL(t *testing.T) {
	result := &relaycommon.TaskInfo{
		Status:        model.TaskStatusInProgress,
		Url:           "/api/video.mp4",
		TerminalError: true,
	}

	normalizeNewAPIVideoTaskResult("https://upstream.example", result)

	assert.Equal(t, model.TaskStatusSuccess, result.Status)
	assert.Equal(t, "100%", result.Progress)
	assert.Equal(t, "https://upstream.example/api/video.mp4", result.Url)
	assert.False(t, result.TerminalError)
}

func TestRedactVideoResponseBodyReplacesUpstreamURLs(t *testing.T) {
	const publicURL = "https://api.example/video-cache/task_public.mp4"
	body := []byte(`{"data":{"result":{"data":[{"url":"https://upstream.example/video.mp4"}]}}}`)

	redacted := redactVideoResponseBody(body, publicURL, true)
	assert.NotContains(t, string(redacted), "upstream.example")
	assert.Equal(t, publicURL, ExtractVideoResultURL(redacted))
}

func TestRedactVideoResponseBodyHidesPendingURLs(t *testing.T) {
	body := []byte(`{"data":{"result":{"data":[{"url":"https://upstream.example/video.mp4"}]}}}`)

	redacted := redactVideoResponseBody(body, "", true)
	assert.NotContains(t, string(redacted), "upstream.example")
	assert.Empty(t, ExtractVideoResultURL(redacted))
}

func TestSanitizeNewAPIVideoTaskDataHidesProviderIdentity(t *testing.T) {
	const (
		publicTaskID   = "task_public"
		upstreamTaskID = "provider_task_123"
		publicURL      = "https://api.example/video-cache/task_public.mp4"
	)
	body := []byte(`{
		"id":"provider_task_123",
		"task_id":"provider_task_123",
		"upstream_task_id":"provider_task_123",
		"status":"SUCCESS",
		"message":"download from https://upstream.example/private/provider_task_123",
		"data":{
			"id":"provider_task_123",
			"taskId":"provider_task_123",
			"downloadUrl":"https://upstream.example/video.mp4",
			"asset":{"id":"asset_456"}
		}
	}`)

	sanitized := SanitizeNewAPIVideoTaskData(body, publicTaskID, upstreamTaskID, publicURL)
	text := string(sanitized)
	assert.NotContains(t, text, "upstream.example")
	assert.NotContains(t, text, upstreamTaskID)
	assert.NotContains(t, text, "upstream_task_id")
	assert.Contains(t, text, publicTaskID)
	assert.Contains(t, text, publicURL)
	assert.Contains(t, text, "asset_456")
}

func TestSanitizeNewAPIVideoTaskDataHidesPendingProviderData(t *testing.T) {
	body := []byte(`{"task_id":"provider_task_123","url":"https://upstream.example/video.mp4"}`)

	sanitized := SanitizeNewAPIVideoTaskData(body, "task_public", "provider_task_123", "")
	assert.JSONEq(t, `{"task_id":"task_public"}`, string(sanitized))
}

func TestSanitizeNewAPIVideoTaskDataRejectsInvalidJSON(t *testing.T) {
	assert.JSONEq(t, `{}`, string(SanitizeNewAPIVideoTaskData([]byte(`not-json`), "task_public", "provider", "")))
}

func TestCleanupVideoCacheRemovesOnlyExpiredFiles(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("VIDEO_CACHE_DIR", cacheDir)

	oldPath := filepath.Join(cacheDir, "old.mp4")
	freshPath := filepath.Join(cacheDir, "fresh.mp4")
	otherPath := filepath.Join(cacheDir, "keep.txt")
	require.NoError(t, os.WriteFile(oldPath, []byte("old"), 0600))
	require.NoError(t, os.WriteFile(freshPath, []byte("fresh"), 0600))
	require.NoError(t, os.WriteFile(otherPath, []byte("keep"), 0600))
	oldTime := time.Now().Add(-defaultVideoCacheTTL - time.Hour)
	require.NoError(t, os.Chtimes(oldPath, oldTime, oldTime))
	require.NoError(t, os.Chtimes(otherPath, oldTime, oldTime))

	removed, err := CleanupVideoCache()
	require.NoError(t, err)
	assert.Equal(t, 1, removed)
	assert.NoFileExists(t, oldPath)
	assert.FileExists(t, freshPath)
	assert.FileExists(t, otherPath)
}

func TestVideoCacheFilePathStaysInsideCacheDirectory(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("VIDEO_CACHE_DIR", cacheDir)

	path := videoCacheFilePath(`../nested\task`)
	assert.Equal(t, filepath.Clean(cacheDir), filepath.Dir(path))
	assert.False(t, strings.Contains(filepath.Base(path), ".."))
}

func TestCacheVideoDataURLWritesLocalFile(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("VIDEO_CACHE_DIR", cacheDir)

	dataURL := "data:video/mp4;base64," + base64.StdEncoding.EncodeToString([]byte("video-bytes"))
	path, err := CacheVideoDataURL(context.Background(), "task_data", dataURL)
	require.NoError(t, err)
	assert.FileExists(t, path)
	contents, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, []byte("video-bytes"), contents)
}

func TestVideoCacheExpiredUsesFixedFirstCacheTime(t *testing.T) {
	now := time.Now().Unix()
	expired := &model.Task{
		FinishTime: now,
		PrivateData: model.TaskPrivateData{
			VideoCachedAt: now - int64(defaultVideoCacheTTL.Seconds()) - 1,
		},
	}
	fresh := &model.Task{
		FinishTime: now - int64(defaultVideoCacheTTL.Seconds()) - 1,
		PrivateData: model.TaskPrivateData{
			VideoCachedAt: now,
		},
	}

	assert.True(t, VideoCacheExpired(expired))
	assert.False(t, VideoCacheExpired(fresh))
	legacy := &model.Task{Status: model.TaskStatusSuccess, UpdatedAt: now - int64(defaultVideoCacheTTL.Seconds()) - 1}
	assert.True(t, VideoCacheExpired(legacy))
	assert.Zero(t, legacy.FinishTime)

	MarkVideoTaskCached(fresh)
	assert.Equal(t, now, fresh.PrivateData.VideoCachedAt)
}

func TestVideoCacheRetryMetadataBacksOffAndClearsOnSuccess(t *testing.T) {
	task := &model.Task{PrivateData: model.TaskPrivateData{}}
	before := time.Now().Add(29 * time.Second).Unix()
	MarkVideoCacheFailure(task, fmt.Errorf("temporary disk error"))
	assert.Equal(t, 1, task.PrivateData.VideoCacheAttempts)
	assert.GreaterOrEqual(t, task.PrivateData.VideoCacheNextRetryAt, before)
	assert.Equal(t, "temporary disk error", task.PrivateData.VideoCacheLastError)

	MarkVideoTaskCached(task)
	assert.NotZero(t, task.PrivateData.VideoCachedAt)
	assert.Zero(t, task.PrivateData.VideoCacheAttempts)
	assert.Zero(t, task.PrivateData.VideoCacheNextRetryAt)
	assert.Empty(t, task.PrivateData.VideoCacheLastError)
}

func TestRetryVideoTaskCachesSuccessfulTaskAfterTransientFailure(t *testing.T) {
	truncate(t)
	t.Setenv("VIDEO_CACHE_DIR", t.TempDir())
	channel := &model.Channel{Id: 981, Type: constant.ChannelTypeKling, Key: "channel-key", Status: common.ChannelStatusEnabled}
	require.NoError(t, model.DB.Create(channel).Error)
	task := &model.Task{
		TaskID:     "task_retry_success",
		Platform:   constant.TaskPlatform("kling"),
		ChannelId:  channel.Id,
		Status:     model.TaskStatusSuccess,
		Progress:   "100%",
		FinishTime: time.Now().Unix(),
		PrivateData: model.TaskPrivateData{
			UpstreamResultURL:     "data:video/mp4;base64," + base64.StdEncoding.EncodeToString([]byte("retry-video")),
			VideoCacheAttempts:    1,
			VideoCacheNextRetryAt: time.Now().Add(-time.Minute).Unix(),
		},
	}
	require.NoError(t, model.DB.Create(task).Error)

	require.NoError(t, RetryVideoTaskCaches(context.Background()))
	var saved model.Task
	require.NoError(t, model.DB.Where("task_id = ?", task.TaskID).First(&saved).Error)
	assert.NotZero(t, saved.PrivateData.VideoCachedAt)
	assert.Zero(t, saved.PrivateData.VideoCacheAttempts)
	assert.FileExists(t, filepath.Join(videoCacheDir(), task.TaskID+".mp4"))
}

func TestRetryVideoTaskCachesDoesNotAbandonAnExhaustedTask(t *testing.T) {
	truncate(t)
	t.Setenv("VIDEO_CACHE_DIR", t.TempDir())
	channel := &model.Channel{Id: 982, Type: constant.ChannelTypeKling, Key: "channel-key", Status: common.ChannelStatusEnabled}
	require.NoError(t, model.DB.Create(channel).Error)
	task := &model.Task{
		TaskID:     "task_retry_after_many_failures",
		Platform:   constant.TaskPlatform("kling"),
		ChannelId:  channel.Id,
		Status:     model.TaskStatusSuccess,
		Progress:   "100%",
		FinishTime: time.Now().Unix(),
		PrivateData: model.TaskPrivateData{
			UpstreamResultURL:     "data:video/mp4;base64," + base64.StdEncoding.EncodeToString([]byte("recovered-video")),
			VideoCacheAttempts:    8,
			VideoCacheNextRetryAt: time.Now().Add(-time.Minute).Unix(),
		},
	}
	require.NoError(t, model.DB.Create(task).Error)

	require.NoError(t, RetryVideoTaskCaches(context.Background()))
	var saved model.Task
	require.NoError(t, model.DB.Where("task_id = ?", task.TaskID).First(&saved).Error)
	assert.NotZero(t, saved.PrivateData.VideoCachedAt)
	assert.Zero(t, saved.PrivateData.VideoCacheAttempts)
	assert.FileExists(t, filepath.Join(videoCacheDir(), task.TaskID+".mp4"))
}

// External DSNs must target disposable test databases, never production data.
func TestVideoCacheWorkerDatabaseMatrix(t *testing.T) {
	for _, dialect := range []struct {
		kind common.DatabaseType
		env  string
	}{
		{common.DatabaseTypeSQLite, ""},
		{common.DatabaseTypeMySQL, "TEST_VIDEO_CACHE_MYSQL_DSN"},
		{common.DatabaseTypePostgreSQL, "TEST_VIDEO_CACHE_POSTGRES_DSN"},
	} {
		t.Run(string(dialect.kind), func(t *testing.T) {
			var driver gorm.Dialector = sqlite.Open(":memory:")
			if dialect.env != "" {
				dsn := os.Getenv(dialect.env)
				if dsn == "" {
					t.Skip(dialect.env + " is not configured")
				}
				if dialect.kind == common.DatabaseTypeMySQL {
					driver = mysql.Open(dsn)
				} else {
					driver = postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true})
				}
			}
			db, err := gorm.Open(driver, &gorm.Config{})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			sqlDB.SetMaxOpenConns(1)
			oldDB, oldType, oldCache := model.DB, common.MainDatabaseType(), common.MemoryCacheEnabled
			model.DB, common.MemoryCacheEnabled = db, false
			common.SetMainDatabaseType(dialect.kind)
			t.Cleanup(func() {
				model.DB, common.MemoryCacheEnabled = oldDB, oldCache
				common.SetMainDatabaseType(oldType)
				assert.NoError(t, sqlDB.Close())
			})
			require.NoError(t, db.AutoMigrate(&model.Task{}, &model.Channel{}))
			query := "select version()"
			if dialect.kind == common.DatabaseTypeSQLite {
				query = "select sqlite_version()"
			}
			var version string
			require.NoError(t, db.Raw(query).Scan(&version).Error)
			t.Logf("database version: %s", version)
			t.Run("detached_download_and_accounting", testVideoCacheDetachedDownload)
			t.Run("backoff_and_page_progress", testVideoCacheRetryPagination)
			t.Run("metadata_compare_and_swap", testVideoCacheMetadataCAS)
		})
	}
}

func videoCacheWorkerFixture(t *testing.T) (*model.Channel, *videoCacheQueue) {
	t.Helper()
	require.NoError(t, model.DB.Where("1 = 1").Delete(&model.Task{}).Error)
	require.NoError(t, model.DB.Where("1 = 1").Delete(&model.Channel{}).Error)
	t.Setenv("VIDEO_CACHE_DIR", t.TempDir())
	channel := &model.Channel{Type: constant.ChannelTypeKling, Key: "test-key", Status: common.ChannelStatusEnabled}
	require.NoError(t, model.DB.Create(channel).Error)
	return channel, newVideoCacheQueue(videoCacheRetryBatchSize)
}

func testVideoCacheDetachedDownload(t *testing.T) {
	channel, queue := videoCacheWorkerFixture(t)
	fetchSetting := system_setting.GetFetchSetting()
	originalFetchSetting := *fetchSetting
	fetchSetting.EnableSSRFProtection = false
	t.Cleanup(func() { *fetchSetting = originalFetchSetting })
	InitHttpClient()
	started, release := make(chan struct{}), make(chan struct{})
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			close(started)
		}
		<-release
		w.Header().Set("Content-Type", "video/mp4")
		_, _ = w.Write([]byte("complete-video"))
	}))
	defer server.Close()
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	task := &model.Task{TaskID: "task_detached", ChannelId: channel.Id, Platform: "kling", Status: model.TaskStatusSuccess,
		Quota: 123, FinishTime: time.Now().Unix(), Progress: "100%",
		PrivateData: model.TaskPrivateData{UpstreamResultURL: server.URL, BillingSource: "wallet", TokenId: 42}}
	require.NoError(t, model.DB.Create(task).Error)
	workerCtx, stop := context.WithCancel(context.Background())
	workerDone := make(chan struct{})
	go func() { queue.run(workerCtx); close(workerDone) }()
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
		stop()
		<-workerDone
	}()
	require.True(t, queue.enqueue(task.TaskID))
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("background download did not start")
	}
	queryCtx, cancelQuery := context.WithCancel(context.Background())
	cancelQuery()
	assert.ErrorIs(t, queue.wait(queryCtx), context.Canceled)
	require.True(t, queue.enqueue(task.TaskID))
	// Simulate a settlement update that happens while the file is downloading.
	privateData, err := common.Marshal(map[string]any{
		"upstream_result_url": server.URL, "billing_source": "subscription", "token_id": 99,
		"unknown_future_field": map[string]any{"preserved": true},
	})
	require.NoError(t, err)
	require.NoError(t, model.DB.Model(task).Updates(map[string]any{"quota": 456, "private_data": string(privateData)}).Error)
	close(release)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, queue.wait(ctx))
	var saved model.Task
	require.NoError(t, model.DB.First(&saved, task.ID).Error)
	assert.Equal(t, int32(1), requests.Load())
	assert.Equal(t, 456, saved.Quota)
	assert.Equal(t, task.FinishTime, saved.FinishTime)
	assert.Equal(t, model.TaskStatus(model.TaskStatusSuccess), saved.Status)
	assert.Equal(t, "subscription", saved.PrivateData.BillingSource)
	assert.Equal(t, 99, saved.PrivateData.TokenId)
	assert.NotZero(t, saved.PrivateData.VideoCachedAt)
	var raw string
	require.NoError(t, model.DB.Model(task).Select("private_data").Scan(&raw).Error)
	assert.Contains(t, raw, "unknown_future_field")
	contents, err := os.ReadFile(videoCacheFilePath(task.TaskID))
	require.NoError(t, err)
	assert.Equal(t, "complete-video", string(contents))
}

func testVideoCacheRetryPagination(t *testing.T) {
	channel, queue := videoCacheWorkerFixture(t)
	now := time.Now().Unix()
	deferred := make([]model.Task, videoCacheRetryBatchSize)
	for i := range deferred {
		deferred[i] = model.Task{TaskID: fmt.Sprintf("task_deferred_%d", i), Platform: "kling", ChannelId: channel.Id,
			Status: model.TaskStatusSuccess, FinishTime: now, PrivateData: model.TaskPrivateData{VideoCacheAttempts: 1, VideoCacheNextRetryAt: now + 300}}
	}
	require.NoError(t, model.DB.Create(&deferred).Error)
	ready := &model.Task{TaskID: "task_after_deferred_page", Platform: "kling", ChannelId: channel.Id, Status: model.TaskStatusSuccess,
		FinishTime: now, PrivateData: model.TaskPrivateData{UpstreamResultURL: "data:video/mp4;base64," + base64.StdEncoding.EncodeToString([]byte("repaired"))}}
	require.NoError(t, model.DB.Create(ready).Error)
	require.NoError(t, queue.scan(context.Background()))
	workerCtx, stop := context.WithCancel(context.Background())
	workerDone := make(chan struct{})
	go func() { queue.run(workerCtx); close(workerDone) }()
	defer func() { stop(); <-workerDone }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, queue.wait(ctx))
	var saved model.Task
	require.NoError(t, model.DB.First(&saved, ready.ID).Error)
	assert.NotZero(t, saved.PrivateData.VideoCachedAt)
	// An HTTP poll may requeue a deferred task, but must not move its retry time.
	require.True(t, queue.enqueue(deferred[0].TaskID))
	require.NoError(t, queue.wait(ctx))
	saved = model.Task{}
	require.NoError(t, model.DB.First(&saved, deferred[0].ID).Error)
	assert.Equal(t, 1, saved.PrivateData.VideoCacheAttempts)
	assert.Equal(t, now+300, saved.PrivateData.VideoCacheNextRetryAt)
}

func testVideoCacheMetadataCAS(t *testing.T) {
	channel, _ := videoCacheWorkerFixture(t)
	task := &model.Task{TaskID: "task_cache_cas", Platform: "kling", ChannelId: channel.Id, Status: model.TaskStatusSuccess, Quota: 321,
		FinishTime: time.Now().Unix(), PrivateData: model.TaskPrivateData{BillingSource: "wallet"}}
	require.NoError(t, model.DB.Create(task).Error)
	expected := task.PrivateData
	MarkVideoTaskCached(task)
	task.PrivateData.ResultURL = "/video-cache/task_cache_cas.mp4"
	won, err := model.UpdateVideoCacheMetadata(task, expected)
	require.NoError(t, err)
	require.True(t, won)
	task.PrivateData = expected
	MarkVideoCacheFailure(task, fmt.Errorf("stale failed transfer"))
	won, err = model.UpdateVideoCacheMetadata(task, expected)
	require.NoError(t, err)
	assert.False(t, won)
	var saved model.Task
	require.NoError(t, model.DB.First(&saved, task.ID).Error)
	assert.NotZero(t, saved.PrivateData.VideoCachedAt)
	assert.Zero(t, saved.PrivateData.VideoCacheAttempts)
	assert.Equal(t, 321, saved.Quota)
	expected = saved.PrivateData
	require.NoError(t, model.DB.Model(task).UpdateColumn("status", model.TaskStatusFailure).Error)
	won, err = model.UpdateVideoCacheMetadata(&saved, expected)
	require.NoError(t, err)
	assert.False(t, won)
}

func TestCacheRemoteVideoWithHeaders(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("VIDEO_CACHE_DIR", cacheDir)
	fetchSetting := system_setting.GetFetchSetting()
	originalFetchSetting := *fetchSetting
	t.Cleanup(func() { *fetchSetting = originalFetchSetting })
	fetchSetting.EnableSSRFProtection = false
	InitHttpClient()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "provider-key", r.Header.Get("x-goog-api-key"))
		w.Header().Set("Content-Type", "video/mp4")
		_, _ = w.Write([]byte("remote-video"))
	}))
	defer server.Close()

	headers := make(http.Header)
	headers.Set("x-goog-api-key", "provider-key")
	path, err := CacheRemoteVideoWithHeaders(context.Background(), "task_remote", server.URL, "", headers)
	require.NoError(t, err)
	contents, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, []byte("remote-video"), contents)
}

func TestCacheVideoSourceSupportsAuthenticatedPost(t *testing.T) {
	t.Setenv("VIDEO_CACHE_DIR", t.TempDir())
	fetchSetting := system_setting.GetFetchSetting()
	originalFetchSetting := *fetchSetting
	t.Cleanup(func() { *fetchSetting = originalFetchSetting })
	fetchSetting.EnableSSRFProtection = false
	InitHttpClient()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "provider-key", r.Header.Get("X-Provider-Key"))
		assert.JSONEq(t, `{"asset":"video"}`, string(body))
		w.Header().Set("Content-Type", "video/mp4")
		_, _ = w.Write([]byte("post-video"))
	}))
	defer server.Close()

	headers := make(http.Header)
	headers.Set("X-Provider-Key", "provider-key")
	path, err := CacheVideoSource(context.Background(), "task_post", VideoCacheSource{
		URL: server.URL, Method: http.MethodPost, Body: []byte(`{"asset":"video"}`), Headers: headers,
	})
	require.NoError(t, err)
	contents, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, []byte("post-video"), contents)
}

func TestCacheVideoSourceRejectsCredentialedCrossOriginRedirect(t *testing.T) {
	t.Setenv("VIDEO_CACHE_DIR", t.TempDir())
	fetchSetting := system_setting.GetFetchSetting()
	originalFetchSetting := *fetchSetting
	t.Cleanup(func() { *fetchSetting = originalFetchSetting })
	fetchSetting.EnableSSRFProtection = false
	InitHttpClient()

	destinationCalled := false
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		destinationCalled = true
		w.Header().Set("Content-Type", "video/mp4")
		_, _ = w.Write([]byte("secret-video"))
	}))
	defer destination.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusFound)
	}))
	defer source.Close()

	headers := make(http.Header)
	headers.Set("Authorization", "Bearer provider-secret")
	_, err := CacheVideoSource(context.Background(), "task_redirect", VideoCacheSource{
		URL: source.URL, Headers: headers,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot redirect across origins")
	assert.False(t, destinationCalled)
}

func TestCacheVideoSourceAllowsCredentiallessCrossOriginRedirect(t *testing.T) {
	t.Setenv("VIDEO_CACHE_DIR", t.TempDir())
	fetchSetting := system_setting.GetFetchSetting()
	originalFetchSetting := *fetchSetting
	t.Cleanup(func() { *fetchSetting = originalFetchSetting })
	fetchSetting.EnableSSRFProtection = false
	InitHttpClient()

	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Empty(t, r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "video/mp4")
		_, _ = w.Write([]byte("redirected-video"))
	}))
	defer destination.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusFound)
	}))
	defer source.Close()

	path, err := CacheVideoSource(context.Background(), "task_public_redirect", VideoCacheSource{URL: source.URL})
	require.NoError(t, err)
	contents, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, []byte("redirected-video"), contents)
}

func TestCacheVideoSourceAllowsTrustedProviderPort(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("VIDEO_CACHE_DIR", cacheDir)
	configureSSRFTestFetchSetting(t)
	fetchSetting := system_setting.GetFetchSetting()
	fetchSetting.AllowPrivateIp = true

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		_, _ = w.Write([]byte("trusted-video"))
	}))
	defer server.Close()

	path, err := CacheVideoSource(context.Background(), "task_trusted_port", VideoCacheSource{
		URL:           server.URL + "/video.mp4",
		TrustedOrigin: server.URL,
	})
	require.NoError(t, err)
	contents, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, []byte("trusted-video"), contents)
}

func TestVideoCacheSourceForTaskUsesContentEndpointAndBearer(t *testing.T) {
	baseURL := "https://upstream.example"
	task := &model.Task{
		TaskID: "task_public",
		PrivateData: model.TaskPrivateData{
			Key:            "provider-key",
			UpstreamTaskID: "provider-task",
		},
	}
	channel := &model.Channel{Type: constant.ChannelTypeSora, Key: "channel-key", BaseURL: &baseURL}

	source, err := VideoCacheSourceForTask(task, channel)
	require.NoError(t, err)
	assert.Equal(t, "https://upstream.example/v1/videos/provider-task/content", source.URL)
	assert.Equal(t, "Bearer provider-key", source.Headers.Get("Authorization"))
	assert.Equal(t, baseURL, source.TrustedOrigin)
}

func TestVideoCacheSourceForTaskPropagatesDedicatedProxySelection(t *testing.T) {
	baseURL := "https://upstream.example"
	task := &model.Task{
		TaskID: "task_public",
		PrivateData: model.TaskPrivateData{
			Key:            "provider-key",
			UpstreamTaskID: "provider-task",
		},
	}
	channel := &model.Channel{Type: constant.ChannelTypeSora, Key: "channel-key", BaseURL: &baseURL}
	channel.SetSetting(dto.ChannelSettings{VideoCacheProxyEnabled: true})

	source, err := VideoCacheSourceForTask(task, channel)
	require.NoError(t, err)
	assert.True(t, source.UseDedicatedProxy)
}

func TestVideoCacheSourceForTaskDoesNotSendCredentialsToExternalResultURL(t *testing.T) {
	previousServerAddress := system_setting.ServerAddress
	system_setting.ServerAddress = "https://api.example"
	t.Cleanup(func() { system_setting.ServerAddress = previousServerAddress })
	baseURL := "https://gateway.example"

	task := &model.Task{
		TaskID: "task_public",
		PrivateData: model.TaskPrivateData{
			Key:            "provider-key",
			UpstreamTaskID: "provider-task",
			ResultURL:      "https://upstream.example/v1/videos/provider-task/content",
		},
	}
	channel := &model.Channel{Type: constant.ChannelTypeOpenAIVideo, BaseURL: &baseURL}

	source, err := VideoCacheSourceForTask(task, channel)
	require.NoError(t, err)
	assert.Equal(t, "https://upstream.example/v1/videos/provider-task/content", source.URL)
	assert.Empty(t, source.Headers.Get("Authorization"))
}

func TestVideoCacheSourceForTaskUsesGrokContentEndpoint(t *testing.T) {
	baseURL := "https://upstream.example/"
	task := &model.Task{
		TaskID: "task_public",
		PrivateData: model.TaskPrivateData{
			Key:            "provider-key",
			UpstreamTaskID: "provider-task",
		},
	}
	channel := &model.Channel{Type: constant.ChannelTypeGrokVideo, BaseURL: &baseURL}

	source, err := VideoCacheSourceForTask(task, channel)
	require.NoError(t, err)
	assert.Equal(t, "https://upstream.example/v1/videos/provider-task/content", source.URL)
	assert.Equal(t, "Bearer provider-key", source.Headers.Get("Authorization"))
}

func TestVideoCacheSourceForTaskUsesMiniMaxVideoContentEndpoint(t *testing.T) {
	baseURL := "https://upstream.example/"
	task := &model.Task{
		TaskID: "task_public",
		PrivateData: model.TaskPrivateData{
			Key:            "provider-key",
			UpstreamTaskID: "provider-task",
		},
	}
	channel := &model.Channel{Type: constant.ChannelTypeMiniMaxVideo, BaseURL: &baseURL}

	source, err := VideoCacheSourceForTask(task, channel)
	require.NoError(t, err)
	assert.Equal(t, "https://upstream.example/v1/videos/provider-task/content", source.URL)
	assert.Equal(t, "Bearer provider-key", source.Headers.Get("Authorization"))
}

func TestVideoCacheSourceIgnoresNonURLFailureReason(t *testing.T) {
	baseURL := "https://upstream.example"
	task := &model.Task{
		TaskID:     "task_public",
		FailReason: "upstream reported a temporary failure",
		PrivateData: model.TaskPrivateData{
			UpstreamTaskID: "provider-task",
		},
	}
	channel := &model.Channel{Type: constant.ChannelTypeSora, BaseURL: &baseURL}

	source, err := VideoCacheSourceForTask(task, channel)
	require.NoError(t, err)
	assert.Equal(t, "https://upstream.example/v1/videos/provider-task/content", source.URL)
}

func TestSanitizeVideoTaskReasonRemovesProviderURL(t *testing.T) {
	reason := "download failed: https://upstream.example/private/video.mp4 (task provider-task)"
	redacted := SanitizeVideoTaskReason(reason, "provider-task")
	assert.Equal(t, "download failed: [redacted] (task [redacted])", redacted)
}

func TestExtractVideoDataURL(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString([]byte("inline-video"))
	body := []byte(`{"response":{"videos":[{"mimeType":"video/mp4","bytesBase64Encoded":"` + encoded + `"}]}}`)

	assert.Equal(t, "data:video/mp4;base64,"+encoded, ExtractVideoDataURL(body))
}

func TestSanitizeOpenAIVideoResponseAddsLocalResultURL(t *testing.T) {
	body := []byte(`{"id":"provider-task","metadata":{"url":"https://upstream.example/video.mp4"}}`)
	localURL := "https://api.example/video-cache/task_public.mp4"
	sanitized := SanitizeOpenAIVideoResponse(body, "task_public", "provider-task", localURL)

	assert.NotContains(t, string(sanitized), "upstream.example")
	assert.NotContains(t, string(sanitized), "provider-task")
	assert.Contains(t, string(sanitized), localURL)
	assert.NotContains(t, string(sanitized), "https://api.example/v1/videos")
}
