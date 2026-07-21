// Package phira 实现对 Phira 上游 API 的 HTTP 调用（认证 / 谱面 / 成绩），
// 满足 server.PhiraAPI 接口。
//
// /me 结果按 token 缓存 6h（加速重连认证），/record/:id 与 /chart/:id 结果缓存 6h
// （记录与谱面基本不可变）；启用 Redis 时缓存转为多实例共享。
// 后台 StartRefresh goroutine 每小时被动失效少量快到期键，触发下次 GetOrSet 时重拉。
//
// HTTP 客户端经 netutil.NewClient() 构造：在 Android（含 Termux）上注入公共 DNS
// 解析以绕开 [::1]:53 / 127.0.0.1:53 connection refused，其它平台走系统 resolver。
package phira

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Pimeng/gooophira-mp/internal/common/cache"
	"github.com/Pimeng/gooophira-mp/internal/common/platform/netutil"
	"github.com/Pimeng/gooophira-mp/internal/config"
	"github.com/Pimeng/gooophira-mp/internal/core/server"
)

// DefaultEndpoint 是 Phira API 默认端点。
const DefaultEndpoint = "https://phira.5wyxi.com"

// 重试策略：网络错误或 5xx 时按线性退避重试。
//   - maxRetries=3：初始 1 次 + 重试 3 次 = 4 次总尝试
//   - retryBaseDelay=500ms：线性退避，第 n 次重试前等待 n*500ms（500ms / 1s / 1.5s）
//   - fetchGlobalTimeout=20s：整个重试过程的全局超时上限
//
// 用 var 而非 const 便于测试临时调短退避时间，避免 3s 等待；运行时视为只读。
var (
	maxRetries         = 3
	retryBaseDelay     = 500 * time.Millisecond
	fetchGlobalTimeout = 20 * time.Second
)

// 进程级共享缓存（对齐 TS phiraApiClient 的 tokenCache / recordCache）。
// token 缓存不落盘（含凭证，仅驻内存）；record / chart 缓存落盘（不可变，重启后仍有效）。
var (
	tokenCache  = cache.NewString[server.PhiraUserInfo](cache.Options{Name: "token_cache.json", TTL: 6 * time.Hour, MaxMem: 500, Persist: false})
	recordCache = cache.NewInt[config.RecordData](cache.Options{Name: "record_cache.json", TTL: 6 * time.Hour, MaxMem: 500, Persist: true})
	// Name 含 v2 后缀：config.Chart 新增 Level/Charter/Illustration 字段后，
	// 旧 chart_cache.json 反序列化会让新字段为零值，改文件名令旧缓存自动失效。
	chartCache    = cache.NewInt[config.Chart](cache.Options{Name: "chart_cache_v2.json", TTL: 6 * time.Hour, MaxMem: 500, Persist: true})
	userNameCache = cache.NewInt[string](cache.Options{Name: "user_name_cache.json", TTL: 6 * time.Hour, MaxMem: 500, Persist: true})
)

// Client 是 Phira API HTTP 客户端。
type Client struct {
	Endpoint string
	HTTP     *http.Client

	// Logger 可选；非 nil 且开启 DEBUG 时，记录重试与请求详情用于排查上游问题。
	Logger server.Logger

	// stop 关闭后台刷新 goroutine；done 在 goroutine 退出时关闭。
	// NewClient 初始化 stop；StartRefresh 初始化 done。
	stop chan struct{}
	done chan struct{}
}

// 确保 Client 满足 server.PhiraAPI。
var _ server.PhiraAPI = (*Client)(nil)

// NewClient 用给定端点创建客户端（空端点用默认值）。
// HTTP.Client 经 netutil.NewClient() 构造（Android 注入公共 DNS 解析，其它平台走系统 resolver）。
// HTTP.Client 不设 Timeout——超时由调用方传入的 ctx 控制（对齐 context 贯穿策略）。
func NewClient(endpoint string) *Client {
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	return &Client{
		Endpoint: strings.TrimRight(endpoint, "/"),
		HTTP:     netutil.NewClient(),
		stop:     make(chan struct{}),
	}
}

// SetLogger 注入日志器，便于在 DEBUG 级别输出重试详情。
func (c *Client) SetLogger(l server.Logger) { c.Logger = l }

// logDebug 在 DEBUG 开启时记录一条 phira 请求日志。
func (c *Client) logDebug(msg string) {
	if c.Logger != nil && c.Logger.DebugEnabled() {
		c.Logger.Debug(msg)
	}
}

// tokenPrefix 返回 token 的脱敏前缀（仅前 8 字符），用于日志排查且不泄露完整凭证。
func tokenPrefix(token string) string {
	if len(token) <= 8 {
		return strings.Repeat("*", len(token))
	}
	return token[:8]
}

