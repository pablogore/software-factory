package domain_test

import (
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
)

type blankRow struct {
	name  string
	text  string
	blank bool
}

func TestBlank(t *testing.T) {
	specs.Describe(t, "Blank", func(s *specs.Spec) {
		rows := []blankRow{
			{"an empty text is blank", "", true},
			{"spaces and tabs are blank", " \t\n", true},
			{"a no-break and an ideographic space are blank", "\u00a0\u3000", true},
			{"a zero-width space is blank", "\u200b", true},
			{"zero-width joiners and a word joiner are blank", "\u200c\u200d\u2060", true},
			{"a byte order mark is blank", "\ufeff", true},
			{"a right-to-left mark is blank", "\u200f", true},
			{"control characters are blank", "\x00\x1b\x7f", true},
			{"a variation selector is blank", "\ufe0f", true},
			{"a Hangul filler is blank", "\u3164", true},
			{"invisible characters around spaces are blank", " \u200b \u2060 ", true},
			{"a dot is blank", ".", true},
			{"only punctuation is blank", "-_.,!?", true},
			{"punctuation between invisible characters is blank", "\u200b-\u200b", true},
			{"non-Latin punctuation is blank", "\u3002\u00bf", true},
			{"a Braille blank is blank", "\u2800", true},
			{"an ideographic half fill space is blank", "\u303f", true},
			{"a null notehead is blank", "\U0001d159", true},
			{"blank-looking symbols with spaces are blank", "\u2800 \u303f", true},
			{"a lone combining accent is blank", "\u0301", true},
			{"a lone enclosing mark is blank", "\u20dd", true},
			{"a combining accent after a space is blank", " \u0301", true},
			{"a private-use character is blank", "\ue000", true},
			{"a supplementary private-use character is blank", "\U000f0000", true},
			{"an unassigned code point is blank", "\u0378", true},
			{"noncharacters are blank", "\ufdd0\uffff", true},
			{"invalid UTF-8 is blank", "\xff\xfe", true},
			{"an encoded surrogate is blank", "\xed\xa0\x80", true},
			{"a login is not blank", "pablogore", false},
			{"a letter with a combining accent is not blank", "e\u0301", false},
			{"a dollar sign is not blank", "$", false},
			{"an emoji is not blank", "\U0001f600", false},
			{"an emoji with a variation selector is not blank", "\u2764\ufe0f", false},
			{"a fullwidth plus is not blank", "\uff0b", false},
			{"a login with a dash is not blank", "pablo-gore", false},
			{"a single letter is not blank", "x", false},
			{"a digit is not blank", "7", false},
			{"a non-Latin name is not blank", "\u7530\u4e2d", false},
			{"a letter hidden among invisible characters is not blank", "\u200ba\u200b", false},
			{"a symbol is not blank", "+", false},
			{"a reason with words is not blank", "style only; merging for the release", false},
		}
		specs.Table(s, rows, func(r blankRow) string { return r.name }, func(ctx *specs.Context, r blankRow) {
			ctx.Expect(domain.Blank(r.text)).ToEqual(r.blank)
		})
	})
}
