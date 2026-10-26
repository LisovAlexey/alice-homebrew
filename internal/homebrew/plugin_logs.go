package homebrew

import "strings"

// Keep the last diagnostics after exit and across a manager restart. Bounds
// match the native runner; logs remain local and disappear on plugin removal.
func boundedLogLines(lines []string) []string {
	if len(lines) == 0 {
		return nil
	}
	if len(lines) > 32 {
		lines = lines[len(lines)-32:]
	}
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.ToValidUTF8(line, "?")
		if len(line) > 2048 {
			line = strings.ToValidUTF8(line[:2045], "?") + "..."
		}
		out = append(out, line)
	}
	return out
}
