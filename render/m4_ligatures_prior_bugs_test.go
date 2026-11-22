package render_test

import (
	"bytes"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"nvgui/drawlist"
	"nvgui/render"
	"nvgui/renderlist"
	"nvgui/screen"
	"nvgui/text"
)

const m4LigatureSample = "-> != === >= <= |>"
const m4LigatureLine40 = "a->b!=c===d>=e<=f|>g->h!=i===j>=k<=l|>.X"

func m4LigatureResolver(t *testing.T) (*text.Resolver, string, bool) {
	t.Helper()
	family, hasTrueLig := m4DetectLigatureFont()
	resolver, err := text.NewResolver(text.Config{GUIFont: family + ":h16", Scale: 1})
	if err != nil {
		t.Fatalf("NewResolver(%s:h16): %v", family, err)
	}
	return resolver, family, hasTrueLig
}

func m4LigatureState(t *testing.T, seq uint64, width int, line string, cursorCol int) (*screen.State, *drawlist.List) {
	t.Helper()
	grid := newBlankGrid(width, 2, 0)
	putRunes(grid.Cells[0], 0, line, 0)
	state := &screen.State{
		Seq:           seq,
		Grids:         map[int]*screen.Grid{1: grid},
		Attrs:         map[int]screen.Attr{0: {}},
		DefaultColors: screen.Colors{FG: 0xffffff, BG: 0x101820},
		Placements:    map[int]screen.Placement{},
		Cursor:        screen.Cursor{Grid: 1, Row: 0, Col: cursorCol},
		Mode:          "normal",
	}
	list, err := drawlist.Build(state)
	if err != nil {
		t.Fatalf("drawlist.Build: %v", err)
	}
	return state, list
}

func m4LigatureImage(t *testing.T, frame renderlist.Frame, width, height int) []byte {
	t.Helper()
	view := render.NewView()
	view.SetFrame(frame)
	tester := ui.NewTester(func(c *ui.Context) { view.Draw(c) }, width, height)
	result := tester.Image()
	if result == nil {
		t.Fatal("Tester.Image() returned nil")
	}
	return append([]byte(nil), result.Pix...)
}

func m4LigatureSharedImage(t *testing.T, view *render.View, tester *ui.Tester, frame renderlist.Frame) []byte {
	t.Helper()
	view.SetFrame(frame)
	tester.Frame()
	result := tester.Image()
	if result == nil {
		t.Fatal("Tester.Image() returned nil")
	}
	return append([]byte(nil), result.Pix...)
}

func m4LigatureFeatures(features string) map[string]string {
	result := make(map[string]string)
	for _, raw := range strings.Split(features, ",") {
		part := strings.TrimSpace(raw)
		if part == "" {
			continue
		}
		tag, value, hasValue := strings.Cut(part, "=")
		if !hasValue {
			value = "1"
		}
		result[strings.TrimSpace(tag)] = strings.TrimSpace(value)
	}
	return result
}

func m4LigatureAssertFrameFeatures(t *testing.T, frame renderlist.Frame, enabled bool) {
	t.Helper()
	glyphCount := 0
	for _, command := range frame.Commands {
		if command.Op != "glyphs" {
			continue
		}
		glyphCount++
		features := m4LigatureFeatures(command.Features)
		for _, tag := range []string{"calt", "liga", "clig", "dlig"} {
			if enabled && features[tag] == "0" {
				t.Fatalf("ligatures ON kept %s=0 in %q", tag, command.Features)
			}
			if !enabled && features[tag] != "0" {
				t.Fatalf("ligatures OFF lost %s=0 in %q", tag, command.Features)
			}
		}
	}
	if glyphCount == 0 {
		t.Fatal("frame emitted zero glyphs commands")
	}
}

