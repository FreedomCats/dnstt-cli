package script

import (
	"slices"
	"strings"
	"unicode"
)

// SortNames sorts file names the way Sort-Object in pwsh sorted them: by the rules of
// the language, not by bytes. Case counts only when the rest is equal (lower case
// first), punctuation comes before digits, digits before letters, and among punctuation
// "_" comes before "-", "-" before "." (listen_unix.go before listen.go,
// creds_vkcalls.go before creds.go). The order was checked against pwsh 7.4, which
// sorts by ICU, and against pwsh 7 on Windows, where .NET also sorts by ICU.
func SortNames(names []string) {
	slices.SortStableFunc(names, CompareNames)
}

// punctuation is the order of punctuation characters in the default ICU collation.
const punctuation = " _-,;:!?.'\"()[]{}@*/\\&#%`^+<=>|~$"

// CompareNames is the order of SortNames: negative if a comes before b.
func CompareNames(a, b string) int {
	if c := slices.Compare(primary(a), primary(b)); c != 0 {
		return c
	}
	// They differ only in case: lower case comes before upper case.
	ra, rb := []rune(a), []rune(b)
	for i := range min(len(ra), len(rb)) {
		la, lb := unicode.IsLower(ra[i]), unicode.IsLower(rb[i])
		if la != lb {
			if la {
				return -1
			}
			return 1
		}
	}
	return strings.Compare(a, b)
}

// primary is the weight of each character, ignoring case.
func primary(s string) []int {
	w := make([]int, 0, len(s))
	for _, r := range s {
		switch {
		case strings.ContainsRune(punctuation, r):
			w = append(w, 1+strings.IndexRune(punctuation, r))
		case unicode.IsDigit(r):
			w = append(w, 1000+int(r))
		default:
			w = append(w, 1<<20+int(unicode.ToLower(r)))
		}
	}
	return w
}
