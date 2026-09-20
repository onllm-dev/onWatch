package web

import (
	"strings"
	"unicode"
)

// titleCaseWords upper-cases the first rune of each word and lower-cases the
// rest, for rendering plan and tier labels ("pro" -> "Pro").
//
// Indexing bytes here would split a multi-byte first rune and render it as a
// replacement character, so the first rune is taken by decoding.
func titleCaseWords(s string) string {
	words := strings.Fields(strings.TrimSpace(s))
	for i, w := range words {
		runes := []rune(w)
		head := string(unicode.ToUpper(runes[0]))
		words[i] = head + strings.ToLower(string(runes[1:]))
	}
	return strings.Join(words, " ")
}
