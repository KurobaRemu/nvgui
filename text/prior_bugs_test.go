// Ticket 43: deterministic text-rendering P0 regression set.
// Covered (3 functions / 4 IDs): neovide#444, macvim#243, neovide#2786, neovide#3431.
// Explicitly deferred (31 total across this set; registered here instead of wiki/m3-exit.md,
// which is outside this ticket's write scope):
//   - 文字渲染 (6): equalsraf/neovim-qt#166 (M4.4 连字), neovim#32539 (二期变字号 statusline),
//     vhakulinen/gnvim#85 (Linux Pango), neovide#589, neovide#1231, rohit-px2/nvui#87 (L3 人眼 A/B).
//   - macOS 应用壳 (14): neovide#1332 (M2.1), neovide#1586 (M2.2), neovide#2378 (M2),
//     macvim#312, macvim#497 (M2.4), neovim#3344, neovim#11393 (TUI 终端协议不适用),
//     macvim#525, macvim#947, macvim#1119, neovide#459, neovide#1859, neovide#2384, vimr#683
//     (历史平台崩溃无现代复现).
//   - 性能 (1): neovim#6950 (上游 jemalloc 5.0 分配器 bug，不适用).
//   - 动画 (3): neovide#850, neovim#279, neovim#8715 (全部 M4).
//   - 分发 (7): neovim-qt#771, macvim#552, macvim#952, neovide#1679, neovim#18504,
//     neovim#9630, gnvim#219 (全部 M5 签名公证/dmg/cask).
//
// 13 covered P0 IDs + 31 deferred = 44/44; no subjective or milestone-specific scenario is forced.
package text_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"

	"nvgui/text"
)

func TestPriorBug_Neovide444_MacVim243_FontFallbackChainCJKEmojiNerd(t *testing.T) {
	resolver, err := text.NewResolver(text.Config{GUIFont: "Menlo:h14", Scale: 1})
	if err != nil {
		t.Fatalf("NewResolver(Menlo:h14): %v", err)
	}
	nerdInstalled := false
	for _, font := range text.BuiltinFallbacks() {
		if strings.Contains(strings.ToLower(font.Family), "nerd") {
			if installedFamily(font.Family) {
				nerdInstalled = true
				break
			}
		}
	}

	cases := []struct {
		text     string
		fallback string
	}{
		{text: "世界", fallback: "cjk"},
		{text: "🌍", fallback: "emoji"},
		{text: "\uE0B0", fallback: "nerd"},
		{text: "\uF07C", fallback: "nerd"},
	}
	for _, testCase := range cases {
		t.Run(testCase.text, func(t *testing.T) {
			font := resolver.FontFor(testCase.text, false, false)
			family := strings.ToLower(font.Family)
			if !strings.HasPrefix(family, "menlo") {
				t.Fatalf("FontFor(%q).Family = %q, want Menlo primary first", testCase.text, font.Family)
			}
			if testCase.fallback == "nerd" && nerdInstalled && !fallbackPresent(family, "nerd") {
				t.Fatalf("FontFor(%q).Family = %q, want installed Nerd Font in fallback chain", testCase.text, font.Family)
			}
			if (testCase.fallback != "nerd" || nerdInstalled) && !fallbackPresent(family, testCase.fallback) {
				t.Fatalf("FontFor(%q).Family = %q, want %s fallback in chain", testCase.text, font.Family, testCase.fallback)
			}
			if !utf8.ValidString(font.Family) || !utf8.ValidString(testCase.text) {
				t.Fatalf("FontFor(%q).Family = %q is not valid UTF-8", testCase.text, font.Family)
			}
			if len(resolver.Shape(testCase.text, font)) == 0 {
				t.Fatalf("Shape(%q) returned no glyphs", testCase.text)
			}
			if font.Size <= 0 || font.Weight <= 0 {
				t.Fatalf("FontFor(%q) = %+v, want positive size and weight", testCase.text, font)
			}
			beforeShape := resolver.ShapeCalls()
			if len(resolver.Shape(testCase.text, font)) == 0 || resolver.ShapeCalls() != beforeShape {
				t.Fatalf("Shape(%q) missed cache: ShapeCalls %d -> %d", testCase.text, beforeShape, resolver.ShapeCalls())
			}
		})
	}

	before := resolver.ShapeCalls()
	for repeat := 0; repeat < 3; repeat++ {
		for _, testCase := range cases {
			font := resolver.FontFor(testCase.text, false, false)
			if len(resolver.Shape(testCase.text, font)) == 0 {
				t.Fatalf("repeat %d Shape(%q) returned no glyphs", repeat, testCase.text)
			}
		}
	}
	if after := resolver.ShapeCalls(); after != before {
		t.Fatalf("ShapeCalls grew from %d to %d across repeated clusters, want cache hit", before, after)
	}
}

