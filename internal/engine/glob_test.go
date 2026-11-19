package engine

import "testing"

func TestGlob(t *testing.T) {
	cases := []struct {
		pattern, s string
		want       bool
	}{
		{"*", "", true},
		{"*", "anything", true},
		{"user:*", "user:42", true},
		{"user:*", "users", false},
		{"h?llo", "hello", true},
		{"h?llo", "hllo", false},
		{"h[ae]llo", "hallo", true},
		{"h[ae]llo", "hillo", false},
		{"h[^e]llo", "hallo", true},
		{"h[^e]llo", "hello", false},
		{"h[a-c]llo", "hbllo", true},
		{"h[a-c]llo", "hdllo", false},
		{"h[c-a]llo", "hbllo", true}, // обратный диапазон нормализуется
		{`a\*b`, "a*b", true},
		{`a\*b`, "axb", false},
		{"*a*b*", "xxaxxbxx", true},
		{"*a*b*", "xxbxxaxx", false},
		{"a**b", "aXb", true},
		{"", "", true},
		{"", "a", false},
		{"abc", "abcd", false},
		{"[ab", "a", true}, // незакрытый класс
	}
	for _, c := range cases {
		if got := globMatch(c.pattern, c.s); got != c.want {
			t.Errorf("glob(%q, %q) = %v, ожидалось %v", c.pattern, c.s, got, c.want)
		}
	}
}
