package fuzzgen_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"nvgui/drawlist"
	"nvgui/fuzzgen"
	"nvgui/protocol"
	"nvgui/screen"
)

func TestGenerateDefaultsAndValidation(t *testing.T) {
	script, err := fuzzgen.Generate(9, fuzzgen.Config{Steps: 20})
	if err != nil {
		t.Fatalf("Generate(defaults): %v", err)
	}
	if script.InitWidth != 80 || script.InitHeight != 24 || script.Version != 1 {
		t.Fatalf("unexpected script header: %+v", script)
	}
	if len(script.Steps) != 20 || len(coveredKindSet(script.Steps)) < 6 {
		t.Fatalf("unexpected coverage: steps=%d kinds=%v", len(script.Steps), coveredKindSet(script.Steps))
	}
	for _, step := range script.Steps {
		if step.Kind == fuzzgen.OpResize &&
			(step.Width < 40 || step.Width > 140 || step.Height < 16 || step.Height > 50) {
			t.Fatalf("resize outside defaults: %+v", step)
		}
	}
	for _, cfg := range []fuzzgen.Config{
		{Steps: 0},
		{Steps: 1, InitWidth: -1},
		{Steps: 1, InitHeight: -1},
		{Steps: 1, MinWidth: 100, MaxWidth: 50},
		{Steps: 1, MinHeight: 40, MaxHeight: 16},
		{Steps: 1, MinWidth: -1},
		{Steps: 1, MaxHeight: -1},
	} {
		if _, err := fuzzgen.Generate(1, cfg); err == nil {
			t.Fatalf("Generate(%+v) unexpectedly succeeded", cfg)
		}
	}
}

func TestGenerateDeterminismAndSmallStepsVaryBySeed(t *testing.T) {
	cfg := fuzzgen.Config{Steps: 10, MinWidth: 1, MinHeight: 3, MaxWidth: 3, MaxHeight: 5}
	first, err := fuzzgen.Generate(1, cfg)
	if err != nil {
		t.Fatalf("Generate(1): %v", err)
	}
	repeat, err := fuzzgen.Generate(1, cfg)
	if err != nil {
		t.Fatalf("Generate(1) repeat: %v", err)
	}
	left, _ := json.Marshal(first)
	repeatJSON, _ := json.Marshal(repeat)
	if !bytes.Equal(left, repeatJSON) {
		t.Fatal("Generate is not deterministic for the same seed")
	}
	different := false
	for seed := int64(2); seed < 20; seed++ {
		other, err := fuzzgen.Generate(seed, cfg)
		if err != nil {
			t.Fatalf("Generate(%d): %v", seed, err)
		}
		right, _ := json.Marshal(other)
		if !bytes.Equal(left, right) {
			different = true
		}
		if len(coveredKindSet(other.Steps)) < 2 {
			t.Fatalf("Generate(%d) small script has only %v", seed, coveredKindSet(other.Steps))
		}
	}
	if !different {
		t.Fatal("10-step scripts did not vary across seeds")
	}
	if cfg.MinWidth != 1 || cfg.MinHeight != 3 {
		t.Fatal("test config mutated")
	}
}