func TestPriorBug_NeovimQt166_LigaturesRenderingAndCellAlignment(t *testing.T) {
	resolver, family, hasTrueLig := m4LigatureResolver(t)
	cell := resolver.Cell()
	planner := render.NewPlanner(resolver)
	if !planner.LigaturesEnabled() {
		t.Fatal("default Planner.LigaturesEnabled() = false, want true")
	}

	if hasTrueLig {
		onFont := ui.Font{Family: family, Size: 16, Weight: 400, Features: "kern=0"}
		offFont := ui.Font{Family: family, Size: 16, Weight: 400, Features: "kern=0,calt=0,liga=0,clig=0,dlig=0"}
		onGlyphs := ui.Shape(m4LigatureSample, onFont)
		offGlyphs := ui.Shape(m4LigatureSample, offFont)
		if len(onGlyphs) == 0 || reflect.DeepEqual(onGlyphs, offGlyphs) {
			t.Fatalf("font %q has no real ligature differences between %+v and %+v", family, onGlyphs, offGlyphs)
		}
	}

	_, list := m4LigatureState(t, 1, 24, m4LigatureSample, 1)
	onFrame, err := planner.Plan(list)
	if err != nil {
		t.Fatalf("Plan(on): %v", err)
	}
	foundTokens := make(map[string]bool)
	for _, command := range onFrame.Commands {
		if command.Op == "glyphs" {
			foundTokens[command.Text] = true
		}
	}
	for _, token := range strings.Fields(m4LigatureSample) {
		if !foundTokens[token] {
			t.Fatalf("sample token %q was not preserved as one merged glyphs command: %+v", token, onFrame.Commands)
		}
	}
	planner.SetLigaturesEnabled(false)
	offFrame, err := planner.Plan(list)
	if err != nil {
		t.Fatalf("Plan(off): %v", err)
	}
	width := int(math.Ceil(24 * float64(cell.Width)))
	height := int(math.Ceil(2 * float64(cell.Height)))
	if hasTrueLig && bytes.Equal(m4LigatureImage(t, onFrame, width, height), m4LigatureImage(t, offFrame, width, height)) {
		t.Fatalf("font %q rendered identical pixels for ligatures ON vs OFF", family)
	}
	planner.SetLigaturesEnabled(true)

	for _, ligOn := range []bool{true, false} {
		planner.SetLigaturesEnabled(ligOn)
		_, merged := m4LigatureState(t, 10, 42, m4LigatureLine40, 1)
		mergedFrame, err := planner.Plan(merged)
		if err != nil {
			t.Fatalf("Plan(merged lig=%v): %v", ligOn, err)
		}
		_, single := m4LigatureState(t, 11, 42, "                                       X", 1)
		singleFrame, err := planner.Plan(single)
		if err != nil {
			t.Fatalf("Plan(single lig=%v): %v", ligOn, err)
		}
		width := int(math.Ceil(42 * float64(cell.Width)))
		height := int(math.Ceil(2 * float64(cell.Height)))
		viewMerged := render.NewView()
		viewMerged.SetFrame(mergedFrame)
		imgMerged := ui.NewTester(func(c *ui.Context) { viewMerged.Draw(c) }, width, height).Image()
		viewSingle := render.NewView()
		viewSingle.SetFrame(singleFrame)
		imgSingle := ui.NewTester(func(c *ui.Context) { viewSingle.Draw(c) }, width, height).Image()
		x0 := int(math.Floor(39 * float64(cell.Width)))
		x1 := int(math.Ceil(40 * float64(cell.Width)))
		y1 := int(math.Ceil(float64(cell.Height)))
		blockMerged := m4CropBlock(imgMerged, x0, 0, x1, y1)
		blockSingle := m4CropBlock(imgSingle, x0, 0, x1, y1)
		if isUniformImage(blockSingle) {
			t.Fatalf("lig=%v standalone 'X' was uniform in col 39", ligOn)
		}
		if !bytes.Equal(blockMerged, blockSingle) {
			t.Fatalf("lig=%v 40-col merged run col 39 drifted from standalone 'X'", ligOn)
		}
	}

	planner.SetLigaturesEnabled(true)
	state, cursorList := m4LigatureState(t, 20, 24, m4LigatureSample, 0)
	cursorFrame, err := planner.Plan(cursorList)
	if err != nil {
		t.Fatalf("Plan(cursor): %v", err)
	}
	var cursor renderlist.Command
	for _, command := range cursorFrame.Commands {
		if command.Op == "cursor" {
			cursor = command
		}
	}
	if cursor.Text != "-" || cursor.X != 0 || cursor.W != cell.Width || cursor.H != cell.Height {
		t.Fatalf("block cursor over '-' = %+v, want standalone cell text and cell geometry", cursor)
	}
	if cursor.Family == "" || cursor.Size <= 0 {
		t.Fatalf("block cursor lost font fields: %+v", cursor)
	}
	if state.Cursor.Col != 0 {
		t.Fatalf("fixture cursor col = %d, want 0", state.Cursor.Col)
	}
	view := render.NewView()
	view.SetFrame(cursorFrame)
	img := ui.NewTester(func(c *ui.Context) { view.Draw(c) }, int(math.Ceil(24*float64(cell.Width))), int(math.Ceil(2*float64(cell.Height)))).Image()
	cellBlock := m4CropBlock(img, 0, 0, int(math.Ceil(float64(cell.Width))), int(math.Ceil(float64(cell.Height))))
	if isUniformImage(cellBlock) {
		t.Fatal("block cursor cell rendered uniform pixels")
	}
}

