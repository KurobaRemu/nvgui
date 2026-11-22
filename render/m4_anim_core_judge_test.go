package render_test

import (
	"bytes"
	imagecolor "image/color"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"nvgui/drawlist"
	"nvgui/render"
	"nvgui/screen"
)

func TestM4Core_ManualClock(t *testing.T) {
	start := time.Unix(1_700_000_000, 0).UTC()
	clock := render.NewManualClock(start)
	if clock == nil {
		t.Fatal("NewManualClock returned nil")
	}
	if got := clock.Now(); !got.Equal(start) {
		t.Fatalf("initial Now() = %v, want %v", got, start)
	}
	t1 := clock.Advance(16 * time.Millisecond)
	want1 := start.Add(16 * time.Millisecond)
	if !t1.Equal(want1) || !clock.Now().Equal(want1) {
		t.Fatalf("after +16ms: Advance=%v Now=%v, want %v", t1, clock.Now(), want1)
	}
	t2 := clock.Advance(34 * time.Millisecond)
	want2 := start.Add(50 * time.Millisecond)
	if !t2.Equal(want2) || !clock.Now().Equal(want2) {
		t.Fatalf("after +34ms: Advance=%v Now=%v, want %v", t2, clock.Now(), want2)
	}
	if !t2.After(t1) {
		t.Fatalf("ManualClock not monotonic: %v <= %v", t2, t1)
	}
}

func TestM4Core_DefaultAndDisabledConfig(t *testing.T) {
	def := render.DefaultAnimationConfig()
	if !def.CursorEnabled {
		t.Errorf("DefaultAnimationConfig().CursorEnabled = false, want true")
	}
	if !def.ScrollEnabled {
		t.Errorf("DefaultAnimationConfig().ScrollEnabled = false, want true")
	}
	if !def.LigaturesEnabled {
		t.Errorf("DefaultAnimationConfig().LigaturesEnabled = false, want true")
	}
	if def.CursorAnimateCommandLine {
		t.Errorf("DefaultAnimationConfig().CursorAnimateCommandLine = true, want false")
	}
	if def.CursorAnimationLength <= 0 || def.CursorShortLength <= 0 || def.CursorShortLength >= def.CursorAnimationLength {
		t.Errorf("DefaultAnimationConfig cursor lengths invalid: len=%v short=%v", def.CursorAnimationLength, def.CursorShortLength)
	}
	if def.CursorTrailSize <= 0 {
		t.Errorf("DefaultAnimationConfig().CursorTrailSize = %v, want > 0", def.CursorTrailSize)
	}
	if def.ScrollAnimationLength <= 0 || def.ScrollFarLines <= 0 {
		t.Errorf("DefaultAnimationConfig scroll params invalid: len=%v far=%d", def.ScrollAnimationLength, def.ScrollFarLines)
	}

	dis := render.DisabledAnimationConfig()
	if dis.CursorEnabled || dis.ScrollEnabled || dis.LigaturesEnabled {
		t.Errorf("DisabledAnimationConfig() = %+v, want CursorEnabled=false ScrollEnabled=false LigaturesEnabled=false", dis)
	}
}