func TestGenerateTinyBoundsAndWindowModel(t *testing.T) {
	for seed := int64(0); seed < 50; seed++ {
		script, err := fuzzgen.Generate(seed, fuzzgen.Config{
			Steps: 100, InitWidth: 1, InitHeight: 3,
			MinWidth: 1, MaxWidth: 3, MinHeight: 3, MaxHeight: 5,
		})
		if err != nil {
			t.Fatalf("Generate(%d): %v", seed, err)
		}
		windows, widths, heights := 1, 1, 3
		floats := map[string]bool{}
		for index, step := range script.Steps {
			switch step.Kind {
			case fuzzgen.OpResize:
				widths, heights = step.Width, step.Height
				maxWindows := maxInt(1, minInt((heights-2)/2, (widths-2)/2))
				if windows > maxWindows {
					windows = maxWindows
				}
			case fuzzgen.OpSplit, fuzzgen.OpVSplit:
				maxWindows := maxInt(1, minInt((heights-2)/2, (widths-2)/2))
				if windows >= maxWindows {
					t.Fatalf("seed %d step %d splits beyond safe window count %d", seed, index, maxWindows)
				}
				windows++
			case fuzzgen.OpCloseWin:
				if windows <= 1 {
					t.Fatalf("seed %d step %d closes the final window", seed, index)
				}
				windows--
			case fuzzgen.OpFloatOpen:
				if step.Width < 1 || step.Height < 1 || step.Width > widths || step.Height > heights ||
					step.Row < 0 || step.Col < 0 || step.Row+step.Height > heights || step.Col+step.Width > widths {
					t.Fatalf("seed %d step %d float %+v exceeds %dx%d", seed, index, step, widths, heights)
				}
				floats[step.FloatTag] = true
			case fuzzgen.OpFloatClose:
				if !floats[step.FloatTag] {
					t.Fatalf("seed %d step %d closes unknown float %q", seed, index, step.FloatTag)
				}
				delete(floats, step.FloatTag)
			case fuzzgen.OpWinCmd:
				if !slicesContains([]string{"h", "j", "k", "l", "w"}, step.WinCmdArg) {
					t.Fatalf("seed %d step %d generated invalid wincmd %q", seed, index, step.WinCmdArg)
				}
			}
		}
	}
}

func TestGenerateVocabularyCoverageAndInsertMode(t *testing.T) {
	for seed := int64(0); seed < 100; seed++ {
		script, err := fuzzgen.Generate(seed, fuzzgen.Config{
			Steps: 200, InitWidth: 80, InitHeight: 24,
			MinWidth: 40, MaxWidth: 140, MinHeight: 16, MaxHeight: 50,
		})
		if err != nil {
			t.Fatalf("Generate(%d): %v", seed, err)
		}
		if !hasVocabulary(script.Steps) {
			t.Fatalf("seed %d does not cover fixed vocabulary", seed)
		}
		zindexes := map[int]bool{}
		windows := 1
		floats := map[string]bool{}
		insert := false
		textInInsert := false
		for index, step := range script.Steps {
			switch step.Kind {
			case fuzzgen.OpSplit, fuzzgen.OpVSplit:
				windows++
			case fuzzgen.OpCloseWin:
				if windows <= 1 {
					t.Fatalf("step %d closes the final normal window", index)
				}
				windows--
			case fuzzgen.OpFloatOpen:
				zindexes[step.ZIndex] = true
				floats[step.FloatTag] = true
			case fuzzgen.OpFloatClose:
				if step.FloatTag == "" || !floats[step.FloatTag] {
					t.Fatalf("step %d closes unknown float %q", index, step.FloatTag)
				}
				delete(floats, step.FloatTag)
			case fuzzgen.OpInput:
				wasInsert := insert
				switch {
				case step.Keys == "<Esc>":
					insert = false
				case step.Keys == "i" || step.Keys == "o" || step.Keys == "<Esc>i":
					insert = true
				case containsAny(step.Keys, []string{"世界", "你好", "e\u0301", "🌍", "┌─┐╭╮", "hello", "nvgui", "ascii text"}):
					if wasInsert {
						textInInsert = true
					}
				}
			}
		}
		for _, zindex := range []int{30, 50, 80} {
			if !zindexes[zindex] {
				t.Errorf("zindex %d was never generated", zindex)
			}
		}
		if !textInInsert {
			t.Fatal("multibyte text was never generated inside insert mode")
		}
	}
}

