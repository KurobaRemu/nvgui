package text

import "github.com/egoist/mygo/ui"

type Config struct {
	GUIFont     string
	Scale       float32
	ScaleFactor float32
	LineSpace   float32
	LineHeight  float32
}

type Font struct {
	Family   string
	Size     float32
	Weight   int
	Italic   bool
	Features string
}

func (f Font) MyGo() ui.Font {
	return fontToMyGo(f)
}

type CellSize struct{ Width, Height float32 }

type Resolver struct{ impl *resolverImpl }

func NewResolver(cfg Config) (*Resolver, error) {
	return newResolver(cfg)
}

func (r *Resolver) Config() Config {
	return resolverConfig(r)
}

func (r *Resolver) Cell() CellSize {
	return resolverCell(r)
}

func (r *Resolver) FontFor(text string, bold, italic bool) Font {
	return resolverFontFor(r, text, bold, italic)
}

func (r *Resolver) Shape(text string, font Font) []ui.Glyph {
	return resolverShape(r, text, font)
}

func (r *Resolver) ShapeCalls() int {
	return resolverShapeCalls(r)
}

func (r *Resolver) ClearCache() {
	resolverClearCache(r)
}

func (r *Resolver) CloneForScale(scale, scaleFactor float32) (*Resolver, error) {
	return resolverCloneForScale(r, scale, scaleFactor)
}
