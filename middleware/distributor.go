package middleware

import (
	"fmt"
	"hash/fnv"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"

	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/ctxkey"
	"github.com/songquanpeng/one-api/common/logger"
	"github.com/songquanpeng/one-api/model"
	"github.com/songquanpeng/one-api/relay/channeltype"
)

type ModelRequest struct {
	Model string `json:"model" form:"model"`
}

// 轮询计数器（按别名维度）
var aliasRoundRobin = struct {
	sync.Mutex
	counters map[string]int
}{counters: make(map[string]int)}

// estimatePromptTokens 粗估当前请求的会话大小（tokens）。
// Distribute 阶段无需精确分词，中英混合按约 3 字节/token 估算即可满足
// "会话大小明显超过模型上下文时不路由过去"的粗筛目的。
func estimatePromptTokens(c *gin.Context) int {
	body, err := common.GetRequestBody(c)
	if err != nil || len(body) == 0 {
		return 0
	}
	return len(body)/3 + 1
}

// filterModelsByContext 过滤掉上下文长度小于当前会话（含 10% 安全余量）的模型。
// 未在 ModelContextTokens 中配置的模型不参与过滤（视为不限）。
func filterModelsByContext(models []string, estTokens int) []string {
	ctxMap := model.GetMergedContextTokens()
	if len(ctxMap) == 0 || estTokens <= 0 {
		return models
	}
	var filtered []string
	for _, m := range models {
		maxTok, ok := ctxMap[m]
		if !ok || maxTok <= 0 || int64(estTokens)*11/10 <= int64(maxTok) {
			filtered = append(filtered, m)
		}
	}
	return filtered
}

// orderModelsForAlias 按负载策略生成候选模型顺序。
// 返回的顺序保证首个元素是策略选中的首选模型，其余为回退顺序。
func orderModelsForAlias(alias string, models []string, strategy string, c *gin.Context) []string {
	n := len(models)
	if n <= 1 {
		return models
	}
	start := 0
	switch strategy {
	case model.AliasStrategyRoundRobin:
		aliasRoundRobin.Lock()
		aliasRoundRobin.counters[alias]++
		start = aliasRoundRobin.counters[alias] % n
		aliasRoundRobin.Unlock()
	case model.AliasStrategyRandom:
		start = rand.Intn(n)
	case model.AliasStrategyIpHash:
		start = int(fnv32a(c.ClientIP()) % uint32(n))
	case model.AliasStrategyCookieHash:
		ck, err := c.Cookie("one-api-future-session")
		if err != nil || ck == "" {
			ck = c.ClientIP() // 无会话 cookie（如纯 API 调用）时回退 IP 哈希
		}
		start = int(fnv32a(ck) % uint32(n))
	case model.AliasStrategyPrimaryBackup:
		fallthrough
	default:
		start = 0
	}
	if start <= 0 {
		return models
	}
	ordered := make([]string, 0, n)
	for i := 0; i < n; i++ {
		ordered = append(ordered, models[(start+i)%n])
	}
	return ordered
}

func fnv32a(s string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(s))
	return h.Sum32()
}

// pickAliasModel 按别名的负载策略选出目标后端模型：
// 1) 过滤上下文不足当前会话的模型；
// 2) 按策略排序候选（轮询/随机/哈希/主备）；
// 3) 逐个探测是否有启用渠道（禁用渠道天然不在 ability 中），取首个可用者；
// 4) 全部无可用渠道时返回首选，让后续流程给出标准的"无可用渠道"错误。
func pickAliasModel(c *gin.Context, group string, alias string, entry model.AliasEntry) string {
	models := entry.Models
	if est := estimatePromptTokens(c); est > 0 {
		if filtered := filterModelsByContext(models, est); len(filtered) > 0 {
			models = filtered
		}
	}
	ordered := orderModelsForAlias(alias, models, entry.Strategy, c)
	for _, m := range ordered {
		if _, err := model.CacheGetRandomSatisfiedChannel(group, m, false); err == nil {
			return m
		}
	}
	logger.SysError(fmt.Sprintf("模型别名 %s 的所有后端模型均无可用渠道：%v", alias, ordered))
	return ordered[0]
}

