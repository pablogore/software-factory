package domain

import (
	"unicode"
	"unicode/utf8"
)

// Blank reports whether s names nobody: it is empty or made only of
// characters a reader cannot tell apart from nothing or that name nobody.
// A person's name or reason that is blank must not be able to act on a job
// while looking like it was signed.
func Blank(s string) bool {
	for _, r := range s {
		if !blankRune(r) {
			return false
		}
	}
	return true
}

// blankLooking are symbols (So) that render as empty space, the usual
// tricks for an invisible name.
var blankLooking = map[rune]bool{
	'\u2800':     true, // BRAILLE PATTERN BLANK
	'\u303f':     true, // IDEOGRAPHIC HALF FILL SPACE
	'\U0001d159': true, // MUSICAL SYMBOL NULL NOTEHEAD
}

// blankRune reports whether r is invisible or names nobody: whitespace,
// punctuation, control, format, combining marks with nothing to combine
// with, private use, surrogates, unassigned code points and noncharacters
// (Cn), invalid UTF-8 (decoded as utf8.RuneError) and the blank-looking
// symbols. A letter or digit carrying combining marks is still
// a name, since its base rune is not blank.
func blankRune(r rune) bool {
	switch {
	case r == utf8.RuneError, blankLooking[r]:
		return true
	case unicode.IsSpace(r), unicode.IsPunct(r):
		return true
	}
	return unicode.In(r, unicode.Cc, unicode.Cf, unicode.Cn, unicode.Co, unicode.Cs, unicode.Mn, unicode.Me,
		unicode.Variation_Selector, unicode.Other_Default_Ignorable_Code_Point)
}