func fallbackPresent(family, class string) bool {
	switch class {
	case "cjk":
		for _, font := range text.BuiltinFallbacks() {
			if strings.Contains(strings.ToLower(family), strings.ToLower(font.Family)) &&
				classifyFontFamily(font.Family) == "cjk" {
				return true
			}
		}
		return false
	case "emoji":
		for _, font := range text.BuiltinFallbacks() {
			if strings.Contains(strings.ToLower(family), strings.ToLower(font.Family)) &&
				classifyFontFamily(font.Family) == "emoji" {
				return true
			}
		}
		return false
	case "nerd":
		for _, font := range text.BuiltinFallbacks() {
			if strings.Contains(strings.ToLower(family), strings.ToLower(font.Family)) &&
				classifyFontFamily(font.Family) == "nerd" {
				return true
			}
		}
		return false
	default:
		return false
	}
}

func classifyFontFamily(family string) string {
	lower := strings.ToLower(family)
	switch {
	case strings.Contains(lower, "emoji"):
		return "emoji"
	case strings.Contains(lower, "nerd"):
		return "nerd"
	case strings.Contains(lower, "pingfang"), strings.Contains(lower, "hiragino"),
		strings.Contains(lower, "heiti"), strings.Contains(lower, "songti"),
		strings.Contains(lower, "cjk"), strings.Contains(lower, "gothic"),
		strings.Contains(lower, "source han"):
		return "cjk"
	default:
		return "unknown"
	}
}

func TestPriorBug_Neovide2786_MonospaceFamilyParsingAndCommaFallback(t *testing.T) {
	cases := []struct {
		name     string
		guifont  string
		contains []string
	}{
		{
			name:     "escaped monospace family",
			guifont:  `PragmataPro\_Mono:h13,monospace:h13`,
			contains: []string{"monospace"},
		},
		{
			name:     "backslash space escaped monospace family",
			guifont:  `PragmataPro\ Mono:h13,monospace:h13`,
			contains: []string{"monospace"},
		},
		{
			name:     "underscore escaped monospace family",
			guifont:  `PragmataPro\_Mono:h13,Menlo:h13`,
			contains: []string{"menlo"},
		},
		{
			name:     "missing primary falls back to the following family",
			guifont:  `Definitely\_Missing\_Prior\_Font:h13,Menlo:h13`,
			contains: []string{"menlo"},
		},
		{
			name:     "missing primary falls back to generic system monospace",
			guifont:  `Definitely\_Missing\_Prior\_Font:h13,monospace:h13`,
			contains: []string{"monospace"},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			fonts, err := text.ParseGUIFont(testCase.guifont)
			if err != nil {
				t.Fatalf("ParseGUIFont(%q): %v", testCase.guifont, err)
			}
			for index, font := range fonts {
				if strings.TrimSpace(font.Family) == "" || font.Size <= 0 {
					t.Fatalf("fonts[%d] = %+v, want a nonempty family and positive size", index, font)
				}
				if strings.Contains(font.Family, "\\") {
					t.Fatalf("fonts[%d].Family = %q, want escape removed", index, font.Family)
				}
			}

			resolver, err := text.NewResolver(text.Config{GUIFont: testCase.guifont, Scale: 1})
			if err != nil {
				t.Fatalf("NewResolver(%q): %v", testCase.guifont, err)
			}
			font := resolver.FontFor("A", false, false)
			family := strings.ToLower(font.Family)
			for _, wanted := range testCase.contains {
				if !strings.Contains(family, strings.ToLower(wanted)) {
					t.Fatalf("FontFor(A).Family = %q, want %q", font.Family, wanted)
				}
			}
			if strings.Contains(family, "definitely") || strings.Contains(family, "missing") {
				t.Fatalf("FontFor(A).Family = %q kept a nonexistent prior family", font.Family)
			}
			if !monospaceCandidate(font.Family) {
				t.Fatalf("FontFor(A).Family = %q, want a legal monospace family", font.Family)
			}
			glyphs := resolver.Shape("A", font)
			if len(glyphs) == 0 {
				t.Fatal("Shape(A) returned no glyphs")
			}
			for index, glyph := range glyphs {
				if glyph.Advance <= 0 {
					t.Fatalf("Shape(A) glyph[%d].Advance = %v, want positive", index, glyph.Advance)
				}
			}
			if cell := resolver.Cell(); cell.Width <= 0 || cell.Height <= 0 {
				t.Fatalf("Cell() = %+v, want positive width and height", cell)
			}
		})
	}
	t.Run("installed non-monospaced primary is preserved", func(t *testing.T) {
		var primary ui.FontFamily
		for _, family := range ui.FontFamilies() {
			if !family.Monospaced && strings.TrimSpace(family.Name) != "" {
				if strings.ContainsAny(family.Name, `,\:"'`) {
					continue
				}
				primary = family
				break
			}
		}
		if primary.Name == "" {
			t.Fatal("no installed non-monospaced family available for regression")
		}
		guifont := strings.ReplaceAll(primary.Name, " ", `\ `)
		resolver, err := text.NewResolver(text.Config{GUIFont: guifont + ":h14", Scale: 1})
		if err != nil {
			t.Fatalf("NewResolver(%q): %v", guifont, err)
		}
		font := resolver.FontFor("A", false, false)
		if !strings.HasPrefix(strings.ToLower(strings.Trim(strings.TrimSpace(font.Family), `"'`)), strings.ToLower(primary.Name)) {
			t.Fatalf("FontFor(A).Family = %q, want installed non-monospaced primary %q", font.Family, primary.Name)
		}
		if primary.Monospaced || !installedFamily(primary.Name) {
			t.Fatalf("primary %+v is monospaced or not installed", primary)
		}
	})
}

