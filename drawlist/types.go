package drawlist

type List struct {
	Version int     `json:"v"` // 恒为 1
	Seq     uint64  `json:"seq"`
	Width   int     `json:"width"`
	Height  int     `json:"height"`
	Layers  []Layer `json:"layers"`
}

type Layer struct {
	Grid      int    `json:"grid"`
	ScreenRow int    `json:"screen_row"`
	ScreenCol int    `json:"screen_col"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	Clip      Rect   `json:"clip"`
	Items     []Item `json:"items"`
}

type Rect struct {
	X int `json:"x"`
	Y int `json:"y"`
	W int `json:"w"`
	H int `json:"h"`
}

type Item struct {
	Kind   string   `json:"kind"` // "fill" | "text" | "cursor" | "box"
	Row    int      `json:"row"`
	Col    int      `json:"col"`
	Width  int      `json:"width"`
	Height int      `json:"height"`
	Text   string   `json:"text,omitempty"`
	Rune   string   `json:"rune,omitempty"`
	Font   string   `json:"font,omitempty"`
	FG     int64    `json:"fg,omitempty"`
	BG     int64    `json:"bg,omitempty"`
	SP     int64    `json:"sp,omitempty"`
	Attrs  []string `json:"attrs,omitempty"`
}

func JSON(list *List) ([]byte, error) {
	return marshalJSON(list)
}
