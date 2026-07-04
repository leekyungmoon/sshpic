package shellquote

import "testing"

func TestQuote(t *testing.T) {
	tests := map[string]string{
		"":                 "''",
		"simple":           "'simple'",
		"two words":        "'two words'",
		"has'quote":        "'has'\\''quote'",
		"$HOME `rm -rf /`": "'$HOME `rm -rf /`'",
		"한글 path":          "'한글 path'",
	}
	for input, want := range tests {
		if got := Quote(input); got != want {
			t.Fatalf("Quote(%q)=%q, want %q", input, got, want)
		}
	}
}
