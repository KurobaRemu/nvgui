package render_test

import (
	"bytes"
	"fmt"
	"image"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"nvgui/drawlist"
	"nvgui/render"
	"nvgui/screen"
	"nvgui/text"
)

func m4ParseFeatureMap(features string) (map[string]string, []string) {
	seen := make(map[string]string)
	var dupes []string
	for _, raw := range strings.Split(features, ",") {
		part := strings.TrimSpace(raw)
		if part == "" {
			continue
		}
		tag, val, hasEq := strings.Cut(part, "=")
		tag = strings.TrimSpace(tag)
		if !hasEq {
			val = "1"
		} else {
			val = strings.TrimSpace(val)
		}
		if _, exists := seen[tag]; exists {
			dupes = append(dupes, tag)
		}
		seen[tag] = val
	}
	return seen, dupes
}

func m4AssertLigaturesDisabled(t *testing.T, label, features string) {
	t.Helper()
	fm, dupes := m4ParseFeatureMap(features)
	if len(dupes) > 0 {
		t.Fatalf("%s: duplicate feature tags %v in %q", label, dupes, features)
	}
	for _, tag := range []string{"calt", "liga", "clig", "dlig"} {
		if fm[tag] != "0" {
			t.Errorf("%s: feature %q = %q in %q, want \"0\"", label, tag, fm[tag], features)
		}
	}
}

func TestM4Ligatures_TextFeatureHelpersAndWithLigatures(t *testing.T) {
	outEmpty := text.DisableLigaturesFeatures("")
	m4AssertLigaturesDisabled(t, "DisableLigaturesFeatures(\"\")", outEmpty)

	outPreserve := text.DisableLigaturesFeatures("kern=0,ss01")
	m4AssertLigaturesDisabled(t, "DisableLigaturesFeatures(\"kern=0,ss01\")", outPreserve)
	fm, _ := m4ParseFeatureMap(outPreserve)
	if fm["kern"] != "0" || fm["ss01"] == "" {
		t.Fatalf("DisableLigaturesFeatures lost existing features: %q", outPreserve)
	}

	outOverride := text.DisableLigaturesFeatures("kern=0,calt=1,liga=1,ss01")
	m4AssertLigaturesDisabled(t, "DisableLigaturesFeatures(override)", outOverride)
	fm2, _ := m4ParseFeatureMap(outOverride)
	if fm2["kern"] != "0" || fm2["ss01"] == "" {
		t.Fatalf("DisableLigaturesFeatures(override) lost kern/ss01: %q", outOverride)
	}

	// Idempotent: calling twice must not introduce duplicates.
	outTwice := text.DisableLigaturesFeatures(outPreserve)
	m4AssertLigaturesDisabled(t, "DisableLigaturesFeatures(idempotent)", outTwice)

	baseFont := text.Font{Family: "Menlo", Size: 14, Weight: 400, Italic: false, Features: "kern=0,ss01"}
	offFont := text.WithLigatures(baseFont, false)
	if offFont.Family != baseFont.Family || offFont.Size != baseFont.Size || offFont.Weight != baseFont.Weight {
		t.Fatalf("WithLigatures(false) altered font metrics/family: %+v", offFont)
	}
	m4AssertLigaturesDisabled(t, "WithLigatures(false)", offFont.Features)
	if fmOff, _ := m4ParseFeatureMap(offFont.Features); fmOff["kern"] != "0" || fmOff["ss01"] == "" {
		t.Fatalf("WithLigatures(false) lost kern/ss01: %q", offFont.Features)
	}

	onFont := text.WithLigatures(offFont, true)
	fmOn, dupes := m4ParseFeatureMap(onFont.Features)
	if len(dupes) > 0 {
		t.Fatalf("WithLigatures(true) has duplicate tags %v in %q", dupes, onFont.Features)
	}
	for _, tag := range []string{"calt", "liga", "clig", "dlig"} {
		if fmOn[tag] == "0" {
			t.Errorf("WithLigatures(true) still has %s=0 in %q", tag, onFont.Features)
		}
	}
	if fmOn["ss01"] == "" {
		t.Errorf("WithLigatures(true) dropped non-ligature feature ss01: %q", onFont.Features)
	}
}