func TestGenerateRejectsForbiddenSequences(t *testing.T) {
	for seed := int64(0); seed < 50; seed++ {
		script, err := fuzzgen.Generate(seed, fuzzgen.Config{Steps: 30})
		if err != nil {
			t.Fatalf("Generate(%d): %v", seed, err)
		}
		for index, step := range script.Steps {
			for _, forbidden := range []string{":qa", ":q!", "500dd", "d500j"} {
				if strings.Contains(strings.ToLower(step.Keys), forbidden) {
					t.Fatalf("seed %d step %d has forbidden keys %q", seed, index, step.Keys)
				}
			}
		}
	}
}

func TestRunAllKindsReplayByteZeroInbound(t *testing.T) {
	before := embedNvimPIDs(t)
	script := allKindsScript()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	result, err := fuzzgen.Run(ctx, script, 5*time.Second)
	if err != nil {
		t.Fatalf("Run(all kinds): %v", err)
	}
	if result.Inconclusive || result.StepsExecuted != len(script.Steps) || result.Committed <= 0 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if result.Stalls != 0 {
		t.Fatalf("Stalls = %d, want 0", result.Stalls)
	}
	if len(coveredKindSet(script.Steps)) != 11 {
		t.Fatalf("all-kinds script covers %d kinds, want 11", len(coveredKindSet(script.Steps)))
	}
	assertReplayAlignment(t, result)
	assertNoNewEmbeddedNvim(t, before)
}

