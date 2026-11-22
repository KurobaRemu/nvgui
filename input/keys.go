package input

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
)

type keyNotation struct{ plain, inner string }

var keyNotations = func() map[ui.Key]keyNotation {
	notations := make(map[ui.Key]keyNotation)
	for index := range 26 {
		char := string(rune('a') + rune(index))
		notations[ui.KeyA+ui.Key(index)] = keyNotation{plain: char, inner: char}
	}
	for index := range 10 {
		char := string(rune('0') + rune(index))
		notations[ui.Key0+ui.Key(index)] = keyNotation{plain: char, inner: char}
	}
	punctuation := map[ui.Key]keyNotation{
		ui.KeyMinus:        {plain: "-", inner: "-"},
		ui.KeyEqual:        {plain: "=", inner: "="},
		ui.KeyComma:        {plain: ",", inner: ","},
		ui.KeyPeriod:       {plain: ".", inner: "."},
		ui.KeySlash:        {plain: "/", inner: "/"},
		ui.KeySemicolon:    {plain: ";", inner: ";"},
		ui.KeyQuote:        {plain: "'", inner: "'"},
		ui.KeyBracketLeft:  {plain: "[", inner: "["},
		ui.KeyBracketRight: {plain: "]", inner: "]"},
		ui.KeyBackslash:    {plain: "<Bslash>", inner: "\\"},
		ui.KeyBackquote:    {plain: "`", inner: "`"},
	}
	for key, value := range punctuation {
		notations[key] = value
	}
	special := map[ui.Key]keyNotation{
		ui.KeyEnter:     {plain: "<CR>", inner: "CR"},
		ui.KeyEscape:    {plain: "<Esc>", inner: "Esc"},
		ui.KeyTab:       {plain: "<Tab>", inner: "Tab"},
		ui.KeyBackspace: {plain: "<BS>", inner: "BS"},
		ui.KeyDelete:    {plain: "<Del>", inner: "Del"},
		ui.KeySpace:     {plain: "<Space>", inner: "Space"},
		ui.KeyInsert:    {plain: "<Insert>", inner: "Insert"},
		ui.KeyHome:      {plain: "<Home>", inner: "Home"},
		ui.KeyEnd:       {plain: "<End>", inner: "End"},
		ui.KeyPageUp:    {plain: "<PageUp>", inner: "PageUp"},
		ui.KeyPageDown:  {plain: "<PageDown>", inner: "PageDown"},
		ui.KeyLeft:      {plain: "<Left>", inner: "Left"},
		ui.KeyRight:     {plain: "<Right>", inner: "Right"},
		ui.KeyUp:        {plain: "<Up>", inner: "Up"},
		ui.KeyDown:      {plain: "<Down>", inner: "Down"},
	}
	for index := range 12 {
		name := fmt.Sprintf("F%d", index+1)
		special[ui.KeyF1+ui.Key(index)] = keyNotation{plain: "<" + name + ">", inner: name}
	}
	for key, value := range special {
		notations[key] = value
	}
	return notations
}()

func Notation(ev ui.InputEvent) (string, bool) {
	notation, ok := keyNotations[ev.Key]
	if !ok {
		return "", false
	}
	mods := ev.Mods & (ui.Ctrl | ui.Alt | ui.Super | ui.Shift)
	if mods == 0 {
		return notation.plain, true
	}
	var builder strings.Builder
	builder.WriteByte('<')
	if mods&ui.Ctrl != 0 {
		builder.WriteString("C-")
	}
	if mods&ui.Alt != 0 {
		builder.WriteString("A-")
	}
	if mods&ui.Super != 0 {
		builder.WriteString("D-")
	}
	if mods&ui.Shift != 0 {
		builder.WriteString("S-")
	}
	builder.WriteString(notation.inner)
	builder.WriteByte('>')
	return builder.String(), true
}

func isModifierOrLockCode(code ui.KeyCode) bool {
	switch code {
	case ui.CodeUnknown, ui.CodeCapsLock, ui.CodeNumLock, ui.CodeScrollLock,
		ui.CodeControlLeft, ui.CodeShiftLeft, ui.CodeAltLeft, ui.CodeSuperLeft,
		ui.CodeControlRight, ui.CodeShiftRight, ui.CodeAltRight, ui.CodeSuperRight,
		ui.CodeKanaMode, ui.CodeConvert, ui.CodeNonConvert,
		ui.CodeLang1, ui.CodeLang2, ui.CodeLang3, ui.CodeLang4:
		return true
	default:
		return false
	}
}

var normalModePunctuation = map[string]string{
	"：": ":", "；": ";", "，": ",", "。": ".", "？": "?", "！": "!",
	"【": "[", "】": "]", "「": "[", "」": "]", "『": "{", "』": "}",
	"《": "<", "〈": "<", "》": ">", "〉": ">",
	"（": "(", "）": ")", "“": "\"", "”": "\"", "‘": "'", "’": "'",
	"・": "/", "ー": "-", "·": "`", "……": "^", "…": "^", "——": "_", "—": "_",
}

func normalizeNormalModeCJKPunctuation(text string, code ui.KeyCode) string {
	if mapped, ok := normalModePunctuation[text]; ok {
		return mapped
	}
	if text == "、" {
		if code == ui.CodeComma {
			return ","
		}
		return `\`
	}
	if text == "￥" || text == "¥" {
		if code == ui.CodeBackslash || code == ui.CodeIntlYen {
			return `\`
		}
		return "$"
	}
	r, size := utf8.DecodeRuneInString(text)
	if size != len(text) || !isFullwidthASCIIPunctuation(r) {
		return text
	}
	return string(r - 0xFEE0)
}

func isFullwidthASCIIPunctuation(r rune) bool {
	return r >= 0xFF01 && r <= 0xFF0F ||
		r >= 0xFF1A && r <= 0xFF20 ||
		r >= 0xFF3B && r <= 0xFF40 ||
		r >= 0xFF5B && r <= 0xFF5E
}

func isTypeableKey(key ui.Key) bool {
	switch {
	case key >= ui.KeyA && key <= ui.KeyZ:
		return true
	case key >= ui.Key0 && key <= ui.Key9:
		return true
	case key == ui.KeySpace:
		return true
	case key == ui.KeyMinus || key == ui.KeyEqual || key == ui.KeyComma ||
		key == ui.KeyPeriod || key == ui.KeySlash || key == ui.KeySemicolon ||
		key == ui.KeyQuote || key == ui.KeyBracketLeft || key == ui.KeyBracketRight ||
		key == ui.KeyBackslash || key == ui.KeyBackquote:
		return true
	default:
		return false
	}
}

func escapeText(text string) string {
	var builder strings.Builder
	for index := 0; index < len(text); index++ {
		current := text[index]
		switch {
		case current == '<':
			builder.WriteString("<lt>")
		case current == '\\':
			builder.WriteString("<Bslash>")
		case current < 0x20 || current == 0x7f:
			builder.WriteString(controlKeycode(current))
		default:
			builder.WriteByte(current)
		}
	}
	return builder.String()
}

func controlKeycode(current byte) string {
	switch current {
	case 0:
		return "<Nul>"
	case 8:
		return "<BS>"
	case 9:
		return "<Tab>"
	case 10:
		return "<NL>"
	case 13:
		return "<CR>"
	case 27:
		return "<Esc>"
	case 127:
		return "<Del>"
	}
	if current >= 1 && current <= 26 {
		return "<C-" + string(rune('a')+rune(current)-1) + ">"
	}
	return "<C-" + string(rune('@')+rune(current)) + ">"
}
