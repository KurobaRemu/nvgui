package text

import (
	"math"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestParseGUIFontPreservesUnknownOptions(t *testing.T) {
	fonts, err := ParseGUIFont(`"Hack Nerd Font":h13:W600:italic:ss01,SFMono\\ Regular:h13:bold`)
	if err != nil {
		t.Fatalf("ParseGUIFont: %v", err)
	}
	if len(fonts) != 2 {
		t.Fatalf("len(fonts) = %d, want 2", len(fonts))
	}
	if !strings.Contains(fonts[0].Features, "ss01") || fonts[0].Weight != 600 || !fonts[0].Italic {
		t.Errorf("font[0] = %+v, want preserved ss01, W600, italic", fonts[0])
	}
	if fonts[1].Weight < 700 {
		t.Errorf("font[1] = %+v, want bold weight", fonts[1])
	}
}

func TestResolverCacheInvalidatesOnFontAndScaleChanges(t *testing.T) {
	resolver, err := NewResolver(Config{GUIFont: "Menlo:h14", Scale: 1})
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	font := resolver.FontFor("hello", false, false)
	_ = resolver.Shape("hello", font)
	if got := resolver.ShapeCalls(); got != 1 {
		t.Fatalf("ShapeCalls = %d, want 1", got)
	}
	bold := resolver.FontFor("hello", true, false)
	_ = resolver.Shape("hello", bold)
	if got := resolver.ShapeCalls(); got != 2 {
		t.Fatalf("ShapeCalls after bold font = %d, want 2", got)
	}
	_ = resolver.Shape("hello", font)
	if got := resolver.ShapeCalls(); got != 2 {
		t.Fatalf("ShapeCalls after cached font = %d, want 2", got)
	}

	impl := resolver.impl
	impl.mu.Lock()
	impl.cfg.Scale = 2
	impl.mu.Unlock()
	if got := resolver.ShapeCalls(); got != 0 {
		t.Fatalf("ShapeCalls after scale change = %d, want 0", got)
	}
	_ = resolver.Shape("hello", font)
	if got := resolver.ShapeCalls(); got != 1 {
		t.Fatalf("ShapeCalls after scale reshape = %d, want 1", got)
	}
}

func TestShapeCacheIsBounded(t *testing.T) {
	resolver, err := NewResolver(Config{GUIFont: "Menlo:h14", Scale: 1})
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	font := resolver.FontFor("hello", false, false)
	for index := 0; index < shapeCacheCapacity+100; index++ {
		_ = resolver.Shape(strconv.Itoa(index), font)
	}
	impl := resolver.impl
	impl.mu.Lock()
	defer impl.mu.Unlock()
	if got := len(impl.shaped.entries); got > shapeCacheCapacity {
		t.Fatalf("cache entries = %d, want <= %d", got, shapeCacheCapacity)
	}
}

func TestFontForFallbackOrder(t *testing.T) {
	resolver, err := NewResolver(Config{GUIFont: "Menlo:h14", Scale: 1})
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if runtime.GOOS != "darwin" {
		t.Skip("macOS fallback families are platform-specific")
	}

	cases := []struct {
		name     string
		text     string
		contains []string
		excludes []string
	}{
		{name: "ascii", text: "hello", contains: []string{"menlo"}, excludes: []string{"emoji", "pingfang"}},
		{name: "cjk", text: "世界", contains: []string{"menlo", "pingfang"}, excludes: []string{"emoji", "nerd"}},
		{name: "emoji", text: "🌍", contains: []string{"menlo", "emoji"}, excludes: []string{"pingfang", "nerd"}},
		{name: "zwj", text: "👨‍👩‍👧‍👦", contains: []string{"menlo", "emoji"}},
		{name: "combining", text: "e\u0301", contains: []string{"menlo", "emoji"}},
		{name: "cjk+emoji", text: "世界🌍", contains: []string{"menlo", "pingfang", "emoji"}, excludes: []string{"nerd"}},
		{name: "combining+cjk", text: "e\u0301世界", contains: []string{"menlo", "pingfang", "emoji"}, excludes: []string{"nerd"}},
		{name: "box", text: "─", contains: []string{"menlo"}, excludes: []string{"emoji", "pingfang", "nerd"}},
		{name: "pua", text: "\uE0B0", contains: puaContains(resolver), excludes: []string{"emoji", "pingfang"}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			family := strings.ToLower(resolver.FontFor(testCase.text, false, false).Family)
			for _, want := range testCase.contains {
				if !strings.Contains(family, want) {
					t.Errorf("FontFor(%q).Family = %q, want substring %q", testCase.text, family, want)
				}
			}
			for _, unwanted := range testCase.excludes {
				if strings.Contains(family, unwanted) {
					t.Errorf("FontFor(%q).Family = %q, want no substring %q", testCase.text, family, unwanted)
				}
			}
			if !strings.HasPrefix(family, "menlo") {
				t.Errorf("FontFor(%q).Family = %q, want primary family first", testCase.text, family)
			}
		})
	}
}

