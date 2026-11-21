package diag

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"nvgui/drawlist"
	"nvgui/protocol"
	"nvgui/screen"
)

const (
	bundleVersion   = 1
	drawListVersion = 1
)

type bundleIndex struct {
	Version int      `json:"version"`
	Kind    string   `json:"kind"`
	File    string   `json:"file"`
	Count   int      `json:"count"`
	Seqs    []uint64 `json:"seqs"`
}

func (r *Recorder) RecordInput(ev TraceEvent) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.clock.Now()
	if ev.Seq == 0 {
		ev.Seq = r.lastInputSeq + 1
	}
	if ev.Seq > r.lastInputSeq {
		r.lastInputSeq = ev.Seq
	}
	r.inputs = append(r.inputs, recordedInput{event: ev, at: now})
	r.trimInputs(now)
}

func ReadBundle(dir string) (*Bundle, error) {
	if err := validateBundleDir(dir); err != nil {
		return nil, err
	}
	meta, err := readBundleMeta(dir)
	if err != nil {
		return nil, err
	}
	if err := validateKeyframe(dir); err != nil {
		return nil, err
	}
	if err := validateInbound(dir); err != nil {
		return nil, err
	}
	if err := validateTiming(dir); err != nil {
		return nil, err
	}
	states, err := Replay(dir)
	if err != nil {
		return nil, err
	}
	committed, err := committedStates(states)
	if err != nil {
		return nil, err
	}
	if len(committed) == 0 {
		return nil, fmt.Errorf("diag: bundle has no committed States")
	}
	drawLists, err := readDrawLists(dir)
	if err != nil {
		return nil, err
	}
	inputs, err := readInputTrace(dir)
	if err != nil {
		return nil, err
	}
	if err := alignFrames(committed, drawLists); err != nil {
		return nil, err
	}
	return &Bundle{
		Meta:        meta,
		States:      committed,
		DrawLists:   drawLists,
		InputEvents: inputs,
	}, nil
}

func ReplayBundle(dir string) (*Bundle, error) {
	bundle, err := ReadBundle(dir)
	if err != nil {
		return nil, err
	}
	rebuilt, err := buildDrawLists(bundle.States)
	if err != nil {
		return nil, err
	}
	for index := range bundle.DrawLists {
		want, err := drawlist.JSON(bundle.DrawLists[index])
		if err != nil {
			return nil, fmt.Errorf("diag: encode recorded draw list %d: %w", index, err)
		}
		got, err := drawlist.JSON(rebuilt[index])
		if err != nil {
			return nil, fmt.Errorf("diag: encode replayed draw list %d: %w", index, err)
		}
		if !bytes.Equal(want, got) {
			return nil, fmt.Errorf("diag: replayed draw list %d differs from bundle", index)
		}
	}
	if err := validateInputEvents(bundle.InputEvents); err != nil {
		return nil, err
	}
	bundle.DrawLists = rebuilt
	return bundle, nil
}

func validateBundleDir(dir string) error {
	if dir == "" {
		return fmt.Errorf("diag: empty bundle directory")
	}
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("diag: stat bundle directory: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("diag: bundle path %q is not a directory", dir)
	}
	required := []string{
		"meta.json", "keyframe.json", "keyframe.gob", "inbound.mpack", "timing.jsonl",
		"replay-from.gob", "draw-list.jsonl", "input-trace.jsonl",
		"draw-list/index.json", "input-trace/index.json",
	}
	for _, name := range required {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			return fmt.Errorf("diag: bundle missing %s: %w", name, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("diag: bundle %s is not a regular file", name)
		}
	}
	for _, name := range []string{"draw-list", "input-trace"} {
		path := filepath.Join(dir, name)
		info, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("diag: bundle missing %s/: %w", name, err)
		}
		if !info.IsDir() {
			return fmt.Errorf("diag: bundle %s/ is not a directory", name)
		}
		entries, err := os.ReadDir(path)
		if err != nil {
			return fmt.Errorf("diag: read %s/: %w", name, err)
		}
		for _, entry := range entries {
			if entry.IsDir() {
				return fmt.Errorf("diag: %s/ contains subdirectory %q", name, entry.Name())
			}
		}
	}
	return nil
}

