package drawlist_test

// Covered P0 IDs: neovide#2491, neovim-qt#164.
// Deferred (16 covered + 7 deferred = 23/23): neovide#3047 (Kitty Text Sizing
// Protocol, phase 2), neovim#21458 (mouseshape, M2/phase 2), neovim#19559
// (detach/reattach, phase 2 remote nvim), neovim#24802 (upstream
// inccommand=split limitation), macvim#721 (legacy "Screen error" without a
// modern reproduction), goneovim#265 (terminal mouse click, M2 mouse),
// neovim#3929 (iTerm2 fullscreen + airline, terminal-only).

import (
	"fmt"
	"strings"
	"testing"

	"nvgui/drawlist"
	"nvgui/screen"
)

func TestPriorBug_Neovide2491_BoxDrawingAndPowerlineClassification(t *testing.T) {
	cases := []struct {
		name string
		text string
		kind string
	}{
		{"box corner", "┌", "box"},
		{"box horizontal", "─", "box"},
		{"box tee", "┬", "box"},
		{"box corner", "┐", "box"},
		{"rounded corner", "╭", "box"},
		{"rounded corner", "╮", "box"},
		{"rounded corner", "╰", "box"},
		{"rounded corner", "╯", "box"},
		{"left block", "▎", "box"},
		{"full block", "█", "box"},
		{"powerline", "", "box"},
		{"nerd font PUA", "\ue0a0", "text"},
		{"nerd font PUA", "\uf015", "text"},
		{"CJK wide", "中", "text"},
	}
	wantWidth := func(text string) int {
		if text == "中" {
			return 2
		}
		return 1
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			state := &screen.State{
				Seq:           1,
				Grids:         map[int]*screen.Grid{1: {Width: 4, Height: 1, Cells: [][]screen.Cell{}}},
				Attrs:         map[int]screen.Attr{0: {}},
				Placements:    map[int]screen.Placement{},
				DefaultColors: screen.Colors{FG: 0xffffff, BG: 0x000000},
				Cursor:        screen.Cursor{Grid: 1},
				Mode:          "normal",
			}
			row := []screen.Cell{{Text: test.text}}
			width := wantWidth(test.text)
			if width == 2 {
				row = append(row, screen.Cell{Text: ""})
			}
			for len(row) < state.Grids[1].Width {
				row = append(row, screen.Cell{Text: " "})
			}
			state.Grids[1].Cells = [][]screen.Cell{row}

			list, err := drawlist.Build(state)
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			if len(list.Layers) != 1 {
				t.Fatalf("layers = %d, want 1", len(list.Layers))
			}
			var foreground []drawlist.Item
			for _, item := range list.Layers[0].Items {
				if item.Kind == "text" || item.Kind == "box" {
					foreground = append(foreground, item)
				}
			}
			if len(foreground) != 1 {
				t.Fatalf("foreground items = %#v, want 1", foreground)
			}
			item := foreground[0]
			if item.Kind != test.kind {
				t.Fatalf("kind = %q, want %q", item.Kind, test.kind)
			}
			if item.Col != 0 || item.Row != 0 || item.Width != width {
				t.Fatalf("item geometry = (%d,%d,%d), want (0,0,%d)", item.Row, item.Col, item.Width, width)
			}
			switch test.kind {
			case "box":
				if item.Rune != test.text {
					t.Fatalf("Rune = %q, want %q", item.Rune, test.text)
				}
				if item.Text != "" {
					t.Fatalf("box item leaked Text %q", item.Text)
				}
			case "text":
				if item.Text != test.text {
					t.Fatalf("Text = %q, want %q", item.Text, test.text)
				}
				if item.Rune != "" {
					t.Fatalf("text item leaked Rune %q", item.Rune)
				}
			}
		})
	}

	allRunes := "┌─┬─┐╭╮╰╯▎█\ue0a0\uf015中"
	state := &screen.State{
		Seq:           1,
		Grids:         map[int]*screen.Grid{1: {Width: 20, Height: 1, Cells: [][]screen.Cell{}}},
		Attrs:         map[int]screen.Attr{0: {}},
		Placements:    map[int]screen.Placement{},
		DefaultColors: screen.Colors{FG: 0xffffff, BG: 0x000000},
		Cursor:        screen.Cursor{Grid: 1},
		Mode:          "normal",
	}
	row := make([]screen.Cell, 0, len(allRunes)+1)
	for _, runeValue := range allRunes {
		row = append(row, screen.Cell{Text: string(runeValue)})
		if string(runeValue) == "中" {
			row = append(row, screen.Cell{Text: ""})
		}
	}
	for len(row) < state.Grids[1].Width {
		row = append(row, screen.Cell{Text: " "})
	}
	state.Grids[1].Cells = [][]screen.Cell{row}
	list, err := drawlist.Build(state)
	if err != nil {
		t.Fatalf("Build mixed row: %v", err)
	}
	var rendered strings.Builder
	for _, item := range list.Layers[0].Items {
		switch item.Kind {
		case "box":
			rendered.WriteString(item.Rune)
		case "text":
			rendered.WriteString(item.Text)
		}
	}
	if got := rendered.String(); got != allRunes {
		t.Fatalf("rendered runes = %q, want %q", got, allRunes)
	}
	for _, item := range list.Layers[0].Items {
		if item.Kind == "text" || item.Kind == "box" {
			want := wantWidth(item.Text)
			if item.Kind == "box" {
				want = 1
			}
			if item.Width != want {
				t.Fatalf("%s item %q width = %d, want %d", item.Kind, fmt.Sprint(item.Text, item.Rune), item.Width, want)
			}
			if item.Rune == "" && item.Text == "" {
				t.Fatalf("%s item at column %d emitted a continuation cell", item.Kind, item.Col)
			}
		}
	}
}
