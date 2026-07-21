// hub_fetch.go 把 Hub 派生上游 HTTP 调用 ctx 的取数方法从 hub.go 拆出。
// 缓存由 phira.Client 层管理，Hub 仅做 ctx 超时与 nil 守卫。
package server

import (
	"errors"
	"time"

	"github.com/Pimeng/gooophira-mp/internal/config"
)

// fetchTimeout 是 Hub 派生上游 HTTP 调用 ctx 的超时上限。
// 设为 20s 与 phira.Client 内部的全局重试超时一致，避免 parent ctx 提前截断重试窗口。
const fetchTimeout = 20 * time.Second

// FetchChart 取谱面（缓存由 phira.Client 层管理，Hub 不再缓存）。
func (h *Hub) FetchChart(user *User, id int) (config.Chart, error) {
	if h.Phira == nil {
		return config.Chart{}, errors.New("chart-fetch-failed")
	}
	ctx, cancel := h.ctxWithTimeout(fetchTimeout)
	defer cancel()
	return h.Phira.FetchChart(ctx, id)
}

// FetchRecord 取成绩（缓存由 phira.Client 层管理，Hub 不再缓存）。
func (h *Hub) FetchRecord(user *User, id int) (config.RecordData, error) {
	if h.Phira == nil {
		return config.RecordData{}, errors.New("record-fetch-failed")
	}
	ctx, cancel := h.ctxWithTimeout(fetchTimeout)
	defer cancel()
	return h.Phira.FetchRecord(ctx, id)
}

// FetchUserInfo 用 token 认证取用户信息（缓存由 phira.Client 层管理）。
// 与 FetchChart/FetchRecord 一致地由 Hub 派生 ctx，调用方无需感知 context。
func (h *Hub) FetchUserInfo(token string) (PhiraUserInfo, error) {
	if h.Phira == nil {
		return PhiraUserInfo{}, errors.New("auth-fetch-failed")
	}
	ctx, cancel := h.ctxWithTimeout(fetchTimeout)
	defer cancel()
	return h.Phira.FetchUserInfo(ctx, token)
}
