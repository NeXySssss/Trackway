package util

import (
	"strings"
	"time"
)

func HTMLEscape(input string) string {
	result := strings.ReplaceAll(input, "&", "&amp;")
	result = strings.ReplaceAll(result, "<", "&lt;")
	result = strings.ReplaceAll(result, ">", "&gt;")
	return result
}

func SplitByLimit(text string, maxLen int) []string {
	if len(text) <= maxLen {
		return []string{text}
	}
	chunks := make([]string, 0, len(text)/maxLen+1)
	for len(text) > maxLen {
		chunks = append(chunks, text[:maxLen])
		text = text[maxLen:]
	}
	if text != "" {
		chunks = append(chunks, text)
	}
	return chunks
}

func SplitByLineLimit(text string, maxLen int) []string {
	if len(text) <= maxLen {
		return []string{text}
	}
	lines := strings.Split(text, "\n")
	chunks := make([]string, 0, len(lines)/2+1)
	var current strings.Builder

	flush := func() {
		if current.Len() == 0 {
			return
		}
		chunks = append(chunks, current.String())
		current.Reset()
	}

	for _, line := range lines {
		if len(line) > maxLen {
			flush()
			chunks = append(chunks, SplitByLimit(line, maxLen)...)
			continue
		}
		if current.Len() == 0 {
			current.WriteString(line)
			continue
		}
		if current.Len()+1+len(line) > maxLen {
			flush()
			current.WriteString(line)
			continue
		}
		current.WriteByte('\n')
		current.WriteString(line)
	}
	flush()
	return chunks
}

func SafeName(name string) string {
	var sb strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			sb.WriteRune(r)
		} else {
			sb.WriteRune('_')
		}
	}
	return sb.String()
}

func FormatTime(ts time.Time) string {
	if ts.IsZero() {
		return "-"
	}
	return ts.UTC().Format(time.RFC3339)
}
