package diag

import (
	"time"

	"nvgui/drawlist"
	"nvgui/screen"
)

type TraceKind uint8

const (
	TraceKeyDown TraceKind = iota + 1
	TraceKeyUp
	TraceText
	TraceCompose
	TraceCommand
)

type TraceEvent struct {
	Seq       uint64    `json:"seq"`
	At        time.Time `json:"at"`
	Kind      TraceKind `json:"kind"`
	Key       uint16    `json:"key,omitempty"`
	Code      uint16    `json:"code,omitempty"`
	Mods      uint8     `json:"mods,omitempty"`
	Repeat    bool      `json:"repeat,omitempty"`
	Text      string    `json:"text,omitempty"`
	Caret     int       `json:"caret,omitempty"`
	Cancelled bool      `json:"cancelled,omitempty"`
}

type Bundle struct {
	Meta        Meta
	States      []*screen.State
	DrawLists   []*drawlist.List
	InputEvents []TraceEvent
}
