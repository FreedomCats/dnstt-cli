package main

import (
	"fmt"
	"regexp"
)

// applyRules applies all rules for the file path to its text, in order. The text has LF:
// the patterns are written for it.
func applyRules(path, text string) (string, error) {
	for _, r := range rules {
		if r.File != path {
			continue
		}
		re, err := regexp.Compile(r.Pattern)
		if err != nil {
			return "", fmt.Errorf("pattern for %s: %w", path, err)
		}
		if found := len(re.FindAllStringIndex(text, -1)); found != r.Count {
			return "", fmt.Errorf("replacement in %s did not match: expected %d matches, found %d.\n"+
				"Upstream changed this code — fix the pattern in scripts/vendorcore/rules.go.\n"+
				"Pattern: %s", path, r.Count, found, r.Pattern)
		}

		// Group 1 is the indentation of the matched line: an insertion goes on the same
		// level.
		var replacement string
		switch {
		case r.Replace != nil:
			replacement = *r.Replace
		case r.InsertAfter != "":
			replacement = "${0}\n${1}" + r.InsertAfter
		case r.InsertBefore != "":
			replacement = "${1}" + r.InsertBefore + "\n${0}"
		default:
			return "", fmt.Errorf("the rule for %s sets none of Replace, InsertAfter, InsertBefore", path)
		}
		text = re.ReplaceAllString(text, replacement)
	}
	return text, nil
}
