package text

import (
	"fmt"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/egoist/mygo/ui"
)

const (
	defaultFontSize        float32 = 14
	defaultFontWeight      int     = 400
	genericMonospaceFamily string  = "monospace"
	defaultCJKFamily       string  = "PingFang SC"
	defaultEmojiFamily     string  = "Apple Color Emoji"
)

type fallbackClass uint8

const (
	fallbackCJK fallbackClass = 1 << iota
	fallbackEmoji
	fallbackNerd
)

type textClass uint8

const (
	needCJK textClass = 1 << iota
	needEmoji
	needNerd
)

type fallbackEntry struct {
	font  Font
	class fallbackClass
}

type guifontEntry struct {
	family  string
	options []string
}

type fontFamilyIndex struct {
	families  []ui.FontFamily
	byName    map[string]ui.FontFamily
	firstMono string
}

func ParseGUIFont(value string) ([]Font, error) {
	if strings.TrimSpace(value) == "" {
		return nil, fmt.Errorf("text: empty guifont")
	}
	entries, err := splitGUIFont(value)
	if err != nil {
		return nil, err
	}
	fonts := make([]Font, 0, len(entries))
	for index, entry := range entries {
		if strings.TrimSpace(entry.family) == "" {
			return nil, fmt.Errorf("text: empty guifont entry %d", index+1)
		}
		font, err := parseGUIFontEntry(entry, index)
		if err != nil {
			return nil, err
		}
		fonts = append(fonts, font)
	}
	return fonts, nil
}

func BuiltinFallbacks() []Font {
	entries := builtinFallbackEntries()
	fonts := make([]Font, 0, len(entries))
	for _, entry := range entries {
		fonts = append(fonts, entry.font)
	}
	return fonts
}

func fontToMyGo(font Font) ui.Font {
	features := normalizeFeatures(font.Features)
	if runtime.GOOS == "darwin" && !containsFeature(features, "kern") {
		features = unkerned(features)
	}
	return ui.Font{
		Family:   font.Family,
		Size:     font.Size,
		Weight:   font.Weight,
		Italic:   font.Italic,
		Features: features,
	}
}

func parseGUIFontEntry(entry guifontEntry, entryIndex int) (Font, error) {
	font := Font{Family: entry.family, Size: defaultFontSize, Weight: defaultFontWeight}
	var features []string
	var heightSeen, weightSeen, boldOption bool

	for _, rawOption := range entry.options {
		option := strings.TrimSpace(rawOption)
		if option == "" {
			return Font{}, fmt.Errorf("text: empty option in guifont entry %d", entryIndex+1)
		}
		lowerOption := strings.ToLower(option)
		switch {
		case isGUIFontNumber(lowerOption, 'h'):
			if heightSeen {
				return Font{}, fmt.Errorf("text: duplicate height in guifont entry %d: %q", entryIndex+1, option)
			}
			size, err := strconv.ParseFloat(option[1:], 32)
			if err != nil {
				return Font{}, fmt.Errorf("text: invalid height in guifont entry %d: %q: %w", entryIndex+1, option, err)
			}
			if !isFinite(float32(size)) || size <= 0 {
				return Font{}, fmt.Errorf("text: invalid height in guifont entry %d: %q", entryIndex+1, option)
			}
			font.Size = float32(size)
			heightSeen = true
		case isGUIFontNumber(lowerOption, 'w'):
			if weightSeen {
				return Font{}, fmt.Errorf("text: duplicate weight in guifont entry %d: %q", entryIndex+1, option)
			}
			weight, err := strconv.Atoi(option[1:])
			if err != nil {
				return Font{}, fmt.Errorf("text: invalid weight in guifont entry %d: %q: %w", entryIndex+1, option, err)
			}
			if weight < 100 || weight > 900 {
				return Font{}, fmt.Errorf("text: invalid weight in guifont entry %d: %q", entryIndex+1, option)
			}
			font.Weight = weight
			weightSeen = true
		case strings.EqualFold(option, "bold") || strings.EqualFold(option, "b"):
			boldOption = true
		case strings.EqualFold(option, "italic") || strings.EqualFold(option, "i"):
			font.Italic = true
		default:
			features = append(features, normalizedFeature(option))
		}
	}
	if boldOption {
		font.Weight = min(max(font.Weight+300, 700), 900)
	}
	font.Features = strings.Join(features, ",")
	return font, nil
}

