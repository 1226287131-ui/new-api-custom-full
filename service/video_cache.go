package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
)

const (
	defaultVideoCacheDir             = "/data/video-cache"
	defaultVideoCacheTTL             = 48 * time.Hour
	defaultVideoCacheCleanupInterval = time.Hour
	videoCacheRetryInterval          = 30 * time.Second
	videoCacheRetryBatchSize         = 100
)

func videoCacheDir() string {
	dir := strings.TrimSpace(os.Getenv("VIDEO_CACHE_DIR"))
	if dir == "" {
		return defaultVideoCacheDir
	}
	return dir
}

func videoCacheFilePath(taskID string) string {
	safeID := strings.TrimSpace(taskID)
	safeID = strings.NewReplacer("/", "_", "\\", "_", "..", "_").Replace(safeID)
	return filepath.Join(videoCacheDir(), safeID+".mp4")
}

// CachedVideoPath returns a complete, atomically-written cache file.
func CachedVideoPath(taskID string) (string, bool) {
	if strings.TrimSpace(taskID) == "" {
		return "", false
	}
	path := videoCacheFilePath(taskID)
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 {
		return "", false
	}
	return path, true
}

// VideoCacheSource describes one authenticated or inline video source.
// Exactly one of URL and DataURL should normally be set.
type VideoCacheSource struct {
	URL               string
	DataURL           string
	Method            string
	Body              []byte
	Headers           http.Header
	Credentialless    bool
	Proxy             string
	UseDedicatedProxy bool
	TrustedOrigin     string // configured channel origin; never taken from user input
}

// CacheVideoSource stores a remote or data URL video in the local cache.
func CacheVideoSource(ctx context.Context, taskID string, source VideoCacheSource) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if cachedPath, ok := CachedVideoPath(taskID); ok {
		return cachedPath, nil
	}

	if strings.TrimSpace(source.DataURL) != "" {
		return cacheVideoDataURL(taskID, source.DataURL)
	}

	remoteURL := strings.TrimSpace(source.URL)
	if remoteURL == "" {
		return "", fmt.Errorf("video cache source URL is empty")
	}
	if strings.HasPrefix(strings.ToLower(remoteURL), "data:") {
		return cacheVideoDataURL(taskID, remoteURL)
	}
	if err := validateVideoCacheFetchURL(source, remoteURL); err != nil {
		return "", fmt.Errorf("video cache URL blocked: %w", err)
	}

	client, err := newVideoCacheHTTPClient(source)
	if err != nil {
		return "", fmt.Errorf("create video cache client: %w", err)
	}
	defer client.CloseIdleConnections()

	timeoutSeconds := common.GetEnvOrDefault("VIDEO_CACHE_DOWNLOAD_TIMEOUT_SECONDS", 600)
	if timeoutSeconds <= 0 {
		timeoutSeconds = 600
	}
	downloadCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSeconds)*time.Second)
	defer cancel()

	method := strings.ToUpper(strings.TrimSpace(source.Method))
	if method == "" {
		method = http.MethodGet
	}
	if method != http.MethodGet && method != http.MethodPost {
		return "", fmt.Errorf("video cache request method %s is not supported", method)
	}
	if len(source.Body) > 1<<20 {
		return "", fmt.Errorf("video cache request body exceeds 1 MiB")
	}
	if source.Credentialless && (method != http.MethodGet || len(source.Body) != 0 || len(source.Headers) != 0) {
		return "", fmt.Errorf("credentialless video cache request contains credentials or a body")
	}

	req, err := http.NewRequestWithContext(downloadCtx, method, remoteURL, bytes.NewReader(source.Body))
	if err != nil {
		return "", fmt.Errorf("create video cache request: %w", err)
	}
	req.Header.Set("Accept", "video/*,application/octet-stream;q=0.9,*/*;q=0.1")
	for key, values := range source.Headers {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("download video for cache: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", fmt.Errorf("video cache upstream returned status %d", resp.StatusCode)
	}
	contentType := strings.ToLower(strings.TrimSpace(strings.Split(resp.Header.Get("Content-Type"), ";")[0]))
	if contentType == "application/json" || contentType == "text/html" || contentType == "text/plain" {
		return "", fmt.Errorf("video cache upstream returned non-video content type %s", contentType)
	}

	return cacheVideoReader(taskID, resp.Body)
}

