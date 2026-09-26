// Package shell_lexer splits command lines into words the way a small shell
// would, so the REPL and future daemon clients can share one quoting behavior.
package shell_lexer

import (
	"fmt"
	"strings"
)

// Fields splits a command line into words: whitespace separates words, single
// quotes are literal, double quotes preserve spaces (with `\"` and `\\`
// escapes), and a backslash escapes the next character outside quotes.
//
// No variable expansion, globbing, or command substitution is performed:
// arguments are passed through literally.
func Fields(s string) ([]string, error) {
	var words []string
	var w strings.Builder
	var quote byte
	started := false

	flush := func() {
		words = append(words, w.String())
		w.Reset()
		started = false
	}

	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote == 0 && (c == ' ' || c == '\t'):
			if started {
				flush()
			}
			continue
		case quote == '\'':
			if c == '\'' {
				quote = 0
			} else {
				w.WriteByte(c)
			}
		case quote == '"':
			switch {
			case c == '"':
				quote = 0
			case c == '\\' && i+1 < len(s) && (s[i+1] == '"' || s[i+1] == '\\'):
				i++
				w.WriteByte(s[i])
			default:
				w.WriteByte(c)
			}
		case c == '\\' && i+1 < len(s):
			i++
			w.WriteByte(s[i])
		case c == '\'' || c == '"':
			quote = c
		default:
			w.WriteByte(c)
		}
		started = true
	}

	if quote != 0 {
		return nil, fmt.Errorf("unclosed %c quote", quote)
	}
	if started {
		flush()
	}
	return words, nil
}
