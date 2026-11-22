package screen

type Viewport struct {
	Grid           int
	Win            int64
	TopLine        int64
	BotLine        int64
	CurLine        int64
	CurCol         int64
	LineCount      int64
	ScrollDelta    float64
	HasLineCount   bool
	HasScrollDelta bool
}

type ViewportMargins struct {
	Top, Bottom, Left, Right int
}

// FrameMeta 是一次 flush 提交后的不可变旁路元数据，不改变 screen.State 与 Canonical JSON v1。
type FrameMeta struct {
	Seq       uint64
	Viewports map[int]Viewport
	Margins   map[int]ViewportMargins
	Busy      bool
	GUIFont   string
}

func (r *Reducer) LatestMeta() *FrameMeta           { return reducerLatestMeta(r) }
func (r *Reducer) MetaForSeq(seq uint64) *FrameMeta { return reducerMetaForSeq(r, seq) }