func validateKeyframe(dir string) error {
	data, err := os.ReadFile(filepath.Join(dir, "keyframe.json"))
	if err != nil {
		return fmt.Errorf("diag: read keyframe.json: %w", err)
	}
	state, err := readGob(filepath.Join(dir, "keyframe.gob"))
	if err != nil {
		return err
	}
	canonical, err := screen.Canonical(state)
	if err != nil {
		return fmt.Errorf("diag: canonical keyframe.gob: %w", err)
	}
	if !bytes.Equal(bytes.TrimSpace(data), canonical) {
		return fmt.Errorf("diag: keyframe.json differs from keyframe.gob")
	}
	return nil
}

func validateInbound(dir string) error {
	inbound, err := os.ReadFile(filepath.Join(dir, "inbound.mpack"))
	if err != nil {
		return fmt.Errorf("diag: read inbound.mpack: %w", err)
	}
	messages, err := protocol.ReadMessages(inbound)
	if err != nil {
		return fmt.Errorf("diag: decode inbound.mpack: %w", err)
	}
	replayFrom, err := readReplayFrom(filepath.Join(dir, "replay-from.gob"))
	if err != nil {
		return err
	}
	if replayFrom > len(messages) {
		return fmt.Errorf("diag: replay-from.gob = %d, inbound has %d messages", replayFrom, len(messages))
	}
	return nil
}

func readReplayFrom(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("diag: read replay-from.gob: %w", err)
	}
	return decodeReplayFrom(data)
}

func readBundleMeta(dir string) (Meta, error) {
	data, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil {
		return Meta{}, fmt.Errorf("diag: read meta.json: %w", err)
	}
	var payload exportMeta
	if err := json.Unmarshal(data, &payload); err != nil {
		return Meta{}, fmt.Errorf("diag: decode meta.json: %w", err)
	}
	if payload.BundleVersion != bundleVersion {
		return Meta{}, fmt.Errorf("diag: bundle version = %d, want %d", payload.BundleVersion, bundleVersion)
	}
	if payload.ExportedAt.IsZero() {
		return Meta{}, fmt.Errorf("diag: meta.json exported_at is zero")
	}
	if payload.WindowMS < 0 {
		return Meta{}, fmt.Errorf("diag: meta.json window_ms = %d, want non-negative", payload.WindowMS)
	}
	return payload.Meta, nil
}

func validateTiming(dir string) error {
	data, err := os.ReadFile(filepath.Join(dir, "timing.jsonl"))
	if err != nil {
		return fmt.Errorf("diag: read timing.jsonl: %w", err)
	}
	lines, err := jsonLines(data)
	if err != nil {
		return fmt.Errorf("diag: timing.jsonl: %w", err)
	}
	previousOffset := int64(0)
	for number, line := range lines {
		var record timingRecord
		if err := json.Unmarshal(line, &record); err != nil {
			return fmt.Errorf("diag: timing.jsonl line %d: %w", number+1, err)
		}
		if record.Offset < 0 {
			return fmt.Errorf("diag: timing.jsonl line %d has offset %d, want non-negative", number+1, record.Offset)
		}
		if number > 0 && record.Offset < previousOffset {
			return fmt.Errorf("diag: timing.jsonl line %d has offset %d, want >= %d", number+1, record.Offset, previousOffset)
		}
		previousOffset = record.Offset
	}
	return nil
}

