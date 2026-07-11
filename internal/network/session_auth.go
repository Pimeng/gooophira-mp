// session_auth.go 把「Phira 鉴权 / 重连恢复 / 一言欢迎语」从 session.go 拆出。
// 鉴权是双阶段进行：阶段 1 在锁内判断新用户还是重连并预绑会话；阶段 2 在锁外完成注册，
// 锁内再二次确认以避免竞态。重连时通过 ProtocolHack 把 WaitForReady 态修回客户端。
package network

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Pimeng/gooophira-mp/internal/l10n"
	"github.com/Pimeng/gooophira-mp/internal/netutil"
	"github.com/Pimeng/gooophira-mp/internal/protocol"
	"github.com/Pimeng/gooophira-mp/internal/server"
)

func (s *Session) handleAuthenticate(token string) {
	s.logAuthStart(token)
	if len(token) > 32 {
		s.failAuth("auth-invalid-token")
		return
	}
	// Phira HTTP 认证：阻塞调用，不持锁。Hub 内部派生 ctx 控制超时与关闭取消。
	info, err := s.hub.FetchUserInfo(token)
	if err != nil {
		s.logAuthFetchError(err)
		s.failAuth(err.Error())
		return
	}

	// 两阶段认证：降低 state.Mu 持有时间。
	// 阶段 1 — 快速检查 + 判断新用户还是重连，尽量减少锁内操作。
	s.state.Mu.Lock()

	// 维护模式：拒绝新连接，但放行已在线用户重连，让其回原房间完成对局。
	if s.state.Maintenance {
		if _, online := s.state.Users[info.ID]; !online {
			reason := "server-maintenance"
			if s.state.MaintenanceMessage != nil && *s.state.MaintenanceMessage != "" {
				reason = *s.state.MaintenanceMessage
			}
			s.state.Mu.Unlock()
			s.failAuth(reason)
			return
		}
	}

	var stale server.Session
	var user *server.User
	var roomState *protocol.ClientRoomState
	var restoreChartID *int32

	existing := s.state.Users[info.ID]
	if existing != nil {
		// ---- 重连路径：全程持锁（需读取/修改 Session、Room 等状态） ----
		if existing.Session() != nil && existing.Session() != server.Session(s) {
			stale = existing.Session()
		}
		existing.SetSession(s) // 先重绑到新会话——旧会话随后 Close 时 cleanup 会因此短路、保留房间
		user = existing
		s.user = user

		// 断线重连：构建客户端房间状态
		var room *server.Room
		var roomStateStr string
		if user.Room != nil {
			room = user.Room
			room.Mu.Lock()
			cs := room.ClientState(user, func(id int) *server.User { return s.state.Users[id] })
			roomStateStr = fmt.Sprintf("%T", room.State)
			// ProtocolHack：WaitForReady 态但已选谱 → 伪装为 SelectChart 让客户端先获知谱面 ID，
			// 随后 session 在延迟 20ms 后再切回 WaitingForReady。
			if _, wfr := room.State.(server.StateWaitForReady); wfr && room.Chart != nil {
				cs.State = s.hub.ClientRoomStateForJoin(room)
				cid := int32(room.Chart.ID)
				restoreChartID = &cid
			}
			room.Mu.Unlock()
			roomState = &cs
		}
		me := user.ToInfo()
		monitor := user.Monitor
		s.logReconnectDetected(user, room, roomStateStr)
		s.state.Mu.Unlock()

		// 踢旧会话（锁外）。此时 user.Session 已指向新会话，旧会话 cleanup 将短路，不会退房。
		if stale != nil {
			s.logReconnectStaleKicked(user, stale)
			stale.TrySend(protocol.SrvMessage{Message: protocol.MsgChat{
				User:    0,
				Content: l10n.TL(s.state.ServerLang, "error-logged-in-elsewhere", nil),
			}})
			stale.Close()
		}

		s.TrySend(protocol.SrvAuthenticate{Result: protocol.Ok(protocol.AuthInfo{Me: me, Room: roomState})})

		// 重连进 WaitForReady：通过 ProtocolHack 把客户端状态修回。
		// 两次延迟：第一次让客户端把构造的 SelectChart 落地，第二次切回 WaitingForReady。
		if restoreChartID != nil && room != nil {
			ph := s.hub.NewProtocolHack()
			ph.FixClientRoomState(room, user)
			s.logReconnectRoomRestored(user, room, roomStateStr, true)
		} else if room != nil {
			s.logReconnectRoomRestored(user, room, roomStateStr, false)
		} else {
			s.logReconnectNoRoom(user)
		}

		s.logAuthSuccess(user, monitor)
		go s.sendWelcome(user)
		return
	}

	// ---- 新用户路径：快速解锁，将 NewUser 分配移出锁外 ----
	s.state.Mu.Unlock()

	user = server.NewUser(info.ID, info.Name, info.Language, s.state)
	user.SetSession(s)

	// 阶段 2 — 重新持锁完成注册（双检避免竞态）
	s.state.Mu.Lock()
	if existing := s.state.Users[info.ID]; existing != nil {
		// 极低概率的竞态：另一个连接在我们 unlock→relock 间注册了同 ID 用户。
		var stale server.Session
		if existing.Session() != nil && existing.Session() != server.Session(s) {
			stale = existing.Session()
		}
		s.state.Mu.Unlock()
		// 关键：丢弃的 user 仍持有 s 的引用（user.SetSession(s) 已建立反向指针），
		// 显式断开该引用避免 user 存活期间 s 不会 GC；user 之后会被 GC 回收。
		user.SetSession(nil)
		// 让 existing 接管此会话，丢弃我们刚创建的 user（未被注册，GC 回收）。
		existing.SetSession(s)
		s.user = existing
		// 踢旧会话（锁外）。
		if stale != nil {
			stale.TrySend(protocol.SrvMessage{Message: protocol.MsgChat{
				User:    0,
				Content: l10n.TL(s.state.ServerLang, "error-logged-in-elsewhere", nil),
			}})
			stale.Close()
		}
		me := existing.ToInfo()
		monitor := existing.Monitor
		s.TrySend(protocol.SrvAuthenticate{Result: protocol.Ok(protocol.AuthInfo{Me: me, Room: nil})})
		s.logAuthSuccess(existing, monitor)
		go s.sendWelcome(existing)
		return
	}
	s.state.Users[info.ID] = user
	s.user = user
	me := user.ToInfo()
	monitor := user.Monitor
	s.state.Mu.Unlock()

	s.TrySend(protocol.SrvAuthenticate{Result: protocol.Ok(protocol.AuthInfo{Me: me, Room: nil})})

	s.logAuthSuccess(user, monitor)
	go s.sendWelcome(user)
}

