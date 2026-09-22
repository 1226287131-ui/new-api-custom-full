package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	taskdto "github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	relaycommon "github.com/QuantumNous/new-api/relay/common"

	"github.com/bytedance/gopkg/util/gopool"
	"github.com/samber/lo"
)

// TaskPollingAdaptor 定义轮询所需的最小适配器接口，避免 service -> relay 的循环依赖
type TaskPollingAdaptor interface {
	Init(info *relaycommon.RelayInfo)
	FetchTask(baseURL string, key string, task *model.Task, proxy string) (*http.Response, error)
	ParseTaskResult(task *model.Task, resp *http.Response, body []byte) (*relaycommon.TaskInfo, error)
	// AdjustBillingOnComplete 在任务到达终态（成功/失败）时由轮询循环调用。
	// 返回正数触发差额结算（补扣/退还），返回 0 保持预扣费金额不变。
	AdjustBillingOnComplete(task *model.Task, taskResult *relaycommon.TaskInfo) int
}

type BatchTaskPollingAdaptor interface {
	TaskPollingAdaptor
	FetchMode() string
	FetchBatchTasks(baseURL, key string, tasks []*model.Task, proxy string) (*http.Response, error)
	ParseBatchResult(tasks []*model.Task, resp *http.Response, body []byte) (map[string]*BatchTaskResult, error)
}

const (
	pollClassOK           = "ok"
	pollClassOtherClient  = "other_client"
	pollClassNotFound     = "not_found"
	pollClassAuth         = "auth"
	pollClassTransient    = "transient"
	pollClassUnrecognized = "unrecognized"
	pollClassHookError    = "hook_error"
	pollClassTransport    = "transport_error"
)

type BatchTaskResult struct {
	TaskInfo   relaycommon.TaskInfo
	Action     string
	SubmitTime int64
	StartTime  int64
	FinishTime int64
	Data       any
}

// GetTaskAdaptorFunc 由 main 包注入，用于获取指定平台的任务适配器。
// 打破 service -> relay -> relay/channel -> service 的循环依赖。
var GetTaskAdaptorFunc func(platform constant.TaskPlatform) TaskPollingAdaptor

// sweepTimedOutTasks 在主轮询之前独立清理超时任务。
// 每次最多处理 100 条，剩余的下个周期继续处理。
// 使用 per-task CAS (UpdateWithStatus) 防止覆盖被正常轮询已推进的任务。
func sweepTimedOutTasks(ctx context.Context) {
	if constant.TaskTimeoutMinutes <= 0 {
		return
	}
	cutoff := time.Now().Unix() - int64(constant.TaskTimeoutMinutes)*60
	tasks := model.GetTimedOutUnfinishedTasks(cutoff, 100)
	if len(tasks) == 0 {
		return
	}

	reason := fmt.Sprintf("任务超时（%d分钟）", constant.TaskTimeoutMinutes)
	legacyReason := "任务超时（旧系统遗留任务，不进行退款，请联系管理员）"
	now := time.Now().Unix()
	timedOutCount := 0

	for _, task := range tasks {
		isLegacy := task.SubmitTime > 0 && task.SubmitTime < model.TaskRefundLegacyCutoff

		oldStatus := task.Status
		task.Status = model.TaskStatusFailure
		task.Progress = "100%"
		task.FinishTime = now
		if isLegacy {
			task.FailReason = legacyReason
			// 旧系统任务明确不退款，随终态 CAS 一并清掉 quota，
			// 避免留下可再次退款的计费状态。
			task.Quota = 0
		} else {
			task.FailReason = reason
		}

		won, err := task.UpdateWithStatus(oldStatus)
		if err != nil {
			logger.LogError(ctx, fmt.Sprintf("sweepTimedOutTasks CAS update error for task %s: %v", task.TaskID, err))
			continue
		}
		if !won {
			logger.LogInfo(ctx, fmt.Sprintf("sweepTimedOutTasks: task %s already transitioned, skip", task.TaskID))
			continue
		}
		timedOutCount++
		if !isLegacy && task.Quota != 0 {
			RefundTaskQuota(ctx, task, reason)
		}
	}

	if timedOutCount > 0 {
		logger.LogInfo(ctx, fmt.Sprintf("sweepTimedOutTasks: timed out %d tasks", timedOutCount))
	}
}

// TaskPollSummary is the result recorded on an async_task_poll system task row,
// summarizing one polling pass.
type TaskPollSummary struct {
	UnfinishedTasks  int `json:"unfinished_tasks"`
	PlatformsScanned int `json:"platforms_scanned"`
	NullTasksFailed  int `json:"null_tasks_failed"`
}

// RunTaskPollingOnce performs one async-task (Suno/video) polling pass
// synchronously. It honors ctx cancellation (the system-task runner cancels it
// when the lease is lost) and, when report is non-nil, reports progress as
// (processedPlatforms, totalPlatforms). It returns immediately if the task
// adaptor factory has not been wired yet, to avoid a nil call during startup.
func RunTaskPollingOnce(ctx context.Context, report func(processed, total int)) TaskPollSummary {
	summary := TaskPollSummary{}
	if GetTaskAdaptorFunc == nil {
		return summary
	}
	if ctx == nil {
		ctx = context.Background()
	}

	common.SysLog("任务进度轮询开始")
	sweepTimedOutTasks(ctx)
	allTasks := model.GetAllUnFinishSyncTasks(constant.TaskQueryLimit)
	summary.UnfinishedTasks = len(allTasks)
	platformTask := make(map[constant.TaskPlatform][]*model.Task)
	for _, t := range allTasks {
		platformTask[t.Platform] = append(platformTask[t.Platform], t)
	}

	totalPlatforms := len(platformTask)
	processedPlatforms := 0
	for platform, tasks := range platformTask {
		if ctx.Err() != nil {
			break
		}
		if report != nil {
			report(processedPlatforms, totalPlatforms)
		}
		processedPlatforms++
		if len(tasks) == 0 {
			continue
		}
		summary.PlatformsScanned++
		taskChannelM := make(map[int][]string)
		taskM := make(map[string]*model.Task)
		nullTaskIds := make([]int64, 0)
		for _, task := range tasks {
			upstreamID := task.GetUpstreamTaskID()
			if upstreamID == "" {
				// 统计失败的未完成任务
				nullTaskIds = append(nullTaskIds, task.ID)
				continue
			}
			taskM[upstreamID] = task
			taskChannelM[task.ChannelId] = append(taskChannelM[task.ChannelId], upstreamID)
		}
		if len(nullTaskIds) > 0 {
			summary.NullTasksFailed += len(nullTaskIds)
			err := model.TaskBulkUpdateByID(nullTaskIds, map[string]any{
				"status":   "FAILURE",
				"progress": "100%",
			})
			if err != nil {
				logger.LogError(ctx, fmt.Sprintf("Fix null task_id task error: %v", err))
			} else {
				logger.LogInfo(ctx, fmt.Sprintf("Fix null task_id task success: %v", nullTaskIds))
			}
		}
		if len(taskChannelM) == 0 {
			continue
		}

		DispatchPlatformUpdate(ctx, platform, taskChannelM, taskM)
	}
	if report != nil && ctx.Err() == nil {
		report(totalPlatforms, totalPlatforms)
	}
	common.SysLog("任务进度轮询完成")
	return summary
}