func TestM4Ligatures_PlannerToggleAndFeatures(t *testing.T) {
	resolver := mustResolver(t)
	planner := render.NewPlanner(resolver)
	if !planner.LigaturesEnabled() {
		t.Fatal("default Planner.LigaturesEnabled() = false, want true")
	}

	grid := newBlankGrid(24, 2, 0)
	putRunes(grid.Cells[0], 0, "-> != === >= <= |>", 0)
	state := &screen.State{
		Seq:           1,
		Grids:         map[int]*screen.Grid{1: grid},
		Attrs:         map[int]screen.Attr{0: {}},
		DefaultColors: screen.Colors{FG: 0xffffff, BG: 0x101820, SP: 0xff0000},
		Placements:    map[int]screen.Placement{},
		Cursor:        screen.Cursor{Grid: 1, Row: 1, Col: 0},
		Mode:          "normal",
	}
	dl, err := drawlist.Build(state)
	if err != nil {
		t.Fatalf("drawlist.Build: %v", err)
	}

	planner.SetLigaturesEnabled(false)
	if planner.LigaturesEnabled() {
		t.Fatal("Planner.LigaturesEnabled() = true after SetLigaturesEnabled(false)")
	}
	offFrame, err := planner.Plan(dl)
	if err != nil {
		t.Fatalf("Plan(ligatures=false): %v", err)
	}
	glyphCount := 0
	for _, cmd := range offFrame.Commands {
		if cmd.Op == "glyphs" {
			glyphCount++
			m4AssertLigaturesDisabled(t, "Plan(ligatures=false)", cmd.Features)
		}
	}
	if glyphCount == 0 {
		t.Fatal("Plan(ligatures=false) emitted zero glyphs commands")
	}

	planner.SetLigaturesEnabled(true)
	if !planner.LigaturesEnabled() {
		t.Fatal("Planner.LigaturesEnabled() = false after SetLigaturesEnabled(true)")
	}
	onFrame, err := planner.Plan(dl)
	if err != nil {
		t.Fatalf("Plan(ligatures=true): %v", err)
	}
	for _, cmd := range onFrame.Commands {
		if cmd.Op == "glyphs" {
			fm, _ := m4ParseFeatureMap(cmd.Features)
			for _, tag := range []string{"calt", "liga", "clig", "dlig"} {
				if fm[tag] == "0" {
					t.Errorf("Plan(ligatures=true) still has %s=0 in %q", tag, cmd.Features)
				}
			}
		}
	}
}

func m4DetectLigatureFont() (family string, hasTrueLigatures bool) {
	priority := []string{
		"JetBrainsMono Nerd Font",
		"JetBrains Mono",
		"Maple Mono",
		"Fira Code",
		"Cascadia Code",
		"Iosevka",
	}
	installed := ui.FontFamilies()
	const sample = "-> != === >= <= |>"

	isTrueLigFont := func(name string) bool {
		on := ui.Shape(sample, ui.Font{Family: name, Size: 16, Weight: 400, Features: "kern=0"})
		off := ui.Shape(sample, ui.Font{Family: name, Size: 16, Weight: 400, Features: "kern=0,calt=0,liga=0,clig=0,dlig=0"})
		if len(on) == 0 || len(off) == 0 {
			return false
		}
		if len(on) != len(off) || !reflect.DeepEqual(on, off) {
			return true
		}
		for _, g := range on {
			if g.Runes > 1 {
				return true
			}
		}
		return false
	}

	// Pass 1: exact (case-insensitive) match against priority list, excluding "NL" no-ligature variants.
	for _, want := range priority {
		for _, fam := range installed {
			name := strings.TrimSpace(fam.Name)
			if name == "" || strings.Contains(name, "NL") {
				continue
			}
			if strings.EqualFold(name, want) && isTrueLigFont(name) {
				return name, true
			}
		}
	}
	// Pass 2: prefix match against priority list (still excluding "NL").
	for _, want := range priority {
		wantLower := strings.ToLower(want)
		for _, fam := range installed {
			name := strings.TrimSpace(fam.Name)
			if name == "" || strings.Contains(name, "NL") {
				continue
			}
			if strings.HasPrefix(strings.ToLower(name), wantLower) && isTrueLigFont(name) {
				return name, true
			}
		}
	}
	return "Menlo", false
}

func m4CropBlock(img *image.RGBA, x0, y0, x1, y1 int) []byte {
	b := img.Bounds()
	if x0 < b.Min.X {
		x0 = b.Min.X
	}
	if y0 < b.Min.Y {
		y0 = b.Min.Y
	}
	if x1 > b.Max.X {
		x1 = b.Max.X
	}
	if y1 > b.Max.Y {
		y1 = b.Max.Y
	}
	if x1 <= x0 || y1 <= y0 {
		return nil
	}
	out := make([]byte, 0, (x1-x0)*(y1-y0)*4)
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			c := img.RGBAAt(x, y)
			out = append(out, c.R, c.G, c.B, c.A)
		}
	}
	return out
}

