package render

import (
	"container/list"
	"fmt"
	"hash/maphash"
	"math"
	"slices"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"

	"nvgui/drawlist"
	"nvgui/renderlist"
	"nvgui/text"
)

type Planner struct {
	resolver *text.Resolver
}

func NewPlanner(resolver *text.Resolver) *Planner {
	return &Planner{resolver: resolver}
}

func (p *Planner) Plan(list *drawlist.List) (renderlist.Frame, error) {
	if list == nil {
		return renderlist.Frame{}, fmt.Errorf("render: nil Draw List")
	}
	if p == nil || p.resolver == nil {
		return renderlist.Frame{}, fmt.Errorf("render: nil font resolver")
	}
	cell := p.resolver.Cell()
	if cell.Width <= 0 || cell.Height <= 0 {
		return renderlist.Frame{}, fmt.Errorf("render: invalid cell size %+v", cell)
	}

	frame := renderlist.Frame{Version: 1, Seq: list.Seq}
	metas := make([]rowMeta, 0)
	for layerIndex, layer := range list.Layers {
		if layer.Clip.W < 0 || layer.Clip.H < 0 {
			return renderlist.Frame{}, fmt.Errorf("render: layer %d has negative clip %+v", layerIndex, layer.Clip)
		}
		clipping := layer.Clip != (drawlist.Rect{}) || layer.Width > 0 || layer.Height > 0 || list.Width > 0 || list.Height > 0
		if clipping && (layer.Clip.W == 0 || layer.Clip.H == 0) {
			continue
		}
		for itemIndex, item := range layer.Items {
			command, ok, err := p.planItem(layer, item, cell)
			if err != nil {
				return renderlist.Frame{}, fmt.Errorf("render: layer %d item %d: %w", layerIndex, itemIndex, err)
			}
			if !ok {
				continue
			}
			if clipping {
				command, ok = clipCommand(command, layer.Clip, cell)
				if !ok {
					continue
				}
			}
			frame.Commands = append(frame.Commands, command)
			metas = append(metas, rowMeta{
				grid:  layer.Grid,
				attrs: joinAttrs(item.Attrs),
				clip:  commandClip(layer.Clip, cell, clipping, command.Op != "fill"),
			})
		}
	}
	storeMetadata(frame, metas)
	return frame, nil
}

func clipCommand(command renderlist.Command, clip drawlist.Rect, cell text.CellSize) (renderlist.Command, bool) {
	commandRect := commandCellRect(command, cell)
	intersection := intersectCellRect(commandRect, clip)
	if intersection.W <= 0 || intersection.H <= 0 {
		return renderlist.Command{}, false
	}
	if command.Op != "fill" {
		return command, true
	}
	command.X = float32(intersection.X) * cell.Width
	command.Y = float32(intersection.Y) * cell.Height
	command.W = float32(intersection.W) * cell.Width
	command.H = float32(intersection.H) * cell.Height
	return command, true
}

func commandCellRect(command renderlist.Command, cell text.CellSize) drawlist.Rect {
	return drawlist.Rect{
		X: int(math.Round(float64(command.X / cell.Width))),
		Y: int(math.Round(float64(command.Y / cell.Height))),
		W: int(math.Round(float64(command.W / cell.Width))),
		H: int(math.Round(float64(command.H / cell.Height))),
	}
}

func commandClip(clip drawlist.Rect, cell text.CellSize, clipping, clipped bool) ui.Rect {
	if !clipping || !clipped {
		return ui.Rect{}
	}
	return ui.Rect{
		X: float32(clip.X) * cell.Width,
		Y: float32(clip.Y) * cell.Height,
		W: float32(clip.W) * cell.Width,
		H: float32(clip.H) * cell.Height,
	}
}

func intersectCellRect(left, right drawlist.Rect) drawlist.Rect {
	x0 := max(left.X, right.X)
	y0 := max(left.Y, right.Y)
	x1 := min(left.X+left.W, right.X+right.W)
	y1 := min(left.Y+left.H, right.Y+right.H)
	if x1 <= x0 || y1 <= y0 {
		return drawlist.Rect{}
	}
	return drawlist.Rect{X: x0, Y: y0, W: x1 - x0, H: y1 - y0}
}

