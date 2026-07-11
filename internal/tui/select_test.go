package tui

import (
	"reflect"
	"testing"
)

func TestParseKey(t *testing.T) {
	tests := []struct {
		name     string
		in       []byte
		wantKey  key
		wantSize int
	}{
		{"empty", nil, keyOther, 0},
		{"enter cr", []byte{'\r'}, keyEnter, 1},
		{"enter lf", []byte{'\n'}, keyEnter, 1},
		{"ctrl-c", []byte{0x03}, keyCancel, 1},
		{"q", []byte{'q'}, keyCancel, 1},
		{"Q", []byte{'Q'}, keyCancel, 1},
		{"lone esc", []byte{0x1b}, keyCancel, 1},
		{"arrow up csi", []byte{0x1b, '[', 'A'}, keyUp, 3},
		{"arrow down csi", []byte{0x1b, '[', 'B'}, keyDown, 3},
		{"arrow up ss3", []byte{0x1b, 'O', 'A'}, keyUp, 3},
		{"arrow down ss3", []byte{0x1b, 'O', 'B'}, keyDown, 3},
		{"arrow left ignored", []byte{0x1b, '[', 'D'}, keyOther, 3},
		{"vi up", []byte{'k'}, keyUp, 1},
		{"vi down", []byte{'j'}, keyDown, 1},
		{"plain char", []byte{'x'}, keyOther, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			k, n := parseKey(tt.in)
			if k != tt.wantKey || n != tt.wantSize {
				t.Fatalf("parseKey(%v) = (%d, %d), want (%d, %d)", tt.in, k, n, tt.wantKey, tt.wantSize)
			}
		})
	}
}

func TestNextIndex(t *testing.T) {
	tests := []struct {
		name   string
		cur, n int
		k      key
		want   int
	}{
		{"down middle", 0, 3, keyDown, 1},
		{"down wraps at end", 2, 3, keyDown, 0},
		{"up middle", 2, 3, keyUp, 1},
		{"up wraps at start", 0, 3, keyUp, 2},
		{"enter no move", 1, 3, keyEnter, 1},
		{"other no move", 1, 3, keyOther, 1},
		{"empty list", 0, 0, keyDown, 0},
		{"single item down", 0, 1, keyDown, 0},
		{"single item up", 0, 1, keyUp, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := nextIndex(tt.cur, tt.n, tt.k); got != tt.want {
				t.Fatalf("nextIndex(%d, %d, %d) = %d, want %d", tt.cur, tt.n, tt.k, got, tt.want)
			}
		})
	}
}

func TestMergeDefault(t *testing.T) {
	tests := []struct {
		name   string
		models []string
		def    string
		want   []string
	}{
		{
			name:   "def present moves to front",
			models: []string{"a", "b", "c"},
			def:    "b",
			want:   []string{"b", "a", "c"},
		},
		{
			name:   "def missing is prepended",
			models: []string{"a", "b"},
			def:    "z",
			want:   []string{"z", "a", "b"},
		},
		{
			name:   "no def preserves order",
			models: []string{"a", "b"},
			def:    "",
			want:   []string{"a", "b"},
		},
		{
			name:   "dedupes and trims blanks",
			models: []string{"a", " a ", "", "b"},
			def:    " a ",
			want:   []string{"a", "b"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mergeDefault(tt.models, tt.def); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("mergeDefault(%v, %q) = %v, want %v", tt.models, tt.def, got, tt.want)
			}
		})
	}
}
