package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/client"
	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/common/ctxkey"
	"github.com/songquanpeng/one-api/common/helper"
	"github.com/songquanpeng/one-api/common/logger"
	"github.com/songquanpeng/one-api/monitor"
	billingratio "github.com/songquanpeng/one-api/relay/billing/ratio"
	"github.com/songquanpeng/one-api/relay/channeltype"
	dbmodel "github.com/songquanpeng/one-api/model"
	"github.com/songquanpeng/one-api/relay/meta"
)

// RelayResponses 以透传方式对接 OpenAI Responses API（POST /v1/responses）。
// 请求报文不做协议转换，仅应用模型别名/映射后原样转发到源渠道；
// 渠道需在编辑页勾选「支持 Responses API」。
// 计费：从响应（非流式 JSON 或流式 response.completed 事件）提取 usage，
// 按与其他 relay 相同的 quota 公式记账，quota = (prompt + completion×completionRatio) × ratio。

type responsesUsage struct {
	InputTokens      int64 `json:"input_tokens"`
	OutputTokens     int64 `json:"output_tokens"`
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
}

func (u *responsesUsage) prompt() int64 {
	if u.InputTokens > 0 {
		return u.InputTokens
	}
	if u.PromptTokens > 0 {
		return u.PromptTokens
	}
	return u.TotalTokens
}

func (u *responsesUsage) completion() int64 {
	if u.OutputTokens > 0 {
		return u.OutputTokens
	}
	return u.CompletionTokens
}

func buildResponsesURL(baseURL string) string {
	baseURL = strings.TrimSuffix(baseURL, "/")
	// base_url 不含 /v1（如 https://api.openai.com）则补全；
	// 自定义渠道若以 /v1 结尾则直接拼接 /responses
	if strings.HasSuffix(baseURL, "/v1") {
		return baseURL + "/responses"
	}
	return baseURL + "/v1/responses"
}

// applyModelMappingToBody 对请求体中的 model 字段应用模型别名/映射，其余字段原样保留
func applyModelMappingToBody(mapping map[string]string, body []byte) []byte {
	if len(mapping) == 0 {
		return body
	}
	var obj map[string]interface{}
	if err := json.Unmarshal(body, &obj); err != nil {
		return body
	}
	if modelName, ok := obj["model"].(string); ok {
		if mapped, ok := mapping[modelName]; ok && mapped != "" && mapped != modelName {
			obj["model"] = mapped
			if newBody, err := json.Marshal(obj); err == nil {
				return newBody
			}
		}
	}
	return body
}

// extractUsageFromLine 从 SSE 数据行中提取 response.completed 事件里的 usage
func extractUsageFromLine(line []byte) *responsesUsage {
	trimmed := bytes.TrimSpace(line)
	if !bytes.HasPrefix(trimmed, []byte("data:")) {
		return nil
	}
	if !bytes.Contains(trimmed, []byte("response.completed")) {
		return nil
	}
	payload := bytes.TrimSpace(bytes.TrimPrefix(trimmed, []byte("data:")))
	var evt struct {
		Response struct {
			Usage *responsesUsage `json:"usage"`
		} `json:"response"`
		Usage *responsesUsage `json:"usage"`
	}
	if err := json.Unmarshal(payload, &evt); err != nil {
		return nil
	}
	if evt.Response.Usage != nil {
		return evt.Response.Usage
	}
	return evt.Usage
}

// preConsumeResponsesQuota 预扣令牌额度（用户额度视为无限，不再检查扣减）
func preConsumeResponsesQuota(meta *meta.Meta, promptTokens int64, ratio float64) int64 {
	preConsumedTokens := config.PreConsumedQuota + promptTokens
	preConsumedQuota := int64(float64(preConsumedTokens) * ratio)
	if preConsumedQuota > 0 {
		if err := dbmodel.PreConsumeTokenQuota(meta.TokenId, preConsumedQuota); err != nil {
			return -1
		}
	}
	return preConsumedQuota
}

func postConsumeResponsesQuota(ctx context.Context, meta *meta.Meta, modelName string, usage *responsesUsage, ratio float64, preConsumedQuota int64, isStream bool, startTime time.Time) {
	promptTokens := usage.prompt()
	completionTokens := usage.completion()
	completionRatio := billingratio.GetCompletionRatio(modelName, meta.ChannelType)
	var quota int64
	quota = int64(math.Ceil((float64(promptTokens) + float64(completionTokens)*completionRatio) * ratio))
	if ratio != 0 && quota <= 0 {
		quota = 1
	}
	quotaDelta := quota - preConsumedQuota
	if err := dbmodel.PostConsumeTokenQuota(meta.TokenId, quotaDelta); err != nil {
		logger.Errorf(ctx, "error consuming token remain quota: %s", err.Error())
	}
	logContent := fmt.Sprintf("倍率：%.2f × %.2f × %.2f（responses 透传）", ratio, 1.0, completionRatio)
	dbmodel.RecordConsumeLog(ctx, &dbmodel.Log{
		UserId:           meta.UserId,
		ChannelId:        meta.ChannelId,
		PromptTokens:     int(promptTokens),
		CompletionTokens: int(completionTokens),
		ModelName:        modelName,
		TokenName:        meta.TokenName,
		Quota:            quota,
		Content:          logContent,
		IsStream:         isStream,
		ElapsedTime:      helper.CalcElapsedTime(startTime),
	})
	dbmodel.UpdateUserUsedQuotaAndRequestCount(meta.UserId, quota)
	dbmodel.UpdateChannelUsedQuota(meta.ChannelId, quota)
}