func TestM4Ligatures_DynamicFontPixelDiffAnd40ColCellAlignment(t *testing.T) {
	family, hasTrueLig := m4DetectLigatureFont()
	resolver, err := text.NewResolver(text.Config{GUIFont: family + ":h16", Scale: 1})
	if err != nil {
		t.Fatalf("NewResolver(%s:h16): %v", family, err)
	}
	cell := resolver.Cell()
	planner := render.NewPlanner(resolver)

	// 1. If a real ligature font is installed, verify on/off renders distinct pixels for "-> != === >= <= |>".
	if hasTrueLig {
		grid := newBlankGrid(24, 2, 0)
		putRunes(grid.Cells[0], 0, "-> != === >= <= |>", 0)
		state := &screen.State{
			Seq:           1,
			Grids:         map[int]*screen.Grid{1: grid},
			Attrs:         map[int]screen.Attr{0: {}},
			DefaultColors: screen.Colors{FG: 0xffffff, BG: 0x101820},
			Placements:    map[int]screen.Placement{},
			Cursor:        screen.Cursor{Grid: 1, Row: 1, Col: 0},
			Mode:          "normal",
		}
		dl, err := drawlist.Build(state)
		if err != nil {
			t.Fatalf("drawlist.Build(lig sample): %v", err)
		}
		imgW := int(math.Ceil(24 * float64(cell.Width)))
		imgH := int(math.Ceil(2 * float64(cell.Height)))

		planner.SetLigaturesEnabled(true)
		onFrame, err := planner.Plan(dl)
		if err != nil {
			t.Fatalf("Plan(on): %v", err)
		}
		vOn := render.NewView()
		vOn.SetFrame(onFrame)
		tOn := ui.NewTester(func(c *ui.Context) { vOn.Draw(c) }, imgW, imgH)
		onPix := append([]byte(nil), tOn.Image().Pix...)

		planner.SetLigaturesEnabled(false)
		offFrame, err := planner.Plan(dl)
		if err != nil {
			t.Fatalf("Plan(off): %v", err)
		}
		vOff := render.NewView()
		vOff.SetFrame(offFrame)
		tOff := ui.NewTester(func(c *ui.Context) { vOff.Draw(c) }, imgW, imgH)
		offPix := append([]byte(nil), tOff.Image().Pix...)

		if bytes.Equal(onPix, offPix) {
			t.Fatalf("true ligature font %q rendered identical pixels for \"-> != === >= <= |>\" with ligatures ON vs OFF", family)
		}
	}

	// 2. 40-column merged ASCII run ending with 'X' at col 39 vs standalone 'X' at col 39:
	//    lock per-cluster grid alignment with zero cumulative drift (neovim-qt#166, neovide#1037).
	const line40 = "a->b!=c===d>=e<=f|>g->h!=i===j>=k<=l|>.X"
	if len(line40) != 40 || line40[39] != 'X' {
		t.Fatalf("fixture error: len=%d last=%q", len(line40), line40[len(line40)-1])
	}

	for _, ligOn := range []bool{true, false} {
		label := fmt.Sprintf("font=%s/lig=%v", family, ligOn)
		t.Run(label, func(t *testing.T) {
			planner.SetLigaturesEnabled(ligOn)

			gridMerged := newBlankGrid(42, 2, 0)
			putRunes(gridMerged.Cells[0], 0, line40, 0)
			stateMerged := &screen.State{
				Seq:           10,
				Grids:         map[int]*screen.Grid{1: gridMerged},
				Attrs:         map[int]screen.Attr{0: {}},
				DefaultColors: screen.Colors{FG: 0xffffff, BG: 0x101820},
				Placements:    map[int]screen.Placement{},
				Cursor:        screen.Cursor{Grid: 1, Row: 1, Col: 0},
				Mode:          "normal",
			}
			dlMerged, err := drawlist.Build(stateMerged)
			if err != nil {
				t.Fatalf("drawlist.Build(merged): %v", err)
			}
			frameMerged, err := planner.Plan(dlMerged)
			if err != nil {
				t.Fatalf("Plan(merged): %v", err)
			}
			found40Run := false
			for _, cmd := range frameMerged.Commands {
				if cmd.Op == "glyphs" && cmd.Text == line40 {
					found40Run = true
				}
			}
			if !found40Run {
				t.Fatalf("expected 40-col ASCII line to merge into a single glyphs command")
			}

			gridSingle := newBlankGrid(42, 2, 0)
			putRunes(gridSingle.Cells[0], 39, "X", 0)
			stateSingle := &screen.State{
				Seq:           11,
				Grids:         map[int]*screen.Grid{1: gridSingle},
				Attrs:         map[int]screen.Attr{0: {}},
				DefaultColors: screen.Colors{FG: 0xffffff, BG: 0x101820},
				Placements:    map[int]screen.Placement{},
				Cursor:        screen.Cursor{Grid: 1, Row: 1, Col: 0},
				Mode:          "normal",
			}
			dlSingle, err := drawlist.Build(stateSingle)
			if err != nil {
				t.Fatalf("drawlist.Build(single): %v", err)
			}
			frameSingle, err := planner.Plan(dlSingle)
			if err != nil {
				t.Fatalf("Plan(single): %v", err)
			}

			imgW := int(math.Ceil(42 * float64(cell.Width)))
			imgH := int(math.Ceil(2 * float64(cell.Height)))

			vMerged := render.NewView()
			vMerged.SetFrame(frameMerged)
			imgMerged := ui.NewTester(func(c *ui.Context) { vMerged.Draw(c) }, imgW, imgH).Image()

			vSingle := render.NewView()
			vSingle.SetFrame(frameSingle)
			imgSingle := ui.NewTester(func(c *ui.Context) { vSingle.Draw(c) }, imgW, imgH).Image()

			x0 := int(math.Floor(39 * float64(cell.Width)))
			x1 := int(math.Ceil(40 * float64(cell.Width)))
			y1 := int(math.Ceil(float64(cell.Height)))

			blockMerged := m4CropBlock(imgMerged, x0, 0, x1, y1)
			blockSingle := m4CropBlock(imgSingle, x0, 0, x1, y1)
			if isUniformImage(blockSingle) {
				t.Fatalf("standalone 'X' at col 39 produced uniform pixels in [%d,%d)x[0,%d)", x0, x1, y1)
			}
			if !bytes.Equal(blockMerged, blockSingle) {
				t.Fatalf("40-col merged run col 39 'X' drifted from standalone col 39 'X' pixels (x=[%d,%d))", x0, x1)
			}
		})
	}
}