// logAuthStart 在认证开始时记录 DEBUG（含 token 脱敏前缀），便于关联 phira 重试链路。
func (s *Session) logAuthStart(token string) {
	lg := s.state.Logger
	if lg == nil || !lg.DebugEnabled() {
		return
	}
	prefix := token
	if len(prefix) > 8 {
		prefix = prefix[:8]
	} else if len(prefix) == 0 {
		prefix = "(empty)"
	}
	lg.Debug(fmt.Sprintf("连接ID：%s，开始 Phira 认证（token 前缀：%s…）", s.id, prefix))
}

// logAuthFetchError 在 Phira 获取用户信息失败时记录 DEBUG（含原始翻译键/错误串），
// 上层 failAuth 仍只记 WARN 本地化原因；此处补充 DEBUG 用于排查上游问题。
func (s *Session) logAuthFetchError(err error) {
	lg := s.state.Logger
	if lg == nil || !lg.DebugEnabled() || err == nil {
		return
	}
	lg.Debug(fmt.Sprintf("连接ID：%s，Phira 获取用户信息失败：%s", s.id, err.Error()))
}

// logReconnectDetected 在重连路径识别后记录 DEBUG（含房间信息）。调用方须持 state.Mu。
func (s *Session) logReconnectDetected(u *server.User, room *server.Room, roomStateStr string) {
	lg := s.state.Logger
	if lg == nil || !lg.DebugEnabled() {
		return
	}
	inRoom := room != nil
	roomID := ""
	if inRoom {
		roomID = string(room.ID)
	}
	lg.Debug(fmt.Sprintf("连接ID：%s，“%s” 走重连路径：在房间=%s，房间ID=%s，房间状态=%s",
		s.id, u.Name, strconv.FormatBool(inRoom), roomID, roomStateStr))
}

// logReconnectStaleKicked 记录 DEBUG 级重连顶号事件。
func (s *Session) logReconnectStaleKicked(u *server.User, stale server.Session) {
	lg := s.state.Logger
	if lg == nil || !lg.DebugEnabled() {
		return
	}
	lg.Debug(fmt.Sprintf("“%s” 重连顶号：踢出旧会话，旧连接ID=%s", u.Name, stale.ID()))
}

