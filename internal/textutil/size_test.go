package textutil

import "testing"

func TestSize(t *testing.T) {
	for _, c := range []struct {
		n      int64
		ru, en string
	}{
		{0, "0 Б", "0 B"},
		{-1, "0 Б", "0 B"}, // a count below zero (an unlimited traffic left) is nothing, not "-1 Б"
		{1023, "1023 Б", "1023 B"},
		{1024, "1 КБ", "1 KB"},
		{1536, "1,5 КБ", "1.5 KB"},
		{100 * 1024, "100 КБ", "100 KB"},
		{5 << 20, "5 МБ", "5 MB"},
		{12<<30 + 512<<20, "12,5 ГБ", "12.5 GB"},
		{100 << 30, "100 ГБ", "100 GB"},
		{1000 << 30, "1000 ГБ", "1000 GB"},
		{1 << 40, "1 ТБ", "1 TB"},
		{5000 << 40, "5000 ТБ", "5000 TB"}, // the largest unit has no larger one to go to
	} {
		if got := Size(c.n, "ru"); got != c.ru {
			t.Errorf("Size(%d, ru) = %q, want %q", c.n, got, c.ru)
		}
		if got := Size(c.n, "en"); got != c.en {
			t.Errorf("Size(%d, en) = %q, want %q", c.n, got, c.en)
		}
		if got := Size(c.n, ""); got != c.ru {
			t.Errorf("Size(%d, \"\") = %q: a language that is not English is Russian", c.n, got)
		}
	}
}