func monospaceCandidate(family string) bool {
	for _, part := range strings.Split(family, ",") {
		part = strings.ToLower(strings.Trim(strings.TrimSpace(part), `"'`))
		if part == "monospace" || installedMonospacedFamily(part) ||
			strings.Contains(part, "mono") || strings.Contains(part, "menlo") ||
			strings.Contains(part, "courier") || strings.Contains(part, "code") {
			return true
		}
	}
	return false
}

func installedFamily(family string) bool {
	if strings.TrimSpace(family) == "" {
		return false
	}
	for _, candidate := range ui.FontFamilies() {
		if strings.EqualFold(strings.TrimSpace(candidate.Name), strings.TrimSpace(family)) {
			return true
		}
	}
	return false
}

func installedMonospacedFamily(family string) bool {
	if strings.TrimSpace(family) == "" || strings.EqualFold(family, "monospace") {
		return false
	}
	for _, candidate := range ui.FontFamilies() {
		if candidate.Monospaced && strings.EqualFold(strings.TrimSpace(candidate.Name), strings.TrimSpace(family)) {
			return true
		}
	}
	return false
}

func TestPriorBug_Neovide3431_EmptyAndDefaultGUIFontNoStartupError(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		config text.Config
	}{
		{name: "empty guifont", config: text.Config{GUIFont: "", Scale: 1}},
		{name: "zero-value config", config: text.Config{}},
		{
			name: "Neovim 0.12.6 default startup",
			config: text.Config{
				GUIFont: "SF Mono,Menlo,Monaco,Courier New,monospace",
				Scale:   1,
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			resolver, err := text.NewResolver(testCase.config)
			if err != nil {
				t.Fatalf("NewResolver(%+v): %v", testCase.config, err)
			}
			cell := resolver.Cell()
			if cell.Width <= 0 || cell.Height <= 0 {
				t.Fatalf("Cell() = %+v, want positive width and height", cell)
			}
			font := resolver.FontFor("M", false, false)
			if strings.TrimSpace(font.Family) == "" || font.Size <= 0 {
				t.Fatalf("FontFor(M) = %+v, want nonempty family and positive size", font)
			}
			if len(resolver.Shape("M", font)) == 0 {
				t.Fatal("Shape(M) returned no glyphs")
			}
		})
	}
}
