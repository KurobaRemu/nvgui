package render

import (
	"math"
	"reflect"
	"testing"
	"time"

	"nvgui/drawlist"
	"nvgui/renderlist"
	"nvgui/screen"
	"nvgui/text"
)

func TestAnimatorSetConfigRecomputesSettledFrame(t *testing.T) {
	resolver, err := text.NewResolver(text.Config{GUIFont: "Menlo:h14", Scale: 1})
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	planner := NewPlanner(resolver)
	list := &drawlist.List{
		Version: 1,
		Seq:     552,
		Width:   10,
		Height:  2,
		Layers: []drawlist.Layer{{
			Grid: 1, Width: 10, Height: 2,
			Clip: drawlist.Rect{X: 0, Y: 0, W: 10, H: 2},
			Items: []drawlist.Item{
				{Kind: "fill", Row: 0, Col: 0, Width: 10, Height: 2, BG: 0x101010},
				{Kind: "text", Row: 0, Col: 0, Width: 1, Height: 1, Text: "A", FG: 0xffffff, BG: 0x101010},
			},
		}},
	}
	animator := NewAnimator(planner, DisabledAnimationConfig(), NewManualClock(time.Unix(1_700_000_000, 0).UTC()))
	if _, _, err := animator.Submit(&screen.State{}, &screen.FrameMeta{Seq: 552}, list); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	impl := animator.impl
	if impl.lastList == nil {
		t.Fatal("Submit did not cache a deep copy of the draw list")
	}
	wantFrame, err := planner.Plan(impl.lastList)
	if err != nil {
		t.Fatalf("Plan cached list: %v", err)
	}
	wantMetas := metadataFor(wantFrame)
	impl.settledFrame = renderlist.Frame{}
	impl.settledMetas = nil

	animator.SetConfig(DefaultAnimationConfig())
	if !reflect.DeepEqual(impl.settledFrame, wantFrame) {
		t.Fatalf("SetConfig settledFrame = %+v, want %+v", impl.settledFrame, wantFrame)
	}
	if !reflect.DeepEqual(impl.settledMetas, wantMetas) {
		t.Fatalf("SetConfig settledMetas = %+v, want %+v", impl.settledMetas, wantMetas)
	}
	if got := metadataFor(impl.settledFrame); !reflect.DeepEqual(got, wantMetas) {
		t.Fatalf("SetConfig metadataLRU = %+v, want %+v", got, wantMetas)
	}
}

func TestAnimatorStepBeforeSubmitDoesNotWriteMetadata(t *testing.T) {
	resolver, err := text.NewResolver(text.Config{GUIFont: "Menlo:h14", Scale: 1})
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	clock := NewManualClock(time.Unix(1_700_000_000, 0).UTC())
	animator := NewAnimator(NewPlanner(resolver), DefaultAnimationConfig(), clock)
	frame := renderlist.Frame{Version: 1, Seq: 992, Commands: []renderlist.Command{{Op: "fill", BG: 7}}}
	key := metadataKey(frame.Seq, frame.Commands)
	clearMetadataLRU(t)
	clock.Advance(16 * time.Millisecond)
	gotFrame, active, err := animator.Step()
	if err != nil || active || !reflect.DeepEqual(gotFrame, renderlist.Frame{}) {
		t.Fatalf("Step before Submit = (%+v, %v, %v), want zero frame and inactive", gotFrame, active, err)
	}
	if metadataIndex[key] != nil {
		t.Fatal("Step before Submit wrote metadataLRU")
	}
}

func TestAnimatorStepZeroDeltaKeepsActiveState(t *testing.T) {
	resolver, err := text.NewResolver(text.Config{GUIFont: "Menlo:h14", Scale: 1})
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	clock := NewManualClock(time.Unix(1_700_000_000, 0).UTC())
	animator := NewAnimator(NewPlanner(resolver), DefaultAnimationConfig(), clock)
	impl := animator.impl
	impl.initialized = true
	impl.active = true
	impl.lastTime = clock.Now()
	frame := renderlist.Frame{Version: 1, Seq: 993, Commands: []renderlist.Command{{Op: "fill", BG: 8}}}
	impl.settledFrame = frame
	impl.settledMetas = []rowMeta{{grid: 1}}
	storeMetadata(frame, impl.settledMetas)
	clearMetadataLRU(t)
	lastTime := impl.lastTime

	gotFrame, active, err := animator.Step()
	if err != nil || !active || !reflect.DeepEqual(gotFrame, frame) {
		t.Fatalf("Step dt<=0 = (%+v, %v, %v), want settled frame and active=true", gotFrame, active, err)
	}
	if !impl.active {
		t.Fatal("Step dt<=0 cleared active")
	}
	if !impl.lastTime.Equal(lastTime) {
		t.Fatalf("Step dt<=0 moved lastTime from %v to %v", lastTime, impl.lastTime)
	}
	key := metadataKey(frame.Seq, frame.Commands)
	if got := metadataFor(gotFrame); !reflect.DeepEqual(got, impl.settledMetas) {
		t.Fatalf("metadataFor(gotFrame) = %+v, want %+v", got, impl.settledMetas)
	}
	if metadataIndex[key] == nil {
		t.Fatal("Step dt<=0 did not refresh metadataLRU")
	}
	clock.Advance(16 * time.Millisecond)
	if _, _, err := animator.Step(); err != nil {
		t.Fatalf("Step after positive dt: %v", err)
	}
}

func TestSpringNonFiniteStateResetsWithoutMutationGuard(t *testing.T) {
	spring := Spring{Position: float32(math.NaN()), Velocity: 2}
	if spring.Update(0, 0.15) || spring.Position != 0 || spring.Velocity != 0 || spring.Active() {
		t.Fatalf("non-finite spring state survived dt=0: %+v", spring)
	}
}

func clearMetadataLRU(t *testing.T) {
	t.Helper()
	metadataMu.Lock()
	defer metadataMu.Unlock()
	metadataLRU.Init()
	clear(metadataIndex)
}
