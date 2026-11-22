package render_test

import (
	"bytes"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"nvgui/drawlist"
	"nvgui/render"
	"nvgui/renderlist"
	"nvgui/screen"
)

func m4PriorFrameCommands(frame renderlist.Frame, op string) []renderlist.Command {
	var commands []renderlist.Command
	for _, command := range frame.Commands {
		if command.Op == op {
			commands = append(commands, command)
		}
	}
	return commands
}

func m4PriorFrameFinite(t *testing.T, frame renderlist.Frame) {
	t.Helper()
	for index, command := range frame.Commands {
		for _, value := range []float32{command.X, command.Y, command.W, command.H} {
			if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
				t.Fatalf("command %d has non-finite geometry %+v", index, command)
			}
		}
	}
}

func m4PriorCursorQuadFinite(t *testing.T, quad [4]ui.Point) {
	t.Helper()
	for _, point := range quad {
		if math.IsNaN(float64(point.X)) || math.IsInf(float64(point.X), 0) ||
			math.IsNaN(float64(point.Y)) || math.IsInf(float64(point.Y), 0) {
			t.Fatalf("non-finite cursor quad %+v", quad)
		}
	}
}

func m4PriorSettle(t *testing.T, animator *render.Animator, clock *render.ManualClock) renderlist.Frame {
	t.Helper()
	for step := 0; step < 200; step++ {
		clock.Advance(16 * time.Millisecond)
		frame, active, err := animator.Step()
		if err != nil {
			t.Fatalf("Step %d: %v", step, err)
		}
		if !active {
			return frame
		}
	}
	t.Fatal("animation did not converge within 200 steps")
	return renderlist.Frame{}
}

func TestPriorBug_Neovide850_CursorAnimSettledEqualsDisabledAndNoScreenMutation(t *testing.T) {
	resolver := mustResolver(t)
	planner := render.NewPlanner(resolver)
	clock := render.NewManualClock(time.Unix(1_700_000_000, 0).UTC())
	animator := render.NewAnimator(planner, render.DefaultAnimationConfig(), clock)
	states := []*screen.State{}
	lists := []*drawlist.List{}
	canonical := [][]byte{}
	positions := [][2]int{{2, 2}, {3, 7}, {14, 31}, {6, 12}, {18, 35}}
	for index, position := range positions {
		state, list := m4CursorFixture(uint64(index+1), position[0], position[1], "normal", nil)
		states = append(states, state)
		lists = append(lists, list)
		data, err := screen.Canonical(state)
		if err != nil {
			t.Fatalf("Canonical %d: %v", index, err)
		}
		canonical = append(canonical, data)
	}
	for index := range states {
		frame, active, err := animator.Submit(states[index], &screen.FrameMeta{Seq: uint64(index + 1)}, lists[index])
		if err != nil {
			t.Fatalf("Submit %d: %v", index, err)
		}
		if index == 1 && !active {
			t.Fatal("multi-step animation did not produce an active intermediate frame")
		}
		m4PriorFrameFinite(t, frame)
		if quad, ok := animator.CursorQuad(); ok {
			m4PriorCursorQuadFinite(t, quad)
		}
		if index > 0 {
			clock.Advance(16 * time.Millisecond)
			intermediate, active, err := animator.Step()
			if err != nil {
				t.Fatalf("Step %d: %v", index, err)
			}
			if active {
				staticCursor := m4PriorFrameCommands(frame, "cursor")
				animatedCursor := m4PriorFrameCommands(intermediate, "cursor")
				if len(staticCursor) == 1 && len(animatedCursor) == 1 && reflect.DeepEqual(staticCursor[0], animatedCursor[0]) {
					t.Fatalf("jump %d produced no intermediate cursor geometry change", index)
				}
			}
		}
	}
	m4PriorSettle(t, animator, clock)
	settled := animatorStepLastFrame(t, animator)
	disabledCfg := render.DefaultAnimationConfig()
	disabledCfg.CursorEnabled = false
	disabledCfg.ScrollEnabled = false
	disabledAnimator := render.NewAnimator(planner, disabledCfg, clock)
	disabled, active, err := disabledAnimator.Submit(states[len(states)-1], &screen.FrameMeta{Seq: uint64(len(states))}, lists[len(lists)-1])
	if err != nil || active {
		t.Fatalf("disabled Submit = (active=%v, err=%v)", active, err)
	}
	if !reflect.DeepEqual(settled, disabled) {
		t.Fatalf("settled frame differs from disabled-animation frame")
	}
	for index := range states {
		data, err := screen.Canonical(states[index])
		if err != nil {
			t.Fatalf("Canonical after Submit %d: %v", index, err)
		}
		if !bytes.Equal(data, canonical[index]) {
			t.Fatalf("screen.Canonical changed for state %d", index)
		}
	}
}

