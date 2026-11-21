package input

import (
	"fmt"
	"strconv"
	"strings"
)

const squirrelAppOptionsKey = "app_options"

func squirrelFlatBundleKey(key string) (string, bool) {
	prefixes := []string{squirrelAppOptionsKey + "/", squirrelAppOptionsKey + "."}
	for _, prefix := range prefixes {
		if strings.HasPrefix(key, prefix) {
			return strings.TrimPrefix(key, prefix), true
		}
	}
	return "", false
}

type squirrelLine struct {
	number  int
	indent  int
	content string
}

type squirrelPath struct {
	indent int
	key    string
}

func squirrelInAppOptions(path []squirrelPath) bool {
	for _, entry := range path {
		if entry.key == squirrelAppOptionsKey {
			return true
		}
	}
	return false
}

func parseSquirrelAppOptions(data []byte) (map[string]SquirrelAppOption, error) {
	options := make(map[string]SquirrelAppOption)
	lines, err := squirrelLines(data)
	if err != nil {
		return nil, err
	}
	path := []squirrelPath{}
	bundleIndent := -1
	bundleID := ""
	for _, line := range lines {
		key, value, hasColon, err := splitSquirrelPair(line.content, line.number)
		if err != nil {
			return nil, err
		}
		if bundleIndent >= 0 && line.indent <= bundleIndent {
			bundleIndent, bundleID = -1, ""
		}
		if bundleIndent >= 0 {
			if line.indent <= bundleIndent {
				return nil, squirrelError(line.number, "unexpected indentation under bundle ID %q", bundleID)
			}
			if err := applySquirrelOption(options, bundleID, line); err != nil {
				return nil, err
			}
			continue
		}
		for len(path) > 0 && line.indent <= path[len(path)-1].indent {
			path = path[:len(path)-1]
		}
		inAppOptions := squirrelInAppOptions(path)
		flatBundleID, isFlatBundle := squirrelFlatBundleKey(key)
		if hasColon && value == "" && (isFlatBundle || inAppOptions) {
			bundleID = key
			if isFlatBundle {
				bundleID = flatBundleID
			}
			if err := validateSquirrelBundleID(bundleID, line.number); err != nil {
				return nil, err
			}
			bundleIndent = line.indent
			continue
		}
		if inAppOptions {
			if bundleID == "" {
				return nil, squirrelError(line.number, "missing bundle ID for app option %q", key)
			}
			if err := applySquirrelOption(options, bundleID, line); err != nil {
				return nil, err
			}
			continue
		}
		if hasColon && value == "" {
			path = append(path, squirrelPath{indent: line.indent, key: key})
			continue
		}
	}
	return options, nil
}

func squirrelLines(data []byte) ([]squirrelLine, error) {
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	rawLines := strings.Split(text, "\n")
	lines := make([]squirrelLine, 0, len(rawLines))
	for index, raw := range rawLines {
		leading := 0
		for leading < len(raw) && raw[leading] == ' ' {
			leading++
		}
		if leading < len(raw) && raw[leading] == '\t' {
			return nil, squirrelError(index+1, "tab indentation is not allowed")
		}
		content, err := stripSquirrelComment(raw)
		if err != nil {
			return nil, squirrelError(index+1, "%v", err)
		}
		indent := 0
		for indent < len(content) && content[indent] == ' ' {
			indent++
		}
		content = strings.TrimSpace(content)
		if content == "" || content == "---" || content == "..." {
			continue
		}
		lines = append(lines, squirrelLine{number: index + 1, indent: indent, content: content})
	}
	return lines, nil
}

func stripSquirrelComment(line string) (string, error) {
	var quote rune
	escaped := false
	for index, char := range line {
		switch {
		case quote != 0:
			if escaped {
				escaped = false
			} else if quote == '"' && char == '\\' {
				escaped = true
			} else if char == quote {
				quote = 0
			}
		case char == '\'' || char == '"':
			quote = char
		case char == '#' && (index == 0 || line[index-1] == ' ' || line[index-1] == '\t'):
			return line[:index], nil
		}
	}
	if quote != 0 {
		return "", fmt.Errorf("unterminated quoted value")
	}
	return line, nil
}