func TestRunGeneratedScriptReplayByteZeroInbound(t *testing.T) {
	before := embedNvimPIDs(t)
	script, err := fuzzgen.Generate(40, fuzzgen.Config{
		Steps: 30, InitWidth: 80, InitHeight: 24,
		MinWidth: 50, MaxWidth: 110, MinHeight: 16, MaxHeight: 36,
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	result, err := fuzzgen.Run(ctx, script, 5*time.Second)
	if err != nil {
		t.Fatalf("Run(generated): %v", err)
	}
	if result.Inconclusive || result.StepsExecuted != len(script.Steps) {
		t.Fatalf("unexpected result: %+v reason=%s", result, result.Reason)
	}
	if result.Stalls != 0 {
		t.Fatalf("Stalls = %d, want 0", result.Stalls)
	}
	assertReplayAlignment(t, result)
	assertNoNewEmbeddedNvim(t, before)
}

func TestRunOptionalEmptyFieldsAndTinyBounds(t *testing.T) {
	before := embedNvimPIDs(t)
	script := &fuzzgen.Script{
		Version: 1, Seed: 101, InitWidth: 1, InitHeight: 3,
		Steps: []fuzzgen.Step{
			{Kind: fuzzgen.OpSetHL, HLGroup: "M3Empty"},
			{Kind: fuzzgen.OpFloatOpen, FloatTag: "empty", Width: 1, Height: 1},
			{Kind: fuzzgen.OpRedraw},
			{Kind: fuzzgen.OpFloatClose, FloatTag: "empty"},
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result, err := fuzzgen.Run(ctx, script, 5*time.Second)
	if err != nil {
		t.Fatalf("Run(empty optional fields): %v", err)
	}
	if result.Inconclusive {
		t.Fatalf("Run(empty optional fields) inconclusive: %s", result.Reason)
	}
	assertReplayAlignment(t, result)
	assertNoNewEmbeddedNvim(t, before)
}

func TestRunCanceledContextIsInconclusiveAndLeavesNoNvim(t *testing.T) {
	before := embedNvimPIDs(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	script := &fuzzgen.Script{
		Version: 1, Seed: 102, InitWidth: 80, InitHeight: 24,
		Steps: []fuzzgen.Step{{Kind: fuzzgen.OpRedraw}},
	}
	result, err := fuzzgen.Run(ctx, script, 5*time.Second)
	if err != nil {
		t.Fatalf("Run(canceled): %v", err)
	}
	if !result.Inconclusive {
		t.Fatalf("Run(canceled) = %+v, want Inconclusive", result)
	}
	assertNoNewEmbeddedNvim(t, before)

	longScript := &fuzzgen.Script{
		Version: 1, Seed: 103, InitWidth: 80, InitHeight: 24,
		Steps: make([]fuzzgen.Step, 100),
	}
	for index := range longScript.Steps {
		longScript.Steps[index] = fuzzgen.Step{Kind: fuzzgen.OpRedraw}
	}
	ctx, cancel = context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	result, err = fuzzgen.Run(ctx, longScript, 5*time.Second)
	if err != nil {
		t.Fatalf("Run(interrupted): %v", err)
	}
	if !result.Inconclusive {
		t.Fatalf("Run(interrupted) = %+v, want Inconclusive", result)
	}
	assertNoNewEmbeddedNvim(t, before)
}

func TestRunValidatesArgumentsAndReportsUnexpectedClose(t *testing.T) {
	for _, script := range []*fuzzgen.Script{
		nil,
		{Version: 2, InitWidth: 80, InitHeight: 24},
		{Version: 1, InitWidth: 0, InitHeight: 24},
		{Version: 1, InitWidth: 80, InitHeight: -1},
	} {
		if _, err := fuzzgen.Run(context.Background(), script, time.Second); err == nil {
			t.Fatalf("Run(%+v) unexpectedly succeeded", script)
		}
	}
	if _, err := fuzzgen.Run(nil, &fuzzgen.Script{Version: 1, InitWidth: 80, InitHeight: 24}, time.Second); err == nil {
		t.Fatal("Run(nil context) unexpectedly succeeded")
	}
}

func TestRunReportsStepExecutionErrorWithoutPanic(t *testing.T) {
	before := embedNvimPIDs(t)
	script := &fuzzgen.Script{
		Version: 1, Seed: 105, InitWidth: 80, InitHeight: 24,
		Steps: []fuzzgen.Step{{Kind: "bad"}},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	result, err := fuzzgen.Run(ctx, script, 5*time.Second)
	if err == nil {
		t.Fatalf("Run(invalid step) unexpectedly succeeded: %+v", result)
	}
	assertNoNewEmbeddedNvim(t, before)
}

func TestGenerateLargeScriptBalancedKindsAndResizeSafeFloats(t *testing.T) {
	cfg := fuzzgen.Config{
		Steps: 50, InitWidth: 80, InitHeight: 24,
		MinWidth: 40, MaxWidth: 120, MinHeight: 16, MaxHeight: 40,
	}
	for _, seed := range []int64{1, 2, 3, 4, 5, 40, 42, 77} {
		script, err := fuzzgen.Generate(seed, cfg)
		if err != nil {
			t.Fatalf("Generate(%d): %v", seed, err)
		}
		counts := map[fuzzgen.OpKind]int{}
		width, height := cfg.InitWidth, cfg.InitHeight
		windows := 1
		floats := map[string]bool{}
		for index, step := range script.Steps {
			counts[step.Kind]++
			switch step.Kind {
			case fuzzgen.OpResize:
				width, height = step.Width, step.Height
			case fuzzgen.OpSplit, fuzzgen.OpVSplit:
				windows++
			case fuzzgen.OpCloseWin:
				windows--
			case fuzzgen.OpFloatOpen:
				if step.Width < 1 || step.Height < 1 || step.Width+2 > width ||
					step.Height+2 > height || step.Row < 0 || step.Col < 0 ||
					step.Row+step.Height+2 > height || step.Col+step.Width+2 > width {
					t.Fatalf("seed %d step %d float %+v exceeds %dx%d with border", seed, index, step, width, height)
				}
				floats[step.FloatTag] = true
			case fuzzgen.OpFloatClose:
				if !floats[step.FloatTag] {
					t.Fatalf("seed %d step %d closes unknown float %q", seed, index, step.FloatTag)
				}
				delete(floats, step.FloatTag)
			}
			if windows < 1 {
				t.Fatalf("seed %d step %d closes the final normal window", seed, index)
			}
		}
		if len(counts) < 6 {
			t.Fatalf("seed %d covered only %d kinds: %v", seed, len(counts), counts)
		}
		if counts[fuzzgen.OpInput] > len(script.Steps)/2 {
			t.Fatalf("seed %d has %d/%d input steps, want balanced kinds",
				seed, counts[fuzzgen.OpInput], len(script.Steps))
		}
	}
}

func TestShrinkHonorsAttemptLimitAndDeepCopy(t *testing.T) {
	script := &fuzzgen.Script{
		Version: 1, Seed: 5, InitWidth: 80, InitHeight: 24,
		Steps: []fuzzgen.Step{
			{Kind: fuzzgen.OpInput, Keys: "i"},
			{Kind: fuzzgen.OpRedraw},
			{Kind: fuzzgen.OpFloatOpen, FloatTag: "marker"},
		},
	}
	before, _ := json.Marshal(script)
	fails := func(candidate *fuzzgen.Script) bool {
		return len(candidate.Steps) > 0 &&
			candidate.Steps[len(candidate.Steps)-1].Kind == fuzzgen.OpFloatOpen
	}
	result, err := fuzzgen.Shrink(script, fuzzgen.ShrinkConfig{MaxAttempts: 1}, fails)
	if err != nil {
		t.Fatalf("Shrink: %v", err)
	}
	if result.Attempts > 1 || result.MinimalSteps > 3 || !fails(result.Minimal) {
		t.Fatalf("unexpected shrink result: %+v", result)
	}
	after, _ := json.Marshal(script)
	if !bytes.Equal(before, after) {
		t.Fatal("Shrink mutated caller's script")
	}
	if result.Minimal == script || &result.Minimal.Steps[0] == &script.Steps[0] {
		t.Fatal("Shrink returned caller-owned step storage")
	}
	for _, candidate := range []struct {
		script *fuzzgen.Script
		fails  func(*fuzzgen.Script) bool
	}{
		{nil, func(*fuzzgen.Script) bool { return true }},
		{&fuzzgen.Script{Version: 1}, nil},
		{&fuzzgen.Script{Version: 1}, func(*fuzzgen.Script) bool { return false }},
	} {
		if _, err := fuzzgen.Shrink(candidate.script, fuzzgen.ShrinkConfig{}, candidate.fails); err == nil {
			t.Fatalf("Shrink(%+v) unexpectedly succeeded", candidate.script)
		}
	}
}

func TestShrinkMutatingPredicateAndBoundaryLengths(t *testing.T) {
	script := &fuzzgen.Script{
		Version: 1, Seed: 6, InitWidth: 80, InitHeight: 24,
		Steps: []fuzzgen.Step{
			{Kind: fuzzgen.OpRedraw}, {Kind: fuzzgen.OpInput, Keys: "<Esc>i"},
			{Kind: fuzzgen.OpFloatOpen, FloatTag: "marker"}, {Kind: fuzzgen.OpRedraw},
			{Kind: fuzzgen.OpInput, Keys: "u"}, {Kind: fuzzgen.OpRedraw},
		},
	}
	before, _ := json.Marshal(script)
	calls := 0
	fails := func(candidate *fuzzgen.Script) bool {
		calls++
		if len(candidate.Steps) > 0 {
			candidate.Steps[0].Kind = "tampered"
		}
		return containsFloatOpen(candidate.Steps)
	}
	result, err := fuzzgen.Shrink(script, fuzzgen.ShrinkConfig{MaxAttempts: 256}, fails)
	if err != nil {
		t.Fatalf("Shrink(mutating fails): %v", err)
	}
	after, _ := json.Marshal(script)
	if !bytes.Equal(before, after) {
		t.Fatal("mutating fails predicate leaked into caller's script")
	}
	if !containsFloatOpen(result.Minimal.Steps) || result.MinimalSteps > 2 {
		t.Fatalf("Minimal = %+v, want <=2 steps still containing float_open", result.Minimal)
	}
	if calls < 2 {
		t.Fatalf("fails called %d times, want multiple isolated calls", calls)
	}
	emptyResult, err := fuzzgen.Shrink(&fuzzgen.Script{Version: 1}, fuzzgen.ShrinkConfig{}, func(*fuzzgen.Script) bool { return true })
	if err != nil {
		t.Fatalf("Shrink(empty script): %v", err)
	}
	if emptyResult.MinimalSteps != 0 || len(emptyResult.Minimal.Steps) != 0 {
		t.Fatalf("Shrink(empty script) = %+v", emptyResult)
	}
	one := &fuzzgen.Script{Version: 1, Seed: 7, InitWidth: 80, InitHeight: 24, Steps: []fuzzgen.Step{{Kind: fuzzgen.OpRedraw}}}
	oneResult, err := fuzzgen.Shrink(one, fuzzgen.ShrinkConfig{MaxAttempts: 256}, func(*fuzzgen.Script) bool { return true })
	if err != nil {
		t.Fatalf("Shrink(one step): %v", err)
	}
	if oneResult.MinimalSteps != 0 || len(oneResult.Minimal.Steps) != 0 {
		t.Fatalf("Shrink(one step) = %+v, want empty script", oneResult)
	}
}

func allKindsScript() *fuzzgen.Script {
	return &fuzzgen.Script{
		Version: 1, Seed: 40, InitWidth: 80, InitHeight: 24,
		Steps: []fuzzgen.Step{
			{Kind: fuzzgen.OpResize, Width: 90, Height: 28},
			{Kind: fuzzgen.OpSplit},
			{Kind: fuzzgen.OpVSplit},
			{Kind: fuzzgen.OpWinCmd, WinCmdArg: "l"},
			{Kind: fuzzgen.OpCloseWin},
			{Kind: fuzzgen.OpFloatOpen, FloatTag: "f1", Width: 12, Height: 3, Row: 1, Col: 1, Anchor: "NW", Border: "rounded", ZIndex: 50},
			{Kind: fuzzgen.OpFloatClose, FloatTag: "f1"},
			{Kind: fuzzgen.OpSetHL, HLGroup: "M3A", Attr: "bold"},
			{Kind: fuzzgen.OpExtmark, Text: "世界", HLGroup: "M3A"},
			{Kind: fuzzgen.OpRedraw},
			{Kind: fuzzgen.OpInput, Keys: "i"},
			{Kind: fuzzgen.OpInput, Keys: "CJK 你好 e\u0301 🌍 ┌─┐╭╮"},
			{Kind: fuzzgen.OpInput, Keys: "<Esc>"},
			{Kind: fuzzgen.OpInput, Keys: "j"},
			{Kind: fuzzgen.OpInput, Keys: "k"},
			{Kind: fuzzgen.OpInput, Keys: "u"},
			{Kind: fuzzgen.OpResize, Width: 80, Height: 24},
			{Kind: fuzzgen.OpWinCmd, WinCmdArg: "w"},
			{Kind: fuzzgen.OpFloatOpen, FloatTag: "f2", Width: 8, Height: 2},
			{Kind: fuzzgen.OpFloatClose, FloatTag: "f2"},
			{Kind: fuzzgen.OpSetHL, HLGroup: "M3B", FG: "#cdd6f4"},
			{Kind: fuzzgen.OpExtmark, Text: "overlay", HLGroup: "M3B"},
			{Kind: fuzzgen.OpRedraw},
			{Kind: fuzzgen.OpInput, Keys: "o"},
			{Kind: fuzzgen.OpInput, Keys: "ascii text"},
			{Kind: fuzzgen.OpInput, Keys: "<Esc>"},
		},
	}
}

func assertReplayAlignment(t *testing.T, result *fuzzgen.RunResult) {
	t.Helper()
	messages, err := protocol.ReadMessages(result.Inbound)
	if err != nil {
		t.Fatalf("protocol.ReadMessages(Inbound): %v", err)
	}
	reducer := screen.NewReducer()
	var frames []*screen.State
	flushMessages := 0
	for _, message := range messages {
		if message.Type != 2 || message.Method != "redraw" {
			continue
		}
		events, err := protocol.DecodeRedraw(message.Args)
		if err != nil {
			t.Fatalf("DecodeRedraw: %v", err)
		}
		for _, event := range events {
			if _, ok := event.(protocol.Flush); ok {
				flushMessages++
			}
		}
		committed, err := reducer.Apply(events)
		if err != nil {
			t.Fatalf("reducer.Apply: %v", err)
		}
		frames = append(frames, committed...)
	}
	if len(result.CanonFrames) != result.Committed || len(result.CanonFrames) != len(frames) || len(result.CanonFrames) != flushMessages {
		t.Fatalf("frame alignment: CanonFrames=%d Committed=%d replayed=%d flush=%d",
			len(result.CanonFrames), result.Committed, len(frames), flushMessages)
	}
	for index, state := range frames {
		canonical, err := screen.Canonical(state)
		if err != nil {
			t.Fatalf("Canonical(frame %d): %v", index, err)
		}
		if !bytes.Equal(canonical, result.CanonFrames[index]) {
			t.Fatalf("CanonFrames[%d] differs from Inbound replay", index)
		}
		list, err := drawlist.Build(state)
		if err != nil {
			t.Fatalf("drawlist.Build(frame %d): %v", index, err)
		}
		if _, err := drawlist.JSON(list); err != nil {
			t.Fatalf("drawlist.JSON(frame %d): %v", index, err)
		}
	}
}

func embedNvimPIDs(t *testing.T) map[string]bool {
	t.Helper()
	output, _ := exec.Command("pgrep", "-f", "--", "--embed").Output()
	pids := map[string]bool{}
	for _, line := range strings.Fields(string(output)) {
		command, err := exec.Command("ps", "-p", line, "-o", "command=").Output()
		if err == nil && strings.Contains(string(command), "nvim") && strings.Contains(string(command), "--embed") {
			pids[line] = true
		}
	}
	return pids
}

func assertNoNewEmbeddedNvim(t *testing.T, before map[string]bool) {
	t.Helper()
	after := embedNvimPIDs(t)
	for pid := range after {
		if !before[pid] {
			t.Fatalf("new embedded nvim process remains: PID %s", pid)
		}
	}
}

func coveredKindSet(steps []fuzzgen.Step) map[fuzzgen.OpKind]bool {
	result := map[fuzzgen.OpKind]bool{}
	for _, step := range steps {
		result[step.Kind] = true
	}
	return result
}

func hasVocabulary(steps []fuzzgen.Step) bool {
	required := map[string]bool{}
	for _, token := range []string{
		"世界", "你好", "e\u0301", "🌍", "┌─┐╭╮", "<Esc>i", "i", "j", "k", "o", "u",
		"NW", "NE", "SW", "SE", "none", "single", "rounded", "30", "50", "80",
		"bold", "italic", "reverse", "underline", "undercurl",
	} {
		required[token] = false
	}
	for _, step := range steps {
		for token := range required {
			if step.Keys == token || step.Text == token || step.Anchor == token ||
				step.Border == token || step.Attr == token || token == strconv.Itoa(step.ZIndex) {
				required[token] = true
			}
		}
	}
	for _, found := range required {
		if !found {
			return false
		}
	}
	return true
}

func containsFloatOpen(steps []fuzzgen.Step) bool {
	for _, step := range steps {
		if step.Kind == fuzzgen.OpFloatOpen {
			return true
		}
	}
	return false
}

func containsAny(value string, tokens []string) bool {
	for _, token := range tokens {
		if strings.Contains(value, token) {
			return true
		}
	}
	return false
}

func slicesContains(values []string, value string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
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
