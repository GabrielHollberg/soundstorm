// Package jsonc strips comments from JSON so a configuration file can be
// documented in place.
//
// A config file nobody can annotate is a config file nobody maintains
// correctly. Supporting // and /* */ costs about sixty lines and no
// dependencies, which is cheaper than pulling in a YAML parser.
//
// Comments are replaced with spaces rather than deleted, so byte offsets in
// any decoder error still line up with the original file.
package jsonc

// Strip returns src with // and /* */ comments blanked out. Comment markers
// inside string literals are left alone.
func Strip(src []byte) []byte {
	out := make([]byte, len(src))
	copy(out, src)

	const (
		code = iota
		inString
		inLine
		inBlock
	)
	state := code
	escaped := false

	for i := 0; i < len(out); i++ {
		c := out[i]
		switch state {
		case code:
			switch {
			case c == '"':
				state = inString
			case c == '/' && i+1 < len(out) && out[i+1] == '/':
				out[i], out[i+1] = ' ', ' '
				i++
				state = inLine
			case c == '/' && i+1 < len(out) && out[i+1] == '*':
				out[i], out[i+1] = ' ', ' '
				i++
				state = inBlock
			}

		case inString:
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				state = code
			}

		case inLine:
			if c == '\n' {
				state = code
			} else {
				out[i] = ' '
			}

		case inBlock:
			if c == '*' && i+1 < len(out) && out[i+1] == '/' {
				out[i], out[i+1] = ' ', ' '
				i++
				state = code
			} else if c != '\n' {
				// Keep newlines so error line numbers stay accurate.
				out[i] = ' '
			}
		}
	}
	return out
}
