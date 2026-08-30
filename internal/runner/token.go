// Лексика HTTP по RFC 7230: token и field-value. Отдельно от config.go —
// меняется вместе с RFC, а не вместе с набором флагов.
package runner

import "strings"

// tchar — символы token сверх букв и цифр.
const tchar = "!#$%&'*+-.^_`|~"

// isToken — непустая строка из tchar, букв и цифр: метод, имя заголовка.
func isToken(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z', '0' <= c && c <= '9':
		case strings.IndexByte(tchar, c) >= 0:
		default:
			return false
		}
	}
	return true
}

// isFieldValue — VCHAR, SP, HTAB и obs-text; управляющие и DEL — нет.
func isFieldValue(s string) bool {
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < 0x20 && c != '\t' || c == 0x7f {
			return false
		}
	}
	return true
}
