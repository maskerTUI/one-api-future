package model

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// 负载策略常量
const (
	AliasStrategyRoundRobin    = "round_robin"    // 轮询
	AliasStrategyRandom        = "random"         // 随机
	AliasStrategyIpHash        = "ip_hash"        // IP 哈希
	AliasStrategyCookieHash    = "cookie_hash"    // Cookie 哈希
	AliasStrategyPrimaryBackup = "primary_backup" // 主备
)

// 全局模型别名表：别名 -> 后端模型（支持一对多，带负载策略）。
// 值支持三种 JSON 形态：
//   "alias": "gpt-4o"                                           单后端（直接映射）
//   "alias": ["m1", "m2"]                                       多后端，默认轮询
//   "alias": {"models": ["m1","m2"], "strategy": "round_robin"} 多后端，指定策略
// 策略：round_robin / random / ip_hash / cookie_hash / primary_backup

var (
	modelAliasMap   = make(map[string]string)
	modelAliasMutex sync.RWMutex
)

// UpdateModelAliasByJSONString 从 JSON 字符串更新别名表。
// 值用 json.RawMessage 保存原始 JSON 文本，以支持字符串/数组/对象三种形态。
func UpdateModelAliasByJSONString(value string) error {
	raw := make(map[string]json.RawMessage)
	if value != "" {
		if err := json.Unmarshal([]byte(value), &raw); err != nil {
			return fmt.Errorf("模型别名配置不是合法的 JSON 对象: %w", err)
		}
	}
	m := make(map[string]string, len(raw))
	for k, v := range raw {
		m[k] = strings.TrimSpace(string(v))
	}
	modelAliasMutex.Lock()
	defer modelAliasMutex.Unlock()
	modelAliasMap = m
	return nil
}

// UnquoteAliasValue 若值是带引号的 JSON 字符串则去掉引号还原文本
func UnquoteAliasValue(raw string) string {
	if strings.HasPrefix(raw, "\"") {
		var s string
		if err := json.Unmarshal([]byte(raw), &s); err == nil {
			return s
		}
	}
	return raw
}

// GetModelAliasMap 返回别名表副本
func GetModelAliasMap() map[string]string {
	modelAliasMutex.RLock()
	defer modelAliasMutex.RUnlock()
	m := make(map[string]string, len(modelAliasMap))
	for k, v := range modelAliasMap {
		m[k] = v
	}
	return m
}

// GetRealModelName 返回别名对应的真实模型名（仅单后端映射，向后兼容）
func GetRealModelName(name string) (string, bool) {
	modelAliasMutex.RLock()
	defer modelAliasMutex.RUnlock()
	real, ok := modelAliasMap[name]
	if !ok || real == "" {
		return name, false
	}
	real = UnquoteAliasValue(real)
	if real == "" || real == name || strings.HasPrefix(real, "[") || strings.HasPrefix(real, "{") {
		return name, false
	}
	return real, true
}

// AliasEntry 解析后的别名条目
type AliasEntry struct {
	Models   []string
	Strategy string
}

// GetAliasEntry 解析别名为候选模型列表与负载策略。
// 单后端映射（字符串值）返回单元素列表与空策略（调用方直接使用，不进入负载选择）。
func GetAliasEntry(name string) (AliasEntry, bool) {
	modelAliasMutex.RLock()
	defer modelAliasMutex.RUnlock()
	raw, ok := modelAliasMap[name]
	if !ok || raw == "" {
		return AliasEntry{}, false
	}
	entry := AliasEntry{}
	switch {
	case strings.HasPrefix(raw, "["), strings.HasPrefix(raw, "{"):
		// 数组或对象形态
		if strings.HasPrefix(raw, "[") {
			var models []string
			if err := json.Unmarshal([]byte(raw), &models); err != nil || len(models) == 0 {
				return AliasEntry{}, false
			}
			entry.Models = models
			entry.Strategy = AliasStrategyRoundRobin
		} else {
			var obj struct {
				Models   []string `json:"models"`
				Strategy string   `json:"strategy"`
			}
			if err := json.Unmarshal([]byte(raw), &obj); err != nil || len(obj.Models) == 0 {
				return AliasEntry{}, false
			}
			entry.Models = obj.Models
			entry.Strategy = obj.Strategy
		}
	default:
		real := UnquoteAliasValue(raw)
		if real == "" || real == name {
			return AliasEntry{}, false
		}
		entry.Models = []string{real}
	}
	return entry, true
}

// 模型上下文长度表（tokens）：{"模型名": 上下文长度}
// 别名负载时用于排除上下文小于当前会话的模型；未配置的模型不参与过滤。
var (
	modelContextTokens map[string]int = make(map[string]int)
	modelContextMutex  sync.RWMutex
)

// UpdateModelContextTokensByJSONString 从 JSON 字符串更新模型上下文长度表
func UpdateModelContextTokensByJSONString(value string) error {
	m := make(map[string]int)
	if value != "" {
		if err := json.Unmarshal([]byte(value), &m); err != nil {
			return fmt.Errorf("模型上下文长度配置不是合法的 JSON 对象: %w", err)
		}
	}
	modelContextMutex.Lock()
	defer modelContextMutex.Unlock()
	modelContextTokens = m
	return nil
}

// GetModelContextTokens 返回模型上下文长度表副本
func GetModelContextTokens() map[string]int {
	modelContextMutex.RLock()
	defer modelContextMutex.RUnlock()
	m := make(map[string]int, len(modelContextTokens))
	for k, v := range modelContextTokens {
		m[k] = v
	}
	return m
}

// GetMergedContextTokens 合并全局配置与各渠道级 model_context 配置的模型上下文长度表。
// 同一模型多处配置时取最大值（保守：按更大的上下文判断）。
func GetMergedContextTokens() map[string]int {
	merged := GetModelContextTokens()
	var channels []Channel
	if DB != nil {
		if err := DB.Where("status = ?", ChannelStatusEnabled).Find(&channels).Error; err == nil {
			for _, ch := range channels {
				if ch.ModelContext == "" {
					continue
				}
				var cm map[string]int
				if err := json.Unmarshal([]byte(ch.ModelContext), &cm); err != nil {
					continue
				}
				for m, v := range cm {
					if v <= 0 {
						continue
					}
					if old, ok := merged[m]; !ok || v > old {
						merged[m] = v
					}
				}
			}
		}
	}
	return merged
}
