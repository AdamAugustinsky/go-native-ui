package ui

import "strings"

const (
	fnvOffset = 14695981039346656037
	fnvPrime  = 1099511628211
)

func mixKey(parent Key, h uint64) Key {
	h ^= h >> 29
	h *= 0xBF58476D1CE4E5B9
	h ^= h >> 32
	if h == 0 {
		h = 1
	}
	return Key(h)
}

// HashString returns the key of identity s below parent. Together with
// KeyN it lets the app derive the keys that BoxKeyed takes.
func HashString(parent Key, s string) Key {
	h := uint64(parent)*0x9E3779B97F4A7C15 + fnvOffset
	for i := 0; i < len(s); i++ {
		h = (h ^ uint64(s[i])) * fnvPrime
	}
	return mixKey(parent, h)
}

// KeyN returns the key of the integer identity n below parent (for rows of a
// list and the like).
func KeyN(parent Key, n uint64) Key {
	h := (uint64(parent)+fnvOffset)*0x9E3779B97F4A7C15 ^ (n+1)*0xD6E8FEB86659FD93
	return mixKey(parent, h)
}

// SplitLabel applies the RAD label rules. The displayed text is everything
// before the first "##"; the identity is the whole label, except that "###"
// makes only the part after it the identity ("Name###id" shows "Name" and is
// identified by "id", so the text may change without losing state).
func SplitLabel(label string) (display, identity string) {
	i := strings.Index(label, "##")
	if i < 0 {
		return label, label
	}
	if strings.HasPrefix(label[i:], "###") {
		return label[:i], label[i+3:]
	}
	return label[:i], label
}