// CacheRemoteVideo downloads a completed video to the local cache using a
// Bearer token. It is kept for existing callers and simple OpenAI-compatible
// providers.
func CacheRemoteVideo(ctx context.Context, taskID, remoteURL, proxy, apiKey string) (string, error) {
	headers := make(http.Header)
	if strings.TrimSpace(apiKey) != "" {
		headers.Set("Authorization", "Bearer "+strings.TrimSpace(apiKey))
	}
	return CacheVideoSource(ctx, taskID, VideoCacheSource{
		URL:     remoteURL,
		Headers: headers,
		Proxy:   proxy,
	})
}

// CacheRemoteVideoWithHeaders downloads a completed video with provider-
// specific headers such as x-goog-api-key or Token authentication.
func CacheRemoteVideoWithHeaders(ctx context.Context, taskID, remoteURL, proxy string, headers http.Header) (string, error) {
	return CacheVideoSource(ctx, taskID, VideoCacheSource{
		URL:     remoteURL,
		Headers: headers,
		Proxy:   proxy,
	})
}

// CacheVideoDataURL decodes an inline base64 video into the local cache.
func CacheVideoDataURL(_ context.Context, taskID, dataURL string) (string, error) {
	return cacheVideoDataURL(taskID, dataURL)
}

func cacheVideoDataURL(taskID, dataURL string) (string, error) {
	parts := strings.SplitN(strings.TrimSpace(dataURL), ",", 2)
	if len(parts) != 2 || !strings.HasPrefix(strings.ToLower(parts[0]), "data:") || !strings.Contains(strings.ToLower(parts[0]), ";base64") {
		return "", fmt.Errorf("invalid base64 video data URL")
	}

	payload := strings.TrimSpace(parts[1])
	decoder := base64.StdEncoding
	if !strings.Contains(payload, "=") && len(payload)%4 != 0 {
		decoder = base64.RawStdEncoding
	}
	return cacheVideoReader(taskID, base64.NewDecoder(decoder, strings.NewReader(payload)))
}

func cacheVideoReader(taskID string, reader io.Reader) (string, error) {
	maxMB := common.GetEnvOrDefault("VIDEO_CACHE_MAX_MB", 1024)
	if maxMB <= 0 {
		maxMB = 1024
	}
	maxBytes := int64(maxMB) * 1024 * 1024

	dir := videoCacheDir()
	if err := os.MkdirAll(dir, 0750); err != nil {
		return "", fmt.Errorf("create video cache directory: %w", err)
	}

	tmp, err := os.CreateTemp(dir, ".video-cache-*.tmp")
	if err != nil {
		return "", fmt.Errorf("create video cache temp file: %w", err)
	}
	tmpPath := tmp.Name()
	cleanupTemp := true
	defer func() {
		_ = tmp.Close()
		if cleanupTemp {
			_ = os.Remove(tmpPath)
		}
	}()

	written, err := io.Copy(tmp, io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return "", fmt.Errorf("write video cache: %w", err)
	}
	if written > maxBytes {
		return "", fmt.Errorf("video cache exceeds %d MB limit", maxMB)
	}
	if written <= 0 {
		return "", fmt.Errorf("video cache response is empty")
	}
	if err := tmp.Sync(); err != nil {
		return "", fmt.Errorf("sync video cache: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("close video cache temp file: %w", err)
	}

	finalPath := videoCacheFilePath(taskID)
	if err := os.Rename(tmpPath, finalPath); err != nil {
		return "", fmt.Errorf("commit video cache: %w", err)
	}
	cleanupTemp = false
	_ = os.Chmod(finalPath, 0640)
	return finalPath, nil
}

// CleanupVideoCache removes files older than the retention period.
func CleanupVideoCache() (int, error) {
	entries, err := os.ReadDir(videoCacheDir())
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}

	cutoff := time.Now().Add(-defaultVideoCacheTTL)
	removed := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".mp4") && !strings.HasPrefix(name, ".video-cache-") {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || info.ModTime().After(cutoff) {
			continue
		}
		if err := os.Remove(filepath.Join(videoCacheDir(), name)); err != nil {
			return removed, err
		}
		removed++
	}
	return removed, nil
}