func TestParseGUIFontEscapesAndQuotes(t *testing.T) {
	fonts, err := ParseGUIFont(`Family\_Name:h14,Family\:With\:Colon:h14,Family\,Comma:h14,Back\\Slash:h14`)
	if err != nil {
		t.Fatalf("ParseGUIFont escaped families: %v", err)
	}
	wantFamilies := []string{"Family Name", "Family:With:Colon", "Family,Comma", `Back\Slash`}
	if len(fonts) != len(wantFamilies) {
		t.Fatalf("len(fonts) = %d, want %d", len(fonts), len(wantFamilies))
	}
	for index, want := range wantFamilies {
		if fonts[index].Family != want {
			t.Errorf("fonts[%d].Family = %q, want %q", index, fonts[index].Family, want)
		}
		if fonts[index].Size != 14 {
			t.Errorf("fonts[%d].Size = %v, want 14", index, fonts[index].Size)
		}
	}

	quoted, err := ParseGUIFont(`"Family:With:Colon":h14`)
	if err != nil {
		t.Fatalf("ParseGUIFont quoted family: %v", err)
	}
	if len(quoted) != 1 || quoted[0].Family != "Family:With:Colon" {
		t.Fatalf("quoted fonts = %+v, want one font Family=Family:With:Colon", quoted)
	}
}

func TestParseGUIFontRejectsInvalidOptions(t *testing.T) {
	invalid := []string{
		"Menlo:h-1",
		"Menlo:h0",
		"Menlo:hNaN",
		"Menlo:hInf",
		"Menlo:h+Inf",
		"Menlo:hnan",
		"Menlo:W-1",
		"Menlo:W50",
		"Menlo:W1000",
		"Menlo:WNaN",
		"Menlo:WInf",
		"Menlo:winf",
		"Menlo:h14:h15",
		"Menlo:H14:h15",
		"Menlo:W600:W700",
		"Menlo:w600:W700",
		"\"Menlo:h14",
		"Menlo:h14\\",
	}
	for _, value := range invalid {
		if _, err := ParseGUIFont(value); err == nil {
			t.Errorf("ParseGUIFont(%q) = nil error, want an explicit error", value)
		}
	}

	fonts, err := ParseGUIFont("Menlo:h14:hist:hlig")
	if err != nil {
		t.Fatalf("ParseGUIFont OpenType tags: %v", err)
	}
	if len(fonts) != 1 {
		t.Fatalf("len(fonts) = %d, want 1", len(fonts))
	}
	if fonts[0].Size != 14 || fonts[0].Weight != defaultFontWeight {
		t.Errorf("font = %+v, want h14 and default weight", fonts[0])
	}
	if !containsFeature(fonts[0].Features, "hist") || !containsFeature(fonts[0].Features, "hlig") {
		t.Errorf("font.Features = %q, want hist and hlig preserved", fonts[0].Features)
	}
}

