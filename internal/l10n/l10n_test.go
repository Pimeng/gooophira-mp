package l10n

import (
	"strings"
	"testing"
)

func en() *Language { return NewLanguage("en-US") }
func zh() *Language { return NewLanguage("zh-CN") }

func TestTL_SimpleMessage(t *testing.T) {
	if got := TL(en(), "create-id-occupied", nil); got != "Room ID is occupied" {
		t.Errorf("en create-id-occupied = %q", got)
	}
	if got := TL(zh(), "create-id-occupied", nil); got != "房间 ID 已被占用" {
		t.Errorf("zh create-id-occupied = %q", got)
	}
}

func TestTL_Interpolation(t *testing.T) {
	args := map[string]string{"user": "alice", "room": "room1"}
	if got := TL(en(), "log-room-created", args); got != `"alice" created room "room1"` {
		t.Errorf("en log-room-created = %q", got)
	}
	if got := TL(zh(), "log-room-created", args); got != `“alice” 创建房间 “room1”` {
		t.Errorf("zh log-room-created = %q", got)
	}
}

func TestTL_SelectExpression_Bool(t *testing.T) {
	if got := TL(en(), "log-msg-lock-room", map[string]string{"lock": "true"}); got != "Room locked" {
		t.Errorf("lock=true → %q, want 'Room locked'", got)
	}
	if got := TL(en(), "log-msg-lock-room", map[string]string{"lock": "false"}); got != "Room unlocked" {
		t.Errorf("lock=false → %q, want 'Room unlocked'", got)
	}
	// 缺失/未知选择值 → 走默认分支 (*[false])
	if got := TL(en(), "log-msg-lock-room", map[string]string{}); got != "Room unlocked" {
		t.Errorf("lock missing → %q, want default 'Room unlocked'", got)
	}
}

func TestTL_SelectInsideText(t *testing.T) {
	base := map[string]string{"user": "alice", "score": "1000000", "acc": "95.00"}
	withFC := map[string]string{"user": "alice", "score": "1000000", "acc": "95.00", "fc": "true"}
	if got := TL(en(), "log-msg-played", withFC); got != "alice finished playing: 1000000 (95.00%), FC" {
		t.Errorf("fc=true → %q", got)
	}
	base["fc"] = "false"
	if got := TL(en(), "log-msg-played", base); got != "alice finished playing: 1000000 (95.00%)" {
		t.Errorf("fc=false → %q (should have no ', FC')", got)
	}
}

func TestTL_MultilineValue(t *testing.T) {
	got := TL(en(), "chat-game-summary", map[string]string{
		"scoreText": "S", "accText": "A", "stdText": "D",
	})
	want := "Match summary:\nS\nA\nD"
	if got != want {
		t.Errorf("chat-game-summary = %q, want %q", got, want)
	}
}

func TestTL_MissingKeyReturnsKey(t *testing.T) {
	if got := TL(en(), "no-such-key-xyz", nil); got != "no-such-key-xyz" {
		t.Errorf("missing key should return key itself, got %q", got)
	}
}

func TestNegotiate(t *testing.T) {
	cases := map[string]string{
		"en-US":          "en-US",
		"en_US.UTF-8":    "en-US",
		"en":             "en-US",
		"zh-CN":          "zh-CN",
		"zh_CN.UTF-8":    "zh-CN",
		"zh-TW":          "zh-TW", // 繁体
		"zh-HK":          "zh-TW",
		"zh":             "zh-CN", // 无地区 → 简体
		"ja-JP":          "ja-JP",
		"ja_JP@calendar": "ja-JP",
		"ko-KR":          "ko-KR",
		"ru-RU":          "ru-RU",
		"ru":             "ru-RU",
		"":               "zh-CN", // 默认
		"fr-FR":          "zh-CN", // 不支持 → 默认
	}
	for hint, want := range cases {
		if got := NewLanguage(hint).Tag; got != want {
			t.Errorf("negotiate(%q) = %q, want %q", hint, got, want)
		}
	}
}

// TestTL_NestedSelectExpression 是 chat-record-send-template 嵌套选择表达式的回归测试。
// $isAp 的 *[false] 分支内嵌套了 { $fc -> [true] ，全连 *[false] {""} }。
// 旧 parseSelect 用正则全局匹配 [..]，不区分花括号层级，会把内层 $fc 的 [true]/[false]
// 当作外层 $isAp 的变体并覆盖外层 [true]（"，AP"），导致 isAp=true 错误命中 $fc 的
// [true] 分支输出"全连"。
func TestTL_NestedSelectExpression(t *testing.T) {
	base := func() map[string]string {
		return map[string]string{
			"user": "皮梦测试号", "userid": "123",
			"score": "1000000", "acc": "100.00",
			"hasStd": "false", "hasMod": "false",
			"perfect": "1000", "good": "0", "bad": "0", "miss": "0",
		}
	}

	// isAp=true → 应输出"，AP"，绝不含"，全连"
	m := base()
	m["isAp"] = "true"
	m["fc"] = "true"
	got := TL(zh(), "chat-record-send-template", m)
	if !strings.Contains(got, "，AP") {
		t.Errorf("isAp=true → %q, want contains '，AP'", got)
	}
	if strings.Contains(got, "全连") {
		t.Errorf("isAp=true → %q, must NOT contain '全连'", got)
	}

	// isAp=false, fc=true → 应输出"，全连"，绝不含"，AP"
	m = base()
	m["isAp"] = "false"
	m["fc"] = "true"
	m["acc"] = "95.00"
	got = TL(zh(), "chat-record-send-template", m)
	if !strings.Contains(got, "，全连") {
		t.Errorf("isAp=false,fc=true → %q, want contains '，全连'", got)
	}
	if strings.Contains(got, "AP") {
		t.Errorf("isAp=false,fc=true → %q, must NOT contain 'AP'", got)
	}

	// isAp=false, fc=false → 既不含"，AP"也不含"，全连"
	m = base()
	m["isAp"] = "false"
	m["fc"] = "false"
	m["acc"] = "90.00"
	got = TL(zh(), "chat-record-send-template", m)
	if strings.Contains(got, "AP") {
		t.Errorf("isAp=false,fc=false → %q, must NOT contain 'AP'", got)
	}
	if strings.Contains(got, "全连") {
		t.Errorf("isAp=false,fc=false → %q, must NOT contain '全连'", got)
	}
}

// TestAllLangsParseConsistently 确保全部 6 种语言都被解析、键数充足、且键集与 en-US 一致。
func TestAllLangsParseConsistently(t *testing.T) {
	ensureBundles()
	enKeys := bundles["en-US"]
	if len(enKeys) < 100 {
		t.Fatalf("en-US expected >=100 keys, got %d", len(enKeys))
	}
	for _, tag := range supportedLangs {
		res := bundles[tag]
		if len(res) < 100 {
			t.Errorf("%s expected >=100 keys, got %d", tag, len(res))
		}
		for k := range enKeys {
			if _, ok := res[k]; !ok {
				t.Errorf("key %q in en-US missing in %s", k, tag)
			}
		}
		// 所有消息可无 panic 渲染。
		for _, pat := range res {
			_ = resolvePattern(pat, nil)
		}
	}
}
