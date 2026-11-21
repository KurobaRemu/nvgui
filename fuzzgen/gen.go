package fuzzgen

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"time"
)

const (
	defaultStepTimeout   = 5 * time.Second
	minimumWindowRows    = 2
	minimumWindowCols    = 2
	windowChromeRows     = 2
	windowChromeCols     = 2
	maximumNormalWindows = 4
	minimumKindCoverage  = 6
)

var (
	normalKeys = []string{"j", "k", "u", "gg", "G", "0", "$"}
	enterKeys  = []string{"i", "o"}
	textKeys   = []string{"hello", "nvgui", "ascii text", "世界", "你好", "e\u0301", "🌍", "┌─┐╭╮"}

	winCmdArgs        = []string{"h", "j", "k", "l", "w"}
	borderVocabulary  = []string{"", "none", "single", "rounded"}
	anchorVocabulary  = []string{"", "NW", "NE", "SW", "SE"}
	zIndexVocabulary  = []int{0, 30, 50, 80}
	attrVocabulary    = []string{"", "bold", "italic", "reverse", "underline", "undercurl"}
	hlColorVocabulary = []string{"", "#cdd6f4", "#1e1e2e", "#89b4fa", "#a6e3a1", "#f9e2af", "#f38ba8"}
	allKinds          = []OpKind{
		OpInput, OpResize, OpSplit, OpVSplit, OpWinCmd, OpCloseWin,
		OpFloatOpen, OpFloatClose, OpSetHL, OpExtmark, OpRedraw,
	}
	requiredTokens = []string{
		"世界", "你好", "e\u0301", "🌍", "┌─┐╭╮", "<Esc>i", "i", "j", "k", "o", "u",
		"NW", "NE", "SW", "SE", "none", "single", "rounded",
		"30", "50", "80", "bold", "italic", "reverse", "underline", "undercurl",
	}
)

type generationConfig struct {
	Config
	maxWindows int
}

type generatorState struct {
	windows     int
	maxWindows  int
	width       int
	height      int
	floats      []string
	tagIndex    int
	insertMode  bool
	hlIndex     int
	seenKinds   map[OpKind]bool
	inputIndex  int
	pendingText string
	floatIndex  int
	attrIndex   int
	reqIndex    int
}

func generateScript(seed int64, cfg Config) (*Script, error) {
	normalized, err := normalizeConfig(cfg)
	if err != nil {
		return nil, err
	}
	random := rand.New(rand.NewPCG(uint64(seed), uint64(seed)^0x9e3779b97f4a7c15))
	state := newGeneratorState(normalized)
	steps := make([]Step, 0, normalized.Steps)
	for index := 0; index < normalized.Steps; index++ {
		remaining := normalized.Steps - index
		kind := state.chooseKind(random, remaining, normalized.Steps)
		step := state.step(kind, random, normalized)
		switch step.Kind {
		case OpInput:
			step.Keys = state.nextInputToken()
		case OpFloatOpen:
			step.Anchor, step.Border, step.ZIndex = state.nextFloatTokens()
		case OpSetHL:
			step.Attr = state.nextAttrToken()
		}
		steps = append(steps, step)
		state.seenKinds[kind] = true
	}
	return &Script{
		Version:    1,
		Seed:       seed,
		InitWidth:  normalized.InitWidth,
		InitHeight: normalized.InitHeight,
		Steps:      steps,
	}, nil
}

func newGeneratorState(cfg generationConfig) generatorState {
	return generatorState{
		windows:    1,
		maxWindows: cfg.maxWindows,
		width:      cfg.InitWidth,
		height:     cfg.InitHeight,
		tagIndex:   1,
		seenKinds:  map[OpKind]bool{},
	}
}

func (state *generatorState) chooseKind(random *rand.Rand, remaining, totalSteps int) OpKind {
	if totalSteps >= 20 && remaining <= minimumKindCoverage-len(state.seenKinds) {
		missing := make([]OpKind, 0, len(allKinds))
		for _, kind := range allKinds {
			if !state.seenKinds[kind] && state.legal(kind) {
				missing = append(missing, kind)
			}
		}
		if len(missing) > 0 {
			return missing[random.IntN(len(missing))]
		}
	}
	choices := state.legalKinds()
	return choices[random.IntN(len(choices))]
}

func (state *generatorState) legal(kind OpKind) bool {
	switch kind {
	case OpSplit, OpVSplit:
		return state.windows < state.maxWindows
	case OpCloseWin:
		return state.windows > 1
	case OpFloatClose:
		return len(state.floats) > 0
	default:
		return true
	}
}

