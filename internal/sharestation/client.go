// Package sharestation 实现对「分享站」(Share Station) 的上传与可见性控制 HTTP 调用。
// 对应 TS server/utils/shareStation.ts。回放可上传到分享站供他人下载观看。
package sharestation

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/Pimeng/gooophira-mp/internal/netutil"
)

// Config 是分享站连接配置。
type Config struct {
	URL   string // 分享站基址（无尾斜杠）
	Token string // Bearer 认证 token
}

// UploadResult 是上传结果。ScoreID 由 replay_id 解析得到（0 表示未解析出）。
type UploadResult struct {
	ReplayID string
	ScoreID  int
}

// Client 是分享站 HTTP 客户端。
type Client struct {
	cfg  Config
	http *http.Client
}

// scoreIDRe 从 replay_id "{user}_{chart}_{score}.phirarec" 中解析 score_id。
var scoreIDRe = regexp.MustCompile(`_(\d+)\.phirarec$`)

var errUploadFailed = errors.New("upload-failed")

const requestTimeout = 60 * time.Second

// NewClient 创建分享站客户端。
// HTTP 客户端经 netutil.NewClient() 构造（Android 注入公共 DNS 解析，
// 其它平台走系统 resolver），代理配置统一由 netutil 管理。
func NewClient(cfg Config) *Client {
	httpClient := netutil.NewClient()
	httpClient.Timeout = requestTimeout
	return &Client{cfg: cfg, http: httpClient}
}

// Upload 以 multipart 上传回放文件到 {URL}/upload_direct，返回解析出的 ScoreID。
func (c *Client) Upload(fileBytes []byte, filename, chartName, username string) (UploadResult, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		return UploadResult{}, err
	}
	if _, err := fw.Write(fileBytes); err != nil {
		return UploadResult{}, err
	}
	if chartName != "" {
		_ = mw.WriteField("chart_name", chartName)
	}
	if username != "" {
		_ = mw.WriteField("username", username)
	}
	if err := mw.Close(); err != nil {
		return UploadResult{}, err
	}

	req, err := http.NewRequest(http.MethodPost, c.cfg.URL+"/upload_direct", &buf)
	if err != nil {
		return UploadResult{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.cfg.Token)
	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err := c.http.Do(req)
	if err != nil {
		return UploadResult{}, errUploadFailed
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return UploadResult{}, errUploadFailed
	}
	var out struct {
		ReplayID string `json:"replay_id"`
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err := json.Unmarshal(body, &out); err != nil {
		return UploadResult{}, errUploadFailed
	}
	res := UploadResult{ReplayID: out.ReplayID}
	if m := scoreIDRe.FindStringSubmatch(out.ReplayID); m != nil {
		res.ScoreID, _ = strconv.Atoi(m[1])
	}
	return res, nil
}

// SetVisibility 设置某成绩在分享站的可见性（show/hide）。
func (c *Client) SetVisibility(scoreID int, visible bool) error {
	endpoint := "/hide/"
	if visible {
		endpoint = "/show/"
	}
	req, err := http.NewRequest(http.MethodPost, c.cfg.URL+endpoint+strconv.Itoa(scoreID), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.cfg.Token)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode/100 != 2 {
		return errUploadFailed
	}
	return nil
}