// StartVideoCacheCleanup runs an immediate cleanup and then checks hourly.
func StartVideoCacheCleanup() {
	cleanup := func() {
		removed, err := CleanupVideoCache()
		if err != nil {
			common.SysError(fmt.Sprintf("video cache cleanup failed: %v", err))
			return
		}
		if removed > 0 {
			common.SysLog(fmt.Sprintf("video cache cleanup removed %d expired files", removed))
		}
	}

	cleanup()
	go func() {
		ticker := time.NewTicker(defaultVideoCacheCleanupInterval)
		defer ticker.Stop()
		for range ticker.C {
			cleanup()
		}
	}()
}

func videoCacheRetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := 30 * time.Second
	for i := 1; i < attempt; i++ {
		delay *= 2
		if delay >= 10*time.Minute {
			return 10 * time.Minute
		}
	}
	return delay
}

func truncateVideoCacheError(err error) string {
	if err == nil {
		return ""
	}
	message := strings.TrimSpace(err.Error())
	if len(message) > 512 {
		message = message[:512]
	}
	return message
}

type videoCacheQueue struct {
	jobs    chan string
	mu      sync.Mutex
	pending map[string]struct{}
	idle    chan struct{}
}

func newVideoCacheQueue(capacity int) *videoCacheQueue {
	idle := make(chan struct{})
	close(idle)
	return &videoCacheQueue{jobs: make(chan string, capacity), pending: make(map[string]struct{}), idle: idle}
}

var videoCacheQueueMu sync.RWMutex
var activeVideoCacheQueue *videoCacheQueue

// enqueue reserves the task before publishing it so polling requests cannot
// create duplicate transfers. A full queue is recovered by the durable scanner.
func (queue *videoCacheQueue) enqueue(taskID string) bool {
	if strings.TrimSpace(taskID) == "" {
		return true
	}
	queue.mu.Lock()
	defer queue.mu.Unlock()
	if _, exists := queue.pending[taskID]; exists {
		return true
	}
	select {
	case queue.jobs <- taskID:
		if len(queue.pending) == 0 {
			queue.idle = make(chan struct{})
		}
		queue.pending[taskID] = struct{}{}
		return true
	default:
		return false
	}
}

func (queue *videoCacheQueue) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case taskID := <-queue.jobs:
			if err := cacheSuccessfulVideoTask(taskID); err != nil {
				common.SysError(fmt.Sprintf("video task %s background cache failed: %v", taskID, err))
			}
			queue.mu.Lock()
			delete(queue.pending, taskID)
			if len(queue.pending) == 0 {
				close(queue.idle)
			}
			queue.mu.Unlock()
		}
	}
}

func (queue *videoCacheQueue) wait(ctx context.Context) error {
	queue.mu.Lock()
	idle := queue.idle
	queue.mu.Unlock()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-idle:
		return nil
	}
}

// QueueVideoTaskCache never borrows the querying client's context or blocks a
// response on a download. Only persisted successful legacy video tasks are read.
func QueueVideoTaskCache(taskID string) {
	videoCacheQueueMu.RLock()
	queue := activeVideoCacheQueue
	videoCacheQueueMu.RUnlock()
	if queue != nil {
		queue.enqueue(taskID)
	}
}

var videoCacheRetryOnce sync.Once

