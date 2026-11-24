package text

import (
	"container/list"
	"fmt"
	"math"
	"strings"
	"sync"

	"github.com/egoist/mygo/ui"
)

const (
	shapeCacheCapacity         = 4096
	minScaleFactor     float32 = 0.5
	maxScaleFactor     float32 = 4.0
	defaultScaleFactor float32 = 1.0
)

type shapeKey struct {
	text        string
	font        Font
	scale       float32
	scaleFactor float32
	bold        bool
	italic      bool
}

type shapeEntry struct {
	key    shapeKey
	glyphs []ui.Glyph
}

type shapeCache struct {
	capacity    int
	scale       float32
	scaleFactor float32
	calls       int
	entries     map[shapeKey]*list.Element
	order       *list.List
}

type resolverImpl struct {
	mu              sync.Mutex
	cfg             Config
	primary         []Font
	fallbacks       []fallbackEntry
	familyIdx       fontFamilyIndex
	cell            CellSize
	cellValid       bool
	cellScale       float32
	cellScaleFactor float32
	shaped          *shapeCache
}

func newResolver(cfg Config) (*Resolver, error) {
	if !isFinite(cfg.Scale) || cfg.Scale < 0 {
		return nil, fmt.Errorf("text: invalid scale %v", cfg.Scale)
	}
	if cfg.Scale == 0 {
		cfg.Scale = 1
	}
	if cfg.ScaleFactor != 0 {
		cfg.ScaleFactor = normalizeScaleFactor(cfg.ScaleFactor)
	}
	index := newFontFamilyIndex(ui.FontFamilies())
	primary, err := parseUserFonts(cfg.GUIFont, index)
	if err != nil {
		return nil, err
	}
	impl := &resolverImpl{
		cfg:             cfg,
		primary:         primary,
		fallbacks:       builtinFallbackEntriesFromIndex(index),
		familyIdx:       index,
		cellScale:       cfg.Scale,
		cellScaleFactor: normalizeScaleFactor(cfg.ScaleFactor),
		shaped:          newShapeCache(cfg.Scale, normalizeScaleFactor(cfg.ScaleFactor)),
	}
	return &Resolver{impl: impl}, nil
}

func resolverConfig(resolver *Resolver) Config {
	impl := resolver.impl
	impl.mu.Lock()
	defer impl.mu.Unlock()
	return impl.cfg
}

func resolverCell(resolver *Resolver) CellSize {
	impl := resolver.impl
	impl.mu.Lock()
	defer impl.mu.Unlock()
	impl.refreshScaleLocked()
	if !impl.cellValid {
		impl.cell = impl.calculateCellLocked()
		impl.cellValid = true
	}
	return impl.cell
}

func resolverFontFor(resolver *Resolver, textValue string, bold, italic bool) Font {
	impl := resolver.impl
	impl.mu.Lock()
	defer impl.mu.Unlock()
	impl.refreshScaleLocked()

	ordered := impl.orderedFonts(classifyText(textValue))
	variant := impl.primary[0]
	variant.Size = scaledFontSize(variant.Size, impl.cfg.ScaleFactor)
	if variant.Weight <= 0 {
		variant.Weight = defaultFontWeight
	}
	if bold {
		variant.Weight = min(max(variant.Weight+300, 700), 900)
	}
	variant.Italic = italic || variant.Italic
	variant.Family = familyChain(ordered)
	return variant
}

func resolverShape(resolver *Resolver, textValue string, font Font) []ui.Glyph {
	impl := resolver.impl
	impl.mu.Lock()
	defer impl.mu.Unlock()
	impl.refreshScaleLocked()

	key := impl.makeShapeKey(textValue, font)
	if cached, ok := impl.shaped.get(key); ok {
		return cached
	}

	glyphs := ui.Shape(textValue, fontToMyGo(key.font))
	impl.shaped.calls++
	impl.shaped.put(key, glyphs)
	return cloneGlyphs(glyphs)
}

func (impl *resolverImpl) makeShapeKey(textValue string, font Font) shapeKey {
	font = impl.normalizedFont(font)
	return shapeKey{
		text:        textValue,
		font:        font,
		scale:       impl.cfg.Scale,
		scaleFactor: normalizeScaleFactor(impl.cfg.ScaleFactor),
		bold:        font.Weight >= 700,
		italic:      font.Italic,
	}
}

