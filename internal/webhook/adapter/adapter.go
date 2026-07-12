// Package adapter 实现各类第三方平台的投递适配器，供 webhook.Dispatcher 编排调用。
//
// 每个适配器实现 Adapter 接口，负责把一条 server.Event 投递到对应平台（单次请求语义）。
// 重试/超时/停止等编排由 webhook.Dispatcher 统一控制；适配器只关心平台调用与错误分类
// （可重试 vs 不可重试 / 静默跳过）。
//
// 已实现适配器：
//   - HTTP：经 HTTP POST 投递通用 JSON / Discord 等格式化载荷（含可选 HMAC 签名）。
//   - Feishu：飞书开放平台 SDK，发交互式模板消息（含图片上传换 image_key）。
//
// 横向扩展：在 adapter/ 下新增适配器文件并实现 Adapter 接口，
// 然后在 webhook.New 时按 config.WebhookTarget.Type 注册到 Dispatcher 即可。
package adapter

import (
	"context"

	"github.com/Pimeng/gooophira-mp/internal/config"
	"github.com/Pimeng/gooophira-mp/internal/server"
)

// userAgent 出站 HTTP 请求统一的 User-Agent（HTTP 投递与飞书下载图片共用）。
const userAgent = "phira-mp-webhook"

// Adapter 是平台投递适配器的统一接口。单次投递语义；调用方（Dispatcher）
// 负责超时控制、按返回值决定重试与否。
//
// 返回值约定：
//   - ok=true：投递成功（或适配器决定静默跳过该目标，例如格式不支持）。
//   - ok=false, retryable=true：瞬时故障（网络/超时/5xx/429），Dispatcher 可重试。
//   - ok=false, retryable=false：客户端/配置/权限类错误，不重试。
type Adapter interface {
	Deliver(ctx context.Context, t config.WebhookTarget, ev server.Event) (ok, retryable bool)
}

// Logger 是适配器所需的最小日志接口（与 webhook.Logger / server.Logger 兼容，便于注入）。
type Logger interface {
	Debug(msg string)
	Warn(msg string)
}
