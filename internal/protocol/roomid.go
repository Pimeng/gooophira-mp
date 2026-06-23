package protocol

import "errors"

// RoomID 是经过校验的房间标识符：非空、≤20 字符，仅含 [A-Za-z0-9_-]。
// 用具名字符串类型替代 TS 的 branded string，靠 ParseRoomID 守卫不变量。
type RoomID string

// 房间 ID 校验相关错误。
var (
	ErrRoomIDEmpty   = errors.New("roomid-empty")
	ErrRoomIDTooLong = errors.New("roomid-too-long")
	ErrRoomIDInvalid = errors.New("roomid-invalid")
)

// ParseRoomID 校验并构造一个 RoomID。
func ParseRoomID(value string) (RoomID, error) {
	if len(value) == 0 {
		return "", ErrRoomIDEmpty
	}
	if len(value) > 20 {
		return "", ErrRoomIDTooLong
	}
	for i := 0; i < len(value); i++ {
		ch := value[i]
		ok := ch == '-' || ch == '_' ||
			(ch >= '0' && ch <= '9') ||
			(ch >= 'A' && ch <= 'Z') ||
			(ch >= 'a' && ch <= 'z')
		if !ok {
			return "", ErrRoomIDInvalid
		}
	}
	return RoomID(value), nil
}

// String 返回房间 ID 的字符串形式。
func (id RoomID) String() string { return string(id) }