// DispatchPlatformUpdate 按平台分发轮询更新
func DispatchPlatformUpdate(ctx context.Context, platform constant.TaskPlatform, taskChannelM map[int][]string, taskM map[string]*model.Task) {
	if ctx == nil {
		ctx = context.Background()
	}
	if platform == constant.TaskPlatformMidjourney {
		// MJ 轮询由其自身处理，这里预留入口
		return
	}
	adaptor := GetTaskAdaptorFunc(platform)
	if batchAdaptor, ok := adaptor.(BatchTaskPollingAdaptor); ok && batchAdaptor.FetchMode() == "batch" {
		if err := UpdateBatchTasks(ctx, batchAdaptor, taskChannelM, taskM); err != nil {
			common.SysLog(fmt.Sprintf("UpdateBatchTasks fail: %s", err))
		}
		return
	}
	if err := UpdateVideoTasks(ctx, platform, taskChannelM, taskM); err != nil {
		common.SysLog(fmt.Sprintf("UpdateVideoTasks fail: %s", err))
	}
}

func UpdateBatchTasks(ctx context.Context, adaptor BatchTaskPollingAdaptor, taskChannelM map[int][]string, taskM map[string]*model.Task) error {
	for channelId, taskIds := range taskChannelM {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		err := updateBatchTasks(ctx, adaptor, channelId, taskIds, taskM)
		if err != nil {
			logger.LogError(ctx, fmt.Sprintf("渠道 #%d 更新异步任务失败: %s", channelId, err.Error()))
		}
	}
	return nil
}

func updateBatchTasks(ctx context.Context, adaptor BatchTaskPollingAdaptor, channelId int, taskIds []string, taskM map[string]*model.Task) error {
	logger.LogInfo(ctx, fmt.Sprintf("渠道 #%d 未完成的任务有: %d", channelId, len(taskIds)))
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if len(taskIds) == 0 {
		return nil
	}
	ch, err := model.CacheGetChannel(channelId)
	if err != nil {
		common.SysLog(fmt.Sprintf("CacheGetChannel: %v", err))
		// Collect DB primary key IDs for bulk update (taskIds are upstream IDs, not task_id column values)
		var failedIDs []int64
		for _, upstreamID := range taskIds {
			if t, ok := taskM[upstreamID]; ok {
				failedIDs = append(failedIDs, t.ID)
			}
		}
		err = model.TaskBulkUpdateByID(failedIDs, map[string]any{
			"fail_reason": fmt.Sprintf("获取渠道信息失败，请联系管理员，渠道ID：%d", channelId),
			"status":      "FAILURE",
			"progress":    "100%",
		})
		if err != nil {
			common.SysLog(fmt.Sprintf("UpdateSunoTask error: %v", err))
		}
		return err
	}
	proxy := ResolveChannelProxy(ch.GetSetting())
	baseURL := ch.GetBaseURL()
	if baseURL == "" {
		baseURL = constant.GetChannelBaseURL(ch.Type)
	}
	tasks := make([]*model.Task, 0, len(taskIds))
	for _, upstreamID := range taskIds {
		if task := taskM[upstreamID]; task != nil {
			tasks = append(tasks, task)
		}
	}
	info := &relaycommon.RelayInfo{}
	info.ChannelMeta = &relaycommon.ChannelMeta{ChannelBaseUrl: baseURL}
	info.ApiKey = ch.Key
	adaptor.Init(info)
	resp, err := adaptor.FetchBatchTasks(baseURL, ch.Key, tasks, proxy)
	if err != nil {
		common.SysLog(fmt.Sprintf("Get Task Do req error: %v", err))
		return recordPollFailureForTasks(ctx, adaptor, tasks, pollClassTransport, 0, err.Error())
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		common.SysLog(fmt.Sprintf("Get Suno Task parse body error: %v", err))
		return recordPollFailureForTasks(ctx, adaptor, tasks, pollClassTransport, resp.StatusCode, err.Error())
	}
	switch classifyPollHTTP(resp.StatusCode) {
	case pollClassNotFound:
		return failTasksFromPoll(ctx, adaptor, tasks, fmt.Sprintf("upstream task not found (HTTP %d)", resp.StatusCode))
	case pollClassAuth:
		logger.LogWarn(ctx, fmt.Sprintf("task poll auth failure channel_id=%d http=%d", channelId, resp.StatusCode))
		return recordPollFailureForTasks(ctx, adaptor, tasks, pollClassAuth, resp.StatusCode, "")
	case pollClassTransient:
		return recordPollFailureForTasks(ctx, adaptor, tasks, pollClassTransient, resp.StatusCode, "")
	}
	responseItems, err := adaptor.ParseBatchResult(tasks, resp, responseBody)
	if err != nil {
		return recordPollFailureForTasks(ctx, adaptor, tasks, pollClassHookError, resp.StatusCode, err.Error())
	}
	for upstreamID, responseItem := range responseItems {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		task := taskM[upstreamID]
		if task == nil {
			logger.LogWarn(ctx, fmt.Sprintf("Batch task response ignored: unknown task_id=%s", upstreamID))
			continue
		}
		snap := task.Snapshot()
		httpClass := classifyPollHTTP(resp.StatusCode)
		parsedStatus := model.TaskStatus(responseItem.TaskInfo.Status)
		if parsedStatus == model.TaskStatusUnknown || parsedStatus == "" || !knownPollStatus(parsedStatus) {
			if err := recordPollFailure(ctx, adaptor, task, snap.Status, pollClassUnrecognized, resp.StatusCode, responseItem.TaskInfo.Reason); err != nil {
				common.SysLog("UpdateSunoTask task error: " + err.Error())
			}
			continue
		}
		if httpClass == pollClassOtherClient && isNonTerminalPollStatus(parsedStatus) {
			if err := recordPollFailure(ctx, adaptor, task, snap.Status, pollClassUnrecognized, resp.StatusCode, responseItem.TaskInfo.Reason); err != nil {
				common.SysLog("UpdateSunoTask task error: " + err.Error())
			}
			continue
		}
		if isNonTerminalPollStatus(parsedStatus) {
			task.PrivateData.PollFailures = 0
		}
		if len(responseItem.TaskInfo.PluginState) > 0 {
			task.PrivateData.PluginState = responseItem.TaskInfo.PluginState
		}
		task.Status = lo.If(parsedStatus != "", parsedStatus).Else(task.Status)
		task.FailReason = lo.If(responseItem.TaskInfo.Reason != "", responseItem.TaskInfo.Reason).Else(task.FailReason)
		task.SubmitTime = lo.If(responseItem.SubmitTime != 0, responseItem.SubmitTime).Else(task.SubmitTime)
		task.StartTime = lo.If(responseItem.StartTime != 0, responseItem.StartTime).Else(task.StartTime)
		task.FinishTime = lo.If(responseItem.FinishTime != 0, responseItem.FinishTime).Else(task.FinishTime)
		if responseItem.TaskInfo.Progress != "" {
			task.Progress = responseItem.TaskInfo.Progress
		}
		if responseItem.TaskInfo.Reason != "" || task.Status == model.TaskStatusFailure {
			logger.LogInfo(ctx, task.TaskID+" 构建失败，"+task.FailReason)
			task.Status = model.TaskStatusFailure
			task.Progress = "100%"
		}
		if responseItem.TaskInfo.Status == model.TaskStatusSuccess {
			task.Progress = "100%"
		}
		if responseItem.Data != nil {
			task.SetData(responseItem.Data)
		} else if task.Status == model.TaskStatusSuccess || task.Status == model.TaskStatusFailure {
			logger.LogWarn(ctx, fmt.Sprintf(
				"Batch task %s reached terminal status without data; preserving existing task data",
				task.TaskID,
			))
		}
		if responseItem.TaskInfo.Url != "" {
			task.PrivateData.ResultURL = responseItem.TaskInfo.Url
		}

		isDone := task.Status == model.TaskStatusSuccess || task.Status == model.TaskStatusFailure
		terminalTransition := isDone && snap.Status != task.Status
		won, updateErr := task.UpdateWithStatus(snap.Status)
		if updateErr != nil {
			common.SysLog("UpdateSunoTask task error: " + updateErr.Error())
			continue
		}
		if !won {
			logger.LogWarn(ctx, fmt.Sprintf("Batch task %s already transitioned by another process, skip billing", task.TaskID))
			continue
		}
		if terminalTransition {
			billingSettled := settleTaskBillingOnComplete(ctx, adaptor, task, &responseItem.TaskInfo)
			if task.Status == model.TaskStatusFailure && !billingSettled && task.Quota != 0 {
				RefundTaskQuota(ctx, task, task.FailReason)
			}
		}
	}
	return nil
}

