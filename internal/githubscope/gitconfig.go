package githubscope

import (
	"bufio"
	"bytes"
	"strings"
)

// parseRemoteURLs extracts remote.<name>.url and remote.<name>.pushurl values
// from a git config file. It returns remote name → URLs.
//
// It is a deliberately small parser: it understands sections, quoted
// subsections, comments, quoted values, escapes, and line continuations. It
// ignores include/includeIf directives — following them would let a workspace
// direct moat to read arbitrary files, and skipping them can only make
// discovery suggest fewer repositories, never grant more.
func parseRemoteURLs(data []byte) map[string][]string {
	remotes := map[string][]string{}
	var section, subsection string

	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), maxConfigBytes)
	var pending string
	for sc.Scan() {
		line := sc.Text()
		// Join backslash-newline continuations (outside comments, which we
		// strip later; git only honors continuations in values).
		if strings.HasSuffix(line, `\`) && !strings.HasSuffix(line, `\\`) {
			pending += strings.TrimSuffix(line, `\`)
			continue
		}
		line = pending + line
		pending = ""

		trimmed := strings.TrimSpace(line)
		if trimmed == "" || trimmed[0] == '#' || trimmed[0] == ';' {
			continue
		}

		if trimmed[0] == '[' {
			section, subsection = parseSectionHeader(trimmed)
			continue
		}
		if section != "remote" || subsection == "" {
			continue
		}

		key, value, hasValue := strings.Cut(trimmed, "=")
		key = strings.ToLower(strings.TrimSpace(key))
		if !hasValue || (key != "url" && key != "pushurl") {
			continue
		}
		v := parseValue(value)
		if v != "" {
			remotes[subsection] = append(remotes[subsection], v)
		}
	}
	return remotes
}

// parseSectionHeader parses `[section "subsection"]` or the legacy
// `[section.subsection]` form. Section names are case-insensitive;
// subsections in the quoted form are case-sensitive.
func parseSectionHeader(s string) (section, subsection string) {
	end := strings.LastIndexByte(s, ']')
	if end < 0 {
		return "", ""
	}
	inner := strings.TrimSpace(s[1:end])
	if q := strings.IndexByte(inner, '"'); q >= 0 {
		section = strings.ToLower(strings.TrimSpace(inner[:q]))
		var b strings.Builder
		for i := q + 1; i < len(inner); i++ {
			c := inner[i]
			if c == '\\' && i+1 < len(inner) {
				i++
				b.WriteByte(inner[i])
				continue
			}
			if c == '"' {
				break
			}
			b.WriteByte(c)
		}
		return section, b.String()
	}
	section, subsection, _ = strings.Cut(inner, ".")
	return strings.ToLower(section), strings.ToLower(subsection)
}

// parseValue decodes a git config value: strips an unquoted trailing comment,
// removes quotes, and handles \" \\ \t \n escapes.
func parseValue(v string) string {
	var b strings.Builder
	inQuote := false
	v = strings.TrimSpace(v)
	for i := 0; i < len(v); i++ {
		c := v[i]
		switch {
		case c == '\\' && i+1 < len(v):
			i++
			switch v[i] {
			case 't':
				b.WriteByte('\t')
			case 'n':
				b.WriteByte('\n')
			default:
				b.WriteByte(v[i])
			}
		case c == '"':
			inQuote = !inQuote
		case (c == '#' || c == ';') && !inQuote:
			return strings.TrimSpace(b.String())
		default:
			b.WriteByte(c)
		}
	}
	return strings.TrimSpace(b.String())
}
