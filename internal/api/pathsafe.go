package api

import (
	"path/filepath"
	"strings"
)

// Every path this API opens on disk is built partly from request input: event
// IDs, camera names, dates and segment names. Two independent checks guard
// that. safeElement rejects anything that is not one plain file-name element,
// so traversal fails early with a clear 400. within then confirms the finished
// path still sits inside the directory it was meant for, a backstop that holds
// even if a future handler forgets the first check.
//
// Neither check is optional. Request paths reach handlers uncleaned: chi does
// not collapse "..", and a client sending a raw path delivers it verbatim.

// safeElement reports whether s is a single, local file-name element.
func safeElement(s string) bool {
	if s == "" || len(s) > 200 || s == "." || s == ".." {
		return false
	}
	if strings.ContainsAny(s, "/\\\x00") || strings.Contains(s, "..") {
		return false
	}
	return filepath.IsLocal(s)
}

// digits reports whether s is exactly n ASCII digits.
func digits(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// within reports whether p lies strictly inside root.
func within(root, p string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(p))
	if err != nil || rel == "." || filepath.IsAbs(rel) {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