func (impl *resolverImpl) scaledPrimarySize() float32 {
	return scaledFontSize(impl.primary[0].Size, impl.cfg.ScaleFactor)
}

func (impl *resolverImpl) normalizedFont(font Font) Font {
	if !isFinite(font.Size) || font.Size <= 0 {
		font.Size = impl.scaledPrimarySize()
	}
	if font.Weight <= 0 {
		font.Weight = defaultFontWeight
	}
	font.Weight = min(max(font.Weight, 100), 900)
	font.Features = normalizeFeatures(font.Features)
	return font
}

func resolverShapeCalls(resolver *Resolver) int {
	impl := resolver.impl
	impl.mu.Lock()
	defer impl.mu.Unlock()
	impl.refreshScaleLocked()
	return impl.shaped.calls
}

func resolverClearCache(resolver *Resolver) {
	impl := resolver.impl
	impl.mu.Lock()
	defer impl.mu.Unlock()
	factor := normalizeScaleFactor(impl.cfg.ScaleFactor)
	impl.shaped.clearFor(impl.cfg.Scale, factor)
	impl.cellValid = false
	impl.cellScale = impl.cfg.Scale
	impl.cellScaleFactor = factor
}

func parseUserFonts(value string, index fontFamilyIndex) ([]Font, error) {
	if strings.TrimSpace(value) == "" {
		return []Font{{
			Family: genericMonospaceFamily,
			Size:   defaultFontSize,
			Weight: defaultFontWeight,
		}}, nil
	}
	return normalizePrimaryFonts(value, index)
}

func builtinFallbackEntriesFromIndex(index fontFamilyIndex) []fallbackEntry {
	cjkFonts := installedCJKFonts(index)
	emojiFonts := installedEmojiFonts(index)
	nerdFonts := installedNerdFonts(index.families)
	entries := make([]fallbackEntry, 0, len(cjkFonts)+len(emojiFonts)+len(nerdFonts))
	for _, font := range cjkFonts {
		entries = append(entries, fallbackEntry{font: font, class: fallbackCJK})
	}
	for _, font := range emojiFonts {
		entries = append(entries, fallbackEntry{font: font, class: fallbackEmoji})
	}
	for _, font := range nerdFonts {
		entries = append(entries, fallbackEntry{font: font, class: fallbackNerd})
	}
	return entries
}

func (impl *resolverImpl) refreshScaleLocked() {
	rawScale := impl.cfg.Scale
	rawFactor := normalizeScaleFactor(impl.cfg.ScaleFactor)
	if impl.shaped.scale == rawScale && impl.shaped.scaleFactor == rawFactor &&
		impl.cellScale == rawScale && impl.cellScaleFactor == rawFactor {
		return
	}
	impl.shaped.clearFor(rawScale, rawFactor)
	impl.cellValid = false
	impl.cellScale = rawScale
	impl.cellScaleFactor = rawFactor
}

func (impl *resolverImpl) calculateCellLocked() CellSize {
	base := impl.firstMonospacedFont()
	base.Size = scaledFontSize(base.Size, impl.cfg.ScaleFactor)
	font := impl.normalizedFont(base)
	var maxAdvance float32
	for _, glyph := range ui.Shape("M", fontToMyGo(font)) {
		if glyph.Advance > maxAdvance {
			maxAdvance = glyph.Advance
		}
	}
	metrics := fontToMyGo(font).Metrics()
	width := maxAdvance
	baseHeight := metrics.Ascent + metrics.Descent + metrics.LineGap
	if !isFinite(width) || width <= 0 {
		width = font.Size * 0.6
	}
	if !isFinite(baseHeight) || baseHeight <= 0 {
		baseHeight = font.Size * 1.25
	}
	lineHeight := impl.cfg.LineHeight
	if !isFinite(lineHeight) || lineHeight <= 0 {
		lineHeight = defaultLineHeight
	}
	lineSpace := impl.cfg.LineSpace
	if !isFinite(lineSpace) {
		lineSpace = 0
	}
	height := baseHeight*lineHeight + lineSpace
	minHeight := max(float32(1), baseHeight*0.5)
	if !isFinite(height) || height < minHeight {
		height = minHeight
	}
	if scale := effectiveScale(impl.cfg.Scale); scale > 0 {
		width = float32(math.Ceil(float64(width*scale)-0.05)) / scale
	}
	return CellSize{Width: width, Height: height}
}