func readDrawLists(dir string) ([]*drawlist.List, error) {
	data, err := os.ReadFile(filepath.Join(dir, "draw-list.jsonl"))
	if err != nil {
		return nil, fmt.Errorf("diag: read draw-list.jsonl: %w", err)
	}
	lines, seqs, err := bundleLines(data, "draw-list.jsonl")
	if err != nil {
		return nil, err
	}
	lists := make([]*drawlist.List, 0, len(lines))
	previousSeq := uint64(0)
	for number, line := range lines {
		var list drawlist.List
		if err := json.Unmarshal(line, &list); err != nil {
			return nil, fmt.Errorf("diag: draw-list.jsonl line %d: %w", number+1, err)
		}
		if list.Version != drawListVersion {
			return nil, fmt.Errorf("diag: draw-list.jsonl line %d has version %d, want %d", number+1, list.Version, drawListVersion)
		}
		if list.Seq == 0 || list.Seq <= previousSeq {
			return nil, fmt.Errorf("diag: draw-list.jsonl line %d has Seq %d, want strictly increasing after %d", number+1, list.Seq, previousSeq)
		}
		previousSeq = list.Seq
		lists = append(lists, &list)
	}
	if err := validateBundleIndex(dir, "draw-list", "draw-list", lines, seqs); err != nil {
		return nil, err
	}
	return lists, nil
}

func readInputTrace(dir string) ([]TraceEvent, error) {
	data, err := os.ReadFile(filepath.Join(dir, "input-trace.jsonl"))
	if err != nil {
		return nil, fmt.Errorf("diag: read input-trace.jsonl: %w", err)
	}
	lines, seqs, err := bundleLines(data, "input-trace.jsonl")
	if err != nil {
		return nil, err
	}
	events := make([]TraceEvent, 0, len(lines))
	for number, line := range lines {
		var event TraceEvent
		if err := json.Unmarshal(line, &event); err != nil {
			return nil, fmt.Errorf("diag: input-trace.jsonl line %d: %w", number+1, err)
		}
		events = append(events, event)
	}
	if err := validateInputEvents(events); err != nil {
		return nil, err
	}
	if err := validateBundleIndex(dir, "input-trace", "input-trace", lines, seqs); err != nil {
		return nil, err
	}
	return events, nil
}

func bundleLines(data []byte, name string) ([][]byte, []uint64, error) {
	if len(data) == 0 {
		return nil, nil, nil
	}
	lines, err := jsonLines(data)
	if err != nil {
		return nil, nil, fmt.Errorf("diag: %s: %w", name, err)
	}
	seqs := make([]uint64, len(lines))
	for number, line := range lines {
		var envelope struct {
			Seq uint64 `json:"seq"`
		}
		if err := json.Unmarshal(line, &envelope); err != nil {
			return nil, nil, fmt.Errorf("diag: %s line %d: %w", name, number+1, err)
		}
		seqs[number] = envelope.Seq
	}
	return lines, seqs, nil
}

func validateBundleIndex(dir, name, kind string, lines [][]byte, seqs []uint64) error {
	data, err := os.ReadFile(filepath.Join(dir, name, "index.json"))
	if err != nil {
		return fmt.Errorf("diag: read %s/index.json: %w", name, err)
	}
	var index bundleIndex
	if err := json.Unmarshal(data, &index); err != nil {
		return fmt.Errorf("diag: decode %s/index.json: %w", name, err)
	}
	if index.Version != bundleVersion || index.Kind != kind {
		return fmt.Errorf("diag: %s/index.json has version %d kind %q, want %d %q", name, index.Version, index.Kind, bundleVersion, kind)
	}
	if index.File != "../"+name+".jsonl" {
		return fmt.Errorf("diag: %s/index.json has file %q, want %q", name, index.File, "../"+name+".jsonl")
	}
	if index.Count != len(lines) || len(index.Seqs) != len(lines) {
		return fmt.Errorf("diag: %s/index.json has inconsistent entries", name)
	}
	for number := range lines {
		if index.Seqs[number] != seqs[number] {
			return fmt.Errorf("diag: %s/index.json frame %d has Seq %d, want %d", name, number+1, index.Seqs[number], seqs[number])
		}
	}
	entries, err := os.ReadDir(filepath.Join(dir, name))
	if err != nil {
		return fmt.Errorf("diag: read %s/: %w", name, err)
	}
	if len(entries) != 1 {
		return fmt.Errorf("diag: %s/ contains %d entries, want only index.json", name, len(entries))
	}
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() != "index.json" {
			return fmt.Errorf("diag: %s/ contains unexpected entry %q", name, entry.Name())
		}
	}
	return nil
}