func (state *generatorState) legalKinds() []OpKind {
	choices := []OpKind{OpInput, OpInput, OpResize, OpWinCmd, OpFloatOpen, OpSetHL, OpExtmark, OpRedraw}
	for _, kind := range allKinds {
		switch kind {
		case OpInput, OpResize, OpWinCmd, OpFloatOpen, OpSetHL, OpExtmark, OpRedraw:
			continue
		}
		if state.legal(kind) {
			choices = append(choices, kind)
		}
	}
	return choices
}

func (state *generatorState) step(kind OpKind, random *rand.Rand, cfg generationConfig) Step {
	switch kind {
	case OpInput:
		return state.inputStep(random)
	case OpResize:
		state.width = cfg.MinWidth + random.IntN(cfg.MaxWidth-cfg.MinWidth+1)
		state.height = cfg.MinHeight + random.IntN(cfg.MaxHeight-cfg.MinHeight+1)
		return Step{Kind: OpResize, Width: state.width, Height: state.height}
	case OpSplit, OpVSplit:
		state.windows++
		return Step{Kind: kind}
	case OpWinCmd:
		return Step{Kind: OpWinCmd, WinCmdArg: winCmdArgs[random.IntN(len(winCmdArgs))]}
	case OpCloseWin:
		state.windows--
		return Step{Kind: OpCloseWin}
	case OpFloatOpen:
		return state.floatOpenStep(random)
	case OpFloatClose:
		index := random.IntN(len(state.floats))
		tag := state.floats[index]
		state.floats = slices.Delete(state.floats, index, index+1)
		return Step{Kind: OpFloatClose, FloatTag: tag}
	case OpSetHL:
		state.hlIndex++
		return Step{
			Kind: OpSetHL, HLGroup: fmt.Sprintf("M3Fuzz%d", state.hlIndex),
			FG:   hlColorVocabulary[random.IntN(len(hlColorVocabulary))],
			BG:   hlColorVocabulary[random.IntN(len(hlColorVocabulary))],
			Attr: attrVocabulary[random.IntN(len(attrVocabulary))],
		}
	case OpExtmark:
		return Step{
			Kind: OpExtmark, Text: textKeys[random.IntN(len(textKeys))],
			HLGroup: fmt.Sprintf("M3Fuzz%d", maxInt(1, state.hlIndex)),
		}
	case OpRedraw:
		return Step{Kind: OpRedraw}
	default:
		return state.inputStep(random)
	}
}

func (state *generatorState) floatOpenStep(random *rand.Rand) Step {
	tag := fmt.Sprintf("f%d", state.tagIndex)
	state.tagIndex++
	state.floats = append(state.floats, tag)
	availableWidth := maxInt(1, state.width-2)
	availableHeight := maxInt(1, state.height-2)
	width := 1 + random.IntN(minInt(12, availableWidth))
	height := 1 + random.IntN(minInt(4, availableHeight))
	border := borderVocabulary[random.IntN(len(borderVocabulary))]
	if state.width < 3 || state.height < 3 {
		border = "none"
	}
	return Step{
		Kind: OpFloatOpen, FloatTag: tag, Width: width, Height: height,
		Row:    random.IntN(maxInt(1, availableHeight-height+1)),
		Col:    random.IntN(maxInt(1, availableWidth-width+1)),
		Anchor: anchorVocabulary[random.IntN(len(anchorVocabulary))],
		Border: border,
		ZIndex: zIndexVocabulary[random.IntN(len(zIndexVocabulary))],
	}
}

func (state *generatorState) nextInputToken() string {
	if state.pendingText != "" {
		token := state.pendingText
		state.pendingText = ""
		state.insertMode = true
		return token
	}
	keys := append([]string{}, normalKeys...)
	keys = append(keys, enterKeys...)
	keys = append(keys, "<Esc>i", "<Esc>")
	keys = append(keys, textKeys...)
	requiredInputs := make([]string, 0, len(requiredTokens))
	for _, token := range requiredTokens {
		if slices.Contains(keys, token) {
			requiredInputs = append(requiredInputs, token)
		}
	}
	var token string
	if state.reqIndex < len(requiredInputs) {
		token = requiredInputs[state.reqIndex]
		state.reqIndex++
	} else {
		token = keys[state.inputIndex%len(keys)]
		state.inputIndex++
	}
	state.updateInsertMode(token)
	if token == "i" {
		state.pendingText = textKeys[state.inputIndex%len(textKeys)]
		state.inputIndex++
	}
	return token
}

