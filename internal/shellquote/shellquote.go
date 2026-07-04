// Package shellquote contains deliberately small POSIX shell quoting helpers.
package shellquote

import "strings"

// Quote returns s as a single POSIX-shell token using single-quote escaping.
func Quote(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// Join quotes and joins tokens for display or remote sh -c commands.
func Join(args ...string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = Quote(arg)
	}
	return strings.Join(quoted, " ")
}
