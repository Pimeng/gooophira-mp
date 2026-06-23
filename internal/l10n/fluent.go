package l10n

import (
	"regexp"
	"strings"
)

// 本文件实现 Fluent(FTL) 的一个子集解析器，覆盖原版 locales/*.ftl 实际用到的特性：
//   - 简单消息            key = text
//   - 变量插值            { $var }
//   - 字符串字面量        { "" } / { "text" }
//   - 选择表达式（布尔）  { $x -> [true] a *[false] b }
//   - 多行值（缩进续行）
//
// 不支持：terms(-x)、消息引用、函数(NUMBER/DATETIME)、属性(.attr)、复数 CLDR 类别。
// 这些原版 FTL 未使用；如将来需要再扩展。

// element 是一个模式元素，按 args 渲染为字符串。
type element interface {
	resolve(args map[string]string) string
}

type textElem struct{ s string }

func (e textElem) resolve(map[string]string) string { return e.s }

type varElem struct{ name string }

func (e varElem) resolve(args map[string]string) string { return args[e.name] }

type selectElem struct {
	selector string
	variants map[string][]element
	def      []element
}

func (e selectElem) resolve(args map[string]string) string {
	if pat, ok := e.variants[args[e.selector]]; ok {
		return resolvePattern(pat, args)
	}
	return resolvePattern(e.def, args)
}

func resolvePattern(pat []element, args map[string]string) string {
	var b strings.Builder
	for _, el := range pat {
		b.WriteString(el.resolve(args))
	}
	return b.String()
}

// resource 是一种语言解析后的消息表：key -> 模式。
type resource map[string][]element

var msgHeaderRe = regexp.MustCompile(`^([A-Za-z][\w-]*) *= *(.*)$`)

// parseResource 解析一整份 FTL 文本为消息表。
func parseResource(text string) resource {
	res := make(resource)
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	for i := 0; i < len(lines); {
		line := lines[i]
		if line == "" || strings.HasPrefix(line, "#") || startsWithSpace(line) {
			i++
			continue
		}
		m := msgHeaderRe.FindStringSubmatch(line)
		if m == nil {
			i++
			continue
		}
		id, inline := m[1], m[2]
		valueLines := []string{inline}
		depth := braceDelta(inline)
		i++
		for i < len(lines) {
			next := lines[i]
			if depth <= 0 {
				if next == "" {
					if !hasIndentedAfterBlanks(lines, i+1) {
						break
					}
				} else if !startsWithSpace(next) {
					break
				}
			}
			valueLines = append(valueLines, next)
			depth += braceDelta(next)
			i++
		}
		res[id] = parsePattern(dedentAndJoin(valueLines))
	}
	return res
}

func startsWithSpace(s string) bool {
	return len(s) > 0 && (s[0] == ' ' || s[0] == '\t')
}

func hasIndentedAfterBlanks(lines []string, from int) bool {
	for j := from; j < len(lines); j++ {
		if lines[j] == "" {
			continue
		}
		return startsWithSpace(lines[j])
	}
	return false
}

// braceDelta 返回一行内 '{' 与 '}' 的数量差（用于跨行追踪未闭合的占位符/选择表达式）。
func braceDelta(s string) int {
	d := 0
	for _, c := range s {
		switch c {
		case '{':
			d++
		case '}':
			d--
		}
	}
	return d
}

// dedentAndJoin 把首行(inline)与续行(去公共缩进后)拼成完整值文本。
func dedentAndJoin(valueLines []string) string {
	if len(valueLines) == 1 {
		return valueLines[0]
	}
	inline := valueLines[0]
	cont := valueLines[1:]

	minIndent := -1
	for _, l := range cont {
		if strings.TrimSpace(l) == "" {
			continue
		}
		ind := len(l) - len(strings.TrimLeft(l, " \t"))
		if minIndent < 0 || ind < minIndent {
			minIndent = ind
		}
	}
	if minIndent < 0 {
		minIndent = 0
	}
	stripped := make([]string, len(cont))
	for i, l := range cont {
		if len(l) >= minIndent {
			stripped[i] = l[minIndent:]
		} else {
			stripped[i] = strings.TrimLeft(l, " \t")
		}
	}
	block := strings.Join(stripped, "\n")
	if strings.TrimSpace(inline) == "" {
		return block
	}
	return inline + "\n" + block
}

// parsePattern 把值文本解析为元素序列。
func parsePattern(s string) []element {
	var out []element
	for {
		i := strings.IndexByte(s, '{')
		if i < 0 {
			if s != "" {
				out = append(out, textElem{s})
			}
			return out
		}
		if i > 0 {
			out = append(out, textElem{s[:i]})
		}
		closeIdx := matchBrace(s, i)
		if closeIdx < 0 {
			out = append(out, textElem{s[i:]})
			return out
		}
		out = append(out, parsePlaceable(s[i+1:closeIdx]))
		s = s[closeIdx+1:]
	}
}

// matchBrace 从 start('{') 起找到配对的 '}'，返回其下标；找不到返回 -1。
func matchBrace(s string, start int) int {
	depth := 0
	for j := start; j < len(s); j++ {
		switch s[j] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return j
			}
		}
	}
	return -1
}

var variantRe = regexp.MustCompile(`(\*)?\[([^\]]*)\]`)

// parsePlaceable 解析 { } 内的内容为一个元素。
func parsePlaceable(inner string) element {
	if strings.Contains(inner, "->") {
		return parseSelect(inner)
	}
	t := strings.TrimSpace(inner)
	switch {
	case strings.HasPrefix(t, `"`):
		return textElem{unquote(t)}
	case strings.HasPrefix(t, "$"):
		return varElem{strings.TrimSpace(t[1:])}
	default:
		return textElem{t}
	}
}

func unquote(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}

func parseSelect(inner string) selectElem {
	arrow := strings.Index(inner, "->")
	selector := strings.TrimSpace(inner[:arrow])
	selector = strings.TrimSpace(strings.TrimPrefix(selector, "$"))
	body := inner[arrow+2:]

	sel := selectElem{selector: selector, variants: make(map[string][]element)}
	ms := variantRe.FindAllStringSubmatchIndex(body, -1)
	for idx, m := range ms {
		isDefault := m[2] >= 0 // 分组1 (*) 命中
		key := body[m[4]:m[5]]
		valStart := m[1]
		valEnd := len(body)
		if idx+1 < len(ms) {
			valEnd = ms[idx+1][0]
		}
		pat := parsePattern(strings.TrimSpace(body[valStart:valEnd]))
		sel.variants[key] = pat
		if isDefault {
			sel.def = pat
		}
	}
	return sel
}