func splitGUIFont(value string) ([]guifontEntry, error) {
	var entries []guifontEntry
	current := guifontEntry{}
	var builder strings.Builder
	var quote rune
	inOptions := false
	escaped := false
	appendEntry := func(last string) {
		if inOptions {
			current.options = append(current.options, last)
		} else {
			current.family = strings.TrimSpace(last)
		}
		entries = append(entries, current)
		current = guifontEntry{}
		inOptions = false
	}
	for _, character := range value {
		if escaped {
			switch character {
			case '_':
				builder.WriteByte(' ')
			case ',', ':', '\\':
				builder.WriteRune(character)
			default:
				builder.WriteByte('\\')
				builder.WriteRune(character)
			}
			escaped = false
			continue
		}
		if character == '\\' {
			escaped = true
			continue
		}
		if quote != 0 {
			if character == quote {
				quote = 0
				continue
			}
			builder.WriteRune(character)
			continue
		}
		if character == '"' || character == '\'' {
			quote = character
			continue
		}
		switch {
		case character == ':' && !inOptions:
			current.family = strings.TrimSpace(builder.String())
			inOptions = true
			builder.Reset()
		case character == ':' && inOptions:
			current.options = append(current.options, builder.String())
			builder.Reset()
		case character == ',':
			appendEntry(builder.String())
			builder.Reset()
		default:
			builder.WriteRune(character)
		}
	}
	if escaped {
		return nil, fmt.Errorf("text: trailing escape in guifont %q", value)
	}
	if quote != 0 {
		return nil, fmt.Errorf("text: unclosed quote in guifont %q", value)
	}
	appendEntry(builder.String())
	return entries, nil
}

func isGUIFontNumber(option string, prefix byte) bool {
	if option == "" || option[0] != prefix {
		return false
	}
	if len(option) == 1 {
		return true
	}
	next := option[1]
	return (next >= '0' && next <= '9') || next == '-' || next == '+' || next == '.' ||
		strings.HasPrefix(option[1:], "nan") || strings.HasPrefix(option[1:], "inf")
}

func normalizeFeatures(features string) string {
	parts := strings.Split(features, ",")
	normalized := make([]string, 0, len(parts))
	for _, feature := range parts {
		trimmed := strings.TrimSpace(feature)
		if trimmed == "" {
			continue
		}
		if result := normalizedFeature(trimmed); result != "" {
			normalized = append(normalized, result)
		}
	}
	return strings.Join(normalized, ",")
}

func normalizedFeature(option string) string {
	trimmed := strings.TrimSpace(option)
	if tag, ok := strings.CutPrefix(trimmed, "-"); ok {
		tag = strings.TrimSpace(tag)
		if tag != "" {
			if strings.Contains(tag, "=") {
				return tag
			}
			return tag + "=0"
		}
	}
	if tag, ok := strings.CutPrefix(trimmed, "+"); ok {
		tag = strings.TrimSpace(tag)
		if tag != "" {
			return tag
		}
	}
	if before, after, found := strings.Cut(trimmed, "="); found {
		return strings.TrimSpace(before) + "=" + strings.TrimSpace(after)
	}
	return trimmed
}

func containsFeature(features, wanted string) bool {
	for _, feature := range strings.Split(features, ",") {
		tag, _, _ := strings.Cut(feature, "=")
		if strings.TrimSpace(tag) == wanted {
			return true
		}
	}
	return false
}

func unkerned(features string) string {
	if features == "" {
		return "kern=0"
	}
	return "kern=0," + features
}

func newFontFamilyIndex(families []ui.FontFamily) fontFamilyIndex {
	index := fontFamilyIndex{
		families:  families,
		byName:    make(map[string]ui.FontFamily, len(families)),
		firstMono: genericMonospaceFamily,
	}
	monoFound := false
	for _, family := range families {
		name := strings.TrimSpace(family.Name)
		if name == "" {
			continue
		}
		index.byName[strings.ToLower(name)] = family
		if !monoFound && family.Monospaced {
			index.firstMono = name
			monoFound = true
		}
	}
	return index
}

func builtinFallbackEntries() []fallbackEntry {
	return builtinFallbackEntriesFromIndex(newFontFamilyIndex(ui.FontFamilies()))
}

func installedCJKFonts(index fontFamilyIndex) []Font {
	candidates := []string{
		"PingFang SC",
		"Hiragino Sans",
		"Heiti SC",
		"Songti SC",
		"Apple SD Gothic Neo",
		"Apple LiGothic",
		"Noto Sans CJK SC",
		"Source Han Sans SC",
	}
	fonts := make([]Font, 0, len(candidates))
	for _, candidate := range candidates {
		family, ok := index.byName[strings.ToLower(candidate)]
		if !ok {
			continue
		}
		fonts = append(fonts, Font{Family: family.Name, Size: defaultFontSize, Weight: defaultFontWeight})
	}
	if len(fonts) == 0 {
		fonts = append(fonts, Font{Family: defaultCJKFamily, Size: defaultFontSize, Weight: defaultFontWeight})
	}
	return fonts
}