func TestNewResolverScaleValidation(t *testing.T) {
	invalidScales := []float32{-1, float32(math.NaN()), float32(math.Inf(1)), float32(math.Inf(-1))}
	for _, scale := range invalidScales {
		if _, err := NewResolver(Config{GUIFont: "Menlo:h14", Scale: scale}); err == nil {
			t.Errorf("NewResolver(Scale=%v) = nil error, want an explicit error", scale)
		}
	}

	resolver, err := NewResolver(Config{GUIFont: "Menlo:h14", Scale: 0})
	if err != nil {
		t.Fatalf("NewResolver(Scale=0): %v", err)
	}
	if got := resolver.Config().Scale; got != 1 {
		t.Errorf("Config().Scale = %v, want 1", got)
	}
}

func TestResolverShapeNormalizesFontKey(t *testing.T) {
	resolver, err := NewResolver(Config{GUIFont: "Menlo:h14", Scale: 1})
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	invalidSizes := []float32{0, -14, float32(math.NaN()), float32(math.Inf(1))}
	for index, size := range invalidSizes {
		textValue := "normalize-" + strconv.Itoa(index)
		font := resolver.FontFor(textValue, false, false)
		font.Size = size
		font.Weight = 0
		_ = resolver.Shape(textValue, font)
		_ = resolver.Shape(textValue, font)
		if got, want := resolver.ShapeCalls(), index+1; got != want {
			t.Fatalf("ShapeCalls after %q = %d, want %d", textValue, got, want)
		}

		impl := resolver.impl
		impl.mu.Lock()
		key := impl.makeShapeKey(textValue, font)
		primarySize := impl.primary[0].Size
		impl.mu.Unlock()
		if !isFinite(key.font.Size) || key.font.Size != primarySize {
			t.Errorf("shape key size = %v, want primary size %v", key.font.Size, primarySize)
		}
		if key.font.Weight != defaultFontWeight {
			t.Errorf("shape key weight = %d, want %d", key.font.Weight, defaultFontWeight)
		}
	}

	impl := resolver.impl
	impl.mu.Lock()
	font := Font{Family: "Menlo", Size: 14, Weight: 1200}
	key := impl.makeShapeKey("weight", font)
	impl.mu.Unlock()
	if key.font.Weight != 900 {
		t.Errorf("shape key weight = %d, want 900", key.font.Weight)
	}
}

func TestShapeAndCellUseDIPsAtScaleTwo(t *testing.T) {
	resolver, err := NewResolver(Config{GUIFont: "Menlo:h14", Scale: 2})
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	font := resolver.FontFor("M", false, false)
	glyphs := resolver.Shape("M", font)
	if len(glyphs) == 0 {
		t.Fatal(`Shape("M") returned no glyphs`)
	}
	var maxAdvance float32
	for _, glyph := range glyphs {
		if glyph.Advance > maxAdvance {
			maxAdvance = glyph.Advance
		}
	}
	if maxAdvance <= 0 || !isFinite(maxAdvance) {
		t.Fatalf(`max Shape("M") advance = %v, want a positive finite DIP value`, maxAdvance)
	}
	cell := resolver.Cell()
	if cell.Width <= 0 || !isFinite(cell.Width) {
		t.Fatalf("Cell().Width = %v, want a positive finite DIP value", cell.Width)
	}
	if difference := math.Abs(float64(cell.Width - maxAdvance)); difference > 0.51 {
		t.Errorf("Cell().Width = %v, max Advance = %v, difference = %v, want DIPs to stay aligned", cell.Width, maxAdvance, difference)
	}
	if cell.Width < maxAdvance/2 || cell.Width > maxAdvance*2 {
		t.Errorf("Cell().Width = %v, max Advance = %v, want the same DIP magnitude", cell.Width, maxAdvance)
	}
}

