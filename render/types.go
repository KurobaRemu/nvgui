package render

import (
	"github.com/egoist/mygo/ui"
	"nvgui/renderlist"
)

type View struct{ impl *viewImpl }

func NewView() *View {
	return newView()
}

func (v *View) Draw(c *ui.Context) ui.Element {
	return viewDraw(v, c)
}

func (v *View) SetFrame(frame renderlist.Frame) {
	viewSetFrame(v, frame)
}

func (v *View) Caret() ui.Rect {
	return viewCaret(v)
}

func (v *View) ShapedRows() int {
	return viewShapedRows(v)
}
