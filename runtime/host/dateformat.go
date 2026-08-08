package host

import "strings"

// ldmlToGoLayout translates a (common subset of) Unicode LDML date pattern,
// as used by Swift's DateFormatter.dateFormat (what Aidoku sources pass to
// std.parse_date), into a Go reference-time layout string.
//
// This covers the tokens real-world sources actually use. True ICU/LDML
// parsing (locale-aware month/day names, arbitrary field widths, era
// markers, etc.) is out of scope; unrecognized runs of letters pass through
// unchanged, which will simply fail to match at parse time rather than
// silently misparsing.
func ldmlToGoLayout(pattern string) string {
	var out strings.Builder
	runes := []rune(pattern)
	i := 0
	for i < len(runes) {
		c := runes[i]

		// Quoted literal text, e.g. 'T' or 'at'. '' is a literal quote.
		if c == '\'' {
			i++
			if i < len(runes) && runes[i] == '\'' {
				out.WriteRune('\'')
				i++
				continue
			}
			start := i
			for i < len(runes) && runes[i] != '\'' {
				i++
			}
			out.WriteString(string(runes[start:i]))
			if i < len(runes) {
				i++ // consume closing quote
			}
			continue
		}

		if !isLDMLLetter(c) {
			out.WriteRune(c)
			i++
			continue
		}

		start := i
		for i < len(runes) && runes[i] == c {
			i++
		}
		run := i - start
		out.WriteString(ldmlToken(c, run))
	}
	return out.String()
}

func isLDMLLetter(c rune) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func ldmlToken(c rune, count int) string {
	switch c {
	case 'y', 'Y':
		if count >= 4 {
			return "2006"
		}
		return "06"
	case 'M', 'L':
		switch {
		case count >= 4:
			return "January"
		case count == 3:
			return "Jan"
		case count == 2:
			return "01"
		default:
			return "1"
		}
	case 'd':
		if count >= 2 {
			return "02"
		}
		return "2"
	case 'D':
		// day of year: Go has no direct layout token; best-effort fallback.
		return "002"
	case 'E':
		if count >= 4 {
			return "Monday"
		}
		return "Mon"
	case 'H':
		if count >= 2 {
			return "15"
		}
		return "15"
	case 'h':
		if count >= 2 {
			return "03"
		}
		return "3"
	case 'm':
		if count >= 2 {
			return "04"
		}
		return "4"
	case 's':
		if count >= 2 {
			return "05"
		}
		return "5"
	case 'S':
		// Assumes the pattern already has a literal '.' immediately before
		// the S run (e.g. "ss.SSS"), which is how LDML patterns write
		// fractional seconds.
		return strings.Repeat("0", count)
	case 'a':
		return "PM"
	case 'z':
		return "MST"
	case 'Z':
		if count >= 5 {
			return "-07:00"
		}
		return "-0700"
	case 'X':
		switch {
		case count >= 3:
			return "Z07:00"
		case count == 2:
			return "Z0700"
		default:
			return "Z07"
		}
	case 'x':
		switch {
		case count >= 3:
			return "-07:00"
		case count == 2:
			return "-0700"
		default:
			return "-07"
		}
	case 'G':
		return "" // era marker, unsupported
	default:
		// Unknown letter run: pass through so mismatches fail loudly at
		// parse time instead of silently mis-mapping.
		return strings.Repeat(string(c), count)
	}
}