func TestM4Core_SpringCriticallyDamped(t *testing.T) {
	const animLen float32 = 0.15
	const dt float32 = 1.0 / 60.0

	// Exact analytic check on first step (Neovide critically damped spring: omega = 4 / animLen).
	s := render.Spring{Position: 100, Velocity: 0}
	if !s.Active() {
		t.Fatal("Spring{Position: 100}.Active() = false, want true")
	}
	omega := float64(4.0 / animLen)
	a := 100.0
	b := a * omega
	c := math.Exp(-omega * float64(dt))
	wantPos := float32((a + b*float64(dt)) * c)
	wantVel := float32(c * (-a*omega - b*float64(dt)*omega + b))

	active := s.Update(dt, animLen)
	if !active {
		t.Fatal("first Spring.Update(1/60, 0.15) returned false, want true")
	}
	if !approxEqual(s.Position, wantPos, 1e-2) || !approxEqual(s.Velocity, wantVel, 1e-1) {
		t.Fatalf("first step got (pos=%v, vel=%v), want analytic (pos=%v, vel=%v)", s.Position, s.Velocity, wantPos, wantVel)
	}

	// Monotonic decay, zero overshoot, finite convergence.
	prev := s.Position
	converged := false
	for step := 0; step < 120; step++ {
		still := s.Update(dt, animLen)
		if s.Position < 0 {
			t.Fatalf("positive spring overshot below zero at step %d: pos=%v", step, s.Position)
		}
		if still {
			if !(s.Position < prev) {
				t.Fatalf("spring did not decay monotonically at step %d: %v -> %v", step, prev, s.Position)
			}
			prev = s.Position
		} else {
			if s.Position != 0 || s.Velocity != 0 || s.Active() {
				t.Fatalf("settled spring not reset to zero: pos=%v vel=%v active=%v", s.Position, s.Velocity, s.Active())
			}
			converged = true
			break
		}
	}
	if !converged {
		t.Fatalf("spring failed to converge within 120 steps: pos=%v vel=%v", s.Position, s.Velocity)
	}

	// Negative initial position: zero overshoot above zero and finite convergence.
	neg := render.Spring{Position: -80}
	negPrev := neg.Position
	negConverged := false
	for step := 0; step < 120; step++ {
		still := neg.Update(dt, animLen)
		if neg.Position > 0 {
			t.Fatalf("negative spring overshot above zero at step %d: pos=%v", step, neg.Position)
		}
		if still {
			if !(neg.Position > negPrev) {
				t.Fatalf("negative spring not monotonic at step %d: %v -> %v", step, negPrev, neg.Position)
			}
			negPrev = neg.Position
		} else {
			negConverged = true
			break
		}
	}
	if !negConverged {
		t.Fatalf("negative spring failed to converge: pos=%v", neg.Position)
	}

	// dt <= 0 must not corrupt state.
	guard := render.Spring{Position: 42, Velocity: -5}
	_ = guard.Update(0, animLen)
	_ = guard.Update(-0.016, animLen)
	if guard.Position != 42 || guard.Velocity != -5 {
		t.Fatalf("dt <= 0 corrupted spring state: got %+v, want {42, -5}", guard)
	}

	// animationLength <= dt immediately resets to zero.
	snap := render.Spring{Position: 50, Velocity: -10}
	if snap.Update(dt, dt) || snap.Position != 0 || snap.Velocity != 0 || snap.Active() {
		t.Fatalf("Update(dt, len<=dt) did not immediately reset: %+v", snap)
	}
	snapZero := render.Spring{Position: 50, Velocity: -10}
	if snapZero.Update(dt, 0) || snapZero.Position != 0 || snapZero.Velocity != 0 || snapZero.Active() {
		t.Fatalf("Update(dt, 0) did not immediately reset: %+v", snapZero)
	}

	// Reset zeroes state.
	r := render.Spring{Position: 12, Velocity: 34}
	r.Reset()
	if r.Position != 0 || r.Velocity != 0 || r.Active() {
		t.Fatalf("Reset() left non-zero state: %+v", r)
	}
}

func m4CloneDrawList(src *drawlist.List) *drawlist.List {
	if src == nil {
		return nil
	}
	dst := *src
	dst.Layers = make([]drawlist.Layer, len(src.Layers))
	for i, l := range src.Layers {
		lCopy := l
		lCopy.Items = make([]drawlist.Item, len(l.Items))
		for j, it := range l.Items {
			itCopy := it
			if it.Attrs != nil {
				itCopy.Attrs = append([]string(nil), it.Attrs...)
			}
			lCopy.Items[j] = itCopy
		}
		dst.Layers[i] = lCopy
	}
	return &dst
}