func TestShapeReturnedGlyphsDoNotPolluteCache(t *testing.T) {
	resolver, err := NewResolver(Config{GUIFont: "Menlo:h14", Scale: 1})
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	font := resolver.FontFor("cache", false, false)
	first := resolver.Shape("cache", font)
	if len(first) == 0 {
		t.Fatal(`Shape("cache") returned no glyphs`)
	}
	original := append([]ui.Glyph(nil), first...)
	first[0].Advance += 123
	first[0].X += 456
	first[0].Cluster += 789
	second := resolver.Shape("cache", font)
	if len(second) != len(original) {
		t.Fatalf("cached Shape len = %d, want %d", len(second), len(original))
	}
	for index := range original {
		if second[index].Advance != original[index].Advance ||
			second[index].X != original[index].X ||
			second[index].Cluster != original[index].Cluster ||
			second[index].Runes != original[index].Runes ||
			second[index].RTL != original[index].RTL {
			t.Fatalf("cached glyph[%d] = %+v, want %+v", index, second[index], original[index])
		}
	}
	if got := resolver.ShapeCalls(); got != 1 {
		t.Fatalf("ShapeCalls = %d, want 1", got)
	}
}

func TestShapeCacheEvictsLeastRecentlyUsed(t *testing.T) {
	resolver, err := NewResolver(Config{GUIFont: "Menlo:h14", Scale: 1})
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	font := resolver.FontFor("lru", false, false)
	impl := resolver.impl
	impl.mu.Lock()
	impl.shaped.clear(impl.cfg.Scale)
	impl.shaped.capacity = 3
	impl.mu.Unlock()

	for _, value := range []string{"a", "b", "c", "a", "d", "c", "e"} {
		_ = resolver.Shape(value, font)
	}

	impl.mu.Lock()
	order := shapeCacheTexts(impl)
	entries := len(impl.shaped.entries)
	impl.mu.Unlock()
	if got, want := strings.Join(order, ","), "e,c,d"; got != want {
		t.Errorf("LRU order = %q, want %q", got, want)
	}
	if entries != 3 {
		t.Errorf("cache entries = %d, want 3", entries)
	}
	if got := resolver.ShapeCalls(); got != 5 {
		t.Errorf("ShapeCalls = %d, want 5", got)
	}
}

func TestResolverConcurrentAccess(t *testing.T) {
	resolver, err := NewResolver(Config{GUIFont: "Menlo:h14", Scale: 2})
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	font := resolver.FontFor("concurrent", false, false)
	var waitGroup sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		waitGroup.Add(1)
		go func(worker int) {
			defer waitGroup.Done()
			for index := 0; index < 200; index++ {
				value := strconv.Itoa((worker + index) % 16)
				_ = resolver.Shape(value, font)
				_ = resolver.ShapeCalls()
				_ = resolver.Cell()
				_ = resolver.FontFor(value, index%2 == 0, false)
				_ = resolver.Config()
				if index%50 == 0 {
					resolver.ClearCache()
				}
			}
		}(worker)
	}
	waitGroup.Wait()
}

func TestFontForKeepsAllFallbackClasses(t *testing.T) {
	resolver, err := NewResolver(Config{GUIFont: "Menlo:h14,Monaco:h14", Scale: 1})
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	impl := resolver.impl
	impl.mu.Lock()
	impl.fallbacks = []fallbackEntry{
		{font: Font{Family: "Test CJK", Size: 14, Weight: 400}, class: fallbackCJK},
		{font: Font{Family: "Test Emoji", Size: 14, Weight: 400}, class: fallbackEmoji},
		{font: Font{Family: "Test Nerd Font", Size: 14, Weight: 400}, class: fallbackNerd},
	}
	impl.mu.Unlock()

	cases := []struct {
		name     string
		text     string
		ordered  []string
		unwanted []string
	}{
		{name: "cjk+emoji", text: "世界🌍", ordered: []string{"menlo", "monaco", "test cjk", "test emoji"}, unwanted: []string{"test nerd font"}},
		{name: "combining+cjk", text: "e\u0301世界", ordered: []string{"menlo", "monaco", "test cjk", "test emoji"}, unwanted: []string{"test nerd font"}},
		{name: "pua", text: "\uE0B0", ordered: []string{"menlo", "monaco", "test nerd font"}, unwanted: []string{"test cjk", "test emoji"}},
		{name: "all", text: "世界🌍\uE0B0", ordered: []string{"menlo", "monaco", "test cjk", "test emoji", "test nerd font"}},
		{name: "box", text: "─\u2588", ordered: []string{"menlo", "monaco"}, unwanted: []string{"test cjk", "test emoji", "test nerd font"}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			family := strings.ToLower(resolver.FontFor(testCase.text, false, false).Family)
			previous := -1
			for _, wanted := range testCase.ordered {
				index := strings.Index(family, wanted)
				if index < 0 {
					t.Fatalf("FontFor(%q).Family = %q, want substring %q", testCase.text, family, wanted)
				}
				if index <= previous {
					t.Errorf("FontFor(%q).Family = %q, want %q after earlier fallbacks", testCase.text, family, wanted)
				}
				previous = index
			}
			for _, unwanted := range testCase.unwanted {
				if strings.Contains(family, unwanted) {
					t.Errorf("FontFor(%q).Family = %q, want no substring %q", testCase.text, family, unwanted)
				}
			}
		})
	}
}