func (p *Planner) planItem(layer drawlist.Layer, item drawlist.Item, cell text.CellSize) (renderlist.Command, bool, error) {
	if item.Width < 0 || item.Height < 0 {
		return renderlist.Command{}, false, fmt.Errorf("negative item size %dx%d", item.Width, item.Height)
	}
	command := renderlist.Command{
		X:    float32(layer.ScreenCol+item.Col) * cell.Width,
		Y:    float32(layer.ScreenRow+item.Row) * cell.Height,
		W:    float32(item.Width) * cell.Width,
		H:    float32(item.Height) * cell.Height,
		FG:   colorValue(item.FG),
		BG:   colorValue(item.BG),
		SP:   colorValue(item.SP),
		Rune: item.Rune,
	}
	switch item.Kind {
	case "fill":
		command.Op = "fill"
	case "cursor":
		command.Op = "cursor"
		if item.Text != "" {
			command.Text = item.Text
			font := p.resolver.FontFor(item.Text, hasAttr(item.Attrs, "bold"), hasAttr(item.Attrs, "italic"))
			command.Family = font.Family
			command.Size = font.Size
			command.Weight = font.Weight
			command.Italic = font.Italic
			command.Features = font.Features
		}
	case "box":
		if item.Rune == "" {
			return renderlist.Command{}, false, fmt.Errorf("box item has no rune")
		}
		command.Op = "box"
	case "text":
		if item.Text == "" {
			return renderlist.Command{}, false, nil
		}
		command.Op = "glyphs"
		command.Text = item.Text
		font := p.resolver.FontFor(item.Text, hasAttr(item.Attrs, "bold"), hasAttr(item.Attrs, "italic"))
		command.Family = font.Family
		command.Size = font.Size
		command.Weight = font.Weight
		command.Italic = font.Italic
		command.Features = font.Features
	default:
		return renderlist.Command{}, false, fmt.Errorf("unknown Draw List kind %q", item.Kind)
	}
	return command, true, nil
}

type rowMeta struct {
	grid  int
	attrs string
	clip  ui.Rect
}

type metadataEntry struct {
	seq      uint64
	commands []renderlist.Command
	metas    []rowMeta
}

const metadataCapacity = 64

var (
	metadataMu    sync.Mutex
	metadataLRU   = list.New()
	metadataIndex = make(map[uint64][]*list.Element)
	metadataSeed  = maphash.MakeSeed()
)

func storeMetadata(frame renderlist.Frame, metas []rowMeta) {
	key := metadataKey(frame.Seq, frame.Commands)
	metadataMu.Lock()
	defer metadataMu.Unlock()
	for index, element := range metadataIndex[key] {
		entry := element.Value.(*metadataEntry)
		if entry.seq != frame.Seq || !slices.Equal(entry.commands, frame.Commands) {
			continue
		}
		metadataLRU.MoveToFront(element)
		entry.metas = append([]rowMeta(nil), metas...)
		bucket := metadataIndex[key]
		copy(bucket[1:index+1], bucket[:index])
		bucket[0] = element
		return
	}
	entry := &metadataEntry{
		seq:      frame.Seq,
		commands: append([]renderlist.Command(nil), frame.Commands...),
		metas:    append([]rowMeta(nil), metas...),
	}
	element := metadataLRU.PushFront(entry)
	metadataIndex[key] = append([]*list.Element{element}, metadataIndex[key]...)
	for metadataLRU.Len() > metadataCapacity {
		oldest := metadataLRU.Back()
		if oldest == nil {
			break
		}
		metadataLRU.Remove(oldest)
		old := oldest.Value.(*metadataEntry)
		oldKey := metadataKey(old.seq, old.commands)
		bucket := metadataIndex[oldKey]
		for index, candidate := range bucket {
			if candidate == oldest {
				bucket = append(bucket[:index], bucket[index+1:]...)
				break
			}
		}
		if len(bucket) == 0 {
			delete(metadataIndex, oldKey)
		} else {
			metadataIndex[oldKey] = bucket
		}
	}
}

