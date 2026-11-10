package resource

import (
	"strings"
	"unicode"
)

func Normalize(digest string) string {
	d := strings.ToLower(strings.TrimSpace(digest))
	d = strings.TrimPrefix(d, "sha256:")
	if len(d) != 64 {
		return ""
	}
	for _, r := range d {
		if !unicode.Is(unicode.ASCII_Hex_Digit, r) {
			return ""
		}
	}
	return "sha256:" + d
}

func Hex(digest string) string {
	return strings.TrimPrefix(Normalize(digest), "sha256:")
}
