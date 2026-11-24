package library

import "testing"

func TestExactTitle(t *testing.T) {
	for _, c := range []struct {
		title, key string
		want       bool
	}{
		{"长夜灯火", "长夜灯火", true},
		{"长夜 灯火！", "长夜灯火", true},
		{"Re:Zero", "re zero", true},
		{"长夜灯火2", "长夜灯火", false},
		{"？？？", "？？？", true},
		{"？？？", "！！！", false},
	} {
		if got := exactTitle(c.title, c.key); got != c.want {
			t.Errorf("exactTitle(%q, %q) = %v, want %v", c.title, c.key, got, c.want)
		}
	}
}