func splitSquirrelPair(content string, lineNumber int) (string, string, bool, error) {
	var quote byte
	escaped := false
	for index := 0; index < len(content); index++ {
		char := content[index]
		switch {
		case quote != 0:
			if escaped {
				escaped = false
			} else if quote == '"' && char == '\\' {
				escaped = true
			} else if char == quote {
				quote = 0
			}
		case char == '\'' || char == '"':
			quote = char
		case char == ':':
			key := strings.TrimSpace(content[:index])
			value := strings.TrimSpace(content[index+1:])
			unquoted, err := unquoteSquirrel(key)
			if err != nil {
				return "", "", false, squirrelError(lineNumber, "%v", err)
			}
			return unquoted, value, true, nil
		}
	}
	if quote != 0 {
		return "", "", false, squirrelError(lineNumber, "unterminated quoted key")
	}
	return "", "", false, squirrelError(lineNumber, "missing colon in %q", content)
}

func unquoteSquirrel(value string) (string, error) {
	if len(value) < 2 || value[0] != value[len(value)-1] || value[0] != '\'' && value[0] != '"' {
		return value, nil
	}
	if value[0] == '"' {
		decoded, err := strconv.Unquote(value)
		if err != nil {
			return "", fmt.Errorf("invalid quoted value %q: %w", value, err)
		}
		return decoded, nil
	}
	return strings.ReplaceAll(value[1:len(value)-1], "''", "'"), nil
}

func applySquirrelOption(options map[string]SquirrelAppOption, bundleID string, line squirrelLine) error {
	key, value, hasColon, err := splitSquirrelPair(line.content, line.number)
	if err != nil {
		return err
	}
	if !hasColon {
		return squirrelError(line.number, "missing colon in %q", line.content)
	}
	boolValue, err := squirrelBool(value, line.number, key)
	if err != nil {
		return err
	}
	option := options[bundleID]
	option.BundleID = bundleID
	switch key {
	case "ascii_mode":
		option.ASCIIMode = boolValue
	case "vim_mode":
		option.VIMMode = boolValue
	case "no_inline":
		option.NoInline = boolValue
	default:
		return squirrelError(line.number, "unknown app option %q", key)
	}
	options[bundleID] = option
	return nil
}

func squirrelBool(value string, lineNumber int, key string) (*bool, error) {
	if value != "true" && value != "false" {
		return nil, squirrelError(lineNumber, "%s value %q is not a boolean", key, value)
	}
	parsed := value == "true"
	return &parsed, nil
}

func validateSquirrelBundleID(bundleID string, lineNumber int) error {
	if bundleID == "" || strings.ContainsAny(bundleID, " \t") {
		return squirrelError(lineNumber, "invalid bundle ID %q", bundleID)
	}
	return nil
}

func squirrelError(lineNumber int, format string, args ...any) error {
	return fmt.Errorf("input: squirrel app_options line %d: %s", lineNumber, fmt.Sprintf(format, args...))
}

func checkSquirrelBundle(bundleID string, launchPath string, opts map[string]SquirrelAppOption) SquirrelBundleStatus {
	var (
		option  SquirrelAppOption
		matched bool
	)
	if bundleID != "" {
		option, matched = opts[bundleID]
	}
	status := SquirrelBundleStatus{
		BundleID:    bundleID,
		Matched:     matched,
		Option:      option,
		CLIMismatch: bundleID == "" || (!matched && !strings.Contains(launchPath, ".app/Contents/MacOS/")),
	}
	if status.CLIMismatch {
		status.Warning = "Squirrel matches app_options by CFBundleIdentifier; CLI or non-.app launch paths bypass these options (see research/09 (d))"
	}
	return status
}