// StartVideoCacheRetry starts asynchronously: slow downloads must not hold up
// application startup or the normal async-task polling/settlement worker.
func StartVideoCacheRetry() {
	videoCacheRetryOnce.Do(func() {
		queue := newVideoCacheQueue(videoCacheRetryBatchSize)
		workers := max(1, min(common.GetEnvOrDefault("VIDEO_CACHE_WORKERS", 2), 8))
		for range workers {
			go queue.run(context.Background())
		}
		videoCacheQueueMu.Lock()
		activeVideoCacheQueue = queue
		videoCacheQueueMu.Unlock()
		go func() {
			ticker := time.NewTicker(videoCacheRetryInterval)
			defer ticker.Stop()
			for {
				if err := queue.scan(context.Background()); err != nil {
					common.SysError(fmt.Sprintf("video cache retry pass failed: %v", err))
				}
				<-ticker.C
			}
		}()
	})
}

func (queue *videoCacheQueue) scan(ctx context.Context) error {
	now := time.Now()
	var afterID int64
	for {
		tasks, err := model.GetSuccessfulVideoTasksForCachePage(ctx, now.Add(-defaultVideoCacheTTL).Unix(), afterID, videoCacheRetryBatchSize)
		if err != nil {
			return err
		}
		for _, task := range tasks {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			afterID = task.ID
			if !videoTaskUsesBackgroundCache(task) || VideoCacheExpired(task) || task.PrivateData.VideoCacheNextRetryAt > now.Unix() {
				continue
			}
			if task.PrivateData.VideoCachedAt > 0 {
				if _, exists := CachedVideoPath(task.TaskID); exists {
					continue
				}
			}
			if !queue.enqueue(task.TaskID) {
				return nil
			}
		}
		if len(tasks) < videoCacheRetryBatchSize {
			return nil
		}
	}
}

// RetryVideoTaskCaches schedules a repair pass and waits for its current queue.
// Canceling that wait does not cancel downloads already accepted by workers.
func RetryVideoTaskCaches(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	queue := newVideoCacheQueue(videoCacheRetryBatchSize)
	workerCtx, stop := context.WithCancel(context.Background())
	var workers sync.WaitGroup
	for range 2 {
		workers.Go(func() { queue.run(workerCtx) })
	}
	defer func() { stop(); workers.Wait() }()
	if err := queue.scan(ctx); err != nil {
		return err
	}
	return queue.wait(ctx)
}

func videoTaskUsesBackgroundCache(task *model.Task) bool {
	return task != nil && task.Status == model.TaskStatusSuccess && constant.IsVideoTaskPlatform(task.Platform) &&
		(task.PrivateData.Execution == nil || task.PrivateData.Execution.TaskPlugin == nil)
}

func cacheSuccessfulVideoTask(taskID string) error {
	task, exists, err := model.GetUniqueByOnlyTaskId(taskID)
	if err != nil || !exists {
		return err
	}
	if !videoTaskUsesBackgroundCache(task) || VideoCacheExpired(task) {
		return nil
	}
	expected := task.PrivateData
	_, cached := CachedVideoPath(task.TaskID)
	if !cached {
		if task.PrivateData.VideoCacheNextRetryAt > time.Now().Unix() {
			return nil
		}
		channel, channelErr := model.CacheGetChannel(task.ChannelId)
		if channelErr != nil || channel == nil || !constant.IsVideoTaskChannelType(channel.Type) {
			err = fmt.Errorf("video cache channel is unavailable")
		} else {
			_, err = CacheVideoTask(context.Background(), task, channel)
		}
		if err != nil {
			MarkVideoCacheFailure(task, err)
			if isLocalVideoProxySource(task.PrivateData.ResultURL) {
				task.PrivateData.ResultURL = ""
			}
		} else {
			cached = true
		}
	}
	if cached {
		MarkVideoTaskCached(task)
		task.PrivateData.ResultURL = taskcommon.BuildPublicVideoURL(task.TaskID)
	}
	if _, updateErr := model.UpdateVideoCacheMetadata(task, expected); updateErr != nil {
		return fmt.Errorf("persist video cache metadata: %w", updateErr)
	}
	return err
}
