package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/egoist/mygo/ui"

	"nvgui/diag"
)

type diagnosticsImpl struct{}

type scenarioDocument struct {
	Version    int                `json:"version"`
	Bundle     scenarioBundle     `json:"bundle"`
	Versions   scenarioVersions   `json:"versions"`
	Font       string             `json:"font"`
	Keyframes  scenarioKeyframes  `json:"keyframes"`
	DrawLists  scenarioDrawLists  `json:"draw_lists"`
	InputTrace scenarioInputTrace `json:"input_trace"`
	Replay     scenarioReplay     `json:"replay"`
}

type scenarioBundle struct {
	Version int `json:"version"`
}

type scenarioVersions struct {
	Nvim string `json:"nvim"`
	GUI  string `json:"gui"`
	GOOS string `json:"goos"`
}

type scenarioKeyframes struct {
	Seqs []uint64 `json:"seqs"`
}

type scenarioDrawLists struct {
	Count  int      `json:"count"`
	Seqs   []uint64 `json:"seqs"`
	Width  int      `json:"width"`
	Height int      `json:"height"`
	Layers int      `json:"layers"`
	Items  int      `json:"items"`
}

type scenarioInputTrace struct {
	Count int      `json:"count"`
	Seqs  []uint64 `json:"seqs"`
	Kinds []string `json:"kinds"`
}

type scenarioReplay struct {
	Commands []string `json:"commands"`
}

func newDiagnostics() *Diagnostics {
	return &Diagnostics{impl: &diagnosticsImpl{}}
}

func diagnosticsBundleToScenario(diagnostics *Diagnostics, dir string, out string) error {
	if diagnostics == nil || diagnostics.impl == nil {
		return fmt.Errorf("app: diagnostics is nil")
	}
	if dir == "" {
		return fmt.Errorf("app: empty bundle directory")
	}
	if out == "" {
		return fmt.Errorf("app: empty scenario output path")
	}
	bundle, err := diag.ReplayBundle(dir)
	if err != nil {
		return fmt.Errorf("app: replay bundle: %w", err)
	}
	document := buildScenarioDocument(bundle, bundle.Meta)
	encoded, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return fmt.Errorf("app: encode scenario.json: %w", err)
	}
	path := out
	if !strings.EqualFold(filepath.Ext(out), ".json") {
		path = filepath.Join(out, "scenario.json")
	}
	parent := filepath.Dir(path)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("app: create scenario output directory: %w", err)
	}
	if err := os.WriteFile(path, append(encoded, '\n'), 0o644); err != nil {
		return fmt.Errorf("app: write scenario.json: %w", err)
	}
	return nil
}

func diagnosticsShortcut(diagnostics *Diagnostics, c *ui.Context) bool {
	if diagnostics == nil || diagnostics.impl == nil || c == nil {
		return false
	}
	if !c.Shortcut(ui.Cmd|ui.Shift, ui.KeyD) {
		return false
	}
	return true
}

func buildScenarioDocument(bundle *diag.Bundle, meta diag.Meta) scenarioDocument {
	keyframeSeqs := make([]uint64, 0, 1)
	if len(bundle.States) > 0 && bundle.States[0] != nil {
		keyframeSeqs = append(keyframeSeqs, bundle.States[0].Seq)
	}
	drawSeqs := make([]uint64, 0, len(bundle.DrawLists))
	width, height, layers, items := 0, 0, 0, 0
	for _, list := range bundle.DrawLists {
		if list == nil {
			continue
		}
		drawSeqs = append(drawSeqs, list.Seq)
		if width == 0 {
			width, height = list.Width, list.Height
		}
		layers += len(list.Layers)
		for _, layer := range list.Layers {
			items += len(layer.Items)
		}
	}
	inputSeqs := make([]uint64, 0, len(bundle.InputEvents))
	kinds := make([]string, 0, len(bundle.InputEvents))
	for _, event := range bundle.InputEvents {
		inputSeqs = append(inputSeqs, event.Seq)
		kinds = append(kinds, traceKindName(event.Kind))
	}
	return scenarioDocument{
		Version: 1,
		Bundle:  scenarioBundle{Version: 1},
		Versions: scenarioVersions{
			Nvim: meta.NvimVersion,
			GUI:  meta.GUIVersion,
			GOOS: meta.GOOS,
		},
		Font:      safeFontSummary(meta.Extra),
		Keyframes: scenarioKeyframes{Seqs: keyframeSeqs},
		DrawLists: scenarioDrawLists{
			Count:  len(bundle.DrawLists),
			Seqs:   drawSeqs,
			Width:  width,
			Height: height,
			Layers: layers,
			Items:  items,
		},
		InputTrace: scenarioInputTrace{
			Count: len(bundle.InputEvents),
			Seqs:  inputSeqs,
			Kinds: kinds,
		},
		Replay: scenarioReplay{
			Commands: []string{
				"diag.ReplayBundle(bundleDir)",
				"input.Replay(bundle.InputEvents, sink)",
			},
		},
	}
}

func safeFontSummary(extra map[string]string) string {
	if extra == nil {
		return ""
	}
	keys := make([]string, 0, len(extra))
	for key := range extra {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, priority := range []string{"font", "guifont", "font_family", "font_size"} {
		for _, key := range keys {
			if strings.ToLower(key) != priority {
				continue
			}
			if safe := sanitizeFontValue(extra[key]); safe != "" {
				return safe
			}
		}
	}
	return ""
}

func sanitizeFontValue(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	for _, char := range trimmed {
		if unicode.IsControl(char) || char == '/' || char == '\\' {
			return ""
		}
	}
	return trimmed
}

func traceKindName(kind diag.TraceKind) string {
	switch kind {
	case diag.TraceKeyDown:
		return "key_down"
	case diag.TraceKeyUp:
		return "key_up"
	case diag.TraceText:
		return "text"
	case diag.TraceCompose:
		return "compose"
	case diag.TraceCommand:
		return "command"
	default:
		return "unknown"
	}
}