func RelayResponses(c *gin.Context) {
	ctx := c.Request.Context()
	meta := meta.GetByContext(c)

	if !c.GetBool(ctxkey.SupportResponses) {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"message": "当前渠道未开启 Responses API 支持，请在渠道编辑中勾选「支持 Responses API」",
				"type":    "one_api_error",
				"code":    "responses_not_supported",
			},
		})
		return
	}

	requestBody, err := common.GetRequestBody(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"message": "无效的请求体：" + err.Error(),
				"type":    "one_api_error",
				"code":    "invalid_request",
			},
		})
		return
	}

	// 解析请求：模型 + 是否流式，并应用模型别名/映射
	var req struct {
		Model string `json:"model"`
		Stream bool  `json:"stream"`
	}
	_ = json.Unmarshal(requestBody, &req)
	realModel, _ := getMappedModelName(req.Model, meta.ModelMapping)
	if realModel == "" {
		realModel = req.Model
	}
	requestBody = applyModelMappingToBody(meta.ModelMapping, requestBody)

	// 计费：倍率（内部部署恒为 1）× 分组倍率
	modelRatio := billingratio.GetModelRatio(realModel, meta.ChannelType)
	groupRatio := billingratio.GetGroupRatio(meta.Group)
	ratio := modelRatio * groupRatio
	// prompt tokens 预估：按 4 字符 ≈ 1 token，最终以响应 usage 为准
	estimatedPromptTokens := int64(len(requestBody) / 4)
	if estimatedPromptTokens < 1 {
		estimatedPromptTokens = 1
	}
	preConsumedQuota := preConsumeResponsesQuota(meta, estimatedPromptTokens, ratio)
	if preConsumedQuota < 0 {
		c.JSON(http.StatusForbidden, gin.H{
			"error": gin.H{
				"message": "令牌额度不足，请调整令牌额度或设置为无限额度",
				"type":    "one_api_error",
				"code":    "insufficient_token_quota",
			},
		})
		return
	}

	// 构建上游请求
	channelType := c.GetInt(ctxkey.Channel)
	baseURL := c.GetString(ctxkey.BaseURL)
	if baseURL == "" {
		baseURL = channeltype.ChannelBaseURLs[channelType]
	}
	targetURL := buildResponsesURL(baseURL)
	apiKey := strings.TrimPrefix(c.Request.Header.Get("Authorization"), "Bearer ")
	upstreamReq, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(requestBody))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{
				"message": "构建上游请求失败：" + err.Error(),
				"type":    "one_api_error",
				"code":    "build_request_failed",
			},
		})
		return
	}
	upstreamReq.Header.Set("Content-Type", "application/json")
	upstreamReq.Header.Set("Accept", c.Request.Header.Get("Accept"))
	if c.Request.Header.Get("OpenAI-Beta") != "" {
		upstreamReq.Header.Set("OpenAI-Beta", c.Request.Header.Get("OpenAI-Beta"))
	}
	upstreamReq.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := client.HTTPClient.Do(upstreamReq)
	if err != nil {
		monitor.Emit(meta.ChannelId, false)
		c.JSON(http.StatusBadGateway, gin.H{
			"error": gin.H{
				"message": "请求上游失败：" + err.Error(),
				"type":    "one_api_error",
				"code":    "upstream_error",
			},
		})
		return
	}
	defer resp.Body.Close()

	c.Status(resp.StatusCode)
	if contentType := resp.Header.Get("Content-Type"); contentType != "" {
		c.Writer.Header().Set("Content-Type", contentType)
	}
	flusher, _ := c.Writer.(http.Flusher)

	usage := &responsesUsage{}
	if req.Stream {
		// 流式：逐块转发并实时 Flush，同时扫描 response.completed 事件提取 usage
		buf := make([]byte, 4096)
		var lineBuf []byte
		for {
			n, readErr := resp.Body.Read(buf)
			if n > 0 {
				if _, writeErr := c.Writer.Write(buf[:n]); writeErr != nil {
					readErr = io.EOF // 客户端断开，停止读取
				} else if flusher != nil {
					flusher.Flush()
				}
				// 注意：最后一次 Read 可能同时返回 (n>0, io.EOF)，即使 readErr != nil 也要扫描
				if usage.prompt() == 0 {
					lineBuf = append(lineBuf, buf[:n]...)
					for {
						idx := bytes.IndexByte(lineBuf, '\n')
						if idx < 0 {
							break
						}
						line := lineBuf[:idx]
						lineBuf = lineBuf[idx+1:]
						if u := extractUsageFromLine(line); u != nil && u.prompt() > 0 {
							usage = u
							break
						}
					}
				}
			}
			if readErr != nil {
				break
			}
		}
	} else {
		// 非流式：读取完整响应后解析 usage 再写回
		respBody, readErr := io.ReadAll(resp.Body)
		if readErr == nil {
			var parsed struct {
				Usage *responsesUsage `json:"usage"`
			}
			if json.Unmarshal(respBody, &parsed) == nil && parsed.Usage != nil {
				usage = parsed.Usage
			}
			if _, writeErr := c.Writer.Write(respBody); writeErr == nil && flusher != nil {
				flusher.Flush()
			}
		}
	}

	success := resp.StatusCode < 400
	monitor.Emit(meta.ChannelId, success)
	// 计费记账（异步，与 chat relay 行为一致）
	go postConsumeResponsesQuota(ctx, meta, realModel, usage, ratio, preConsumedQuota, req.Stream, meta.StartTime)
}