func m4CloneState(src *screen.State) *screen.State {
	if src == nil {
		return nil
	}
	dst := *src
	dst.Grids = make(map[int]*screen.Grid, len(src.Grids))
	for id, g := range src.Grids {
		gc := &screen.Grid{Width: g.Width, Height: g.Height, Cells: make([][]screen.Cell, len(g.Cells))}
		for r := range g.Cells {
			gc.Cells[r] = append([]screen.Cell(nil), g.Cells[r]...)
		}
		dst.Grids[id] = gc
	}
	dst.Attrs = make(map[int]screen.Attr, len(src.Attrs))
	for id, a := range src.Attrs {
		ac := make(screen.Attr, len(a))
		for k, v := range a {
			ac[k] = v
		}
		dst.Attrs[id] = ac
	}
	dst.Placements = make(map[int]screen.Placement, len(src.Placements))
	for id, p := range src.Placements {
		dst.Placements[id] = p
	}
	if src.Msg != nil {
		mc := *src.Msg
		dst.Msg = &mc
	}
	return &dst
}

func TestM4Core_AnimatorDisabledPassThroughAndNoMutation(t *testing.T) {
	resolver := mustResolver(t)
	planner := render.NewPlanner(resolver)
	clock := render.NewManualClock(time.Unix(1_700_000_000, 0).UTC())
	anim := render.NewAnimator(planner, render.DisabledAnimationConfig(), clock)
	if anim == nil {
		t.Fatal("NewAnimator returned nil")
	}

	grid := newBlankGrid(24, 6, 0)
	putRunes(grid.Cells[0], 0, "hello_disabled_anim", 0)
	state := &screen.State{
		Seq:           1,
		Grids:         map[int]*screen.Grid{1: grid},
		Attrs:         map[int]screen.Attr{0: {}},
		DefaultColors: screen.Colors{FG: 0xcdd6f4, BG: 0x1e1e2e, SP: 0xf38ba8},
		Placements:    map[int]screen.Placement{},
		Cursor:        screen.Cursor{Grid: 1, Row: 2, Col: 5},
		Mode:          "normal",
	}
	meta := &screen.FrameMeta{
		Seq: 1,
		Viewports: map[int]screen.Viewport{
			1: {Grid: 1, TopLine: 0, BotLine: 6, ScrollDelta: 3.0, HasScrollDelta: true},
		},
	}
	dl, err := drawlist.Build(state)
	if err != nil {
		t.Fatalf("drawlist.Build: %v", err)
	}
	wantFrame, err := planner.Plan(dl)
	if err != nil {
		t.Fatalf("planner.Plan: %v", err)
	}

	stateBefore := m4CloneState(state)
	dlBefore := m4CloneDrawList(dl)

	gotFrame, active, err := anim.Submit(state, meta, dl)
	if err != nil {
		t.Fatalf("Animator.Submit: %v", err)
	}
	if active || anim.Active() {
		t.Fatalf("DisabledAnimationConfig Submit active=%v anim.Active()=%v, want false", active, anim.Active())
	}
	if !reflect.DeepEqual(gotFrame, wantFrame) {
		t.Fatal("DisabledAnimationConfig Submit frame differs from Planner.Plan(list)")
	}

	clock.Advance(16 * time.Millisecond)
	stepFrame, stepActive, err := anim.Step()
	if err != nil {
		t.Fatalf("Animator.Step: %v", err)
	}
	if stepActive || anim.Active() {
		t.Fatalf("DisabledAnimationConfig Step active=%v anim.Active()=%v, want false", stepActive, anim.Active())
	}
	if !reflect.DeepEqual(stepFrame, wantFrame) {
		t.Fatal("DisabledAnimationConfig Step frame differs from Planner.Plan(list)")
	}

	if !reflect.DeepEqual(state, stateBefore) {
		t.Fatal("Animator mutated input *screen.State")
	}
	if !reflect.DeepEqual(dl, dlBefore) {
		t.Fatal("Animator mutated input *drawlist.List")
	}
}

