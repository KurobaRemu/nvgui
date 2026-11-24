package renderlist

type Command struct {
	Op       string  `json:"op"` // "fill" | "glyphs" | "box" | "cursor"
	X        float32 `json:"x"`
	Y        float32 `json:"y"`
	W        float32 `json:"w"`
	H        float32 `json:"h"`
	Text     string  `json:"text,omitempty"`
	Family   string  `json:"family,omitempty"`
	Size     float32 `json:"size,omitempty"`
	Weight   int     `json:"weight,omitempty"`
	Italic   bool    `json:"italic,omitempty"`
	Features string  `json:"features,omitempty"`
	FG       uint32  `json:"fg,omitempty"`
	BG       uint32  `json:"bg,omitempty"`
	SP       uint32  `json:"sp,omitempty"`
	Blend    uint8   `json:"blend,omitempty"`
	Rune     string  `json:"rune,omitempty"`
}

type Frame struct {
	Version  int       `json:"v"` // 恒为 1
	Seq      uint64    `json:"seq"`
	Commands []Command `json:"commands"`
}