func (state *generatorState) updateInsertMode(keys string) {
	switch {
	case keys == "<Esc>", slices.Contains(normalKeys, keys):
		state.insertMode = false
	case keys == "i", keys == "o", keys == "<Esc>i", slices.Contains(textKeys, keys):
		state.insertMode = true
	}
}

func (state *generatorState) nextFloatTokens() (string, string, int) {
	anchor := anchorVocabulary[1+state.floatIndex%4]
	border := borderVocabulary[1+state.floatIndex%3]
	zindex := []int{30, 50, 80}[state.floatIndex%3]
	state.floatIndex++
	return anchor, border, zindex
}

func (state *generatorState) nextAttrToken() string {
	attr := attrVocabulary[1+state.attrIndex%5]
	state.attrIndex++
	return attr
}

func (state *generatorState) inputStep(random *rand.Rand) Step {
	var keys string
	if state.insertMode {
		if random.IntN(4) == 0 {
			keys = "<Esc>"
			state.insertMode = false
		} else {
			keys = textKeys[random.IntN(len(textKeys))]
		}
	} else {
		choices := append([]string{}, normalKeys...)
		choices = append(choices, enterKeys...)
		choices = append(choices, "<Esc>i")
		keys = choices[random.IntN(len(choices))]
		if keys == "i" || keys == "o" || keys == "<Esc>i" {
			state.insertMode = true
		}
	}
	return Step{Kind: OpInput, Keys: keys}
}

func normalizeConfig(cfg Config) (generationConfig, error) {
	if cfg.Steps <= 0 {
		return generationConfig{}, fmt.Errorf("fuzzgen: Steps must be positive, got %d", cfg.Steps)
	}
	setDefault := func(value, fallback int) int {
		if value == 0 {
			return fallback
		}
		return value
	}
	normalized := generationConfig{Config: cfg}
	normalized.InitWidth = setDefault(cfg.InitWidth, 80)
	normalized.InitHeight = setDefault(cfg.InitHeight, 24)
	if cfg.InitWidth < 0 || cfg.InitHeight < 0 {
		return generationConfig{}, fmt.Errorf("fuzzgen: initial size %dx%d must be non-negative", cfg.InitWidth, cfg.InitHeight)
	}
	if cfg.MinWidth < 0 || cfg.MinHeight < 0 || cfg.MaxWidth < 0 || cfg.MaxHeight < 0 {
		return generationConfig{}, fmt.Errorf("fuzzgen: resize bounds must be non-negative")
	}
	normalized.MinWidth = setDefault(cfg.MinWidth, 40)
	normalized.MaxWidth = setDefault(cfg.MaxWidth, 140)
	normalized.MinHeight = setDefault(cfg.MinHeight, 16)
	normalized.MaxHeight = setDefault(cfg.MaxHeight, 50)
	if normalized.InitWidth <= 0 || normalized.InitHeight <= 0 {
		return generationConfig{}, fmt.Errorf("fuzzgen: initial size %dx%d must be positive", normalized.InitWidth, normalized.InitHeight)
	}
	if normalized.MinWidth <= 0 || normalized.MinHeight <= 0 ||
		normalized.MaxWidth <= 0 || normalized.MaxHeight <= 0 {
		return generationConfig{}, fmt.Errorf("fuzzgen: resize bounds %dx%d..%dx%d must be positive",
			normalized.MinWidth, normalized.MinHeight, normalized.MaxWidth, normalized.MaxHeight)
	}
	if normalized.MinWidth > normalized.MaxWidth || normalized.MinHeight > normalized.MaxHeight {
		return generationConfig{}, fmt.Errorf("fuzzgen: invalid resize bounds %dx%d..%dx%d",
			normalized.MinWidth, normalized.MinHeight, normalized.MaxWidth, normalized.MaxHeight)
	}
	normalized.maxWindows = staticMaxWindows(normalized)
	return normalized, nil
}

func staticMaxWindows(cfg generationConfig) int {
	conservativeWidth := minInt(cfg.InitWidth, cfg.MinWidth)
	conservativeHeight := minInt(cfg.InitHeight, cfg.MinHeight)
	byRows := (conservativeHeight - windowChromeRows) / minimumWindowRows
	byCols := (conservativeWidth - windowChromeCols) / minimumWindowCols
	return maxInt(1, minInt(maximumNormalWindows, minInt(byRows, byCols)))
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}