// UpdateVideoTasks 按渠道更新所有视频任务
func UpdateVideoTasks(ctx context.Context, platform constant.TaskPlatform, taskChannelM map[int][]string, taskM map[string]*model.Task) error {
	channelIDs := make([]int, 0, len(taskChannelM))
	for channelID := range taskChannelM {
		channelIDs = append(channelIDs, channelID)
	}
	sort.Ints(channelIDs)

	var wg sync.WaitGroup
	for _, channelId := range channelIDs {
		taskIds := taskChannelM[channelId]
		if len(taskIds) == 0 {
			continue
		}
		taskIds = append([]string(nil), taskIds...)

		wg.Add(1)
		gopool.Go(func() {
			defer wg.Done()
			if err := updateVideoTasks(ctx, platform, channelId, taskIds, taskM); err != nil {
				logger.LogError(ctx, fmt.Sprintf("Channel #%d failed to update video async tasks: %s", channelId, err.Error()))
			}
		})
	}
	wg.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return nil
}

func updateVideoTasks(ctx context.Context, platform constant.TaskPlatform, channelId int, taskIds []string, taskM map[string]*model.Task) error {
	logger.LogInfo(ctx, fmt.Sprintf("Channel #%d pending video tasks: %d", channelId, len(taskIds)))
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if len(taskIds) == 0 {
		return nil
	}
	cacheGetChannel, err := model.CacheGetChannel(channelId)
	if err != nil {
		// Collect DB primary key IDs for bulk update (taskIds are upstream IDs, not task_id column values)
		var failedIDs []int64
		for _, upstreamID := range taskIds {
			if t, ok := taskM[upstreamID]; ok {
				failedIDs = append(failedIDs, t.ID)
			}
		}
		errUpdate := model.TaskBulkUpdateByID(failedIDs, map[string]any{
			"fail_reason": fmt.Sprintf("Failed to get channel info, channel ID: %d", channelId),
			"status":      "FAILURE",
			"progress":    "100%",
		})
		if errUpdate != nil {
			common.SysLog(fmt.Sprintf("UpdateVideoTask error: %v", errUpdate))
		}
		return fmt.Errorf("CacheGetChannel failed: %w", err)
	}
	adaptor := GetTaskAdaptorFunc(platform)
	if adaptor == nil {
		return fmt.Errorf("video adaptor not found")
	}
	info := &relaycommon.RelayInfo{}
	info.ChannelMeta = &relaycommon.ChannelMeta{
		ChannelBaseUrl: cacheGetChannel.GetBaseURL(),
		ChannelSetting: cacheGetChannel.GetSetting(),
	}
	info.ApiKey = cacheGetChannel.Key
	adaptor.Init(info)
	disablePollingSleep := cacheGetChannel.GetOtherSettings().DisableTaskPollingSleep
	for i, taskId := range taskIds {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := updateVideoSingleTask(ctx, adaptor, cacheGetChannel, taskId, taskM); err != nil {
			logger.LogError(ctx, fmt.Sprintf("Failed to update video task %s: %s", taskId, err.Error()))
		}
		if disablePollingSleep || i == len(taskIds)-1 {
			continue
		}

		// sleep 1 second between tasks for this channel only.
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(1 * time.Second):
		}
	}
	return nil
}

