// Package phira 实现对 Phira 上游 API 的 HTTP 调用（认证 / 谱面 / 成绩），
// 满足 server.PhiraAPI 接口。
//
// /me 结果按 token 缓存 6h（加速重连认证），/record/:id 与 /chart/:id 结果缓存 6h
// （记录与谱面基本不可变）；启用 Redis 时缓存转为多实例共享。
// 后台 StartRefresh goroutine 每小时被动失效少量快到期键，触发下次 GetOrSet 时重拉。
package phira

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Pimeng/gooophira-mp/internal/cache"
	"github.com/Pimeng/gooophira-mp/internal/config"
	"github.com/Pimeng/gooophira-mp/internal/server"
)

// DefaultEndpoint 是 Phira API 默认端点。
const DefaultEndpoint = "https://phira.5wyxi.com"

const fetchTimeout = 10 * time.Second

// 进程级共享缓存（对齐 TS phiraApiClient 的 tokenCache / recordCache）。
// token 缓存不落盘（含凭证，仅驻内存）；record / chart 缓存落盘（不可变，重启后仍有效）。
var (
	tokenCache  = cache.NewString[server.PhiraUserInfo](cache.Options{Name: "token_cache.json", TTL: 6 * time.Hour, MaxMem: 500, Persist: false})
	recordCache = cache.NewInt[config.RecordData](cache.Options{Name: "record_cache.json", TTL: 6 * time.Hour, MaxMem: 500, Persist: true})
	chartCache  = cache.NewInt[config.Chart](cache.Options{Name: "chart_cache.json", TTL: 6 * time.Hour, MaxMem: 500, Persist: true})
)

// Client 是 Phira API HTTP 客户端。
type Client struct {
	Endpoint string
	HTTP     *http.Client

	// stop 关闭后台刷新 goroutine；done 在 goroutine 退出时关闭。
	// NewClient 初始化 stop；StartRefresh 初始化 done。
	stop chan struct{}
	done chan struct{}
}

// 确保 Client 满足 server.PhiraAPI。
var _ server.PhiraAPI = (*Client)(nil)

// NewClient 用给定端点创建客户端（空端点用默认值）。
// HTTP.Client 不设 Timeout——超时由调用方传入的 ctx 控制（对齐 context 贯穿策略）。
func NewClient(endpoint string) *Client {
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	return &Client{
		Endpoint: strings.TrimRight(endpoint, "/"),
		HTTP:     &http.Client{},
		stop:     make(chan struct{}),
	}
}

func (c *Client) get(ctx context.Context, path string, header map[string]string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Endpoint+path, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	return c.HTTP.Do(req)
}

// FetchUserInfo 调用 /me 认证并返回用户信息。成功结果按 token 缓存 6 小时，重连时跳过 HTTP；
// 并发的相同 token 请求经 GetOrSet 合并为一次调用。
func (c *Client) FetchUserInfo(ctx context.Context, token string) (server.PhiraUserInfo, error) {
	return tokenCache.GetOrSet(token, func() (server.PhiraUserInfo, error) {
		return c.fetchUserInfo(ctx, token)
	})
}

func (c *Client) fetchUserInfo(ctx context.Context, token string) (server.PhiraUserInfo, error) {
	var zero server.PhiraUserInfo
	resp, err := c.get(ctx, "/me", map[string]string{"Authorization": "Bearer " + token})
	if err != nil {
		return zero, fmt.Errorf("auth-fetch-me-failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return zero, fmt.Errorf("auth-fetch-me-failed")
	}
	var data struct {
		ID       int    `json:"id"`
		Name     string `json:"name"`
		Language string `json:"language"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return zero, fmt.Errorf("auth-invalid-response")
	}
	name := strings.TrimSpace(data.Name)
	if name == "" {
		return zero, fmt.Errorf("auth-invalid-user-name")
	}
	return server.PhiraUserInfo{ID: data.ID, Name: name, Language: data.Language}, nil
}

// FetchChart 调用 /chart/:id 取谱面信息。结果缓存 6h（谱面基本不可变）。
func (c *Client) FetchChart(ctx context.Context, id int) (config.Chart, error) {
	return chartCache.GetOrSet(id, func() (config.Chart, error) {
		return c.fetchChart(ctx, id)
	})
}

func (c *Client) fetchChart(ctx context.Context, id int) (config.Chart, error) {
	resp, err := c.get(ctx, fmt.Sprintf("/chart/%d", id), nil)
	if err != nil {
		return config.Chart{}, fmt.Errorf("chart-fetch-failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return config.Chart{}, fmt.Errorf("chart-fetch-failed")
	}
	var data struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return config.Chart{}, fmt.Errorf("chart-fetch-failed")
	}
	return config.Chart{ID: data.ID, Name: data.Name}, nil
}

// FetchRecord 调用 /record/:id 取成绩数据。结果缓存 6h（记录不可变）。
func (c *Client) FetchRecord(ctx context.Context, id int) (config.RecordData, error) {
	return recordCache.GetOrSet(id, func() (config.RecordData, error) {
		return c.fetchRecord(ctx, id)
	})
}

func (c *Client) fetchRecord(ctx context.Context, id int) (config.RecordData, error) {
	resp, err := c.get(ctx, fmt.Sprintf("/record/%d", id), nil)
	if err != nil {
		return config.RecordData{}, fmt.Errorf("record-fetch-failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return config.RecordData{}, fmt.Errorf("record-fetch-failed")
	}
	var data struct {
		ID        int      `json:"id"`
		Player    int      `json:"player"`
		Chart     *int     `json:"chart"`
		Score     int      `json:"score"`
		Perfect   int      `json:"perfect"`
		Good      int      `json:"good"`
		Bad       int      `json:"bad"`
		Miss      int      `json:"miss"`
		MaxCombo  int      `json:"max_combo"`
		Accuracy  float64  `json:"accuracy"`
		FullCombo bool     `json:"full_combo"`
		Std       *float64 `json:"std"`
		StdScore  *float64 `json:"std_score"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return config.RecordData{}, fmt.Errorf("record-fetch-failed")
	}
	return config.RecordData{
		ID: data.ID, Player: data.Player, Chart: data.Chart, Score: data.Score,
		Perfect: data.Perfect, Good: data.Good, Bad: data.Bad, Miss: data.Miss,
		MaxCombo: data.MaxCombo, Accuracy: data.Accuracy, FullCombo: data.FullCombo,
		Std: data.Std, StdScore: data.StdScore,
	}, nil
}
