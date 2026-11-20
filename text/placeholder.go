package text

import (
	"errors"

	"github.com/egoist/mygo/ui"
)

var errNotImplemented = errors.New("text: not implemented")

type resolverImpl struct{}

func fontToMyGo(_ Font) ui.Font {
	return ui.Font{}
}

func newResolver(_ Config) (*Resolver, error) {
	return nil, errNotImplemented
}

func resolverConfig(_ *Resolver) Config {
	return Config{}
}

func resolverCell(_ *Resolver) CellSize {
	return CellSize{}
}

func resolverFontFor(_ *Resolver, _ string, _, _ bool) Font {
	return Font{}
}

func resolverShape(_ *Resolver, _ string, _ Font) []ui.Glyph {
	return nil
}

func resolverShapeCalls(_ *Resolver) int {
	return 0
}

func resolverClearCache(_ *Resolver) {}

func ParseGUIFont(_ string) ([]Font, error) {
	return nil, errNotImplemented
}

func BuiltinFallbacks() []Font {
	return nil
}