func TestPriorBug_Neovide1037_DisableLigaturesOptionAndToggle(t *testing.T) {
	resolver, family, hasTrueLig := m4LigatureResolver(t)
	cell := resolver.Cell()
	planner := render.NewPlanner(resolver)
	_, list := m4LigatureState(t, 30, 24, m4LigatureSample, 1)
	width := int(math.Ceil(24 * float64(cell.Width)))
	height := int(math.Ceil(2 * float64(cell.Height)))

	onFrame, err := planner.Plan(list)
	if err != nil {
		t.Fatalf("Plan(on): %v", err)
	}
	view := render.NewView()
	tester := ui.NewTester(func(c *ui.Context) { view.Draw(c) }, width, height)
	onPixels := m4LigatureSharedImage(t, view, tester, onFrame)
	shapedOn := view.ShapedRows()

	planner.SetLigaturesEnabled(false)
	offFrame, err := planner.Plan(list)
	if err != nil {
		t.Fatalf("Plan(off): %v", err)
	}
	m4LigatureAssertFrameFeatures(t, offFrame, false)
	offPixels := m4LigatureSharedImage(t, view, tester, offFrame)
	if hasTrueLig && bytes.Equal(onPixels, offPixels) {
		t.Fatalf("font %q rendered identical pixels for ligatures ON vs OFF", family)
	}
	shapedOff := view.ShapedRows()
	if shapedOff <= shapedOn {
		t.Fatalf("shared View shaped rows ON=%d OFF=%d, want OFF to grow", shapedOn, shapedOff)
	}

	backOnPixels := m4LigatureSharedImage(t, view, tester, onFrame)
	if !bytes.Equal(backOnPixels, onPixels) {
		t.Fatal("shared View ligatures-ON pixels changed after OFF frame")
	}
	shapedBackOn := view.ShapedRows()
	if shapedBackOn != shapedOff {
		t.Fatalf("shared View shaped rows OFF=%d back ON=%d, want cached ON frame with zero growth", shapedOff, shapedBackOn)
	}

	clock := render.NewManualClock(time.Unix(1_700_000_000, 0).UTC())
	state, animatedList := m4LigatureState(t, 31, 24, m4LigatureSample, 1)
	freshPlanner := render.NewPlanner(resolver)
	if !freshPlanner.LigaturesEnabled() {
		t.Fatal("fresh Planner ligatures disabled by default")
	}
	freshAnimator := render.NewAnimator(freshPlanner, render.DisabledAnimationConfig(), clock)
	if freshPlanner.LigaturesEnabled() {
		t.Fatal("NewAnimator(freshPlanner, DisabledAnimationConfig()) left ligatures ON")
	}
	_ = freshAnimator

	animator := render.NewAnimator(planner, render.DisabledAnimationConfig(), clock)
	if planner.LigaturesEnabled() {
		t.Fatal("NewAnimator(DisabledAnimationConfig()) left ligatures ON")
	}
	if _, _, err := animator.Submit(state, &screen.FrameMeta{Seq: 31}, animatedList); err != nil {
		t.Fatalf("Animator.Submit: %v", err)
	}
	animatorOffFrame, _, err := animator.Step()
	if err != nil {
		t.Fatalf("Animator.Step(off): %v", err)
	}
	m4LigatureAssertFrameFeatures(t, animatorOffFrame, false)
	if !bytes.Equal(m4LigatureSharedImage(t, view, tester, animatorOffFrame), offPixels) {
		t.Fatal("Animator produced pixels inconsistent with ligatures OFF")
	}

	animator.SetConfig(render.DefaultAnimationConfig())
	if !planner.LigaturesEnabled() {
		t.Fatal("Animator.SetConfig(DefaultAnimationConfig()) left ligatures OFF")
	}
	animatorOnFrame, _, err := animator.Step()
	if err != nil {
		t.Fatalf("Animator.Step(ON toggle): %v", err)
	}
	m4LigatureAssertFrameFeatures(t, animatorOnFrame, true)
	if hasTrueLig && !bytes.Equal(m4LigatureSharedImage(t, view, tester, animatorOnFrame), onPixels) {
		t.Fatal("Animator.SetConfig(ON) did not restore ligatures-ON pixels")
	}
	animator.SetConfig(render.DisabledAnimationConfig())
	if planner.LigaturesEnabled() {
		t.Fatal("Animator.SetConfig(DisabledAnimationConfig()) left ligatures ON")
	}
	animatorOffAgain, _, err := animator.Step()
	if err != nil {
		t.Fatalf("Animator.Step(OFF toggle): %v", err)
	}
	m4LigatureAssertFrameFeatures(t, animatorOffAgain, false)
	if hasTrueLig && !bytes.Equal(m4LigatureSharedImage(t, view, tester, animatorOffAgain), offPixels) {
		t.Fatal("Animator.SetConfig(OFF) did not restore ligatures-OFF pixels")
	}

	for _, transition := range []struct {
		enabled bool
		name    string
	}{
		{enabled: true, name: "on"},
		{enabled: false, name: "off"},
		{enabled: true, name: "on again"},
	} {
		cfg := render.AnimationConfig{LigaturesEnabled: transition.enabled, CursorEnabled: true, ScrollEnabled: true, CursorAnimationLength: 0.15, ScrollAnimationLength: 0.25}
		animator.SetConfig(cfg)
		if got := planner.LigaturesEnabled(); got != transition.enabled {
			t.Fatalf("%s: planner ligatures = %v, want %v", transition.name, got, transition.enabled)
		}
		frame, _, err := animator.Step()
		if err != nil {
			t.Fatalf("Animator.Step(%s): %v", transition.name, err)
		}
		m4LigatureAssertFrameFeatures(t, frame, transition.enabled)
		if hasTrueLig {
			pixels := m4LigatureSharedImage(t, view, tester, frame)
			if transition.enabled && !bytes.Equal(pixels, onPixels) {
				t.Fatalf("%s did not restore ligatures-ON pixels", transition.name)
			}
			if !transition.enabled && !bytes.Equal(pixels, offPixels) {
				t.Fatalf("%s did not restore ligatures-OFF pixels", transition.name)
			}
		}
	}

	planner.SetLigaturesEnabled(false)
	firstOff, err := planner.Plan(list)
	if err != nil {
		t.Fatalf("Plan(first off): %v", err)
	}
	planner.SetLigaturesEnabled(true)
	planner.SetLigaturesEnabled(false)
	secondOff, err := planner.Plan(list)
	if err != nil {
		t.Fatalf("Plan(second off): %v", err)
	}
	if !reflect.DeepEqual(firstOff, secondOff) {
		t.Fatal("rowCache command isolation failed across ligature toggles")
	}
}
