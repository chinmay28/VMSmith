package storage

import (
	"testing"
)

func TestFindLastSlash(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"user@host/image-name", 9},
		{"a/b/c", 3},
		{"/leading", 0},
		{"noslash", -1},
		{"", -1},
		{"trailing/", 8},
	}

	for _, tt := range tests {
		got := findLastSlash(tt.input)
		if got != tt.want {
			t.Errorf("findLastSlash(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}
