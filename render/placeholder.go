package render

import (
	"errors"

	"github.com/egoist/mygo/ui"
	"nvgui/drawlist"
	"nvgui/renderlist"
	"nvgui/text"
)

var errNotImplemented = errors.New("render: not implemented")

type viewImpl struct{}

func newView() *View {
	return &View{impl: &viewImpl{}}
}

func viewDraw(_ *View, _ *ui.Context) ui.Element {
	return ui.Element{}
}

func viewSetFrame(_ *View, _ renderlist.Frame) {}

func viewCaret(_ *View) ui.Rect {
	return ui.Rect{}
}

func viewShapedRows(_ *View) int {
	return 0
}

type Planner struct{ resolver *text.Resolver }

func NewPlanner(resolver *text.Resolver) *Planner {
	return &Planner{resolver: resolver}
}

func (p *Planner) Plan(_ *drawlist.List) (renderlist.Frame, error) {
	return renderlist.Frame{}, errNotImplemented
}

func Draw(_ *ui.Painter, _ renderlist.Frame) {}
