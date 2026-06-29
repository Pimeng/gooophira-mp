package server

import (
	"sort"
	"strconv"
	"strings"

	"github.com/Pimeng/gooophira-mp/internal/l10n"
)

// Hitokoto 是一言（每日一句）数据。
type Hitokoto struct {
	Quote string
	From  string
}

// BuildWelcomeText 组装认证成功后发给用户的欢迎系统聊天文本（用户语言）。
// 内容：清屏空行 + 欢迎语 + 版本 + 房间列表 + 提示(可选) + 一言(可选)。调用方须持 Mu。
func (s *ServerState) BuildWelcomeText(user *User, hitokoto *Hitokoto) string {
	lang := user.Lang
	tl := func(key string, args map[string]string) string { return l10n.TL(lang, key, args) }
	sep := strings.Repeat("=", 73) + "\n"

	var b strings.Builder
	b.WriteString(strings.Repeat("\n", 40))
	b.WriteString(tl("chat-welcome", map[string]string{"userName": user.Name, "serverName": s.ServerName}) + "\n")
	b.WriteString(sep)
	b.WriteString(tl("chat-welcome-version", map[string]string{"version": s.Version}) + "\n")
	b.WriteString(tl("chat-welcome-stats", map[string]string{
		"online": strconv.Itoa(len(s.Users)),
		"rooms":  strconv.Itoa(len(s.Rooms)),
	}) + "\n")
	b.WriteString(sep)
	b.WriteString(tl("chat-roomlist-title", nil) + "\n")
	b.WriteString(s.availableRoomsText(lang) + "\n")
	b.WriteString(sep)
	if tip := s.Config.EffectiveRoomListTip(); tip != "" {
		b.WriteString(tip + "\n")
	}
	if hitokoto != nil {
		from := hitokoto.From
		if from == "" {
			from = tl("chat-hitokoto-from-unknown", nil)
		}
		b.WriteString(tl("chat-hitokoto", map[string]string{"quote": hitokoto.Quote, "from": from}))
	}
	return b.String()
}

// availableRoomsText 返回可加入房间列表的本地化文本。
// 过滤：排除 `_` 前缀、已锁定、非 SelectChart/Playing、已满员的房间；按 id 升序。
func (s *ServerState) availableRoomsText(lang *l10n.Language) string {
	type entry struct {
		id    string
		count int
		maxU  int
	}
	var rooms []entry
	for id, room := range s.Rooms {
		sid := string(id)
		if strings.HasPrefix(sid, "_") || room.Locked {
			continue
		}
		switch room.State.(type) {
		case StateSelectChart, StatePlaying:
		default:
			continue // WaitForReady 不列出
		}
		count := room.UserCount()
		if count >= room.MaxUsers {
			continue
		}
		rooms = append(rooms, entry{sid, count, room.MaxUsers})
	}
	if len(rooms) == 0 {
		return l10n.TL(lang, "chat-roomlist-empty", nil)
	}
	sort.Slice(rooms, func(i, j int) bool { return rooms[i].id < rooms[j].id })

	joiner := "; "
	if lang != nil && lang.Tag == "zh-CN" {
		joiner = "；"
	}
	items := make([]string, len(rooms))
	for i, r := range rooms {
		items[i] = l10n.TL(lang, "chat-roomlist-item", map[string]string{
			"id": r.id, "count": strconv.Itoa(r.count), "max": strconv.Itoa(r.maxU),
		})
	}
	return strings.Join(items, joiner)
}