func updateVideoSingleTask(ctx context.Context, adaptor TaskPollingAdaptor, ch *model.Channel, taskId string, taskM map[string]*model.Task) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	baseURL := constant.GetChannelBaseURL(ch.Type)
	if ch.GetBaseURL() != "" {
		baseURL = ch.GetBaseURL()
	}
	proxy := ResolveChannelProxy(ch.GetSetting())

	task := taskM[taskId]
	if task == nil {
		logger.LogError(ctx, fmt.Sprintf("Task %s not found in taskM", taskId))
		return fmt.Errorf("task %s not found", taskId)
	}
	key := ch.Key

	privateData := task.PrivateData
	if privateData.Key != "" {
		key = privateData.Key
	}
	snap := task.Snapshot()
	legacyTask := task.PrivateData.Execution == nil || task.PrivateData.Execution.TaskPlugin == nil
	resp, err := adaptor.FetchTask(baseURL, key, task, proxy)
	if err != nil {
		if legacyTask {
			return err
		}
		return recordPollFailure(ctx, adaptor, task, snap.Status, pollClassTransport, 0, err.Error())
	}
	if resp == nil {
		return fmt.Errorf("upstream returned an empty task response")
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		if legacyTask {
			return err
		}
		return recordPollFailure(ctx, adaptor, task, snap.Status, pollClassTransport, resp.StatusCode, err.Error())
	}

	logger.LogDebug(ctx, "updateVideoSingleTask response: %s", responseBody)

	now := time.Now().Unix()
	if !legacyTask {
		switch classifyPollHTTP(resp.StatusCode) {
		case pollClassNotFound:
			return failTaskFromPoll(ctx, adaptor, task, snap.Status, fmt.Sprintf("upstream task not found (HTTP %d)", resp.StatusCode))
		case pollClassAuth:
			logger.LogWarn(ctx, fmt.Sprintf("task poll auth failure channel_id=%d task=%s http=%d", ch.Id, task.TaskID, resp.StatusCode))
			return recordPollFailure(ctx, adaptor, task, snap.Status, pollClassAuth, resp.StatusCode, "")
		case pollClassTransient:
			return recordPollFailure(ctx, adaptor, task, snap.Status, pollClassTransient, resp.StatusCode, "")
		}
	} else if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= http.StatusInternalServerError || resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil
	}

	taskResult := &relaycommon.TaskInfo{}
	// try parse as New API response format
	var responseItems taskdto.TaskResponse[model.Task]
	if err = common.Unmarshal(responseBody, &responseItems); err == nil && responseItems.IsSuccess() {
		logger.LogDebug(ctx, "updateVideoSingleTask parsed as new api response format: %+v", responseItems)
		t := responseItems.Data
		taskResult.TaskID = t.TaskID
		taskResult.Status = string(t.Status)
		taskResult.Url = t.GetResultURL()
		taskResult.Progress = t.Progress
		taskResult.Reason = t.FailReason
		task.Data = t.Data
	} else if taskResult, err = adaptor.ParseTaskResult(task, resp, responseBody); err != nil {
		if legacyTask {
			return err
		}
		return recordPollFailure(ctx, adaptor, task, snap.Status, pollClassHookError, resp.StatusCode, err.Error())
	}
	if taskResult == nil {
		return fmt.Errorf("upstream task parser returned an empty task result")
	}

	// Some NewAPI-compatible upstreams put the completed video URL only in
	// data.result.data[].url instead of the normalized task result fields.
	if ch.Type == constant.ChannelTypeNewAPIVideo && taskResult.Url == "" {
		taskResult.Url = ExtractVideoResultURL(responseBody)
	}
	if ch.Type == constant.ChannelTypeNewAPIVideo || ch.Type == constant.ChannelTypeOpenAIVideo || ch.Type == constant.ChannelTypeMiniMaxVideo {
		normalizeNewAPIVideoTaskResult(baseURL, taskResult)
	}
	if taskResult.Url != "" && !strings.HasPrefix(strings.ToLower(taskResult.Url), "data:") {
		taskResult.Url = ResolveVideoResultURL(baseURL, taskResult.Url)
	}
	if taskResult.RemoteUrl != "" && !strings.HasPrefix(strings.ToLower(taskResult.RemoteUrl), "data:") {
		taskResult.RemoteUrl = ResolveVideoResultURL(baseURL, taskResult.RemoteUrl)
	}
	logger.LogDebug(ctx, "updateVideoSingleTask taskResult: %+v", taskResult)

	parsedStatus := model.TaskStatus(taskResult.Status)
	if !legacyTask {
		if parsedStatus == model.TaskStatusUnknown || parsedStatus == "" || !knownPollStatus(parsedStatus) {
			return recordPollFailure(ctx, adaptor, task, snap.Status, pollClassUnrecognized, resp.StatusCode, unrecognizedPollDetail(taskResult.Reason, responseBody))
		}
	}
	if !legacyTask && classifyPollHTTP(resp.StatusCode) == pollClassOtherClient && isNonTerminalPollStatus(parsedStatus) {
		return recordPollFailure(ctx, adaptor, task, snap.Status, pollClassUnrecognized, resp.StatusCode, unrecognizedPollDetail(taskResult.Reason, responseBody))
	}
	if taskResult.Status == "" {
		// Do not leave a malformed or undocumented terminal response in progress
		// forever. It gets a short recovery window below before it is failed.
		taskResult = &relaycommon.TaskInfo{
			Status:        model.TaskStatusInProgress,
			Progress:      taskcommon.ProgressInProgress,
			TerminalError: true,
			Reason:        "upstream returned an unrecognized task status",
		}
	}
	if taskResult.Status != model.TaskStatusSuccess && taskResult.Status != model.TaskStatusFailure {
		if reason := terminalVideoTaskStatusReason(responseBody); reason != "" {
			taskResult.Status = model.TaskStatusInProgress
			taskResult.TerminalError = true
			taskResult.Reason = reason
		}
		if resp.StatusCode >= http.StatusBadRequest && resp.StatusCode < http.StatusInternalServerError && resp.StatusCode != http.StatusTooManyRequests {
			taskResult.Status = model.TaskStatusInProgress
			taskResult.TerminalError = true
			if taskResult.Reason == "" {
				taskResult.Reason = fmt.Sprintf("upstream rejected task status lookup with HTTP %d", resp.StatusCode)
			}
		}
	}
	if taskResult.TerminalError && taskResult.Status != model.TaskStatusSuccess && taskResult.Status != model.TaskStatusFailure && taskTerminalErrorExpired(task, now) {
		logger.LogWarn(ctx, fmt.Sprintf("Task %s exceeded terminal error timeout: %s", task.TaskID, taskResult.Reason))
		taskResult.Status = model.TaskStatusFailure
		taskResult.Progress = taskcommon.ProgressComplete
		if strings.TrimSpace(taskResult.Reason) == "" {
			taskResult.Reason = "upstream reported a terminal error without a usable video result"
		}
	}

	localVideoURL := ""
	if legacyTask && constant.IsVideoTaskChannelType(ch.Type) && taskResult.Status == model.TaskStatusSuccess {
		videoSourceURL := strings.TrimSpace(taskResult.Url)
		if videoSourceURL == "" {
			videoSourceURL = strings.TrimSpace(taskResult.RemoteUrl)
		}
		if videoSourceURL == "" {
			videoSourceURL = ExtractVideoDataURL(responseBody)
		}
		// Inline results are already downloaded. Store them before sanitizing
		// the response, without persisting large base64 payloads in the database.
		if strings.HasPrefix(strings.ToLower(videoSourceURL), "data:") {
			if _, cacheErr := CacheVideoDataURL(ctx, task.TaskID, videoSourceURL); cacheErr != nil {
				MarkVideoCacheFailure(task, cacheErr)
				logger.LogError(ctx, fmt.Sprintf("Failed to store inline video task %s: %s", task.TaskID, truncateVideoCacheError(cacheErr)))
			} else {
				MarkVideoTaskCached(task)
			}
		} else if err := PrepareVideoTaskCacheSource(task, ch, videoSourceURL); err != nil {
			MarkVideoCacheFailure(task, err)
		}
		if _, cached := CachedVideoPath(task.TaskID); cached && !VideoCacheExpired(task) {
			localVideoURL = taskcommon.BuildPublicVideoURL(task.TaskID)
		}
		taskResult.TerminalError = false
		task.PrivateData.ResultURL = localVideoURL
	}

	if legacyTask && constant.IsVideoTaskChannelType(ch.Type) {
		task.Data = SanitizeVideoTaskData(responseBody, task.TaskID, task.GetUpstreamTaskID(), localVideoURL)
	} else {
		task.Data = redactVideoResponseBody(responseBody, localVideoURL, false)
	}

	logger.LogDebug(ctx, "updateVideoSingleTask taskResult: %+v", taskResult)

	parsedStatus = model.TaskStatus(taskResult.Status)
	if len(taskResult.PluginState) > 0 {
		task.PrivateData.PluginState = taskResult.PluginState
	}
	if isNonTerminalPollStatus(parsedStatus) {
		task.PrivateData.PollFailures = 0
	}

	shouldFinalizeBilling := false

	task.Status = parsedStatus
	switch parsedStatus {
	case model.TaskStatusSubmitted:
		task.Progress = taskcommon.ProgressSubmitted
	case model.TaskStatusQueued:
		task.Progress = taskcommon.ProgressQueued
	case model.TaskStatusInProgress:
		task.Progress = taskcommon.ProgressInProgress
		if task.StartTime == 0 {
			task.StartTime = now
		}
	case model.TaskStatusSuccess:
		task.Progress = taskcommon.ProgressComplete
		if task.FinishTime == 0 {
			task.FinishTime = now
		}
		if legacyTask && constant.IsVideoTaskChannelType(ch.Type) {
			// Publish a shareable local URL only after its complete file exists.
			task.PrivateData.ResultURL = localVideoURL
		} else if taskResult.Url != "" {
			task.PrivateData.ResultURL = taskResult.Url
		} else {
			// No URL from a non-video adaptor: preserve the historical proxy URL.
			task.PrivateData.ResultURL = taskcommon.BuildProxyURL(task.TaskID)
		}
		shouldFinalizeBilling = true
	case model.TaskStatusFailure:
		logger.LogJson(ctx, fmt.Sprintf("Task %s failed", taskId), task)
		task.Status = model.TaskStatusFailure
		task.Progress = taskcommon.ProgressComplete
		if task.FinishTime == 0 {
			task.FinishTime = now
		}
		task.FailReason = taskResult.Reason
		logger.LogInfo(ctx, fmt.Sprintf("Task %s failed: %s", task.TaskID, task.FailReason))
		taskResult.Progress = taskcommon.ProgressComplete
		shouldFinalizeBilling = true
	}
	if taskResult.Progress != "" {
		task.Progress = taskResult.Progress
	}

	isDone := task.Status == model.TaskStatusSuccess || task.Status == model.TaskStatusFailure
	if isDone && snap.Status != task.Status {
		won, err := task.UpdateWithStatus(snap.Status)
		if err != nil {
			logger.LogError(ctx, fmt.Sprintf("UpdateWithStatus failed for task %s: %s", task.TaskID, err.Error()))
			shouldFinalizeBilling = false
		} else if !won {
			logger.LogWarn(ctx, fmt.Sprintf("Task %s CAS lost or no-op update, skip billing", task.TaskID))
			shouldFinalizeBilling = false
		}
	} else if !snap.Equal(task.Snapshot()) {
		if _, err := task.UpdateWithStatus(snap.Status); err != nil {
			logger.LogError(ctx, fmt.Sprintf("Failed to update task %s: %s", task.TaskID, err.Error()))
		}
	} else {
		// No changes, skip update
		logger.LogDebug(ctx, "No update needed for task %s", task.TaskID)
	}

	if shouldFinalizeBilling {
		billingSettled := settleTaskBillingOnComplete(ctx, adaptor, task, taskResult)
		if task.Status == model.TaskStatusFailure && !billingSettled && task.Quota != 0 {
			RefundTaskQuota(ctx, task, task.FailReason)
		}
	}
	if legacyTask && constant.IsVideoTaskChannelType(ch.Type) && task.Status == model.TaskStatusSuccess {
		// The worker reloads the persisted SUCCESS row and never settles billing.
		QueueVideoTaskCache(task.TaskID)
	}

	return nil
}