func TestM4Core_MetadataLRUEvictionPreservesClipAndDecorations(t *testing.T) {
	resolver := mustResolver(t)
	cell := resolver.Cell()
	planner := render.NewPlanner(resolver)
	clock := render.NewManualClock(time.Unix(1_700_000_000, 0).UTC())

	const (
		rootW = 24
		rootH = 6
	)
	floatItems := []drawlist.Item{
		{Kind: "fill", Row: 0, Col: 0, Width: 16, Height: 3, BG: 0x202020},
	}
	// Underlined green text extending past clip right edge (clip cols [2, 8), items span [2, 16)).
	for c := 0; c < 14; c++ {
		floatItems = append(floatItems, drawlist.Item{
			Kind: "text", Row: 0, Col: c, Width: 1, Height: 1,
			Text: "W", FG: 0x00ff00, BG: 0x202020, SP: 0xff0044,
			Attrs: []string{"underline"},
		})
	}
	// Row 2 strictly below clip (clip rows [1, 3), row 2 -> screen row 3).
	for c := 0; c < 6; c++ {
		floatItems = append(floatItems, drawlist.Item{
			Kind: "text", Row: 2, Col: c, Width: 1, Height: 1,
			Text: "X", FG: 0x00ff00, BG: 0x202020,
		})
	}

	baseDL := &drawlist.List{
		Version: 1,
		Seq:     1,
		Width:   rootW,
		Height:  rootH,
		Layers: []drawlist.Layer{
			{
				Grid: 1, ScreenRow: 0, ScreenCol: 0, Width: rootW, Height: rootH,
				Clip: drawlist.Rect{X: 0, Y: 0, W: rootW, H: rootH},
				Items: []drawlist.Item{
					{Kind: "fill", Row: 0, Col: 0, Width: rootW, Height: rootH, BG: 0x0a1020},
					{Kind: "cursor", Row: 4, Col: 18, Width: 1, Height: 1, FG: 0x0a1020, BG: 0xffcc00},
				},
			},
			{
				Grid: 2, ScreenRow: 1, ScreenCol: 2, Width: 16, Height: 3,
				Clip:  drawlist.Rect{X: 2, Y: 1, W: 6, H: 2},
				Items: floatItems,
			},
		},
	}
	baseState := &screen.State{
		Seq:           1,
		Grids:         map[int]*screen.Grid{1: newBlankGrid(rootW, rootH, 0), 2: newBlankGrid(16, 3, 0)},
		Attrs:         map[int]screen.Attr{0: {}},
		DefaultColors: screen.Colors{FG: 0xffffff, BG: 0x0a1020, SP: 0xff0044},
		Placements: map[int]screen.Placement{
			2: {Kind: screen.PlacedFloat, Win: 1002, Anchor: "NW", ScreenRow: 1, ScreenCol: 2, ZIndex: 50, CompIndex: 1},
		},
		Cursor: screen.Cursor{Grid: 1, Row: 4, Col: 18},
		Mode:   "normal",
	}
	baseMeta := &screen.FrameMeta{Seq: 1}

	imgW := int(math.Floor(float64(rootW) * float64(cell.Width)))
	imgH := int(math.Floor(float64(rootH) * float64(cell.Height)))

	// Reference render directly from Planner.Plan before LRU eviction.
	refFrame, err := planner.Plan(baseDL)
	if err != nil {
		t.Fatalf("reference Plan: %v", err)
	}
	refView := render.NewView()
	refView.SetFrame(refFrame)
	refTester := ui.NewTester(func(c *ui.Context) { refView.Draw(c) }, imgW, imgH)
	refImg := refTester.Image()
	if refImg == nil {
		t.Fatal("reference Tester.Image() returned nil")
	}
	refPix := append([]byte(nil), refImg.Pix...)

	// Submit base frame to Animator (cold start settles immediately).
	anim := render.NewAnimator(planner, render.DefaultAnimationConfig(), clock)
	if _, _, err := anim.Submit(baseState, baseMeta, baseDL); err != nil {
		t.Fatalf("Animator.Submit(base): %v", err)
	}
	for i := 0; i < 60 && anim.Active(); i++ {
		clock.Advance(16 * time.Millisecond)
		if _, _, err := anim.Step(); err != nil {
			t.Fatalf("Animator.Step(settle): %v", err)
		}
	}

	// Flush the global 64-slot metadataLRU with 75 distinct frames.
	for i := 0; i < 75; i++ {
		noiseDL := &drawlist.List{
			Version: 1,
			Seq:     uint64(100 + i),
			Width:   10,
			Height:  2,
			Layers: []drawlist.Layer{
				{
					Grid: 1, Width: 10, Height: 2,
					Clip: drawlist.Rect{X: 0, Y: 0, W: 10, H: 2},
					Items: []drawlist.Item{
						{Kind: "fill", Row: 0, Col: 0, Width: 10, Height: 2, BG: int64(0x101010 + i)},
						{Kind: "text", Row: 0, Col: i % 8, Width: 1, Height: 1, Text: "Z", FG: 0xffffff, BG: 0x101010},
					},
				},
			},
		}
		if _, err := planner.Plan(noiseDL); err != nil {
			t.Fatalf("noise Plan %d: %v", i, err)
		}
	}

	// Retrieve settled base frame via Animator.Step() and render through a fresh View.
	clock.Advance(16 * time.Millisecond)
	steppedFrame, active, err := anim.Step()
	if err != nil {
		t.Fatalf("Animator.Step after LRU flush: %v", err)
	}
	if active {
		t.Fatal("settled Animator became active unexpectedly after LRU flush")
	}

	gotView := render.NewView()
	gotView.SetFrame(steppedFrame)
	gotTester := ui.NewTester(func(c *ui.Context) { gotView.Draw(c) }, imgW, imgH)
	gotImg := gotTester.Image()
	if gotImg == nil {
		t.Fatal("got Tester.Image() returned nil")
	}

	clipX0 := int(math.Floor(float64(2 * cell.Width)))
	clipY0 := int(math.Floor(float64(1 * cell.Height)))
	clipX1 := int(math.Ceil(float64(8 * cell.Width)))
	clipY1 := int(math.Ceil(float64(3 * cell.Height)))

	// 1. Verify underline decoration (vivid red 0xff0044) is still rendered inside the clip.
	foundRedUnderline := false
	for y := clipY0; y < clipY1 && y < imgH; y++ {
		for x := clipX0; x < clipX1 && x < imgW; x++ {
			px := gotImg.RGBAAt(x, y)
			if px.R > 180 && px.R > px.G+100 {
				foundRedUnderline = true
				break
			}
		}
		if foundRedUnderline {
			break
		}
	}
	if !foundRedUnderline {
		t.Fatal("underline decoration lost after 75-frame metadataLRU flush (rowMeta evicted)")
	}

	// 2. Verify Clip did not leak outside right or below edges.
	rootBG := imagecolor.RGBA{R: 0x0a, G: 0x10, B: 0x20, A: 0xff}
	for y := clipY0; y < clipY1 && y < imgH; y++ {
		for x := clipX1 + 2; x < imgW; x++ {
			if px := gotImg.RGBAAt(x, y); px != rootBG {
				t.Fatalf("float clip leaked right at (%d,%d) after metadataLRU flush: got %+v, want %+v", x, y, px, rootBG)
			}
		}
	}
	for y := clipY1 + 2; y < imgH; y++ {
		for x := clipX0; x < clipX1 && x < imgW; x++ {
			if px := gotImg.RGBAAt(x, y); px != rootBG {
				t.Fatalf("float clip leaked below at (%d,%d) after metadataLRU flush: got %+v, want %+v", x, y, px, rootBG)
			}
		}
	}

	// 3. Rendered pixels must match reference frame byte-for-byte.
	if !bytes.Equal(gotImg.Pix, refPix) {
		t.Fatal("rendered pixels after metadataLRU flush diverge from reference frame")
	}
}
