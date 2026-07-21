// Package l10n 提供多语言本地化（Fluent/FTL）。
//
// 内置 zh-CN、en-US、zh-TW、ja-JP、ko-KR、ru-RU 六种语言。FTL 源以 go:embed 打进二进制；解析见 fluent.go。
package l10n

import (
	_ "embed"
	"strings"
	"sync"
)

//go:embed locales/zh-CN.ftl
var zhCNFtl string

//go:embed locales/en-US.ftl
var enUSFtl string

//go:embed locales/zh-TW.ftl
var zhTWFtl string

//go:embed locales/ja-JP.ftl
var jaJPFtl string

//go:embed locales/ko-KR.ftl
var koKRFtl string

//go:embed locales/ru-RU.ftl
var ruRUFtl string

// DefaultLang 是协商失败时的兜底语言。
const DefaultLang = "zh-CN"

// 已支持语言（与 EMBEDDED 顺序一致）。
var supportedLangs = []string{"en-US", "zh-CN", "zh-TW", "ja-JP", "ko-KR", "ru-RU"}

var (
	bundlesOnce sync.Once
	bundles     map[string]resource
)

func ensureBundles() {
	bundlesOnce.Do(func() {
		bundles = map[string]resource{
			"zh-CN": parseResource(zhCNFtl),
			"en-US": parseResource(enUSFtl),
			"zh-TW": parseResource(zhTWFtl),
			"ja-JP": parseResource(jaJPFtl),
			"ko-KR": parseResource(koKRFtl),
			"ru-RU": parseResource(ruRUFtl),
		}
	})
}

// Language 表示协商后的目标语言。
type Language struct {
	// Tag 是已支持语言标签（如 "zh-CN"、"en-US"）。
	Tag string
}

// NewLanguage 由语言提示（可为 POSIX 形式如 "en_US.UTF-8" 或 BCP47）协商出支持语言。
func NewLanguage(hint string) *Language {
	return &Language{Tag: negotiate(hint)}
}

// normalizeHint 把 POSIX 风格提示规范为 BCP47（"en_US.UTF-8" → "en-US"）。空串原样返回。
func normalizeHint(hint string) string {
	t := strings.TrimSpace(hint)
	if t == "" {
		return ""
	}
	// 去掉编码后缀（.UTF-8 / @euro 等）
	if i := strings.IndexAny(t, ".@"); i >= 0 {
		t = t[:i]
	}
	return strings.ReplaceAll(t, "_", "-")
}

// negotiate 把提示协商为某个 supportedLangs；失败回退 DefaultLang。
func negotiate(hint string) string {
	norm := strings.ToLower(normalizeHint(hint))
	if norm == "" {
		return DefaultLang
	}
	// 精确匹配优先
	for _, lang := range supportedLangs {
		if strings.ToLower(lang) == norm {
			return lang
		}
	}
	// 主码 + 地区前缀匹配。
	primary, region, _ := strings.Cut(norm, "-")
	switch primary {
	case "en":
		return "en-US"
	case "zh":
		// 繁体地区（TW/HK/MO/Hant）→ zh-TW，其余 → zh-CN。
		switch region {
		case "tw", "hk", "mo", "hant":
			return "zh-TW"
		default:
			return "zh-CN"
		}
	case "ja":
		return "ja-JP"
	case "ko":
		return "ko-KR"
	case "ru":
		return "ru-RU"
	default:
		return DefaultLang
	}
}

// TL 按 key 翻译为 lang 对应文本，args 为命名插值参数（可为 nil）。
// 缺失翻译时回退到默认语言；仍缺失则返回 key 本身（避免崩溃，便于发现遗漏）。
func TL(lang *Language, key string, args map[string]string) string {
	ensureBundles()
	tag := DefaultLang
	if lang != nil && lang.Tag != "" {
		tag = lang.Tag
	}
	if pat, ok := bundles[tag][key]; ok {
		return resolvePattern(pat, args)
	}
	if pat, ok := bundles[DefaultLang][key]; ok {
		return resolvePattern(pat, args)
	}
	return key
}
