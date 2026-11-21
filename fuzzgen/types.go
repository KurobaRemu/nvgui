package fuzzgen

import (
	"context"
	"time"
)

type OpKind string

const (
	OpInput      OpKind = "input"
	OpResize     OpKind = "resize"
	OpSplit      OpKind = "split"
	OpVSplit     OpKind = "vsplit"
	OpWinCmd     OpKind = "wincmd"
	OpCloseWin   OpKind = "close_win"
	OpFloatOpen  OpKind = "float_open"
	OpFloatClose OpKind = "float_close"
	OpSetHL      OpKind = "set_hl"
	OpExtmark    OpKind = "extmark"
	OpRedraw     OpKind = "redraw"
)

type Step struct {
	Kind      OpKind `json:"kind"`
	Keys      string `json:"keys,omitempty"`
	Width     int    `json:"width,omitempty"`
	Height    int    `json:"height,omitempty"`
	Row       int    `json:"row,omitempty"`
	Col       int    `json:"col,omitempty"`
	Anchor    string `json:"anchor,omitempty"`
	Border    string `json:"border,omitempty"`
	ZIndex    int    `json:"zindex,omitempty"`
	HLGroup   string `json:"hl_group,omitempty"`
	FG        string `json:"fg,omitempty"`
	BG        string `json:"bg,omitempty"`
	Attr      string `json:"attr,omitempty"`
	Text      string `json:"text,omitempty"`
	FloatTag  string `json:"float_tag,omitempty"`
	WinCmdArg string `json:"wincmd_arg,omitempty"`
}

type Config struct {
	Steps       int           `json:"steps"`
	InitWidth   int           `json:"init_width"`
	InitHeight  int           `json:"init_height"`
	MinWidth    int           `json:"min_width"`
	MaxWidth    int           `json:"max_width"`
	MinHeight   int           `json:"min_height"`
	MaxHeight   int           `json:"max_height"`
	StepTimeout time.Duration `json:"step_timeout,omitempty"`
}

type Script struct {
	Version    int    `json:"v"` // 恒为 1
	Seed       int64  `json:"seed"`
	InitWidth  int    `json:"init_width"`
	InitHeight int    `json:"init_height"`
	Steps      []Step `json:"steps"`
}

type RunResult struct {
	Seed          int64    `json:"seed"`
	StepsExecuted int      `json:"steps_executed"`
	Committed     int      `json:"committed"`
	Stalls        int      `json:"stalls"`
	Inconclusive  bool     `json:"inconclusive"`
	Reason        string   `json:"reason,omitempty"`
	Inbound       []byte   `json:"-"`
	CanonFrames   [][]byte `json:"-"`
}

type ShrinkConfig struct {
	MaxAttempts int `json:"max_attempts"`
}

type ShrinkResult struct {
	OriginalSteps int     `json:"original_steps"`
	MinimalSteps  int     `json:"minimal_steps"`
	Attempts      int     `json:"attempts"`
	Minimal       *Script `json:"minimal"`
}

func Generate(seed int64, cfg Config) (*Script, error) {
	return generateScript(seed, cfg)
}

func Run(ctx context.Context, script *Script, stepTimeout time.Duration) (*RunResult, error) {
	return runScript(ctx, script, stepTimeout)
}

func Shrink(script *Script, cfg ShrinkConfig, fails func(*Script) bool) (*ShrinkResult, error) {
	return shrinkScript(script, cfg, fails)
}