func metadataFor(frame renderlist.Frame) []rowMeta {
	key := metadataKey(frame.Seq, frame.Commands)
	metadataMu.Lock()
	defer metadataMu.Unlock()
	for index, element := range metadataIndex[key] {
		entry := element.Value.(*metadataEntry)
		if entry.seq != frame.Seq || !slices.Equal(entry.commands, frame.Commands) {
			continue
		}
		metadataLRU.MoveToFront(element)
		bucket := metadataIndex[key]
		copy(bucket[1:index+1], bucket[:index])
		bucket[0] = element
		return append([]rowMeta(nil), entry.metas...)
	}
	return nil
}

func metadataKey(seq uint64, commands []renderlist.Command) uint64 {
	var scratch [8]byte
	var hash maphash.Hash
	hash.SetSeed(metadataSeed)
	writeUint64(&hash, seq, &scratch)
	for index := range commands {
		writeCommand(&hash, commands[index], &scratch)
	}
	return hash.Sum64()
}

func writeUint64(hash *maphash.Hash, value uint64, scratch *[8]byte) {
	scratch[0] = byte(value)
	scratch[1] = byte(value >> 8)
	scratch[2] = byte(value >> 16)
	scratch[3] = byte(value >> 24)
	scratch[4] = byte(value >> 32)
	scratch[5] = byte(value >> 40)
	scratch[6] = byte(value >> 48)
	scratch[7] = byte(value >> 56)
	_, _ = hash.Write(scratch[:])
}

func writeUint32(hash *maphash.Hash, value uint32, scratch *[8]byte) {
	scratch[0] = byte(value)
	scratch[1] = byte(value >> 8)
	scratch[2] = byte(value >> 16)
	scratch[3] = byte(value >> 24)
	_, _ = hash.Write(scratch[:4])
}

func writeFloat32(hash *maphash.Hash, value float32, scratch *[8]byte) {
	writeUint32(hash, math.Float32bits(value), scratch)
}

func writeText(hash *maphash.Hash, value string, scratch *[8]byte) {
	writeUint64(hash, uint64(len(value)), scratch)
	_, _ = hash.WriteString(value)
}

func writeCommand(hash *maphash.Hash, command renderlist.Command, scratch *[8]byte) {
	writeText(hash, command.Op, scratch)
	writeFloat32(hash, command.X, scratch)
	writeFloat32(hash, command.Y, scratch)
	writeFloat32(hash, command.W, scratch)
	writeFloat32(hash, command.H, scratch)
	writeText(hash, command.Text, scratch)
	writeText(hash, command.Family, scratch)
	writeFloat32(hash, command.Size, scratch)
	writeUint64(hash, uint64(command.Weight), scratch)
	scratch[0] = 0
	if command.Italic {
		scratch[0] = 1
	}
	_, _ = hash.Write(scratch[:1])
	writeText(hash, command.Features, scratch)
	writeUint32(hash, command.FG, scratch)
	writeUint32(hash, command.BG, scratch)
	writeUint32(hash, command.SP, scratch)
	writeText(hash, command.Rune, scratch)
}

func joinAttrs(attrs []string) string {
	return strings.Join(attrs, "\x00")
}

func splitAttrs(value string) []string {
	if value == "" {
		return nil
	}
	return strings.Split(value, "\x00")
}

type viewImpl struct {
	mu    sync.Mutex
	frame renderlist.Frame
	caret ui.Rect
	metas []rowMeta
	cache *rowCache
	cell  text.CellSize
}

type drawState struct {
	cache *rowCache
	metas []rowMeta
}

var (
	drawStatesMu sync.Mutex
	drawStates   = make(map[*ui.Painter][]drawState)
)

func pushDrawState(p *ui.Painter, state drawState) func() {
	drawStatesMu.Lock()
	drawStates[p] = append(drawStates[p], state)
	drawStatesMu.Unlock()
	return func() {
		drawStatesMu.Lock()
		defer drawStatesMu.Unlock()
		stack := drawStates[p]
		if len(stack) <= 1 {
			delete(drawStates, p)
			return
		}
		drawStates[p] = stack[:len(stack)-1]
	}
}

