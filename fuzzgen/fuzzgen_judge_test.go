package fuzzgen_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"nvgui/drawlist"
	"nvgui/fuzzgen"
	"nvgui/protocol"
	"nvgui/screen"
)

var validOpKinds = map[fuzzgen.OpKind]bool{
	fuzzgen.OpInput:      true,
	fuzzgen.OpResize:     true,
	fuzzgen.OpSplit:      true,
	fuzzgen.OpVSplit:     true,
	fuzzgen.OpWinCmd:     true,
	fuzzgen.OpCloseWin:   true,
	fuzzgen.OpFloatOpen:  true,
	fuzzgen.OpFloatClose: true,
	fuzzgen.OpSetHL:      true,
	fuzzgen.OpExtmark:    true,
	fuzzgen.OpRedraw:     true,
}

func TestM3FuzzgenGenerateDeterminismAndCoverage(t *testing.T) {
	// Invalid configs must fail cleanly.
	if _, err := fuzzgen.Generate(1, fuzzgen.Config{Steps: 0}); err == nil {
		t.Fatal("Generate(Steps=0) returned nil error, want error")
	}
	if _, err := fuzzgen.Generate(1, fuzzgen.Config{
		Steps: 5, InitWidth: 80, InitHeight: 24, MinWidth: 100, MaxWidth: 50, MinHeight: 16, MaxHeight: 40,
	}); err == nil {
		t.Fatal("Generate(MinWidth > MaxWidth) returned nil error, want error")
	}

	cfg := fuzzgen.Config{
		Steps:      30,
		InitWidth:  80,
		InitHeight: 24,
		MinWidth:   40,
		MaxWidth:   120,
		MinHeight:  16,
		MaxHeight:  40,
	}

	s1, err := fuzzgen.Generate(42, cfg)
	if err != nil {
		t.Fatalf("Generate(42) first call: %v", err)
	}
	s2, err := fuzzgen.Generate(42, cfg)
	if err != nil {
		t.Fatalf("Generate(42) second call: %v", err)
	}
	if s1 == nil || s2 == nil {
		t.Fatal("Generate returned nil script with nil error")
	}
	b1, err := json.Marshal(s1)
	if err != nil {
		t.Fatalf("marshal s1: %v", err)
	}
	b2, err := json.Marshal(s2)
	if err != nil {
		t.Fatalf("marshal s2: %v", err)
	}
	if !bytes.Equal(b1, b2) {
		t.Fatalf("Generate(42) is not deterministic:\nfirst:  %s\nsecond: %s", b1, b2)
	}

	sOther, err := fuzzgen.Generate(43, cfg)
	if err != nil {
		t.Fatalf("Generate(43): %v", err)
	}
	bOther, err := json.Marshal(sOther)
	if err != nil {
		t.Fatalf("marshal sOther: %v", err)
	}
	if bytes.Equal(b1, bOther) {
		t.Fatal("Generate(42) and Generate(43) produced identical scripts")
	}

	if s1.Version != 1 || s1.Seed != 42 || s1.InitWidth != 80 || s1.InitHeight != 24 {
		t.Fatalf("script header = (v=%d seed=%d %dx%d), want (1, 42, 80x24)", s1.Version, s1.Seed, s1.InitWidth, s1.InitHeight)
	}
	if len(s1.Steps) != cfg.Steps {
		t.Fatalf("len(Steps) = %d, want %d", len(s1.Steps), cfg.Steps)
	}

	seenKinds := make(map[fuzzgen.OpKind]int)
	for i, step := range s1.Steps {
		if !validOpKinds[step.Kind] {
			t.Fatalf("step %d has unknown OpKind %q", i, step.Kind)
		}
		seenKinds[step.Kind]++
		lowerKeys := strings.ToLower(step.Keys)
		for _, forbidden := range []string{":qa", ":q!", "500dd", "d500j"} {
			if strings.Contains(lowerKeys, forbidden) {
				t.Errorf("step %d contains forbidden key sequence %q in %q", i, forbidden, step.Keys)
			}
		}
		if step.Kind == fuzzgen.OpResize {
			if step.Width < cfg.MinWidth || step.Width > cfg.MaxWidth || step.Height < cfg.MinHeight || step.Height > cfg.MaxHeight {
				t.Errorf("step %d resize %dx%d out of bounds [%d..%d]x[%d..%d]",
					i, step.Width, step.Height, cfg.MinWidth, cfg.MaxWidth, cfg.MinHeight, cfg.MaxHeight)
			}
		}
		if step.Kind == fuzzgen.OpFloatClose && step.FloatTag == "" {
			t.Errorf("step %d OpFloatClose has empty FloatTag", i)
		}
	}
	if len(seenKinds) < 6 {
		t.Fatalf("30-step generated script covered only %d OpKinds (%v), want >= 6", len(seenKinds), seenKinds)
	}
}