func animatorStepLastFrame(t *testing.T, animator *render.Animator) renderlist.Frame {
	t.Helper()
	frame, active, err := animator.Step()
	if err != nil || active {
		t.Fatalf("final Step = (active=%v, err=%v)", active, err)
	}
	return frame
}

func TestPriorBug_Neovim279_OffScreenAndClippedCursorNoPanicOrDrift(t *testing.T) {
	resolver := mustResolver(t)
	cell := resolver.Cell()
	planner := render.NewPlanner(resolver)
	clock := render.NewManualClock(time.Unix(1_700_000_000, 0).UTC())
	animator := render.NewAnimator(planner, render.DefaultAnimationConfig(), clock)
	visibleState, visibleList := m4CursorFixture(1, 2, 2, "normal", nil)
	if _, _, err := animator.Submit(visibleState, &screen.FrameMeta{Seq: 1}, visibleList); err != nil {
		t.Fatalf("Submit visible: %v", err)
	}
	for index, position := range [][2]int{{-3, -5}, {500, 500}, {5, -2}, {5, 500}} {
		state, list := m4CursorFixture(uint64(index+2), position[0], position[1], "normal", nil)
		frame, active, err := animator.Submit(state, &screen.FrameMeta{Seq: uint64(index + 2)}, list)
		if err != nil {
			t.Fatalf("Submit offscreen %+v: %v", position, err)
		}
		m4PriorFrameFinite(t, frame)
		if len(m4PriorFrameCommands(frame, "cursor")) != 0 {
			t.Fatalf("offscreen %+v emitted cursor command", position)
		}
		if quad, ok := animator.CursorQuad(); ok {
			t.Fatalf("offscreen %+v produced visible quad %+v", position, quad)
		}
		clock.Advance(16 * time.Millisecond)
		stepped, active, err := animator.Step()
		if err != nil {
			t.Fatalf("Step offscreen %+v: %v", position, err)
		}
		m4PriorFrameFinite(t, stepped)
		if active {
			t.Fatalf("offscreen %+v remained animated", position)
		}
	}
	clippedState, clippedList := m4CursorFixture(20, 5, 10, "normal", nil)
	for layerIndex := range clippedList.Layers {
		clippedList.Layers[layerIndex].Clip = drawlist.Rect{X: 0, Y: 0, W: 4, H: 4}
	}
	clippedFrame, _, err := animator.Submit(clippedState, &screen.FrameMeta{Seq: 20}, clippedList)
	if err != nil {
		t.Fatalf("Submit clipped: %v", err)
	}
	m4PriorFrameFinite(t, clippedFrame)
	if len(m4PriorFrameCommands(clippedFrame, "cursor")) != 0 {
		t.Fatal("clipped cursor emitted a command")
	}
	returnState, returnList := m4CursorFixture(21, 6, 12, "normal", nil)
	returnFrame, active, err := animator.Submit(returnState, &screen.FrameMeta{Seq: 21}, returnList)
	if err != nil {
		t.Fatalf("Submit reentry: %v", err)
	}
	if active || len(m4PriorFrameCommands(returnFrame, "cursor")) != 1 {
		t.Fatalf("return to visible grid = (active=%v, cursor=%d), want immediate visible snap",
			active, len(m4PriorFrameCommands(returnFrame, "cursor")))
	}
	clock.Advance(16 * time.Millisecond)
	resumeState, resumeList := m4CursorFixture(22, 9, 18, "normal", nil)
	_, active, err = animator.Submit(resumeState, &screen.FrameMeta{Seq: 22}, resumeList)
	if err != nil || !active {
		t.Fatalf("resume animation = (active=%v, err=%v)", active, err)
	}
	for step := 0; step < 200; step++ {
		clock.Advance(16 * time.Millisecond)
		frame, active, err := animator.Step()
		if err != nil {
			t.Fatalf("Step reentry %d: %v", step, err)
		}
		m4PriorFrameFinite(t, frame)
		if quad, ok := animator.CursorQuad(); ok {
			m4PriorCursorQuadFinite(t, quad)
		}
		if !active {
			break
		}
	}
	if animator.Active() {
		t.Fatal("resumed animation did not converge")
	}
	quad, ok := animator.CursorQuad()
	if !ok {
		t.Fatal("CursorQuad ok=false after returning onscreen")
	}
	wantX := float32(18) * cell.Width
	wantY := float32(9) * cell.Height
	minX, minY, maxX, maxY := m4QuadBounds(quad)
	if !approxEqual(minX, wantX, 0.75) || !approxEqual(minY, wantY, 0.75) ||
		!approxEqual(maxX, wantX+cell.Width, 0.75) || !approxEqual(maxY, wantY+cell.Height, 0.75) {
		t.Fatalf("recovered quad bounds = [%v,%v]x[%v,%v], want cell (9,18)", minX, maxX, minY, maxY)
	}
}