func redactVideoResponseBody(body []byte, localVideoURL string, hideVideoURLs bool) []byte {
	var m map[string]any
	if err := common.Unmarshal(body, &m); err != nil {
		return body
	}
	resp, _ := m["response"].(map[string]any)
	if resp != nil {
		delete(resp, "bytesBase64Encoded")
		if v, ok := resp["video"].(string); ok {
			resp["video"] = truncateBase64(v)
		}
		if vs, ok := resp["videos"].([]any); ok {
			for i := range vs {
				if vm, ok := vs[i].(map[string]any); ok {
					delete(vm, "bytesBase64Encoded")
				}
			}
		}
	}
	if localVideoURL != "" {
		replaceVideoResultURLs(m, localVideoURL)
	} else if hideVideoURLs {
		removeVideoResultURLs(m)
	}
	b, err := common.Marshal(m)
	if err != nil {
		return body
	}
	return b
}

// SanitizeVideoTaskData keeps provider response details useful without
// exposing provider URLs or provider task identifiers through task responses.
func SanitizeVideoTaskData(body []byte, publicTaskID, upstreamTaskID, localVideoURL string) []byte {
	redacted := redactVideoResponseBody(body, localVideoURL, true)
	var m map[string]any
	if err := common.Unmarshal(redacted, &m); err != nil {
		return []byte(`{}`)
	}

	replaceVideoTaskIdentifiers(m, publicTaskID, 0)
	replaceSensitiveVideoStrings(m, publicTaskID, upstreamTaskID, localVideoURL)
	b, err := common.Marshal(m)
	if err != nil {
		return []byte(`{}`)
	}
	return SanitizeErrorResponseBody(b)
}

