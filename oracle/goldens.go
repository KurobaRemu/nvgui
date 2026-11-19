package oracle

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
)

// Meta records the frozen generation facts for one golden scenario.
type Meta struct {
	Scenario        string          `json:"scenario"`
	StepCount       int             `json:"step_count"`
	Inconclusive    bool            `json:"inconclusive"`
	StepEndFrames   []int           `json:"step_end_frames"`
	APILevel        int             `json:"api_level"`
	UIOptions       map[string]bool `json:"ui_options"`
	ScreenLuaSHA256 string          `json:"screen_lua_sha256"`
	FlushCount      int             `json:"flush_count"`
	Width           int             `json:"width"`
	Height          int             `json:"height"`
	Purpose         string          `json:"purpose"`
	NvimVersion     string          `json:"nvim_version"`
	UIEventsSHA256  string          `json:"ui_events_sha256"`
	FrameCount      int             `json:"frame_count"`
	RedrawCount     int             `json:"redraw_count"`
}

// Golden is one frozen inbound byte stream, its per-flush Canonical frames, and generation metadata.
type Golden struct {
	Name    string
	Inbound []byte
	Frames  []json.RawMessage
	Meta    Meta
}

// List reads all frozen goldens from oracle/goldens. It locates that directory through
// runtime.Caller so callers in any nvgui subpackage use the same source-tree fixtures.
func List() []Golden {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		panic("oracle: cannot locate goldens.go")
	}
	directory := filepath.Join(filepath.Dir(source), "goldens")
	entries, err := os.ReadDir(directory)
	if err != nil {
		panic(fmt.Sprintf("oracle: read goldens directory: %v", err))
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)

	goldens := make([]Golden, 0, len(names))
	for _, name := range names {
		scenarioDir := filepath.Join(directory, name)
		inbound, err := os.ReadFile(filepath.Join(scenarioDir, "inbound.mpack"))
		if err != nil {
			panic(fmt.Sprintf("oracle: read %s/inbound.mpack: %v", name, err))
		}
		frameData, err := os.ReadFile(filepath.Join(scenarioDir, "frames.jsonl"))
		if err != nil {
			panic(fmt.Sprintf("oracle: read %s/frames.jsonl: %v", name, err))
		}
		var frames []json.RawMessage
		for lineNumber, line := range splitLines(frameData) {
			if len(line) == 0 {
				continue
			}
			if !json.Valid(line) {
				panic(fmt.Sprintf("oracle: invalid JSON in %s/frames.jsonl line %d", name, lineNumber))
			}
			frames = append(frames, append(json.RawMessage(nil), line...))
		}
		metaData, err := os.ReadFile(filepath.Join(scenarioDir, "meta.json"))
		if err != nil {
			panic(fmt.Sprintf("oracle: read %s/meta.json: %v", name, err))
		}
		var meta Meta
		if err := json.Unmarshal(metaData, &meta); err != nil {
			panic(fmt.Sprintf("oracle: decode %s/meta.json: %v", name, err))
		}
		goldens = append(goldens, Golden{
			Name:    name,
			Inbound: inbound,
			Frames:  frames,
			Meta:    meta,
		})
	}
	return goldens
}

func splitLines(data []byte) [][]byte {
	var lines [][]byte
	start := 0
	for index, value := range data {
		if value != '\n' {
			continue
		}
		line := data[start:index]
		if len(line) > 0 && line[len(line)-1] == '\r' {
			line = line[:len(line)-1]
		}
		lines = append(lines, line)
		start = index + 1
	}
	if start < len(data) {
		lines = append(lines, data[start:])
	}
	return lines
}
