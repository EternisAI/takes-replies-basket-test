package llm

import "strings"

// ExtractJSONObject returns the first balanced {...} block in s. It is tolerant
// of prose preambles and ```json code fences that models sometimes add despite
// a JSON schema being requested (notably z-ai/* and moonshotai/* on OpenRouter,
// which wrap structured output in markdown fences). If no object is found it
// returns s unchanged so the caller's decoder produces the original error.
func ExtractJSONObject(s string) string {
	s = strings.TrimSpace(s)
	start := strings.Index(s, "{")
	if start < 0 {
		return s
	}
	depth := 0
	inStr := false
	escape := false
	for i := start; i < len(s); i++ {
		c := s[i]
		if escape {
			escape = false
			continue
		}
		if c == '\\' && inStr {
			escape = true
			continue
		}
		if c == '"' {
			inStr = !inStr
			continue
		}
		if inStr {
			continue
		}
		switch c {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return s[start:]
}
