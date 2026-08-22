package submission

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"

	"apm/internal/utils/jsonstrict"
)

// CanonicalizeJSON 按 RFC 8785 生成确定性的 UTF-8 字节序列：
// 对象键按 UTF-16 码元升序，字符串做最小转义，数字按 ECMAScript 最短表示。
// 解码前用 jsonstrict 拒绝重复键与尾随值。
func CanonicalizeJSON(raw json.RawMessage) ([]byte, error) {
	var probe any
	if err := jsonstrict.Decode(raw, &probe, false); err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	writeJCS(&buf, value)
	return buf.Bytes(), nil
}

func writeJCS(buf *bytes.Buffer, value any) {
	switch v := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Slice(keys, func(i, j int) bool { return lessUTF16(keys[i], keys[j]) })
		buf.WriteByte('{')
		for i, key := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			writeJCSString(buf, key)
			buf.WriteByte(':')
			writeJCS(buf, v[key])
		}
		buf.WriteByte('}')
	case []any:
		buf.WriteByte('[')
		for i, item := range v {
			if i > 0 {
				buf.WriteByte(',')
			}
			writeJCS(buf, item)
		}
		buf.WriteByte(']')
	case json.Number:
		buf.WriteString(canonicalNumber(string(v)))
	case string:
		writeJCSString(buf, v)
	case bool:
		if v {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
	case nil:
		buf.WriteString("null")
	}
}

// lessUTF16 按 UTF-16 码元升序比较字符串，符合 RFC 8785 的属性排序要求。
func lessUTF16(a, b string) bool {
	ar := utf16.Encode([]rune(a))
	br := utf16.Encode([]rune(b))
	for i := 0; i < len(ar) && i < len(br); i++ {
		if ar[i] != br[i] {
			return ar[i] < br[i]
		}
	}
	return len(ar) < len(br)
}

// writeJCSString 按 RFC 8785 最小转义规则写字符串：仅转义引号、反斜线
// 与 U+0000–U+001F 控制字符；\b \t \n \f \r 用单字符短转义，其余用 \uXXXX。
func writeJCSString(buf *bytes.Buffer, s string) {
	buf.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			buf.WriteString("\\\"")
		case '\\':
			buf.WriteString("\\\\")
		case '\b':
			buf.WriteString("\\b")
		case '\t':
			buf.WriteString("\\t")
		case '\n':
			buf.WriteString("\\n")
		case '\f':
			buf.WriteString("\\f")
		case '\r':
			buf.WriteString("\\r")
		default:
			if r < 0x20 {
				fmt.Fprintf(buf, "\\u%04x", r)
			} else {
				buf.WriteRune(r)
			}
		}
	}
	buf.WriteByte('"')
}

// canonicalNumber 将数值规范化为 ECMAScript JSON.stringify 的表示：
// 纯整数透传以保留精确值；带小数点或指数的数值在 1e-6 <= |f| < 1e21 时用
// 十进制，否则用科学计数法并去掉指数前导零。
func canonicalNumber(number string) string {
	if !strings.ContainsAny(number, ".eE") {
		if number == "-0" {
			return "0"
		}
		return number
	}
	f, err := strconv.ParseFloat(number, 64)
	if err != nil {
		return number
	}
	if f == 0 {
		return "0"
	}
	abs := math.Abs(f)
	if abs >= 1e-6 && abs < 1e21 {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	return normalizeExponent(strconv.FormatFloat(f, 'e', -1, 64))
}

// normalizeExponent 去掉科学计数法指数部分的前导零，如 "e+08" -> "e+8"。
func normalizeExponent(s string) string {
	i := strings.LastIndexAny(s, "eE")
	if i < 0 {
		return s
	}
	mantissa := s[:i+1]
	exp := s[i+1:]
	sign := ""
	if len(exp) > 0 && (exp[0] == '+' || exp[0] == '-') {
		sign = string(exp[0])
		exp = exp[1:]
	}
	exp = strings.TrimLeft(exp, "0")
	if exp == "" {
		exp = "0"
	}
	return mantissa + sign + exp
}
