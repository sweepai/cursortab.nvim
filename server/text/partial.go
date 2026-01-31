package text

import (
	"unicode/utf8"
)

// WordBoundaryChars defines characters that end a word for partial accept.
// Includes spaces, tabs, and common punctuation.
const WordBoundaryChars = " \t.,;:!?()[]{}\"'`<>/"

// FindNextWordBoundary returns the byte index of the next word boundary in text.
// The boundary character itself is included in the returned length.
// If no boundary is found, returns len(text).
func FindNextWordBoundary(text string) int {
	for i := 0; i < len(text); {
		r, size := utf8.DecodeRuneInString(text[i:])
		// Check if this rune is a boundary character
		for _, boundary := range WordBoundaryChars {
			if r == boundary {
				// Include this boundary character
				return i + size
			}
		}
		i += size
	}
	// No boundary found - return full length
	return len(text)
}
