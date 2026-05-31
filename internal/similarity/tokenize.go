package similarity

import (
	"encoding/json"
	"strings"
	"unicode"
)

var stopWords = map[string]bool{
	"the": true, "a": true, "an": true, "is": true, "are": true,
	"was": true, "were": true, "be": true, "been": true, "being": true,
	"have": true, "has": true, "had": true, "do": true, "does": true,
	"did": true, "will": true, "would": true, "shall": true, "should": true,
	"may": true, "might": true, "can": true, "could": true, "of": true,
	"in": true, "to": true, "for": true, "with": true, "on": true,
	"at": true, "by": true, "from": true, "as": true, "or": true,
	"and": true, "but": true, "not": true, "this": true, "that": true,
	"it": true, "its": true,
}

// tokenize lowercases text, splits on non-letter/digit boundaries, removes
// stop words and short tokens, and produces CJK bigrams where appropriate.
func tokenize(text string) []string {
	text = strings.ToLower(text)
	parts := strings.FieldsFunc(text, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})

	var tokens []string
	for _, p := range parts {
		if hasCJK(p) {
			runes := []rune(p)
			for k := 0; k+1 < len(runes); k++ {
				tokens = append(tokens, string(runes[k:k+2]))
			}
		} else {
			if len(p) < 2 || stopWords[p] {
				continue
			}
			tokens = append(tokens, p)
		}
	}
	return tokens
}

func hasCJK(s string) bool {
	for _, r := range s {
		if isCJK(r) {
			return true
		}
	}
	return false
}

func isCJK(r rune) bool {
	return unicode.In(r, unicode.Han, unicode.Hangul, unicode.Katakana, unicode.Hiragana)
}

func extractTagSet(metadataJSON string) map[string]bool {
	set := make(map[string]bool)
	if metadataJSON == "" {
		return set
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(metadataJSON), &m); err != nil {
		return set
	}
	arr, _ := m["tags"].([]any)
	for _, v := range arr {
		if s, ok := v.(string); ok {
			set[strings.ToLower(s)] = true
		}
	}
	return set
}