var videoURLInTaskReason = regexp.MustCompile(`https?://[^\s"'<>]+`)

// SanitizeVideoTaskReason removes provider URLs and provider task IDs from
// task log error text while preserving the rest of the diagnostic message.
func SanitizeVideoTaskReason(reason, upstreamTaskID string) string {
	if upstreamTaskID != "" {
		reason = strings.ReplaceAll(reason, upstreamTaskID, "[redacted]")
	}
	reason = videoURLInTaskReason.ReplaceAllString(reason, "[redacted]")
	return SanitizeErrorMessage(reason)
}

// SanitizeNewAPIVideoTaskData is kept as a compatibility wrapper for callers
// and tests that use the historical function name.
func SanitizeNewAPIVideoTaskData(body []byte, publicTaskID, upstreamTaskID, localVideoURL string) []byte {
	return SanitizeVideoTaskData(body, publicTaskID, upstreamTaskID, localVideoURL)
}

// SanitizeOpenAIVideoResponse normalizes converter output so OpenAI video
// fetches also expose only the local cache URL.
func SanitizeOpenAIVideoResponse(body []byte, publicTaskID, upstreamTaskID, localVideoURL string) []byte {
	sanitized := SanitizeVideoTaskData(body, publicTaskID, upstreamTaskID, localVideoURL)
	if strings.TrimSpace(localVideoURL) == "" {
		return sanitized
	}
	var response map[string]any
	if err := common.Unmarshal(sanitized, &response); err != nil {
		return sanitized
	}
	response["result_url"] = localVideoURL
	metadata, _ := response["metadata"].(map[string]any)
	if metadata == nil {
		metadata = make(map[string]any)
	}
	metadata["url"] = localVideoURL
	response["metadata"] = metadata
	encoded, err := common.Marshal(response)
	if err != nil {
		return sanitized
	}
	return encoded
}

func isVideoURLField(key string) bool {
	normalized := strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(key))
	return normalized == "url" || strings.HasSuffix(normalized, "url")
}

func removeVideoResultURLs(node any) {
	switch value := node.(type) {
	case map[string]any:
		for key, child := range value {
			if isVideoURLField(key) {
				delete(value, key)
				continue
			}
			removeVideoResultURLs(child)
		}
	case []any:
		for _, child := range value {
			removeVideoResultURLs(child)
		}
	}
}

func ExtractVideoResultURL(body []byte) string {
	var root any
	if err := common.Unmarshal(body, &root); err != nil {
		return ""
	}
	return findVideoResultURL(root)
}

// ExtractVideoDataURL finds common inline base64 video response shapes. It is
// used for legacy Vertex tasks and for providers that return bytes directly.
func ExtractVideoDataURL(body []byte) string {
	var root any
	if err := common.Unmarshal(body, &root); err != nil {
		return ""
	}
	return findVideoDataURL(root)
}

func findVideoDataURL(node any) string {
	switch value := node.(type) {
	case map[string]any:
		if encoded, ok := value["bytesBase64Encoded"].(string); ok && strings.TrimSpace(encoded) != "" {
			return buildVideoDataURL(value["mimeType"], value["encoding"], encoded)
		}
		if encoded, ok := value["video"].(string); ok && strings.TrimSpace(encoded) != "" {
			if strings.HasPrefix(strings.ToLower(strings.TrimSpace(encoded)), "data:") {
				return strings.TrimSpace(encoded)
			}
			if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(encoded)), "http://") && !strings.HasPrefix(strings.ToLower(strings.TrimSpace(encoded)), "https://") {
				return buildVideoDataURL(value["mimeType"], value["encoding"], encoded)
			}
		}
		for _, key := range []string{"response", "data", "video", "videos", "output", "result"} {
			if child, ok := value[key]; ok {
				if candidate := findVideoDataURL(child); candidate != "" {
					return candidate
				}
			}
		}
	case []any:
		for _, child := range value {
			if candidate := findVideoDataURL(child); candidate != "" {
				return candidate
			}
		}
	}
	return ""
}

func buildVideoDataURL(mimeValue, encodingValue any, encoded string) string {
	mimeType, _ := mimeValue.(string)
	mimeType = strings.TrimSpace(mimeType)
	if mimeType == "" {
		encoding, _ := encodingValue.(string)
		encoding = strings.TrimSpace(encoding)
		if encoding == "" {
			encoding = "mp4"
		}
		if strings.Contains(encoding, "/") {
			mimeType = encoding
		} else {
			mimeType = "video/" + encoding
		}
	}
	return "data:" + mimeType + ";base64," + strings.TrimSpace(encoded)
}

// ResolveVideoResultURL converts provider-relative video paths into absolute URLs.
// NewAPI-compatible providers commonly return paths such as /api/v1/gen/cached/...
func ResolveVideoResultURL(baseURL, resultURL string) string {
	resultURL = strings.TrimSpace(resultURL)
	if resultURL == "" {
		return ""
	}

	parsedResult, err := url.Parse(resultURL)
	if err != nil || parsedResult.IsAbs() {
		return resultURL
	}

	parsedBase, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || !parsedBase.IsAbs() || parsedBase.Host == "" {
		return resultURL
	}
	return parsedBase.ResolveReference(parsedResult).String()
}

// VideoResultURLFailureReason recognizes provider error text disguised as a URL.
func VideoResultURLFailureReason(resultURL string) string {
	decoded := strings.TrimSpace(resultURL)
	if decoded == "" {
		return ""
	}
	for i := 0; i < 2; i++ {
		next, err := url.PathUnescape(decoded)
		if err != nil || next == decoded {
			break
		}
		decoded = next
	}
	normalized := strings.ToLower(strings.ReplaceAll(decoded, "+", " "))
	if strings.Contains(normalized, "video generation returned no final video url") {
		return "upstream video generation returned no final video URL"
	}
	return ""
}

