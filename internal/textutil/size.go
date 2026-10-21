package textutil

import (
	"fmt"
	"strings"
)

// Size writes a count of bytes the way the bot does: "12,5 ГБ" in Russian, with a comma,
// "12.5 GB" in English (Lang: "en"; any other is Russian). One decimal below 100 of a unit,
// none above it; below zero is 0.
func Size(n int64, lang string) string {
	units, sep := []string{"Б", "КБ", "МБ", "ГБ", "ТБ"}, ","
	if Lang(lang).En() {
		units, sep = []string{"B", "KB", "MB", "GB", "TB"}, "."
	}
	v, i := float64(max(n, 0)), 0
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	s := fmt.Sprintf("%.0f", v)
	if i > 0 && v < 100 {
		s = strings.Replace(strings.TrimSuffix(fmt.Sprintf("%.1f", v), ".0"), ".", sep, 1)
	}
	return s + " " + units[i]
}