func TestM4Ligatures_ASCIIRunMergeEfficiencyPreserved(t *testing.T) {
	resolver := mustResolver(t)
	planner := render.NewPlanner(resolver)

	const (
		w = 80
		h = 20
	)
	grid := newBlankGrid(w, h, 0)
	for r := 0; r < h; r++ {
		putRunes(grid.Cells[r], 0, fmt.Sprintf("fn_row_%02d(a->b!=c===d>=e<=f|>g) // comment_segment_0123456789", r), 0)
	}
	state := &screen.State{
		Seq:           20,
		Grids:         map[int]*screen.Grid{1: grid},
		Attrs:         map[int]screen.Attr{0: {}},
		DefaultColors: screen.Colors{FG: 0xcdd6f4, BG: 0x1e1e2e},
		Placements:    map[int]screen.Placement{},
		Cursor:        screen.Cursor{Grid: 1, Row: 0, Col: 0},
		Mode:          "normal",
	}
	dl, err := drawlist.Build(state)
	if err != nil {
		t.Fatalf("drawlist.Build: %v", err)
	}
	textItems := 0
	for _, layer := range dl.Layers {
		for _, item := range layer.Items {
			if item.Kind == "text" {
				textItems++
			}
		}
	}
	if textItems < 500 {
		t.Fatalf("fixture textItems = %d, want >= 500", textItems)
	}

	for _, enabled := range []bool{true, false} {
		planner.SetLigaturesEnabled(enabled)
		frame, err := planner.Plan(dl)
		if err != nil {
			t.Fatalf("Plan(lig=%v): %v", enabled, err)
		}
		glyphCmds := 0
		for _, cmd := range frame.Commands {
			if cmd.Op == "glyphs" {
				glyphCmds++
			}
		}
		if glyphCmds == 0 || glyphCmds*4 > textItems {
			t.Fatalf("ligatures=%v broke ASCII run merge efficiency: glyphCmds=%d textItems=%d (want glyphCmds*4 <= textItems)",
				enabled, glyphCmds, textItems)
		}
	}
}