func installedEmojiFonts(index fontFamilyIndex) []Font {
	candidates := []string{
		"Apple Color Emoji",
		"Noto Color Emoji",
		"Segoe UI Emoji",
	}
	fonts := make([]Font, 0, len(candidates))
	for _, candidate := range candidates {
		family, ok := index.byName[strings.ToLower(candidate)]
		if !ok {
			continue
		}
		fonts = append(fonts, Font{Family: family.Name, Size: defaultFontSize, Weight: defaultFontWeight})
	}
	if len(fonts) == 0 {
		fonts = append(fonts, Font{Family: defaultEmojiFamily, Size: defaultFontSize, Weight: defaultFontWeight})
	}
	return fonts
}

func installedNerdFonts(families []ui.FontFamily) []Font {
	var fonts []Font
	seen := make(map[string]bool)
	for _, family := range families {
		name := strings.TrimSpace(family.Name)
		if name == "" || !strings.Contains(strings.ToLower(name), "nerd") {
			continue
		}
		key := strings.ToLower(name)
		if seen[key] {
			continue
		}
		seen[key] = true
		fonts = append(fonts, Font{Family: name, Size: defaultFontSize, Weight: defaultFontWeight})
	}
	sort.Slice(fonts, func(left, right int) bool {
		return strings.ToLower(fonts[left].Family) < strings.ToLower(fonts[right].Family)
	})
	return fonts
}

func defaultMonospaceFamily(index fontFamilyIndex) string {
	return index.firstMono
}

func isMonospacedFamily(name string, index fontFamilyIndex) bool {
	trimmed := strings.TrimSpace(name)
	if strings.EqualFold(trimmed, genericMonospaceFamily) {
		return true
	}
	family, ok := index.byName[strings.ToLower(trimmed)]
	return ok && family.Monospaced
}

func classifyText(value string) textClass {
	var classes textClass
	for _, character := range value {
		if isEmojiRune(character) || isCombiningRune(character) || character == 0x200D {
			classes |= needEmoji
		}
		if isCJKCharacter(character) {
			classes |= needCJK
		}
		if isPUACharacter(character) {
			classes |= needNerd
		}
	}
	return classes
}

func isEmojiRune(character rune) bool {
	switch {
	case character >= 0x1F000 && character <= 0x1FAFF,
		character >= 0x2600 && character <= 0x27BF,
		character >= 0x2300 && character <= 0x23FF,
		character >= 0x2B00 && character <= 0x2BFF,
		character >= 0xFE00 && character <= 0xFE0F,
		character >= 0xE0020 && character <= 0xE007F,
		character >= 0xE0100 && character <= 0xE01EF:
		return true
	}
	return false
}

func isCombiningRune(character rune) bool {
	if character == 0x200D || character == 0x20E3 {
		return true
	}
	if character >= 0xFE00 && character <= 0xFE0F {
		return true
	}
	if character >= 0xE0100 && character <= 0xE01EF {
		return true
	}
	return unicode.In(character, unicode.Mn, unicode.Mc, unicode.Me)
}

func isCJKCharacter(character rune) bool {
	if unicode.In(character, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul, unicode.Bopomofo) {
		return true
	}
	switch {
	case character >= 0x2E80 && character <= 0x2FDF,
		character >= 0x3000 && character <= 0x303F,
		character >= 0x3100 && character <= 0x312F,
		character >= 0x3190 && character <= 0x31EF,
		character >= 0x3400 && character <= 0x4DBF,
		character >= 0x4E00 && character <= 0x9FFF,
		character >= 0xF900 && character <= 0xFAFF,
		character >= 0xFF00 && character <= 0xFFEF,
		character >= 0x20000 && character <= 0x2FA1F:
		return true
	}
	return false
}

func isPUACharacter(character rune) bool {
	switch {
	case character >= 0xE000 && character <= 0xF8FF,
		character >= 0xF0000 && character <= 0xFFFFD,
		character >= 0x100000 && character <= 0x10FFFD:
		return true
	}
	return false
}

func familyChain(fonts []Font) string {
	var families []string
	seen := make(map[string]bool)
	for _, font := range fonts {
		family := strings.TrimSpace(font.Family)
		if family == "" {
			continue
		}
		key := strings.ToLower(family)
		if seen[key] {
			continue
		}
		seen[key] = true
		families = append(families, quoteFamily(family))
	}
	return strings.Join(families, ",")
}

func quoteFamily(family string) string {
	if strings.ContainsAny(family, ",\"'\\") {
		return strconv.Quote(family)
	}
	return family
}
