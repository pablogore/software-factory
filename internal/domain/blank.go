package domain

import "unicode"

// Blank reports whether s names nobody: it is empty or made only of
// characters a reader cannot tell apart from nothing (spaces, control and
// format characters such as zero-width ones, other default-ignorable code
// points) or of punctuation. A person's name or reason that is blank must
// not be able to act on a job while looking like it was signed.
func Blank(s string) bool {
	for _, r := range s {
		if !unicode.IsSpace(r) && !unicode.IsPunct(r) &&
			!unicode.In(r, unicode.Cc, unicode.Cf, unicode.Variation_Selector, unicode.Other_Default_Ignorable_Code_Point) {
			return false
		}
	}
	return true
}
