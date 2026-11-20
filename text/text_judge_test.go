package text_test

import (
	"math"
	"runtime"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"nvgui/text"
)

func TestTextParseGUIFont(t *testing.T) {
	fonts, err := text.ParseGUIFont("Menlo:h14")
	if err != nil {
		t.Fatalf("ParseGUIFont(Menlo:h14): %v", err)
	}
	if len(fonts) != 1 {
		t.Fatalf("len(fonts) = %d, want 1", len(fonts))
	}
	if fonts[0].Family != "Menlo" || fonts[0].Size != 14 {
		t.Errorf("font[0] = %+v, want Family=Menlo Size=14", fonts[0])
	}

	list, err := text.ParseGUIFont("JetBrains Mono:h13:W600:italic,Menlo:h13:bold")
	if err != nil {
		t.Fatalf("ParseGUIFont fallback list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("len(list) = %d, want 2", len(list))
	}
	if list[0].Family != "JetBrains Mono" || list[0].Size != 13 || list[0].Weight != 600 || !list[0].Italic {
		t.Errorf("list[0] = %+v, want JetBrains Mono h13 W600 italic", list[0])
	}
	if list[1].Family != "Menlo" || list[1].Size != 13 || list[1].Weight < 700 {
		t.Errorf("list[1] = %+v, want Menlo h13 bold (Weight >= 700)", list[1])
	}

	for _, invalid := range []string{"", "   ", ",Menlo:h14", "Menlo:h14,", "Menlo:h14,,Monaco:h14"} {
		if _, err := text.ParseGUIFont(invalid); err == nil {
			t.Errorf("ParseGUIFont(%q) = nil error, want error for empty entry", invalid)
		}
	}

	// Unknown option must either be preserved in Features or return an explicit error (never silently dropped).
	unk, err := text.ParseGUIFont("Menlo:h14:ss01")
	if err == nil {
		if len(unk) != 1 || !strings.Contains(unk[0].Features, "ss01") {
			t.Errorf("ParseGUIFont(\"Menlo:h14:ss01\") silently dropped unknown option: %+v", unk)
		}
	}
}

func TestTextBuiltinFallbacks(t *testing.T) {
	fallbacks := text.BuiltinFallbacks()
	if len(fallbacks) < 2 {
		t.Fatalf("len(BuiltinFallbacks()) = %d, want >= 2 (CJK + emoji)", len(fallbacks))
	}
	cjkIdx, emojiIdx := -1, -1
	for i, f := range fallbacks {
		if strings.TrimSpace(f.Family) == "" {
			t.Errorf("BuiltinFallbacks()[%d] has empty Family", i)
		}
		lower := strings.ToLower(f.Family)
		if emojiIdx < 0 && strings.Contains(lower, "emoji") {
			emojiIdx = i
		}
		if cjkIdx < 0 && (strings.Contains(lower, "pingfang") || strings.Contains(lower, "hiragino") ||
			strings.Contains(lower, "heiti") || strings.Contains(lower, "songti") || strings.Contains(lower, "cjk")) {
			cjkIdx = i
		}
	}
	if runtime.GOOS == "darwin" {
		if cjkIdx < 0 {
			t.Errorf("darwin BuiltinFallbacks() missing macOS CJK family: %+v", fallbacks)
		}
		if emojiIdx < 0 {
			t.Errorf("darwin BuiltinFallbacks() missing macOS Emoji family: %+v", fallbacks)
		}
		if cjkIdx >= 0 && emojiIdx >= 0 && cjkIdx > emojiIdx {
			t.Errorf("darwin BuiltinFallbacks() order invalid: CJK(%d) should precede Emoji(%d)", cjkIdx, emojiIdx)
		}
	}
}

func TestTextFontMyGoDarwinKern(t *testing.T) {
	base := text.Font{Family: "Menlo", Size: 14, Weight: 500, Italic: true}
	mf := base.MyGo()
	if mf.Family != "Menlo" || mf.Size != 14 || mf.Weight != 500 || !mf.Italic {
		t.Errorf("MyGo() = %+v, want Family=Menlo Size=14 Weight=500 Italic=true", mf)
	}
	if runtime.GOOS == "darwin" {
		if !hasFeatureToken(mf.Features, "kern=0") {
			t.Errorf("darwin MyGo().Features = %q, want kern=0 prepended", mf.Features)
		}
		withOther := text.Font{Family: "Menlo", Size: 14, Features: "calt=0"}.MyGo()
		if !hasFeatureToken(withOther.Features, "kern=0") || !hasFeatureToken(withOther.Features, "calt=0") {
			t.Errorf("darwin MyGo() with calt=0 = %q, want both kern=0 and calt=0", withOther.Features)
		}
		withExplicitKern := text.Font{Family: "Menlo", Size: 14, Features: "kern=1"}.MyGo()
		if hasFeatureToken(withExplicitKern.Features, "kern=0") || !hasFeatureToken(withExplicitKern.Features, "kern=1") {
			t.Errorf("darwin MyGo() with explicit kern=1 = %q, must preserve kern=1 without kern=0", withExplicitKern.Features)
		}
	}
}

func hasFeatureToken(features, want string) bool {
	for _, tok := range strings.Split(features, ",") {
		if strings.TrimSpace(tok) == want {
			return true
		}
	}
	return false
}

func TestTextResolverCellAndFontFor(t *testing.T) {
	cfg := text.Config{GUIFont: "Menlo:h14", Scale: 2}
	r, err := text.NewResolver(cfg)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if r == nil {
		t.Fatal("NewResolver returned nil resolver with nil error")
	}
	if got := r.Config(); got != cfg {
		t.Errorf("Config() = %+v, want %+v", got, cfg)
	}

	cell := r.Cell()
	if cell.Width <= 0 || cell.Height <= 0 || math.IsNaN(float64(cell.Width)) || math.IsNaN(float64(cell.Height)) {
		t.Fatalf("Cell() = %+v, want positive finite dimensions", cell)
	}
	if cell.Width > 100 || cell.Height > 100 {
		t.Errorf("Cell() = %+v looks unreasonable for 14pt Menlo", cell)
	}

	refGlyphs := ui.Shape("M", text.Font{Family: "Menlo", Size: 14}.MyGo())
	if len(refGlyphs) > 0 {
		var maxAdv float32
		for _, g := range refGlyphs {
			if g.Advance > maxAdv {
				maxAdv = g.Advance
			}
		}
		if maxAdv > 0 && (cell.Width < maxAdv-0.5 || cell.Width > maxAdv+3.0) {
			t.Errorf("Cell().Width = %v, expected near ui.Shape(\"M\") advance %v", cell.Width, maxAdv)
		}
	}

	ascii := r.FontFor("hello", false, false)
	if !strings.Contains(ascii.Family, "Menlo") {
		t.Errorf("FontFor(ASCII).Family = %q, want primary font Menlo", ascii.Family)
	}
	boldItalic := r.FontFor("hello", true, true)
	if boldItalic.Weight < 700 || !boldItalic.Italic {
		t.Errorf("FontFor(bold, italic) = %+v, want Weight>=700 and Italic=true", boldItalic)
	}
	cjk := r.FontFor("世界", false, false)
	if !strings.Contains(cjk.Family, "Menlo") || cjk.Family == "" {
		t.Errorf("FontFor(CJK).Family = %q, must include primary fallback chain", cjk.Family)
	}
	emoji := r.FontFor("🌍", false, false)
	if !strings.Contains(emoji.Family, "Menlo") || emoji.Family == "" {
		t.Errorf("FontFor(emoji).Family = %q, must include primary fallback chain", emoji.Family)
	}
	if runtime.GOOS == "darwin" && !strings.Contains(strings.ToLower(emoji.Family), "emoji") {
		t.Errorf("darwin FontFor(emoji).Family = %q, want emoji fallback family included", emoji.Family)
	}
	zwj := r.FontFor("👨‍👩‍👧‍👦", false, false)
	if !strings.Contains(zwj.Family, "Menlo") {
		t.Errorf("FontFor(ZWJ).Family = %q, must include primary fallback", zwj.Family)
	}
	comb := r.FontFor("e\u0301", false, false)
	if !strings.Contains(comb.Family, "Menlo") {
		t.Errorf("FontFor(combining).Family = %q, must include primary fallback", comb.Family)
	}
}

func TestTextShapeCacheAndSingleEmojiStress(t *testing.T) {
	r, err := text.NewResolver(text.Config{GUIFont: "Menlo:h14", Scale: 2})
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if r == nil {
		t.Fatal("NewResolver returned nil")
	}

	r.ClearCache()
	if got := r.ShapeCalls(); got != 0 {
		t.Fatalf("ShapeCalls after ClearCache = %d, want 0", got)
	}

	fNormal := r.FontFor("hello", false, false)
	g1 := r.Shape("hello world", fNormal)
	if len(g1) == 0 {
		t.Fatal("Shape(\"hello world\") returned empty glyphs")
	}
	if got := r.ShapeCalls(); got != 1 {
		t.Fatalf("ShapeCalls after 1st shape = %d, want 1", got)
	}
	for i := 0; i < 100; i++ {
		g2 := r.Shape("hello world", fNormal)
		if len(g2) != len(g1) {
			t.Fatalf("cached Shape len = %d, want %d", len(g2), len(g1))
		}
	}
	if got := r.ShapeCalls(); got != 1 {
		t.Fatalf("ShapeCalls after 100 cached repeats = %d, want 1", got)
	}

	_ = r.Shape("different text", fNormal)
	if got := r.ShapeCalls(); got != 2 {
		t.Fatalf("ShapeCalls after new text = %d, want 2", got)
	}

	fBold := r.FontFor("hello", true, false)
	_ = r.Shape("hello world", fBold)
	if got := r.ShapeCalls(); got != 3 {
		t.Fatalf("ShapeCalls after bold variant = %d, want 3", got)
	}

	r.ClearCache()
	if got := r.ShapeCalls(); got != 0 {
		t.Fatalf("ShapeCalls after second ClearCache = %d, want 0", got)
	}

	// Single emoji repeated 100,000 times must not grow cache linearly or re-shape repeatedly.
	fEmoji := r.FontFor("🌍", false, false)
	for i := 0; i < 100_000; i++ {
		glyphs := r.Shape("🌍", fEmoji)
		if i == 0 && len(glyphs) == 0 {
			t.Fatal("Shape(\"🌍\") returned empty glyphs")
		}
	}
	if got := r.ShapeCalls(); got != 1 {
		t.Fatalf("ShapeCalls after 100,000 repeated single-emoji shapes = %d, want 1", got)
	}
}
