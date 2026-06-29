// Package phira 实现对 Phira 上游 API 的 HTTP 调用（认证 / 谱面 / 成绩），
// 满足 server.PhiraAPI 接口。
//
// /me 结果按 token 缓存 30s（加速重连认证），/record/:id 结果缓存 1h（记录不可变）；
// 启用 Redis 时缓存转为多实例共享。出站代理 / 重试为后续项。
package phira

import (
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
// token 缓存不落盘（含凭证，仅驻内存）；record 缓存落盘（记录不可变，重启后仍有效）。
var (
	tokenCache  = cache.NewString[server.PhiraUserInfo](cache.Options{Name: "token_cache.json", TTL: 3 * time.Hour, MaxMem: 500, Persist: false})
	recordCache = cache.NewInt[config.RecordData](cache.Options{Name: "record_cache.json", TTL: time.Hour, MaxMem: 500, Persist: true})
)

// Client 是 Phira API HTTP 客户端。
type Client struct {
	Endpoint string
	HTTP     *http.Client
}

// 确保 Client 满足 server.PhiraAPI。
var _ server.PhiraAPI = (*Client)(nil)

// NewClient 用给定端点创建客户端（空端点用默认值）。
func NewClient(endpoint string) *Client {
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	return &Client{
		Endpoint: strings.TrimRight(endpoint, "/"),
		HTTP:     &http.Client{Timeout: fetchTimeout},
	}
}

func (c *Client) get(path string, header map[string]string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodGet, c.Endpoint+path, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	return c.HTTP.Do(req)
}

// FetchUserInfo 调用 /me 认证并返回用户信息。成功结果按 token 缓存 3 小时，重连时跳过 HTTP；
// 并发的相同 token 请求经 GetOrSet 合并为一次调用。
func (c *Client) FetchUserInfo(token string) (server.PhiraUserInfo, error) {
	return tokenCache.GetOrSet(token, func() (server.PhiraUserInfo, error) {
		return c.fetchUserInfo(token)
	})
}

func (c *Client) fetchUserInfo(token string) (server.PhiraUserInfo, error) {
	var zero server.PhiraUserInfo
	resp, err := c.get("/me", map[string]string{"Authorization": "Bearer " + token})
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

// FetchChart 调用 /chart/:id 取谱面信息。
func (c *Client) FetchChart(id int) (config.Chart, error) {
	resp, err := c.get(fmt.Sprintf("/chart/%d", id), nil)
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

// FetchRecord 调用 /record/:id 取成绩数据。结果缓存 1h（记录不可变）。
func (c *Client) FetchRecord(id int) (config.RecordData, error) {
	return recordCache.GetOrSet(id, func() (config.RecordData, error) {
		return c.fetchRecord(id)
	})
}

func (c *Client) fetchRecord(id int) (config.RecordData, error) {
	resp, err := c.get(fmt.Sprintf("/record/%d", id), nil)
	if err != nil {
		return config.RecordData{}, fmt.Errorf("record-fetch-failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return config.RecordData{}, fmt.Errorf("record-fetch-failed")
	}
	var data struct {
		ID        int     `json:"id"`
		Player    int     `json:"player"`
		Chart     *int    `json:"chart"`
		Score     int     `json:"score"`
		Perfect   int     `json:"perfect"`
		Good      int     `json:"good"`
		Bad       int     `json:"bad"`
		Miss      int     `json:"miss"`
		MaxCombo  int     `json:"max_combo"`
		Accuracy  float64 `json:"accuracy"`
		FullCombo bool    `json:"full_combo"`
		Std       float64 `json:"std"`
		StdScore  float64 `json:"std_score"`
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
