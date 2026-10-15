package model

import (
	"encoding/json"
	"fmt"
	"sync"
)

// 全局模型别名表：别名 -> 真实模型名。
// 用户请求别名时，网关先解析为真实模型再做渠道选择与上游调用；
// 后端模型变更时只需修改此映射，用户侧配置保持不变。

var (
	modelAliasMap   = make(map[string]string)
	modelAliasMutex sync.RWMutex
)

// UpdateModelAliasByJSONString 从 JSON 字符串（{"别名":"真实模型",...}）更新别名表
func UpdateModelAliasByJSONString(value string) error {
	m := make(map[string]string)
	if value != "" {
		if err := json.Unmarshal([]byte(value), &m); err != nil {
			return fmt.Errorf("模型别名配置不是合法的 JSON 对象: %w", err)
		}
	}
	modelAliasMutex.Lock()
	defer modelAliasMutex.Unlock()
	modelAliasMap = m
	return nil
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

// GetRealModelName 返回别名对应的真实模型名，不存在则返回原名与 false
func GetRealModelName(name string) (string, bool) {
	modelAliasMutex.RLock()
	defer modelAliasMutex.RUnlock()
	real, ok := modelAliasMap[name]
	if !ok || real == "" || real == name {
		return name, false
	}
	return real, true
}