func drawStateFor(p *ui.Painter) (drawState, bool) {
	drawStatesMu.Lock()
	defer drawStatesMu.Unlock()
	stack := drawStates[p]
	if len(stack) == 0 {
		return drawState{}, false
	}
	return stack[len(stack)-1], true
}

type rowKey struct {
	grid     int
	row      int
	text     string
	attrs    string
	fg       uint32
	bg       uint32
	family   string
	size     float32
	weight   int
	italic   bool
	features string
}

const rowCacheCapacity = 4096

type rowCacheEntry struct {
	key    rowKey
	glyphs []ui.Glyph
}

type rowCache struct {
	mu       sync.Mutex
	rows     map[rowKey]*list.Element
	order    *list.List
	capacity int
	shaped   int
}

func newRowCache() *rowCache {
	return &rowCache{
		rows:     make(map[rowKey]*list.Element),
		order:    list.New(),
		capacity: rowCacheCapacity,
	}
}

func (cache *rowCache) glyphs(command renderlist.Command, meta rowMeta) []ui.Glyph {
	key := rowKey{
		grid:     meta.grid,
		row:      int(math.Round(float64(command.Y / positiveHeight(command.H)))),
		text:     command.Text,
		attrs:    meta.attrs,
		fg:       command.FG,
		bg:       command.BG,
		family:   command.Family,
		size:     command.Size,
		weight:   command.Weight,
		italic:   command.Italic,
		features: command.Features,
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if element, ok := cache.rows[key]; ok {
		cache.order.MoveToFront(element)
		return append([]ui.Glyph(nil), element.Value.(*rowCacheEntry).glyphs...)
	}
	glyphs := shapeGlyphs(command)
	element := cache.order.PushFront(&rowCacheEntry{key: key, glyphs: glyphs})
	cache.rows[key] = element
	cache.shaped++
	for len(cache.rows) > cache.capacity {
		oldest := cache.order.Back()
		if oldest == nil {
			break
		}
		cache.order.Remove(oldest)
		delete(cache.rows, oldest.Value.(*rowCacheEntry).key)
	}
	return append([]ui.Glyph(nil), glyphs...)
}

func (cache *rowCache) shapedRows() int {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return cache.shaped
}

func (cache *rowCache) size() int {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return len(cache.rows)
}

func fallbackCell() text.CellSize {
	resolver, err := text.NewResolver(text.Config{GUIFont: "", Scale: 1})
	if err != nil {
		return text.CellSize{}
	}
	return resolver.Cell()
}

func newView() *View {
	return &View{impl: &viewImpl{cache: newRowCache()}}
}

type viewSnapshot struct {
	frame renderlist.Frame
	caret ui.Rect
	metas []rowMeta
	cache *rowCache
}

func viewDraw(view *View, c *ui.Context) ui.Element {
	if view == nil || view.impl == nil {
		return ui.Element{}
	}
	view.impl.mu.Lock()
	snapshot := viewSnapshot{
		frame: cloneFrame(view.impl.frame),
		caret: view.impl.caret,
		metas: append([]rowMeta(nil), view.impl.metas...),
		cache: view.impl.cache,
	}
	view.impl.mu.Unlock()
	if snapshot.cache == nil {
		snapshot.cache = globalCache
	}
	return ui.Box(c).Fill().Focusable().TextCaret(snapshot.caret).Draw(func(p *ui.Painter, r ui.Rect) {
		paint := func() {
			state := drawState{cache: snapshot.cache, metas: snapshot.metas}
			done := pushDrawState(p, state)
			defer done()
			drawFrame(p, snapshot.frame, snapshot.cache, snapshot.metas)
		}
		transformed := func() {
			if r.X != 0 || r.Y != 0 {
				p.Transform(ui.Translation(r.X, r.Y), 0, 0, paint)
			} else {
				paint()
			}
		}
		if r.W > 0 && r.H > 0 {
			p.Clip(r, 0, transformed)
		} else {
			transformed()
		}
	})
}

func viewSetFrame(view *View, frame renderlist.Frame) {
	if view == nil || view.impl == nil {
		return
	}
	view.impl.mu.Lock()
	defer view.impl.mu.Unlock()
	view.impl.frame = cloneFrame(frame)
	view.impl.caret = caretRect(view.impl.frame, view.impl.cell)
	view.impl.metas = metadataFor(view.impl.frame)
}

func cloneFrame(frame renderlist.Frame) renderlist.Frame {
	frame.Commands = append([]renderlist.Command(nil), frame.Commands...)
	return frame
}

func viewCaret(view *View) ui.Rect {
	if view == nil || view.impl == nil {
		return ui.Rect{}
	}
	view.impl.mu.Lock()
	defer view.impl.mu.Unlock()
	return view.impl.caret
}

func viewShapedRows(view *View) int {
	if view == nil || view.impl == nil || view.impl.cache == nil {
		return 0
	}
	return view.impl.cache.shapedRows()
}

func caretRect(frame renderlist.Frame, cell text.CellSize) ui.Rect {
	var caret ui.Rect
	for _, cmd := range frame.Commands {
		if cmd.Op != "cursor" {
			continue
		}
		width, height := cmd.W, cmd.H
		if width <= 0 || height <= 0 {
			if cell.Width <= 0 || cell.Height <= 0 {
				cell = fallbackCell()
			}
			if width <= 0 {
				width = cell.Width
			}
			if height <= 0 {
				height = cell.Height
			}
		}
		caret = ui.Rect{X: cmd.X, Y: cmd.Y, W: width, H: height}
	}
	return caret
}

func Draw(p *ui.Painter, frame renderlist.Frame) {
	if p == nil {
		return
	}
	state, ok := drawStateFor(p)
	if !ok {
		state = drawState{cache: globalCache, metas: metadataFor(frame)}
	}
	drawFrame(p, frame, state.cache, state.metas)
}

var globalCache = newRowCache()

func drawFrame(p *ui.Painter, frame renderlist.Frame, cache *rowCache, metas []rowMeta) {
	if cache == nil {
		cache = globalCache
	}
	for index, command := range frame.Commands {
		meta := rowMeta{}
		if index < len(metas) {
			meta = metas[index]
		}
		paint := func() {
			switch command.Op {
			case "fill":
				p.Fill(commandRect(command), color(command.BG), 0)
				drawDecorations(p, command, meta)
			case "glyphs":
				drawGlyphs(p, command, cache, meta)
			case "box":
				drawBoxRune(p, command, color(command.FG))
			case "cursor":
				drawCursor(p, command, cache, meta)
			}
		}
		if meta.clip.W > 0 && meta.clip.H > 0 {
			p.Clip(meta.clip, 0, paint)
		} else {
			paint()
		}
	}
}

func commandRect(command renderlist.Command) ui.Rect {
	return ui.Rect{X: command.X, Y: command.Y, W: command.W, H: command.H}
}

func drawGlyphs(p *ui.Painter, command renderlist.Command, cache *rowCache, meta rowMeta) {
	if command.Text == "" || command.W <= 0 || command.H <= 0 {
		return
	}
	glyphs := cache.glyphs(command, meta)
	font := commandFont(command)
	baseline := command.Y + font.Metrics().Ascent
	p.Glyphs(glyphs, command.X, baseline, color(command.FG))
	drawDecorations(p, command, meta)
}

func shapeGlyphs(command renderlist.Command) []ui.Glyph {
	return ui.Shape(command.Text, commandFont(command))
}

func commandFont(command renderlist.Command) ui.Font {
	return text.Font{
		Family:   command.Family,
		Size:     command.Size,
		Weight:   command.Weight,
		Italic:   command.Italic,
		Features: command.Features,
	}.MyGo()
}

func drawDecorations(p *ui.Painter, command renderlist.Command, meta rowMeta) {
	attrs := splitAttrs(meta.attrs)
	if len(attrs) == 0 || command.W <= 0 || command.H <= 0 {
		return
	}
	decorationColor := color(command.FG)
	if command.SP != 0 {
		decorationColor = color(command.SP)
	}
	thick := max(1, command.Size/10)
	underlineY := command.Y + command.H - max(1, command.Size/10)
	for _, attr := range attrs {
		switch attr {
		case "underline":
			p.Fill(ui.Rect{X: command.X, Y: underlineY, W: command.W, H: thick}, decorationColor, 0)
		case "underdouble":
			p.Fill(ui.Rect{X: command.X, Y: underlineY - 2*thick, W: command.W, H: thick}, decorationColor, 0)
			p.Fill(ui.Rect{X: command.X, Y: underlineY + thick, W: command.W, H: thick}, decorationColor, 0)
		case "undercurl":
			drawWave(p, command.X, underlineY, command.W, thick, decorationColor)
		case "underdotted", "underdashed":
			step, dash := 2*thick, thick
			if attr == "underdashed" {
				step = max(command.W/2, 2*thick)
				dash = max(command.W/4, thick)
			}
			for x := command.X; x < command.X+command.W; x += step {
				p.Fill(ui.Rect{X: x, Y: underlineY, W: min(dash, command.X+command.W-x), H: thick}, decorationColor, 0)
			}
		case "strikethrough":
			p.Fill(ui.Rect{X: command.X, Y: command.Y + command.H/2 - thick/2, W: command.W, H: thick}, decorationColor, 0)
		case "overline":
			p.Fill(ui.Rect{X: command.X, Y: command.Y, W: command.W, H: thick}, decorationColor, 0)
		}
	}
}

func drawWave(p *ui.Painter, x, y, width, thick float32, paintColor ui.Color) {
	period := max(thick*4, 4)
	points := max(8, int(width/period*8))
	var path ui.Path
	path.MoveTo(x, y)
	for index := 1; index <= points; index++ {
		position := x + width*float32(index)/float32(points)
		waveY := y + thick*float32(math.Sin(float64((position-x)/period*2*math.Pi)))
		path.LineTo(position, waveY)
	}
	p.StrokePath(&path, thick, paintColor)
}

func drawCursor(p *ui.Painter, command renderlist.Command, cache *rowCache, meta rowMeta) {
	rect := commandRect(command)
	if rect.W <= 0 || rect.H <= 0 {
		return
	}
	fillColor := color(command.BG)
	if command.FG == 0 && command.BG == 0 {
		fillColor = ui.Color{R: 128, G: 128, B: 128, A: 200}
	}
	attrs := splitAttrs(meta.attrs)
	switch {
	case hasAttr(attrs, "cursor_beam"):
		beamWidth := min(rect.W, max(float32(2), min(float32(3), rect.W*0.25)))
		p.Fill(ui.Rect{X: rect.X, Y: rect.Y, W: beamWidth, H: rect.H}, fillColor, 0)
		return
	case hasAttr(attrs, "cursor_underline"):
		barHeight := min(rect.H, max(float32(2), rect.H*0.2))
		p.Fill(ui.Rect{X: rect.X, Y: rect.Y + rect.H - barHeight, W: rect.W, H: barHeight}, fillColor, 0)
		return
	}
	p.Fill(rect, fillColor, 0)
	if command.Text != "" && command.Family != "" && command.Size > 0 {
		drawGlyphs(p, command, cache, meta)
	} else if command.Rune != "" {
		drawBoxRune(p, command, color(command.FG))
	}
}

func hasAttr(attrs []string, wanted string) bool {
	return slices.Contains(attrs, wanted)
}

func colorValue(value int64) uint32 {
	if value < 0 {
		return 0
	}
	return uint32(value)
}

func color(value uint32) ui.Color {
	if value > 0xffffff {
		return ui.Color{R: byte(value >> 16), G: byte(value >> 8), B: byte(value), A: byte(value >> 24)}
	}
	return ui.RGB(byte(value>>16), byte(value>>8), byte(value))
}

func positiveHeight(value float32) float32 {
	if value > 0 {
		return value
	}
	return 18
}

func decodeRune(value string) (rune, bool) {
	character, size := utf8.DecodeRuneInString(value)
	return character, character != utf8.RuneError && size == len(value)
}