func Distribute() func(c *gin.Context) {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		userId := c.GetInt(ctxkey.Id)
		userGroup, _ := model.CacheGetUserGroup(userId)
		c.Set(ctxkey.Group, userGroup)
		var requestModel string
		var channel *model.Channel
		channelId, ok := c.Get(ctxkey.SpecificChannelId)
		if ok {
			id, err := strconv.Atoi(channelId.(string))
			if err != nil {
				abortWithMessage(c, http.StatusBadRequest, "无效的渠道 Id")
				return
			}
			channel, err = model.GetChannelById(id, true)
			if err != nil {
				abortWithMessage(c, http.StatusBadRequest, "无效的渠道 Id")
				return
			}
			if channel.Status != model.ChannelStatusEnabled {
				abortWithMessage(c, http.StatusForbidden, "该渠道已被禁用")
				return
			}
		} else {
			requestModel = c.GetString(ctxkey.RequestModel)
			// 全局模型别名解析：
			//   单后端映射 -> 直接换真实模型；
			//   多后端（数组/对象）-> 按负载策略选出模型（过滤禁用渠道与上下文不足的模型）
			if entry, ok := model.GetAliasEntry(requestModel); ok {
				aliasName := requestModel
				requestModel = pickAliasModel(c, userGroup, requestModel, entry)
				// 记录别名 -> 本次选中模型的映射，供报文改写（含重试路径）使用
				c.Set(ctxkey.AliasOverride, map[string]string{aliasName: requestModel})
			} else if realModel, ok := model.GetRealModelName(requestModel); ok {
				requestModel = realModel
			}
			var err error
			channel, err = model.CacheGetRandomSatisfiedChannel(userGroup, requestModel, false)
			if err != nil {
				message := fmt.Sprintf("当前分组 %s 下对于模型 %s 无可用渠道", userGroup, requestModel)
				if channel != nil {
					logger.SysError(fmt.Sprintf("渠道不存在：%d", channel.Id))
					message = "数据库一致性已被破坏，请联系管理员"
				}
				abortWithMessage(c, http.StatusServiceUnavailable, message)
				return
			}
		}
		logger.Debugf(ctx, "user id %d, user group: %s, request model: %s, using channel #%d", userId, userGroup, requestModel, channel.Id)
		SetupContextForSelectedChannel(c, channel, requestModel)
		c.Next()
	}
}

func SetupContextForSelectedChannel(c *gin.Context, channel *model.Channel, modelName string) {
	c.Set(ctxkey.Channel, channel.Type)
	c.Set(ctxkey.ChannelId, channel.Id)
	c.Set(ctxkey.ChannelName, channel.Name)
	c.Set(ctxkey.SupportResponses, channel.SupportResponses)
	if channel.SystemPrompt != nil && *channel.SystemPrompt != "" {
		c.Set(ctxkey.SystemPrompt, *channel.SystemPrompt)
	}
	// 渠道级 model_mapping 并入全局模型别名表，
	// 让请求报文中的别名在转发前被改写为真实模型。
	// 多后端别名（数组/对象值）不进 mapping——其最终模型由
	// Distribute 的负载选择决定，通过 AliasOverride 覆盖。
	mapping := channel.GetModelMapping()
	if mapping == nil {
		mapping = make(map[string]string)
	}
	for alias, real := range model.GetModelAliasMap() {
		if strings.HasPrefix(real, "[") || strings.HasPrefix(real, "{") {
			continue
		}
		mapping[alias] = model.UnquoteAliasValue(real)
	}
	if ov, ok := c.Get(ctxkey.AliasOverride); ok {
		if om, ok := ov.(map[string]string); ok {
			for alias, chosen := range om {
				mapping[alias] = chosen
			}
		}
	}
	c.Set(ctxkey.ModelMapping, mapping)
	c.Set(ctxkey.OriginalModel, modelName) // for retry
	c.Request.Header.Set("Authorization", fmt.Sprintf("Bearer %s", channel.Key))
	c.Set(ctxkey.BaseURL, channel.GetBaseURL())
	cfg, _ := channel.LoadConfig()
	// this is for backward compatibility
	if channel.Other != nil {
		switch channel.Type {
		case channeltype.Azure:
			if cfg.APIVersion == "" {
				cfg.APIVersion = *channel.Other
			}
		case channeltype.Xunfei:
			if cfg.APIVersion == "" {
				cfg.APIVersion = *channel.Other
			}
		case channeltype.Gemini:
			if cfg.APIVersion == "" {
				cfg.APIVersion = *channel.Other
			}
		case channeltype.AIProxyLibrary:
			if cfg.LibraryID == "" {
				cfg.LibraryID = *channel.Other
			}
		case channeltype.Ali:
			if cfg.Plugin == "" {
				cfg.Plugin = *channel.Other
			}
		}
	}
	c.Set(ctxkey.Config, cfg)
}