func committedStates(states []*screen.State) ([]*screen.State, error) {
	committed := make([]*screen.State, 0, len(states))
	previousSeq := uint64(0)
	for index, state := range states {
		if state == nil {
			return nil, fmt.Errorf("diag: State frame %d is nil", index)
		}
		if state.Seq == 0 {
			if index != 0 {
				return nil, fmt.Errorf("diag: State frame %d has zero Seq after committed frames", index)
			}
			continue
		}
		if state.Seq <= previousSeq {
			return nil, fmt.Errorf("diag: State frame %d has Seq %d, want strictly increasing after %d", index, state.Seq, previousSeq)
		}
		previousSeq = state.Seq
		committed = append(committed, state)
	}
	return committed, nil
}

func alignFrames(states []*screen.State, lists []*drawlist.List) error {
	if len(states) != len(lists) {
		return fmt.Errorf("diag: States len = %d, DrawLists len = %d, want equal", len(states), len(lists))
	}
	for index := range states {
		if lists[index] == nil {
			return fmt.Errorf("diag: frame %d DrawList is nil", index)
		}
		if lists[index].Seq != states[index].Seq {
			return fmt.Errorf("diag: frame %d DrawList Seq = %d, State Seq = %d", index, lists[index].Seq, states[index].Seq)
		}
	}
	return nil
}

func validateInputEvents(events []TraceEvent) error {
	previousSeq := uint64(0)
	for index := range events {
		event := events[index]
		if event.Kind < TraceKeyDown || event.Kind > TraceCommand {
			return fmt.Errorf("diag: input event %d has invalid kind %d", index, event.Kind)
		}
		if event.Seq == 0 || event.Seq <= previousSeq {
			return fmt.Errorf("diag: input event %d has Seq %d, want strictly increasing after %d", index, event.Seq, previousSeq)
		}
		previousSeq = event.Seq
	}
	return nil
}

func buildDrawLists(states []*screen.State) ([]*drawlist.List, error) {
	lists := make([]*drawlist.List, len(states))
	for index, state := range states {
		list, err := drawlist.Build(state)
		if err != nil {
			return nil, fmt.Errorf("diag: build draw list for frame %d: %w", index, err)
		}
		lists[index] = list
	}
	return lists, nil
}

func jsonLines(data []byte) ([][]byte, error) {
	if len(data) == 0 {
		return nil, nil
	}
	data = bytes.TrimSuffix(data, []byte("\n"))
	lines := bytes.Split(data, []byte("\n"))
	for number := range lines {
		lines[number] = bytes.TrimSuffix(lines[number], []byte("\r"))
		if len(lines[number]) == 0 || len(bytes.TrimSpace(lines[number])) == 0 {
			return nil, fmt.Errorf("line %d is empty or whitespace-only", number+1)
		}
	}
	return lines, nil
}

func cloneInputEvents(records []recordedInput) []TraceEvent {
	events := make([]TraceEvent, len(records))
	for index, record := range records {
		events[index] = record.event
	}
	return events
}

func jsonlBytes(lines [][]byte) []byte {
	var buffer bytes.Buffer
	for _, line := range lines {
		buffer.Write(line)
		buffer.WriteByte('\n')
	}
	return buffer.Bytes()
}

func bundleIndexBytes(name, kind string, lines [][]byte) ([]byte, error) {
	index := bundleIndex{
		Version: bundleVersion,
		Kind:    kind,
		File:    "../" + name + ".jsonl",
		Count:   len(lines),
		Seqs:    make([]uint64, len(lines)),
	}
	for number, line := range lines {
		var envelope struct {
			Seq uint64 `json:"seq"`
		}
		if err := json.Unmarshal(line, &envelope); err != nil {
			return nil, fmt.Errorf("diag: decode %s line %d for index: %w", name, number+1, err)
		}
		index.Seqs[number] = envelope.Seq
	}
	encoded, err := json.Marshal(index)
	if err != nil {
		return nil, fmt.Errorf("diag: encode %s/index.json: %w", name, err)
	}
	return append(encoded, '\n'), nil
}