func normalizeNewAPIVideoTaskResult(baseURL string, taskResult *relaycommon.TaskInfo) {
	taskResult.Url = ResolveVideoResultURL(baseURL, taskResult.Url)
	if reason := VideoResultURLFailureReason(taskResult.Url); reason != "" {
		taskResult.Status = model.TaskStatusFailure
		taskResult.Progress = taskcommon.ProgressComplete
		taskResult.Reason = reason
		return
	}
	if taskResult.Url != "" && taskResult.Status != model.TaskStatusFailure {
		taskResult.Status = model.TaskStatusSuccess
		taskResult.Progress = taskcommon.ProgressComplete
		taskResult.TerminalError = false
	}
}

func taskTerminalErrorExpired(task *model.Task, now int64) bool {
	timeoutMinutes := constant.TaskTerminalErrorTimeoutMinutes
	if timeoutMinutes < 0 {
		return false
	}
	if timeoutMinutes == 0 {
		return true
	}
	startedAt := task.SubmitTime
	if startedAt <= 0 {
		startedAt = task.CreatedAt
	}
	return startedAt > 0 && now >= startedAt+int64(timeoutMinutes)*60
}

func terminalVideoTaskStatusReason(body []byte) string {
	var response map[string]any
	if err := common.Unmarshal(body, &response); err != nil {
		return ""
	}

	candidates := []map[string]any{response}
	for _, key := range []string{"data", "task", "result"} {
		if nested, ok := response[key].(map[string]any); ok {
			candidates = append(candidates, nested)
		}
	}
	for _, candidate := range candidates {
		status, ok := candidate["status"].(string)
		if !ok {
			continue
		}
		normalized := strings.ToLower(strings.TrimSpace(status))
		switch normalized {
		case "unknown", "not_found", "notfound", "expired", "deleted", "gone":
			return "upstream task status is " + normalized
		}
	}
	return ""
}

func findVideoResultURL(node any) string {
	switch value := node.(type) {
	case map[string]any:
		for _, key := range []string{"result_url", "video_url", "url"} {
			if candidate, ok := value[key].(string); ok && strings.TrimSpace(candidate) != "" {
				return strings.TrimSpace(candidate)
			}
		}
		visited := make(map[string]struct{}, 4)
		for _, key := range []string{"result", "output", "data", "video"} {
			child, ok := value[key]
			if !ok {
				continue
			}
			visited[key] = struct{}{}
			if candidate := findVideoResultURL(child); candidate != "" {
				return candidate
			}
		}
		keys := make([]string, 0, len(value))
		for key := range value {
			if _, ok := visited[key]; !ok {
				keys = append(keys, key)
			}
		}
		sort.Strings(keys)
		for _, key := range keys {
			if candidate := findVideoResultURL(value[key]); candidate != "" {
				return candidate
			}
		}
	case []any:
		for _, child := range value {
			if candidate := findVideoResultURL(child); candidate != "" {
				return candidate
			}
		}
	}
	return ""
}

func replaceVideoResultURLs(node any, replacement string) {
	switch value := node.(type) {
	case map[string]any:
		for key, child := range value {
			if isVideoURLField(key) {
				if _, ok := child.(string); ok {
					value[key] = replacement
					continue
				}
			}
			replaceVideoResultURLs(child, replacement)
		}
	case []any:
		for _, child := range value {
			replaceVideoResultURLs(child, replacement)
		}
	}
}

func replaceVideoTaskIdentifiers(node any, publicTaskID string, depth int) {
	if publicTaskID == "" {
		return
	}
	switch value := node.(type) {
	case map[string]any:
		for key, child := range value {
			normalizedKey := strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(key))
			switch normalizedKey {
			case "upstreamtaskid", "upstreamid":
				delete(value, key)
				continue
			case "taskid":
				if _, ok := child.(string); ok {
					value[key] = publicTaskID
					continue
				}
			case "id":
				if depth <= 1 {
					if _, ok := child.(string); ok {
						value[key] = publicTaskID
						continue
					}
				}
			}
			replaceVideoTaskIdentifiers(child, publicTaskID, depth+1)
		}
	case []any:
		for _, child := range value {
			replaceVideoTaskIdentifiers(child, publicTaskID, depth+1)
		}
	}
}

func replaceSensitiveVideoStrings(node any, publicTaskID, upstreamTaskID, localVideoURL string) {
	switch value := node.(type) {
	case map[string]any:
		for key, child := range value {
			s, ok := child.(string)
			if ok {
				if upstreamTaskID != "" && upstreamTaskID != publicTaskID {
					s = strings.ReplaceAll(s, upstreamTaskID, publicTaskID)
				}
				if (strings.Contains(s, "http://") || strings.Contains(s, "https://")) && s != localVideoURL {
					s = "[redacted]"
				}
				value[key] = s
				continue
			}
			replaceSensitiveVideoStrings(child, publicTaskID, upstreamTaskID, localVideoURL)
		}
	case []any:
		for i, child := range value {
			s, ok := child.(string)
			if ok {
				if upstreamTaskID != "" && upstreamTaskID != publicTaskID {
					s = strings.ReplaceAll(s, upstreamTaskID, publicTaskID)
				}
				if (strings.Contains(s, "http://") || strings.Contains(s, "https://")) && s != localVideoURL {
					s = "[redacted]"
				}
				value[i] = s
				continue
			}
			replaceSensitiveVideoStrings(child, publicTaskID, upstreamTaskID, localVideoURL)
		}
	}
}

func truncateBase64(s string) string {
	const maxKeep = 256
	if len(s) <= maxKeep {
		return s
	}
	return s[:maxKeep] + "..."
}

