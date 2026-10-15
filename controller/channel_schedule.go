package controller

import (
	"strconv"
	"strings"
	"time"

	"github.com/songquanpeng/one-api/common/logger"
	"github.com/songquanpeng/one-api/model"
)

// hhmm2minutes 将 "HH:MM" 转换为当天的分钟数，非严格两位格式或解析失败返回 -1
func hhmm2minutes(s string) int {
	s = strings.TrimSpace(s)
	parts := strings.Split(s, ":")
	if len(parts) != 2 || len(parts[0]) != 2 || len(parts[1]) != 2 {
		return -1
	}
	h, err := strconv.Atoi(parts[0])
	if err != nil || h < 0 || h > 23 {
		return -1
	}
	m, err := strconv.Atoi(parts[1])
	if err != nil || m < 0 || m > 59 {
		return -1
	}
	return h*60 + m
}

// inDisableWindow 判断当前时间是否处于禁用时间窗内（支持跨零点，如 23:00 禁用、06:00 启用）
func inDisableWindow(nowMinutes int, disableMinutes int, enableMinutes int) bool {
	if disableMinutes < 0 || enableMinutes < 0 || disableMinutes == enableMinutes {
		return false
	}
	if disableMinutes > enableMinutes {
		// 跨零点：禁用时间晚于启用时间，如 23:00 -> 06:00
		return nowMinutes >= disableMinutes || nowMinutes < enableMinutes
	}
	return nowMinutes >= disableMinutes && nowMinutes < enableMinutes
}

// applyChannelSchedules 根据每个渠道的定时配置，把处于状态 1（启用）/2（手动禁用）
// 的渠道切换到时间窗对应的状态。状态 3（测试自动禁用）不受影响，仍由测试恢复逻辑管理。
func applyChannelSchedules(now time.Time) {
	channels, err := model.GetScheduledChannels()
	if err != nil {
		logger.SysError("failed to load scheduled channels: " + err.Error())
		return
	}
	nowMinutes := now.Hour()*60 + now.Minute()
	for _, channel := range channels {
		if channel.Status != model.ChannelStatusEnabled && channel.Status != model.ChannelStatusManuallyDisabled {
			continue
		}
		disableMinutes := hhmm2minutes(channel.AutoDisableTime)
		enableMinutes := hhmm2minutes(channel.AutoEnableTime)
		if disableMinutes < 0 || enableMinutes < 0 {
			continue
		}
		if inDisableWindow(nowMinutes, disableMinutes, enableMinutes) {
			if channel.Status == model.ChannelStatusEnabled {
				logger.SysLogf("channel #%d(%s) 进入定时禁用时间窗，自动禁用", channel.Id, channel.Name)
				model.UpdateChannelStatusById(channel.Id, model.ChannelStatusManuallyDisabled)
			}
		} else {
			if channel.Status == model.ChannelStatusManuallyDisabled {
				logger.SysLogf("channel #%d(%s) 离开定时禁用时间窗，自动启用", channel.Id, channel.Name)
				model.UpdateChannelStatusById(channel.Id, model.ChannelStatusEnabled)
			}
		}
	}
}

// StartChannelScheduleMonitor 每 30 秒检查一次所有启用了定时启停的渠道
func StartChannelScheduleMonitor() {
	logger.SysLog("channel schedule monitor started")
	for {
		applyChannelSchedules(time.Now())
		time.Sleep(30 * time.Second)
	}
}
