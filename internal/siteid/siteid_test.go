package siteid

import "testing"

func TestNew(t *testing.T) {
	seen := map[string]bool{}
	for range 1000 {
		id := New()
		if !Valid(id) {
			t.Fatalf("New() = %q, not valid", id)
		}
		if seen[id] {
			t.Fatalf("duplicate id %q", id)
		}
		seen[id] = true
	}
}

func TestValid(t *testing.T) {
	for s, want := range map[string]bool{
		"abcdefghijkl":  true,
		"a2b3c4d5e6f7":  true,
		"abcdefghijk":   false, // 11
		"abcdefghijklm": false, // 13
		"Abcdefghijkl":  false, // uppercase
		"abcdefghijk1":  false, // 1 is not base32
		"abcdefghijk8":  false,
		"abcdefghij-k":  false,
		"":              false,
	} {
		if got := Valid(s); got != want {
			t.Errorf("Valid(%q) = %v, want %v", s, got, want)
		}
	}
}