func (impl *resolverImpl) firstMonospacedFont() Font {
	for _, font := range impl.primary {
		family := strings.TrimSpace(font.Family)
		if family == "" || !isMonospacedFamily(family, impl.familyIdx) {
			continue
		}
		candidate := font
		candidate.Family = family
		return candidate
	}
	if len(impl.primary) > 0 {
		return impl.primary[0]
	}
	return Font{Family: genericMonospaceFamily, Size: defaultFontSize, Weight: defaultFontWeight}
}

func (impl *resolverImpl) orderedFonts(classes textClass) []Font {
	wanted := []struct {
		flag  textClass
		class fallbackClass
	}{
		{needCJK, fallbackCJK},
		{needEmoji, fallbackEmoji},
		{needNerd, fallbackNerd},
	}
	fonts := append([]Font(nil), impl.primary...)
	for _, candidate := range wanted {
		if classes&candidate.flag == 0 {
			continue
		}
		for _, entry := range impl.fallbacks {
			if entry.class == candidate.class {
				fonts = append(fonts, entry.font)
			}
		}
	}
	return fonts
}

func newShapeCache(scale, scaleFactor float32) *shapeCache {
	cache := &shapeCache{
		capacity: shapeCacheCapacity,
		order:    list.New(),
	}
	cache.clearFor(scale, scaleFactor)
	return cache
}

func (cache *shapeCache) get(key shapeKey) ([]ui.Glyph, bool) {
	element, ok := cache.entries[key]
	if !ok {
		return nil, false
	}
	cache.order.MoveToFront(element)
	entry := element.Value.(*shapeEntry)
	return cloneGlyphs(entry.glyphs), true
}

func (cache *shapeCache) put(key shapeKey, glyphs []ui.Glyph) {
	if element, ok := cache.entries[key]; ok {
		cache.order.MoveToFront(element)
		element.Value.(*shapeEntry).glyphs = cloneGlyphs(glyphs)
		return
	}
	element := cache.order.PushFront(&shapeEntry{key: key, glyphs: cloneGlyphs(glyphs)})
	cache.entries[key] = element
	for len(cache.entries) > cache.capacity {
		oldest := cache.order.Back()
		if oldest == nil {
			return
		}
		cache.order.Remove(oldest)
		entry := oldest.Value.(*shapeEntry)
		delete(cache.entries, entry.key)
	}
}

func (cache *shapeCache) clear(scale float32) {
	cache.clearFor(scale, cache.scaleFactor)
}

func (cache *shapeCache) clearFor(scale, scaleFactor float32) {
	cache.scale = scale
	cache.scaleFactor = scaleFactor
	cache.calls = 0
	cache.entries = make(map[shapeKey]*list.Element, cache.capacity)
	cache.order.Init()
}

func cloneGlyphs(glyphs []ui.Glyph) []ui.Glyph {
	cloned := make([]ui.Glyph, len(glyphs))
	copy(cloned, glyphs)
	return cloned
}

func effectiveScale(scale float32) float32 {
	if !isFinite(scale) || scale <= 0 {
		return 1
	}
	return scale
}

func normalizeScaleFactor(factor float32) float32 {
	if !isFinite(factor) || factor <= 0 {
		return defaultScaleFactor
	}
	if factor < minScaleFactor {
		return minScaleFactor
	}
	if factor > maxScaleFactor {
		return maxScaleFactor
	}
	return factor
}

func scaledFontSize(baseSize, factor float32) float32 {
	if !isFinite(baseSize) || baseSize <= 0 {
		baseSize = defaultFontSize
	}
	return baseSize * normalizeScaleFactor(factor)
}

func isFinite(value float32) bool {
	floatValue := float64(value)
	return !math.IsNaN(floatValue) && !math.IsInf(floatValue, 0)
}
