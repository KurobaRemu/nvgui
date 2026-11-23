package app

import (
	"errors"
	"strings"
	"unicode"
)

var dropSpecialChars = map[rune]bool{
	'\\': true, ' ': true, '\t': true, '|': true, '"': true, '\'': true,
	'%': true, '#': true, '$': true, '*': true, '?': true, '[': true,
	'{': true, '~': true, '!': true, ';': true, '(': true, ')': true,
	'&': true, '<': true, '>': true, '`': true, ',': true, '+': true, '=': true,
}

func parseOpenInPolicy(raw string) (OpenInPolicy, bool) {
	switch OpenInPolicy(strings.ToLower(strings.TrimSpace(raw))) {
	case OpenInFront:
		return OpenInFront, true
	case OpenInNew:
		return OpenInNew, true
	default:
		return "", false
	}
}

func formatDropCommand(paths []string) (string, error) {
	if len(paths) == 0 {
		return "", errors.New("app: drop requires at least one path")
	}
	escapedPaths := make([]string, 0, len(paths))
	for _, path := range paths {
		if err := validateDropPath(path); err != nil {
			return "", err
		}
		escapedPaths = append(escapedPaths, escapeDropPath(path))
	}
	return "drop " + strings.Join(escapedPaths, " "), nil
}

func validateDropPath(path string) error {
	if path == "" {
		return errors.New("app: drop path is empty")
	}
	if strings.ContainsAny(path, "\x00\n\r") {
		return errors.New("app: drop path contains a control character")
	}
	return nil
}

func escapeDropPath(path string) string {
	var escaped strings.Builder
	for _, char := range path {
		if dropSpecialChars[char] || unicode.IsSpace(char) {
			escaped.WriteByte('\\')
		}
		escaped.WriteRune(char)
	}
	return escaped.String()
}