func TestM3FuzzgenRunRealNvimAndReplayInvariants(t *testing.T) {
	if _, err := fuzzgen.Run(context.Background(), nil, 5*time.Second); err == nil {
		t.Fatal("Run(nil) returned nil error, want error")
	}
	if _, err := fuzzgen.Run(context.Background(), &fuzzgen.Script{Version: 2, InitWidth: 80, InitHeight: 24}, 5*time.Second); err == nil {
		t.Fatal("Run(Version=2) returned nil error, want error")
	}

	cfg := fuzzgen.Config{
		Steps:      10,
		InitWidth:  80,
		InitHeight: 24,
		MinWidth:   50,
		MaxWidth:   110,
		MinHeight:  16,
		MaxHeight:  36,
	}

	for _, seed := range []int64{1, 2, 3} {
		script, err := fuzzgen.Generate(seed, cfg)
		if err != nil {
			t.Fatalf("Generate(seed=%d): %v", seed, err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		res, err := fuzzgen.Run(ctx, script, 5*time.Second)
		cancel()
		if err != nil {
			t.Fatalf("Run(seed=%d): %v", seed, err)
		}
		if res == nil {
			t.Fatalf("Run(seed=%d) returned nil result", seed)
		}
		if res.Inconclusive {
			t.Fatalf("Run(seed=%d) marked inconclusive: %s", seed, res.Reason)
		}
		if res.Stalls != 0 {
			t.Fatalf("Run(seed=%d) Stalls = %d, want 0", seed, res.Stalls)
		}
		if res.Committed <= 0 {
			t.Fatalf("Run(seed=%d) Committed = %d, want > 0", seed, res.Committed)
		}
		if res.StepsExecuted != len(script.Steps) {
			t.Fatalf("Run(seed=%d) StepsExecuted = %d, want %d", seed, res.StepsExecuted, len(script.Steps))
		}
		if len(res.CanonFrames) != res.Committed {
			t.Fatalf("Run(seed=%d) len(CanonFrames)=%d != Committed=%d", seed, len(res.CanonFrames), res.Committed)
		}
		if len(res.Inbound) == 0 {
			t.Fatalf("Run(seed=%d) Inbound is empty", seed)
		}

		// Replay captured byte-0 Inbound and confirm 1:1 Canonical alignment and drawlist.Build validity.
		msgs, err := protocol.ReadMessages(res.Inbound)
		if err != nil {
			t.Fatalf("seed=%d protocol.ReadMessages(Inbound): %v", seed, err)
		}
		reducer := screen.NewReducer()
		var replayed []*screen.State
		for _, msg := range msgs {
			if msg.Type != 2 || msg.Method != "redraw" {
				continue
			}
			events, err := protocol.DecodeRedraw(msg.Args)
			if err != nil {
				t.Fatalf("seed=%d DecodeRedraw: %v", seed, err)
			}
			committed, err := reducer.Apply(events)
			if err != nil {
				t.Fatalf("seed=%d reducer.Apply: %v", seed, err)
			}
			replayed = append(replayed, committed...)
		}
		if len(replayed) != len(res.CanonFrames) {
			t.Fatalf("seed=%d replayed committed frames = %d, want %d (must align 1:1 with Inbound flushes)",
				seed, len(replayed), len(res.CanonFrames))
		}
		for i, st := range replayed {
			gotCanon, err := screen.Canonical(st)
			if err != nil {
				t.Fatalf("seed=%d frame %d Canonical: %v", seed, i, err)
			}
			if !bytes.Equal(gotCanon, res.CanonFrames[i]) {
				t.Fatalf("seed=%d frame %d CanonFrames mismatch vs replayed Inbound", seed, i)
			}
			dl, err := drawlist.Build(st)
			if err != nil {
				t.Fatalf("seed=%d frame %d drawlist.Build: %v", seed, i, err)
			}
			if dl == nil || dl.Version != 1 || dl.Seq != st.Seq {
				t.Fatalf("seed=%d frame %d invalid drawlist header: %+v", seed, i, dl)
			}
			if _, err := drawlist.JSON(dl); err != nil {
				t.Fatalf("seed=%d frame %d drawlist.JSON: %v", seed, i, err)
			}
		}
	}
}

func TestM3FuzzgenShrinkDeterministic(t *testing.T) {
	baseScript := &fuzzgen.Script{
		Version:    1,
		Seed:       77,
		InitWidth:  80,
		InitHeight: 24,
		Steps: []fuzzgen.Step{
			{Kind: fuzzgen.OpInput, Keys: "ihello<Esc>"},
			{Kind: fuzzgen.OpRedraw},
			{Kind: fuzzgen.OpSetHL, HLGroup: "M3A", FG: "#cdd6f4"},
			{Kind: fuzzgen.OpSplit},
			{Kind: fuzzgen.OpWinCmd, WinCmdArg: "w"},
			{Kind: fuzzgen.OpResize, Width: 90, Height: 28},
			{Kind: fuzzgen.OpInput, Keys: "oworld<Esc>"},
			{Kind: fuzzgen.OpFloatOpen, FloatTag: "f1", Width: 20, Height: 5, Row: 2, Col: 4, Anchor: "NW", Border: "single", ZIndex: 50},
			{Kind: fuzzgen.OpExtmark, Text: "ovl", HLGroup: "M3A"},
			{Kind: fuzzgen.OpInput, Keys: "j"},
			{Kind: fuzzgen.OpVSplit},
			{Kind: fuzzgen.OpRedraw},
			{Kind: fuzzgen.OpInput, Keys: "k"},
			{Kind: fuzzgen.OpCloseWin},
			{Kind: fuzzgen.OpFloatClose, FloatTag: "f1"},
			{Kind: fuzzgen.OpInput, Keys: "u"},
			{Kind: fuzzgen.OpResize, Width: 80, Height: 24},
			{Kind: fuzzgen.OpSetHL, HLGroup: "M3B", BG: "#1e1e2e"},
			{Kind: fuzzgen.OpRedraw},
			{Kind: fuzzgen.OpInput, Keys: "gg"},
		},
	}

	origJSON, err := json.Marshal(baseScript)
	if err != nil {
		t.Fatalf("marshal baseScript: %v", err)
	}

	// Error paths.
	if _, err := fuzzgen.Shrink(nil, fuzzgen.ShrinkConfig{MaxAttempts: 32}, func(*fuzzgen.Script) bool { return true }); err == nil {
		t.Fatal("Shrink(nil) returned nil error, want error")
	}
	if _, err := fuzzgen.Shrink(baseScript, fuzzgen.ShrinkConfig{MaxAttempts: 32}, nil); err == nil {
		t.Fatal("Shrink(nil fails) returned nil error, want error")
	}
	if _, err := fuzzgen.Shrink(baseScript, fuzzgen.ShrinkConfig{MaxAttempts: 32}, func(*fuzzgen.Script) bool { return false }); err == nil {
		t.Fatal("Shrink(non-failing initial script) returned nil error, want error")
	}

	// Case 1: Single-step failure predicate (contains OpFloatOpen and len >= 1).
	hasFloatOpen := func(s *fuzzgen.Script) bool {
		if s == nil || len(s.Steps) == 0 {
			return false
		}
		for _, st := range s.Steps {
			if st.Kind == fuzzgen.OpFloatOpen {
				return true
			}
		}
		return false
	}

	res1, err := fuzzgen.Shrink(baseScript, fuzzgen.ShrinkConfig{MaxAttempts: 256}, hasFloatOpen)
	if err != nil {
		t.Fatalf("Shrink(hasFloatOpen): %v", err)
	}
	if res1 == nil || res1.Minimal == nil {
		t.Fatal("Shrink(hasFloatOpen) returned nil result/minimal")
	}
	if res1.OriginalSteps != len(baseScript.Steps) {
		t.Errorf("OriginalSteps = %d, want %d", res1.OriginalSteps, len(baseScript.Steps))
	}
	if res1.MinimalSteps < 1 || res1.MinimalSteps >= res1.OriginalSteps || res1.MinimalSteps > 2 {
		t.Fatalf("MinimalSteps = %d (from %d), want in [1, 2]", res1.MinimalSteps, res1.OriginalSteps)
	}
	if len(res1.Minimal.Steps) != res1.MinimalSteps {
		t.Fatalf("len(Minimal.Steps) = %d != MinimalSteps = %d", len(res1.Minimal.Steps), res1.MinimalSteps)
	}
	if !hasFloatOpen(res1.Minimal) {
		t.Fatal("shrunk minimal script no longer satisfies fails predicate")
	}
	if res1.Attempts <= 0 || res1.Attempts > 256 {
		t.Errorf("Attempts = %d, want in (0, 256]", res1.Attempts)
	}

	// Ensure caller's original Script was never mutated.
	afterJSON, _ := json.Marshal(baseScript)
	if !bytes.Equal(origJSON, afterJSON) {
		t.Fatal("Shrink mutated caller's original Script")
	}

	// Case 2: Two-step ordered predicate (OpFloatOpen "f1" followed by OpFloatClose "f1") + determinism check.
	hasOpenClosePair := func(s *fuzzgen.Script) bool {
		if s == nil {
			return false
		}
		opened := false
		for _, st := range s.Steps {
			if st.Kind == fuzzgen.OpFloatOpen && st.FloatTag == "f1" {
				opened = true
			} else if opened && st.Kind == fuzzgen.OpFloatClose && st.FloatTag == "f1" {
				return true
			}
		}
		return false
	}

	pairA, err := fuzzgen.Shrink(baseScript, fuzzgen.ShrinkConfig{MaxAttempts: 256}, hasOpenClosePair)
	if err != nil {
		t.Fatalf("Shrink(pair A): %v", err)
	}
	pairB, err := fuzzgen.Shrink(baseScript, fuzzgen.ShrinkConfig{MaxAttempts: 256}, hasOpenClosePair)
	if err != nil {
		t.Fatalf("Shrink(pair B): %v", err)
	}
	if pairA.Minimal == nil || pairB.Minimal == nil {
		t.Fatal("Shrink(pair) returned nil Minimal")
	}
	if pairA.MinimalSteps < 2 || pairA.MinimalSteps > 3 {
		t.Fatalf("pair MinimalSteps = %d, want in [2, 3]", pairA.MinimalSteps)
	}
	if !hasOpenClosePair(pairA.Minimal) {
		t.Fatal("pair Minimal no longer satisfies fails predicate")
	}
	minA, _ := json.Marshal(pairA.Minimal)
	minB, _ := json.Marshal(pairB.Minimal)
	if !bytes.Equal(minA, minB) {
		t.Fatalf("Shrink is not deterministic:\nA: %s\nB: %s", minA, minB)
	}
}