func TestClassifyTextUsesClassBitmask(t *testing.T) {
	cases := []struct {
		text     string
		want     textClass
		unwanted textClass
	}{
		{text: "世界", want: needCJK, unwanted: needEmoji | needNerd},
		{text: "🌍", want: needEmoji, unwanted: needCJK | needNerd},
		{text: "e\u0301世界", want: needCJK | needEmoji, unwanted: needNerd},
		{text: "─\u2588", want: 0, unwanted: needCJK | needEmoji | needNerd},
		{text: "\uE0B0", want: needNerd, unwanted: needCJK | needEmoji},
		{text: "世界🌍\uE0B0", want: needCJK | needEmoji | needNerd, unwanted: 0},
	}
	for _, testCase := range cases {
		got := classifyText(testCase.text)
		if got&testCase.want != testCase.want {
			t.Errorf("classifyText(%q) = %d, want bits %d", testCase.text, got, testCase.want)
		}
		if got&testCase.unwanted != 0 {
			t.Errorf("classifyText(%q) = %d, want no bits %d", testCase.text, got, testCase.unwanted)
		}
	}
}

func TestFontMyGoNormalizesFeatures(t *testing.T) {
	if got := normalizeFeatures("calt=0, "); got != "calt=0" {
		t.Errorf(`normalizeFeatures("calt=0, ") = %q, want "calt=0"`, got)
	}
	font := Font{Family: "Menlo", Size: 14, Weight: 400, Features: "calt=0, , -liga, +ss01"}
	want := "calt=0,liga=0,ss01"
	if runtime.GOOS == "darwin" {
		want = "kern=0," + want
	}
	if got := font.MyGo().Features; got != want {
		t.Errorf("MyGo().Features = %q, want %q", got, want)
	}
	if runtime.GOOS == "darwin" {
		features := font.MyGo().Features
		kern := ""
		for _, feature := range strings.Split(features, ",") {
			tag, _, _ := strings.Cut(feature, "=")
			if strings.TrimSpace(tag) == "kern" {
				kern = strings.TrimSpace(feature)
			}
		}
		if kern != "kern=0" {
			t.Errorf("darwin MyGo().Features = %q, want exactly kern=0", features)
		}
	}

	explicit := Font{Family: "Menlo", Size: 14, Features: "calt=0, kern=1, "}.MyGo()
	if got, want := explicit.Features, "calt=0,kern=1"; got != want {
		t.Errorf("explicit kern MyGo().Features = %q, want %q", got, want)
	}
	if containsFeature(explicit.Features, "kern") && strings.Contains(explicit.Features, "kern=0") {
		t.Errorf("explicit kern MyGo().Features = %q, want no kern=0", explicit.Features)
	}
}

func puaContains(resolver *Resolver) []string {
	contains := []string{"menlo"}
	for _, entry := range resolver.impl.fallbacks {
		if entry.class == fallbackNerd {
			contains = append(contains, strings.ToLower(entry.font.Family))
			break
		}
	}
	return contains
}

func shapeCacheTexts(impl *resolverImpl) []string {
	var texts []string
	for element := impl.shaped.order.Front(); element != nil; element = element.Next() {
		texts = append(texts, element.Value.(*shapeEntry).key.text)
	}
	return texts
}
