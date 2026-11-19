package screen

// State 是一次 flush 提交后的 Committed Screen。发布后不可变：任何持有者都不得修改，Reducer 也不得再写它引用的任何 slice/map。
type State struct {
	Seq           uint64            // 第几次提交，从 1 开始
	Grids         map[int]*Grid     // gridID → Grid，含 grid 1 与消息 grid
	Attrs         map[int]Attr      // hl_attr_define 的 id → rgb_attr；id 0 恒为空 Attr
	DefaultColors Colors
	Placements    map[int]Placement // gridID → 最近一次 win_pos / win_float_pos / win_external_pos；未放置或已 win_hide 的 grid 不在表中
	Msg           *MsgPlacement     // 最近一次 msg_set_pos；没有则 nil
	Cursor        Cursor
	Mode          string
	ModeIdx       int
}

type Grid struct {
	Width, Height int
	Cells         [][]Cell // [row][col]，len == Height，每行 len == Width
}

// Cell.Text 为空串表示双宽字符的右半延续格。HL 永远是显式的 hl id。
type Cell struct {
	Text string
	HL   int
}

// Attr 原样保存 rgb_attr 字典（含未来新增键）。值只可能是 int64、bool、string。
type Attr map[string]any

type Colors struct{ FG, BG, SP int64 }

type PlacementKind int

const (
	PlacedWin PlacementKind = iota + 1
	PlacedFloat
	PlacedExternal
)

type Placement struct {
	Kind PlacementKind
	Win  int64
	// PlacedWin
	Row, Col, Width, Height int
	// PlacedFloat
	Anchor               string
	AnchorGrid           int
	AnchorRow, AnchorCol float64
	MouseEnabled         bool
	ZIndex, CompIndex    int
	ScreenRow, ScreenCol int
}

type MsgPlacement struct {
	Grid, Row         int
	Scrolled          bool
	SepChar           string
	ZIndex, CompIndex int
}

type Cursor struct{ Grid, Row, Col int } // 0-based