func TestPriorBug_Neovide341_CursorCommandLineSnapDefaultAndToggle(t *testing.T) {
	resolver := mustResolver(t)
	cell := resolver.Cell()
	planner := render.NewPlanner(resolver)

	runDefault := func() {
		clock := render.NewManualClock(time.Unix(1_700_000_000, 0).UTC())
		animator := render.NewAnimator(planner, render.DefaultAnimationConfig(), clock)
		state1, list1 := m4CursorFixture(1, 2, 2, "normal", nil)
		if _, _, err := animator.Submit(state1, &screen.FrameMeta{Seq: 1}, list1); err != nil {
			t.Fatalf("Submit normal: %v", err)
		}
		state2, list2 := m4CursorFixture(2, 19, 10, "cmdline_normal", nil)
		if _, active, err := animator.Submit(state2, &screen.FrameMeta{Seq: 2}, list2); err != nil || active {
			t.Fatalf("enter cmdline = (active=%v, err=%v), want snap", active, err)
		}
		quad, ok := animator.CursorQuad()
		if !ok {
			t.Fatal("CursorQuad ok=false in cmdline_normal")
		}
		m4AssertQuadMatchesCell(t, quad, 19, 10, cell)
		state3, list3 := m4CursorFixture(3, 8, 22, "normal", nil)
		if _, active, err := animator.Submit(state3, &screen.FrameMeta{Seq: 3}, list3); err != nil || active {
			t.Fatalf("leave cmdline = (active=%v, err=%v), want snap", active, err)
		}
		msg := &screen.MsgPlacement{Grid: 3, Row: 18, SepChar: " ", ZIndex: 200, CompIndex: 10}
		state4, list4 := m4CursorFixture(4, 0, 5, "normal", msg)
		if _, active, err := animator.Submit(state4, &screen.FrameMeta{Seq: 4}, list4); err != nil || active {
			t.Fatalf("msg_set_pos grid = (active=%v, err=%v), want snap", active, err)
		}
	}
	runDefault()
	insertClock := render.NewManualClock(time.Unix(1_700_000_000, 0).UTC())
	insertCfg := render.DefaultAnimationConfig()
	insertCfg.CursorAnimateInsertMode = false
	insertAnimator := render.NewAnimator(planner, insertCfg, insertClock)
	insertState, insertList := m4CursorFixture(10, 2, 2, "normal", nil)
	if _, _, err := insertAnimator.Submit(insertState, &screen.FrameMeta{Seq: 10}, insertList); err != nil {
		t.Fatalf("Submit before insert: %v", err)
	}
	insertTarget, insertTargetList := m4CursorFixture(11, 12, 20, "insert", nil)
	if _, active, err := insertAnimator.Submit(insertTarget, &screen.FrameMeta{Seq: 11}, insertTargetList); err != nil || active {
		t.Fatalf("insert-mode snap = (active=%v, err=%v)", active, err)
	}

	cfg := render.DefaultAnimationConfig()
	cfg.CursorAnimateCommandLine = true
	clock := render.NewManualClock(time.Unix(1_700_000_000, 0).UTC())
	animator := render.NewAnimator(planner, cfg, clock)
	state1, list1 := m4CursorFixture(1, 2, 2, "normal", nil)
	if _, _, err := animator.Submit(state1, &screen.FrameMeta{Seq: 1}, list1); err != nil {
		t.Fatalf("Submit normal: %v", err)
	}
	state2, list2 := m4CursorFixture(2, 19, 10, "cmdline_normal", nil)
	_, active, err := animator.Submit(state2, &screen.FrameMeta{Seq: 2}, list2)
	if err != nil || !active {
		t.Fatalf("cmdline animation with toggle = (active=%v, err=%v)", active, err)
	}
	clock.Advance(16 * time.Millisecond)
	frame, active, err := animator.Step()
	if err != nil || !active {
		t.Fatalf("cmdline Step = (active=%v, err=%v)", active, err)
	}
	m4PriorFrameFinite(t, frame)
	if quad, ok := animator.CursorQuad(); !ok {
		t.Fatal("CursorQuad ok=false during cmdline animation")
	} else {
		m4PriorCursorQuadFinite(t, quad)
	}
}

