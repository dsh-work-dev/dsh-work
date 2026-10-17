// Package redact removes secret-shaped values from text before it is retained or projected.
package redact

import "strings"

// Secrets is intentionally conservative: it removes common secret-shaped
// values before output is retained in a diagnostic buffer or projected.
func Secrets(input string) string {
	if input == "" {
		return ""
	}
	redacted := input
	for _, key := range []string{"api_key", "apikey", "api-key", "token", "password", "secret", "authorization", "cookie"} {
		redacted = redactKeyValue(redacted, key)
	}
	return redacted
}

func redactKeyValue(input, key string) string {
	lower := strings.ToLower(input)
	searchFrom := 0
	for {
		index := strings.Index(lower[searchFrom:], key)
		if index < 0 {
			return input
		}
		index += searchFrom
		if (index > 0 && isWordByte(input[index-1])) || (index+len(key) < len(input) && isWordByte(input[index+len(key)])) {
			searchFrom = index + len(key)
			continue
		}
		separator := index + len(key)
		for separator < len(input) && (input[separator] == ' ' || input[separator] == '\t') {
			separator++
		}
		if separator < len(input) && input[separator] == '"' {
			separator++
			for separator < len(input) && (input[separator] == ' ' || input[separator] == '\t') {
				separator++
			}
		}
		if separator < len(input) && (input[separator] == ':' || input[separator] == '=') {
			separator++
			for separator < len(input) && (input[separator] == ' ' || input[separator] == '\t') {
				separator++
			}
		} else {
			searchFrom = separator
			continue
		}
		if separator >= len(input) {
			return input
		}
		if input[separator] == '"' {
			valueStart := separator + 1
			end := valueStart
			for end < len(input) {
				if input[end] == '"' && input[end-1] != '\\' {
					break
				}
				end++
			}
			if end >= len(input) {
				end = len(input)
			}
			input = input[:valueStart] + "[REDACTED]" + input[end:]
			lower = strings.ToLower(input)
			searchFrom = valueStart + len("[REDACTED]")
			continue
		}
		if strings.EqualFold(key, "authorization") {
			if schemeEnd := strings.IndexAny(input[separator:], " \t"); schemeEnd > 0 {
				schemeEnd += separator
				scheme := strings.ToLower(input[separator:schemeEnd])
				if scheme == "bearer" || scheme == "basic" || scheme == "digest" {
					valueStart := schemeEnd
					for valueStart < len(input) && (input[valueStart] == ' ' || input[valueStart] == '\t') {
						valueStart++
					}
					separator = valueStart
				}
			}
		}
		end := separator
		for end < len(input) && input[end] != ' ' && input[end] != '\t' && input[end] != '\r' && input[end] != '\n' && input[end] != ',' && input[end] != ';' {
			end++
		}
		input = input[:separator] + "[REDACTED]" + input[end:]
		lower = strings.ToLower(input)
		searchFrom = separator + len("[REDACTED]")
	}
}

func isWordByte(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9' || value == '_'
}