// settleTaskBillingOnComplete 任务完成时的统一计费调整。
// 返回 true 表示用量结算路径已接管最终计费；失败任务仅在返回 false 时补做全额退款。
// 优先级：1. tiered snapshot → 2. adaptor 调整 → 3. token 重算。
//
// 表达式求值失败会保留预扣额度，因此也视为已接管，避免错误全退。
func settleTaskBillingOnComplete(ctx context.Context, adaptor TaskPollingAdaptor, task *model.Task, taskResult *relaycommon.TaskInfo) bool {
	if bc := task.PrivateData.BillingContext; bc != nil && bc.TieredSnapshot != nil {
		// 用量表达式结算只适用于成功任务；失败任务由调用方全额退款。
		if task.Status == model.TaskStatusFailure {
			return false
		}
		result, usageFacts, err := EvaluateTaskCompletionUsage(bc.TieredSnapshot, taskResult.UsageFacts)
		if err != nil {
			logger.LogWarn(ctx, fmt.Sprintf("任务 %s 表达式结算失败，保留预扣额度: %v", task.TaskID, err))
			return true
		}
		if result.Clamp != nil {
			logger.LogWarn(ctx, fmt.Sprintf("任务 %s 表达式结算额度发生饱和: %+v", task.TaskID, result.Clamp))
		}
		bc.TieredSnapshot.UsageFacts = usageFacts
		bc.TieredSnapshot.EstimatedTier = result.MatchedTier
		RecalculateTaskQuota(ctx, task, result.ActualQuotaAfterGroup, "任务用量表达式结算", result.Clamp)
		return true
	}
	// 按次计费的成功任务保持预扣；失败任务由调用方全额退款。
	if bc := task.PrivateData.BillingContext; bc != nil && bc.PerCallBilling {
		logger.LogInfo(ctx, fmt.Sprintf("任务 %s 按次计费，跳过差额结算", task.TaskID))
		return false
	}
	// 优先让 adaptor 决定最终额度。
	if actualQuota := adaptor.AdjustBillingOnComplete(task, taskResult); actualQuota > 0 {
		RecalculateTaskQuota(ctx, task, actualQuota, "adaptor计费调整")
		return true
	}
	// 回退到 token 重算。
	tokens := taskResult.TotalTokens
	if tokens == 0 && taskResult.CompletionTokens > 0 {
		tokens = taskResult.CompletionTokens
	}
	if tokens > 0 {
		return RecalculateTaskQuotaByTokens(ctx, task, tokens)
	}
	return false
}

func classifyPollHTTP(statusCode int) string {
	switch {
	case statusCode >= 200 && statusCode < 300:
		return pollClassOK
	case statusCode == http.StatusNotFound || statusCode == http.StatusGone:
		return pollClassNotFound
	case statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden:
		return pollClassAuth
	case statusCode == http.StatusTooManyRequests || statusCode >= 500:
		return pollClassTransient
	case statusCode >= 400 && statusCode < 500:
		return pollClassOtherClient
	default:
		return pollClassTransient
	}
}

func knownPollStatus(status model.TaskStatus) bool {
	switch status {
	case model.TaskStatusNotStart, model.TaskStatusSubmitted, model.TaskStatusQueued, model.TaskStatusInProgress, model.TaskStatusSuccess, model.TaskStatusFailure:
		return true
	default:
		return false
	}
}

func isNonTerminalPollStatus(status model.TaskStatus) bool {
	switch status {
	case model.TaskStatusNotStart, model.TaskStatusSubmitted, model.TaskStatusQueued, model.TaskStatusInProgress:
		return true
	default:
		return false
	}
}

func pollFailureReason(class string, statusCode int, detail string) string {
	reason := fmt.Sprintf("poll failed: %s", class)
	if statusCode > 0 {
		reason = fmt.Sprintf("poll failed: %s (HTTP %d)", class, statusCode)
	}
	if detail != "" {
		reason = reason + ": " + detail
	}
	return reason
}

// unrecognizedPollDetail pairs the plugin's reason with a bounded copy of the
// upstream body so the WARN line is enough to diagnose a parser gap.
func unrecognizedPollDetail(reason string, body []byte) string {
	const maxBodyChars = 512
	redacted := string(redactVideoResponseBody(body, "", false))
	if len(redacted) > maxBodyChars {
		redacted = redacted[:maxBodyChars] + "…"
	}
	if strings.TrimSpace(reason) == "" {
		return "body=" + redacted
	}
	return reason + "; body=" + redacted
}

func recordPollFailure(ctx context.Context, adaptor TaskPollingAdaptor, task *model.Task, fromStatus model.TaskStatus, class string, statusCode int, detail string) error {
	task.PrivateData.PollFailures++
	if class == pollClassUnrecognized || class == pollClassHookError {
		// The redacted body is intentionally not persisted to Task.Data on these
		// paths, so the WARN line is the only operator-visible copy of what the
		// plugin could not interpret.
		logger.LogWarn(ctx, fmt.Sprintf("task %s poll %s (failures=%d, http=%d): %s", task.TaskID, class, task.PrivateData.PollFailures, statusCode, detail))
	}
	// TASK_POLL_MAX_FAILURES <= 0 disables the consecutive-failure cutoff, matching
	// TASK_TIMEOUT_MINUTES semantics; the 24h sweep remains the only backstop.
	if constant.TaskPollMaxFailures > 0 && task.PrivateData.PollFailures >= constant.TaskPollMaxFailures {
		return failTaskFromPoll(ctx, adaptor, task, fromStatus, pollFailureReason(class, statusCode, detail))
	}
	if _, err := task.UpdateWithStatus(fromStatus); err != nil {
		return err
	}
	return nil
}

func recordPollFailureForTasks(ctx context.Context, adaptor TaskPollingAdaptor, tasks []*model.Task, class string, statusCode int, detail string) error {
	var firstErr error
	for _, task := range tasks {
		if task == nil {
			continue
		}
		if err := recordPollFailure(ctx, adaptor, task, task.Status, class, statusCode, detail); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func failTaskFromPoll(ctx context.Context, adaptor TaskPollingAdaptor, task *model.Task, fromStatus model.TaskStatus, reason string) error {
	now := time.Now().Unix()
	task.Status = model.TaskStatusFailure
	task.Progress = taskcommon.ProgressComplete
	if task.FinishTime == 0 {
		task.FinishTime = now
	}
	task.FailReason = reason
	won, err := task.UpdateWithStatus(fromStatus)
	if err != nil {
		return err
	}
	if !won {
		return nil
	}
	taskResult := relaycommon.FailTaskInfo(reason)
	billingSettled := settleTaskBillingOnComplete(ctx, adaptor, task, taskResult)
	if !billingSettled && task.Quota != 0 {
		RefundTaskQuota(ctx, task, reason)
	}
	return nil
}

func failTasksFromPoll(ctx context.Context, adaptor TaskPollingAdaptor, tasks []*model.Task, reason string) error {
	var firstErr error
	for _, task := range tasks {
		if task == nil {
			continue
		}
		if err := failTaskFromPoll(ctx, adaptor, task, task.Status, reason); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
