package server

import "github.com/Pimeng/gooophira-mp/internal/config"

// PhiraUserInfo 是 Phira API /me 返回的用户信息。
type PhiraUserInfo struct {
	ID       int
	Name     string
	Language string
}

// PhiraAPI 抽象对 Phira 上游 API 的调用（认证 / 取谱面 / 取成绩）。
// 具体 HTTP 实现见 Stage 4 network/phira；测试用 mock。
type PhiraAPI interface {
	// FetchUserInfo 用 token 认证并返回用户信息。
	FetchUserInfo(token string) (PhiraUserInfo, error)
	// FetchChart 按 id 取谱面信息。
	FetchChart(id int) (config.Chart, error)
	// FetchRecord 按 id 取成绩数据。
	FetchRecord(id int) (config.RecordData, error)
}