func TestPriorBug_Neovim25980_BusyStartSuppressesCursorCmdlineFlicker(t *testing.T) {
	resolver := mustResolver(t)
	cell := resolver.Cell()
	planner := render.NewPlanner(resolver)
	clock := render.NewManualClock(time.Unix(1_700_000_000, 0).UTC())
	cfg := render.DefaultAnimationConfig()
	cfg.CursorAnimateCommandLine = true
	animator := render.NewAnimator(planner, cfg, clock)

	state1, list1 := m4CursorFixture(1, 2, 2, "normal", nil)
	if _, _, err := animator.Submit(state1, &screen.FrameMeta{Seq: 1, Busy: false}, list1); err != nil {
		t.Fatalf("Submit normal: %v", err)
	}
	busyPositions := [][2]int{{19, 30}, {18, 5}, {19, 38}}
	for index, position := range busyPositions {
		state, list := m4CursorFixture(uint64(index+2), position[0], position[1], "normal", nil)
		frame, active, err := animator.Submit(state, &screen.FrameMeta{Seq: uint64(index + 2), Busy: true}, list)
		if err != nil {
			t.Fatalf("Submit busy %d: %v", index, err)
		}
		if active || animator.Active() {
			t.Fatalf("busy frame %d started cursor animation", index)
		}
		if len(m4PriorFrameCommands(frame, "cursor")) != 0 {
			t.Fatalf("busy frame %d emitted cmdline cursor", index)
		}
		if quad, ok := animator.CursorQuad(); ok {
			t.Fatalf("busy frame %d exposed quad %+v", index, quad)
		}
		clock.Advance(16 * time.Millisecond)
		stepped, active, err := animator.Step()
		if err != nil || active {
			t.Fatalf("busy Step %d = (active=%v, err=%v)", index, active, err)
		}
		if len(m4PriorFrameCommands(stepped, "cursor")) != 0 {
			t.Fatalf("busy Step %d emitted a cursor", index)
		}
		if quad, ok := animator.CursorQuad(); ok {
			m4PriorCursorQuadFinite(t, quad)
			_, minY, _, _ := m4QuadBounds(quad)
			if minY > 15*cell.Height {
				t.Fatalf("busy Step %d exposed cmdline quad %+v", index, quad)
			}
		}
	}
	state4, list4 := m4CursorFixture(5, 8, 15, "normal", nil)
	frame, active, err := animator.Submit(state4, &screen.FrameMeta{Seq: 5, Busy: false}, list4)
	if err != nil || active {
		t.Fatalf("recovery Submit = (active=%v, err=%v), want direct snap", active, err)
	}
	recoveryCursor := m4PriorFrameCommands(frame, "cursor")
	if len(recoveryCursor) != 1 {
		t.Fatalf("recovery cursor commands = %d, want 1", len(recoveryCursor))
	}
	wantX, wantY := float32(15)*cell.Width, float32(8)*cell.Height
	if !approxEqual(recoveryCursor[0].X, wantX, 0.001) || !approxEqual(recoveryCursor[0].Y, wantY, 0.001) {
		t.Fatalf("recovery cursor = (%v,%v), want (%v,%v)", recoveryCursor[0].X, recoveryCursor[0].Y, wantX, wantY)
	}
	quad, ok := animator.CursorQuad()
	if !ok {
		t.Fatal("CursorQuad ok=false after recovery")
	}
	m4AssertQuadMatchesCell(t, quad, 8, 15, cell)
}