// logReconnectRoomRestored 记录 DEBUG 级重连房间状态恢复结果。
func (s *Session) logReconnectRoomRestored(u *server.User, room *server.Room, roomStateStr string, hack bool) {
	lg := s.state.Logger
	if lg == nil || !lg.DebugEnabled() {
		return
	}
	lg.Debug(fmt.Sprintf("“%s” 重连恢复房间状态：房间 “%s”，状态=%s，触发协议修正=%s",
		u.Name, string(room.ID), roomStateStr, strconv.FormatBool(hack)))
}

// logReconnectNoRoom 记录 DEBUG 级重连时已无房间。
func (s *Session) logReconnectNoRoom(u *server.User) {
	lg := s.state.Logger
	if lg == nil || !lg.DebugEnabled() {
		return
	}
	lg.Debug(fmt.Sprintf("“%s” 重连时已无房间（可能已超时移除或房间解散）", u.Name))
}

// logAuthSuccess 记录认证成功：DEBUG 级 log-auth-ok 与 INFO 级 log-player-join（对齐原版）。
func (s *Session) logAuthSuccess(user *server.User, monitor bool) {
	lg := s.state.Logger
	if lg == nil {
		return
	}
	suffix := ""
	if monitor {
		suffix = l10n.TL(s.state.ServerLang, "label-monitor-suffix", nil)
	}
	lg.Debug(fmt.Sprintf("连接ID：%s，“ %s ” %s 认证成功，协议版本：“%s”",
		s.id, user.Name, suffix, strconv.Itoa(protocolVersion)))
	lg.Info(l10n.TL(s.state.ServerLang, "log-player-join", map[string]string{
		"user": user.Name, "id": strconv.Itoa(user.ID), "monitorSuffix": suffix,
	}))
}

// sendWelcome 拉取一言（可选）并把欢迎系统聊天发给用户。
func (s *Session) sendWelcome(user *server.User) {
	var hk *server.Hitokoto
	if url := s.state.Config.EffectiveHitokotoAPIURL(); url != "" {
		hk = fetchHitokoto(url)
	}
	s.state.Mu.Lock()
	text := s.state.BuildWelcomeText(user, hk)
	sysID := s.state.SystemChatUserID()
	s.state.Mu.Unlock()
	user.TrySend(protocol.SrvMessage{Message: protocol.MsgChat{User: sysID, Content: text}})
}

// fetchHitokoto 拉取一言；失败返回 nil（欢迎消息照常发，只是不带一言）。
// HTTP 客户端经 netutil.NewClient() 构造（Android 注入公共 DNS 解析以绕开
// [::1]:53 connection refused；其它平台保留系统 DNS 行为）。
func fetchHitokoto(url string) *server.Hitokoto {
	client := netutil.NewClient()
	client.Timeout = 5 * time.Second
	resp, err := client.Get(url)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil
	}
	var data struct {
		Hitokoto string `json:"hitokoto"`
		From     string `json:"from"`
		FromWho  string `json:"from_who"`
	}
	if json.NewDecoder(resp.Body).Decode(&data) != nil {
		return nil
	}
	quote := strings.TrimSpace(data.Hitokoto)
	if quote == "" {
		return nil
	}
	// 部分一言源把换行写成字面量 "\n"（反斜杠+n 两字符），JSON 解码不会还原；这里转成真实换行。
	quote = strings.ReplaceAll(quote, "\\r\\n", "\n")
	quote = strings.ReplaceAll(quote, "\\n", "\n")
	// 出处优先用 from_who（一言官方 API 的具体出处常在此字段），为空再回退 from（对齐原版）。
	from := strings.TrimSpace(data.FromWho)
	if from == "" {
		from = strings.TrimSpace(data.From)
	}
	return &server.Hitokoto{Quote: quote, From: from}
}

func (s *Session) failAuth(reasonKey string) {
	// reasonKey 可能是翻译键（auth-invalid-token / server-maintenance）或原始错误串（如 API 超时）；
	// TL 对非键原样返回，故可统一本地化。对齐原版：发本地化原因给客户端并记 WARN 日志。
	reason := l10n.TL(s.state.ServerLang, reasonKey, nil)
	if lg := s.state.Logger; lg != nil {
		lg.Warn(l10n.TL(s.state.ServerLang, "log-auth-failed", map[string]string{"id": s.id, "reason": reason}))
	}
	s.TrySend(protocol.SrvAuthenticate{Result: protocol.Errr[protocol.AuthInfo](reason)})
	s.Close()
}
