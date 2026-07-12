package webhook

import (
	"github.com/Pimeng/gooophira-mp/internal/server"
	"github.com/Pimeng/gooophira-mp/internal/webhook/adapter"
)

// Format 按目标类型把事件编码为请求体与 Content-Type。
//   - generic：结构化 JSON（含全部字段），便于自定义机器人自行渲染。
//   - discord：{"content": "<文本>"}
//   - feishu ：走飞书开放平台 SDK 投递，不经 HTTP POST 通道（返回 nil 跳过）。
//
// 返回 nil body 表示该类型无法编码 / 不走 HTTP（跳过该目标）。
// 实际格式化逻辑在 adapter 包，这里保留导出 API 转调以兼容外部（测试）使用。
func Format(typ string, ev server.Event) (body []byte, contentType string) {
	return adapter.Format(typ, ev)
}

// RenderText 生成事件的人类可读文本（用于 Discord/飞书等纯文本机器人）。
// 转调 adapter.RenderText 以保留导出 API。
func RenderText(ev server.Event) string {
	return adapter.RenderText(ev)
}