func (c *Client) get(ctx context.Context, path string, header map[string]string) (*http.Response, error) {
	// 全局超时覆盖整个重试过程：即使 parent ctx 未设超时，phira 侧也保证 20s 上限。
	retryCtx, cancel := context.WithTimeout(ctx, fetchGlobalTimeout)
	defer cancel()

	totalAttempts := maxRetries + 1
	c.logDebug(fmt.Sprintf("[phira] GET %s 开始（最多 %d 次尝试，全局超时 %s）", path, totalAttempts, fetchGlobalTimeout))

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			// 线性退避：第 1 次重试等 500ms，第 2 次 1s，第 3 次 1.5s。
			delay := retryBaseDelay * time.Duration(attempt)
			c.logDebug(fmt.Sprintf("[phira] GET %s 第 %d/%d 次重试（等待 %s，上次错误：%v）", path, attempt+1, totalAttempts, delay, lastErr))
			select {
			case <-retryCtx.Done():
				c.logDebug(fmt.Sprintf("[phira] GET %s 全局超时中断（已尝试 %d/%d 次，最后错误：%v）", path, attempt, totalAttempts, lastErr))
				if lastErr != nil {
					return nil, lastErr
				}
				return nil, retryCtx.Err()
			case <-time.After(delay):
			}
		}

		resp, err := c.getOnce(retryCtx, path, header)
		if err != nil {
			lastErr = err
			c.logDebug(fmt.Sprintf("[phira] GET %s 第 %d/%d 次请求失败（网络错误）：%v", path, attempt+1, totalAttempts, err))
			continue // 网络错误/超时：重试
		}
		// 5xx 服务器错误：关闭 body 后重试。
		if resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("phira-server-error-%d", resp.StatusCode)
			c.logDebug(fmt.Sprintf("[phira] GET %s 第 %d/%d 次收到 HTTP %d（5xx 触发重试）", path, attempt+1, totalAttempts, resp.StatusCode))
			_ = resp.Body.Close()
			continue
		}
		c.logDebug(fmt.Sprintf("[phira] GET %s 第 %d/%d 次成功（状态码 %d）", path, attempt+1, totalAttempts, resp.StatusCode))
		// 2xx 成功或 4xx 客户端错误（不可重试）：交由调用方处理状态码。
		return resp, nil
	}
	c.logDebug(fmt.Sprintf("[phira] GET %s 重试耗尽（共 %d 次，最后错误：%v）", path, totalAttempts, lastErr))
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, retryCtx.Err()
}

// getOnce 发起单次 GET 请求（无重试）。resp.Body 由调用方负责关闭。
func (c *Client) getOnce(ctx context.Context, path string, header map[string]string) (*http.Response, error) {
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
	c.logDebug(fmt.Sprintf("[phira] /me 认证请求（token 前缀：%s…）", tokenPrefix(token)))
	resp, err := c.get(ctx, "/me", map[string]string{"Authorization": "Bearer " + token})
	if err != nil {
		c.logDebug(fmt.Sprintf("[phira] /me 请求失败（底层错误）：%v", err))
		return zero, fmt.Errorf("auth-fetch-me-failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		c.logDebug(fmt.Sprintf("[phira] /me 认证被拒（HTTP %d）", resp.StatusCode))
		return zero, fmt.Errorf("auth-fetch-me-failed")
	}
	var data struct {
		ID       int    `json:"id"`
		Name     string `json:"name"`
		Language string `json:"language"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		c.logDebug(fmt.Sprintf("[phira] /me 响应解码失败：%v", err))
		return zero, fmt.Errorf("auth-invalid-response")
	}
	name := strings.TrimSpace(data.Name)
	if name == "" {
		c.logDebug(fmt.Sprintf("[phira] /me 返回空用户名（id=%d）", data.ID))
		return zero, fmt.Errorf("auth-invalid-user-name")
	}
	c.logDebug(fmt.Sprintf("[phira] /me 认证成功（id=%d, name=%s）", data.ID, name))
	return server.PhiraUserInfo{ID: data.ID, Name: name, Language: data.Language}, nil
}

// FetchUserName 调用 /user/:id 取用户公开昵称。结果按 id 缓存 6h（昵称极少变化）。
// 用于配置 SYSTEM_USER_ID 后，让回放假观战者与系统聊天发送者统一呈现为该 bot 身份。
func (c *Client) FetchUserName(ctx context.Context, id int) (string, error) {
	return userNameCache.GetOrSet(id, func() (string, error) {
		return c.fetchUserName(ctx, id)
	})
}

func (c *Client) fetchUserName(ctx context.Context, id int) (string, error) {
	resp, err := c.get(ctx, fmt.Sprintf("/user/%d", id), nil)
	if err != nil {
		return "", fmt.Errorf("user-fetch-failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("user-fetch-failed")
	}
	var data struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return "", fmt.Errorf("user-fetch-failed")
	}
	name := strings.TrimSpace(data.Name)
	if name == "" {
		return "", fmt.Errorf("user-fetch-empty-name")
	}
	return name, nil
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
		ID           int    `json:"id"`
		Name         string `json:"name"`
		Level        string `json:"level"`
		Charter      string `json:"charter"`
		Illustration string `json:"illustration"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return config.Chart{}, fmt.Errorf("chart-fetch-failed")
	}
	return config.Chart{
		ID:           data.ID,
		Name:         data.Name,
		Level:        data.Level,
		Charter:      data.Charter,
		Illustration: data.Illustration,
	}, nil
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
