package normalise

import "testing"

func TestAccept(t *testing.T) {
	bs := string(rune(92)) // a single backslash, built without escaping ambiguity
	cases := []struct {
		in   string
		want bool
	}{
		// Clean brand names (normalised form) — accepted.
		{"reece bathrooms", true},
		{"beauty villa", true},
		{"dr. snoopy", true},      // dots are fine
		{"stella's finds", true},  // apostrophes are fine
		{"country 104.3", true},   // digits are fine
		{"khiladi 786", true},     // trailing number is fine
		{"toyota kowaka", true},   // lowercase multiword
		{"m&m's canada", true},    // a real ampersand is fine

		// Junk — rejected.
		{"", false},                       // empty
		{"786", false},                    // no letters at all
		{"60 / 24-7", false},              // digits + symbols, no letters
		{"tx mart.com02", false},          // scraped domain suffix
		{"daudff bgg.com02", false},       // scraped domain suffix
		{"visit www.shop.example", false}, // url
		{"m" + bs + "u0026m's canada", false}, // leftover \uXXXX escape (broken encoding)
		{"bad\x07name", false},                 // control character
	}
	for _, c := range cases {
		if got := Accept(c.in); got != c.want {
			t.Errorf("Accept(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
