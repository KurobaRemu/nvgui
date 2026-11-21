package diag

import (
	"bytes"
	"encoding/gob"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/neovim/go-client/msgpack"

	"nvgui/drawlist"
	"nvgui/protocol"
	"nvgui/screen"
)

type Clock interface{ Now() time.Time }

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

type recordedMessage struct {
	raw      []byte
	at       time.Time
	sequence int
}

type recordedInput struct {
	event TraceEvent
	at    time.Time
}

const maxRecordedInputs = 65536

type keyframeRecord struct {
	state *screen.State
	at    time.Time
	end   int
}

type Recorder struct {
	mu      sync.Mutex
	window  time.Duration
	clock   Clock
	pending []byte
	nextSeq int

	messages     []recordedMessage
	keyframes    []keyframeRecord
	inputs       []recordedInput
	lastInputSeq uint64
}

func NewRecorder(window time.Duration, clock Clock) *Recorder {
	if window < 0 {
		window = 0
	}
	if clock == nil {
		clock = systemClock{}
	}
	return &Recorder{window: window, clock: clock}
}

func (r *Recorder) Write(p []byte) (int, error) {
	written := len(p)
	r.mu.Lock()
	defer r.mu.Unlock()

	if len(p) == 0 {
		return written, nil
	}
	now := r.clock.Now()
	r.pending = append(r.pending, p...)
	r.ingest(now)
	r.trim(now)
	return written, nil
}

func (r *Recorder) ingest(now time.Time) {
	if len(r.pending) == 0 {
		return
	}
	complete, err := completeMessageBoundary(r.pending)
	if err != nil && len(complete) == 0 {
		return
	}
	if len(complete) > 0 {
		messages, readErr := protocol.ReadMessages(complete)
		if readErr == nil {
			for _, message := range messages {
				r.messages = append(r.messages, recordedMessage{
					raw:      append([]byte(nil), message.Raw...),
					at:       now,
					sequence: r.nextSeq,
				})
				r.nextSeq++
			}
		}
	}
	if len(complete) == len(r.pending) {
		r.pending = nil
	} else {
		r.pending = append([]byte(nil), r.pending[len(complete):]...)
	}
}

func completeMessageBoundary(buffer []byte) ([]byte, error) {
	position := 0
	for position < len(buffer) {
		consumed, err := messageLength(buffer[position:])
		if err != nil {
			return buffer[:position], err
		}
		if consumed <= 0 {
			return buffer[:position], io.ErrUnexpectedEOF
		}
		position += consumed
		if position > len(buffer) {
			return nil, fmt.Errorf("msgpack decoder consumed past buffer")
		}
	}
	return buffer, nil
}

func messageLength(buffer []byte) (int, error) {
	reader := &countingReader{source: bytes.NewReader(buffer)}
	decoder := msgpack.NewDecoder(reader)
	var fields []any
	err := decodeCompleteValue(decoder, &fields)
	if err != nil {
		return 0, err
	}
	return reader.count, nil
}

func decodeCompleteValue(decoder *msgpack.Decoder, target *[]any) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = io.ErrUnexpectedEOF
		}
	}()
	return decoder.Decode(target)
}

type countingReader struct {
	source *bytes.Reader
	count  int
}

func (reader *countingReader) Read(buffer []byte) (int, error) {
	if len(buffer) > 1 {
		buffer = buffer[:1]
	}
	count, err := reader.source.Read(buffer)
	reader.count += count
	return count, err
}

func (r *Recorder) trim(now time.Time) {
	r.trimInputs(now)
	if len(r.keyframes) == 0 {
		return
	}
	cutoff := now.Add(-r.window)
	retained := -1
	for index, keyframe := range r.keyframes {
		if !keyframe.at.After(cutoff) {
			retained = index
		} else {
			break
		}
	}
	if retained < 0 {
		return
	}
	messageStart := 0
	for messageStart < len(r.messages) && r.messages[messageStart].sequence < r.keyframes[retained].end {
		messageStart++
	}
	if retained > 0 || messageStart > 0 {
		messages := make([]recordedMessage, len(r.messages)-messageStart, maxInt(1, 2*(len(r.messages)-messageStart)))
		copy(messages, r.messages[messageStart:])
		r.messages = messages
		keyframes := make([]keyframeRecord, len(r.keyframes)-retained, maxInt(1, 2*(len(r.keyframes)-retained)))
		copy(keyframes, r.keyframes[retained:])
		r.keyframes = keyframes
	}
}

func (r *Recorder) trimInputs(now time.Time) {
	cutoff := now.Add(-r.window)
	retained := 0
	for retained < len(r.inputs) && r.inputs[retained].at.Before(cutoff) {
		retained++
	}
	if excess := len(r.inputs) - retained - maxRecordedInputs; excess > 0 {
		retained += excess
	}
	if retained <= 0 {
		return
	}
	kept := len(r.inputs) - retained
	inputs := make([]recordedInput, kept)
	copy(inputs, r.inputs[retained:])
	r.inputs = inputs
}

func (r *Recorder) Keyframe(s *screen.State) {
	if s == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.clock.Now()
	r.keyframes = append(r.keyframes, keyframeRecord{
		state: cloneState(s),
		at:    now,
		end:   r.nextSeq,
	})
	r.trim(now)
}

type Meta struct {
	NvimVersion string            `json:"nvim_version"`
	APILevel    int               `json:"api_level"`
	UIOptions   map[string]bool   `json:"ui_options"`
	GUIVersion  string            `json:"gui_version"`
	GOOS        string            `json:"goos"`
	Extra       map[string]string `json:"extra,omitempty"`
}

type exportMeta struct {
	Meta
	BundleVersion int       `json:"bundle_version"`
	ExportedAt    time.Time `json:"exported_at"`
	WindowMS      int64     `json:"window_ms"`
}

type timingRecord struct {
	Offset int64 `json:"offset"`
	TMS    int64 `json:"t_ms"`
}

type recorderSnapshot struct {
	meta         Meta
	keyframe     *screen.State
	replayFrom   int
	keyframeAt   time.Time
	exportedAt   time.Time
	inbound      []byte
	timing       []timingRecord
	windowMillis int64
	inputs       []TraceEvent
	drawData     []byte
	inputData    []byte
	drawIndex    []byte
	inputIndex   []byte
}

type bundleReplay struct {
	keyframeGob []byte
	inbound     []byte
	replayFrom  []byte
}

type exportFile struct {
	name string
	data []byte
}

func (r *Recorder) Export(dir string, meta Meta) error {
	if dir == "" {
		return fmt.Errorf("diag: empty export directory")
	}
	snapshot, err := r.snapshot(meta)
	if err != nil {
		return err
	}
	if err := validateInputEvents(snapshot.inputs); err != nil {
		return fmt.Errorf("diag: recorded input trace: %w", err)
	}
	replayData := bundleReplay{
		inbound:    append([]byte(nil), snapshot.inbound...),
		replayFrom: encodeInt(snapshot.replayFrom),
	}
	var gobBuffer bytes.Buffer
	if err := gob.NewEncoder(&gobBuffer).Encode(normalizeState(snapshot.keyframe)); err != nil {
		return fmt.Errorf("diag: encode keyframe.gob: %w", err)
	}
	replayData.keyframeGob = gobBuffer.Bytes()
	states, err := replayStates(replayData)
	if err != nil {
		return err
	}
	committed, err := committedStates(states)
	if err != nil {
		return err
	}
	drawLists, err := buildDrawLists(committed)
	if err != nil {
		return err
	}
	if err := alignFrames(committed, drawLists); err != nil {
		return err
	}
	drawLines := make([][]byte, 0, len(drawLists))
	for index, list := range drawLists {
		encoded, err := drawlist.JSON(list)
		if err != nil {
			return fmt.Errorf("diag: encode draw-list.jsonl line %d: %w", index+1, err)
		}
		drawLines = append(drawLines, encoded)
	}
	inputLines := make([][]byte, 0, len(snapshot.inputs))
	for index := range snapshot.inputs {
		encoded, err := json.Marshal(snapshot.inputs[index])
		if err != nil {
			return fmt.Errorf("diag: encode input-trace.jsonl line %d: %w", index+1, err)
		}
		inputLines = append(inputLines, encoded)
	}
	snapshot.drawData = jsonlBytes(drawLines)
	snapshot.inputData = jsonlBytes(inputLines)
	snapshot.drawIndex, err = bundleIndexBytes("draw-list", "draw-list", drawLines)
	if err != nil {
		return err
	}
	snapshot.inputIndex, err = bundleIndexBytes("input-trace", "input-trace", inputLines)
	if err != nil {
		return err
	}
	metaData, err := json.Marshal(exportMeta{
		Meta:          snapshot.meta,
		BundleVersion: bundleVersion,
		ExportedAt:    snapshot.exportedAt,
		WindowMS:      snapshot.windowMillis,
	})
	if err != nil {
		return fmt.Errorf("diag: encode meta.json: %w", err)
	}
	canonical, err := screen.Canonical(snapshot.keyframe)
	if err != nil {
		return fmt.Errorf("diag: encode keyframe.json: %w", err)
	}
	if err := requireEmptyDirectory(dir); err != nil {
		return err
	}
	_, statErr := os.Stat(dir)
	createdRoot := errors.Is(statErr, os.ErrNotExist)
	var timing bytes.Buffer
	timingEncoder := json.NewEncoder(&timing)
	for _, record := range snapshot.timing {
		if err := timingEncoder.Encode(record); err != nil {
			return fmt.Errorf("diag: encode timing.jsonl: %w", err)
		}
	}
	files := []exportFile{
		{name: "meta.json", data: append(metaData, '\n')},
		{name: "keyframe.json", data: append(canonical, '\n')},
		{name: "keyframe.gob", data: replayData.keyframeGob},
		{name: "inbound.mpack", data: snapshot.inbound},
		{name: "replay-from.gob", data: replayData.replayFrom},
		{name: "timing.jsonl", data: timing.Bytes()},
		{name: "draw-list.jsonl", data: snapshot.drawData},
		{name: "input-trace.jsonl", data: snapshot.inputData},
		{name: filepath.Join("draw-list", "index.json"), data: snapshot.drawIndex},
		{name: filepath.Join("input-trace", "index.json"), data: snapshot.inputIndex},
	}
	if err := writeExportFiles(dir, createdRoot, files); err != nil {
		return err
	}
	return nil
}

func writeExportFiles(dir string, createdRoot bool, files []exportFile) (err error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("diag: create export directory: %w", err)
	}
	defer func() {
		if err == nil {
			return
		}
		if cleanupErr := cleanupExportFiles(dir, createdRoot, files); cleanupErr != nil {
			err = fmt.Errorf("%w; cleanup: %v", err, cleanupErr)
		}
	}()
	for _, name := range []string{"draw-list", "input-trace"} {
		if err := os.MkdirAll(filepath.Join(dir, name), 0o755); err != nil {
			return fmt.Errorf("diag: create %s: %w", name, err)
		}
	}
	for _, file := range files {
		if err := writeFile(filepath.Join(dir, file.name), file.data); err != nil {
			return err
		}
	}
	return nil
}

func cleanupExportFiles(dir string, createdRoot bool, files []exportFile) error {
	var failures []error
	remove := func(path string) {
		removeErr := os.Remove(path)
		if removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			failures = append(failures, fmt.Errorf("remove %s: %w", path, removeErr))
		}
	}
	for _, file := range files {
		remove(filepath.Join(dir, file.name))
	}
	for _, name := range []string{"draw-list", "input-trace"} {
		remove(filepath.Join(dir, name))
	}
	if createdRoot {
		remove(dir)
	}
	return errors.Join(failures...)
}

func (r *Recorder) snapshot(meta Meta) (recorderSnapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	exportedAt := r.clock.Now()
	r.trimInputs(exportedAt)

	keyframe := &screen.State{
		Grids:      map[int]*screen.Grid{},
		Attrs:      map[int]screen.Attr{0: {}},
		Placements: map[int]screen.Placement{},
		Cursor:     screen.Cursor{Grid: 1},
		Mode:       "normal",
	}
	keyframeAt := time.Time{}
	start := 0
	baseSequence := 0
	replayFrom := 0
	if len(r.keyframes) > 0 {
		exported := r.keyframes[0]
		keyframe = cloneState(exported.state)
		keyframeAt = exported.at
		for start < len(r.messages) && r.messages[start].sequence < exported.end {
			start++
		}
		if start < len(r.messages) {
			baseSequence = r.messages[start].sequence
		} else {
			baseSequence = r.nextSeq
		}
		latest := r.keyframes[len(r.keyframes)-1]
		keyframe = cloneState(latest.state)
		replayFrom = latest.end - baseSequence
		exportedMessages := len(r.messages) - start
		if replayFrom < 0 {
			replayFrom = 0
		} else if replayFrom > exportedMessages {
			replayFrom = exportedMessages
		}
	}
	inboundSize := 0
	for index := start; index < len(r.messages); index++ {
		inboundSize += len(r.messages[index].raw)
	}
	inbound := make([]byte, 0, inboundSize)
	timing := make([]timingRecord, 0, len(r.messages)-start)
	for index := start; index < len(r.messages); index++ {
		message := r.messages[index]
		timing = append(timing, timingRecord{
			Offset: int64(len(inbound)),
			TMS:    message.at.Sub(keyframeAt).Milliseconds(),
		})
		inbound = append(inbound, message.raw...)
	}
	return recorderSnapshot{
		meta:         cloneMeta(meta),
		keyframe:     keyframe,
		replayFrom:   replayFrom,
		keyframeAt:   keyframeAt,
		exportedAt:   exportedAt,
		inbound:      inbound,
		timing:       timing,
		windowMillis: r.window.Milliseconds(),
		inputs:       cloneInputEvents(r.inputs),
	}, nil
}

func requireEmptyDirectory(dir string) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("diag: inspect export directory: %w", err)
	}
	if len(entries) != 0 {
		return fmt.Errorf("diag: export directory %q is not empty", dir)
	}
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("diag: stat export directory: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("diag: export path %q is not a directory", dir)
	}
	return nil
}

func cloneMeta(meta Meta) Meta {
	clone := meta
	if meta.UIOptions != nil {
		clone.UIOptions = make(map[string]bool, len(meta.UIOptions))
		for key, value := range meta.UIOptions {
			clone.UIOptions[key] = value
		}
	}
	if meta.Extra != nil {
		clone.Extra = make(map[string]string, len(meta.Extra))
		for key, value := range meta.Extra {
			clone.Extra[key] = value
		}
	}
	return clone
}

func writeFile(path string, data []byte) error {
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("diag: write %s: %w", filepath.Base(path), err)
	}
	return nil
}

func Replay(dir string) ([]*screen.State, error) {
	keyframeGob, err := os.ReadFile(filepath.Join(dir, "keyframe.gob"))
	if err != nil {
		return nil, fmt.Errorf("diag: read keyframe.gob: %w", err)
	}
	inbound, err := os.ReadFile(filepath.Join(dir, "inbound.mpack"))
	if err != nil {
		return nil, fmt.Errorf("diag: read inbound.mpack: %w", err)
	}
	replayFrom, err := os.ReadFile(filepath.Join(dir, "replay-from.gob"))
	if err != nil {
		return nil, fmt.Errorf("diag: read replay-from.gob: %w", err)
	}
	return replayStates(bundleReplay{
		keyframeGob: keyframeGob,
		inbound:     inbound,
		replayFrom:  replayFrom,
	})
}

func replayStates(data bundleReplay) ([]*screen.State, error) {
	var state screen.State
	if err := gob.NewDecoder(bytes.NewReader(data.keyframeGob)).Decode(&state); err != nil {
		return nil, fmt.Errorf("diag: decode keyframe.gob: %w", err)
	}
	messages, err := protocol.ReadMessages(data.inbound)
	if err != nil {
		return nil, fmt.Errorf("diag: decode inbound.mpack: %w", err)
	}
	skipMessages, err := decodeReplayFrom(data.replayFrom)
	if err != nil {
		return nil, err
	}
	reducer := screen.NewReducerFrom(&state)
	states := []*screen.State{cloneState(&state)}
	for _, message := range messages {
		if skipMessages > 0 {
			skipMessages--
			continue
		}
		if message.Type != 2 || message.Method != "redraw" {
			continue
		}
		events, err := protocol.DecodeRedraw(message.Args)
		if err != nil {
			return nil, fmt.Errorf("diag: decode redraw message: %w", err)
		}
		committed, err := reducer.Apply(events)
		if err != nil {
			return nil, fmt.Errorf("diag: replay redraw message: %w", err)
		}
		states = append(states, committed...)
	}
	return states, nil
}

func encodeInt(value int) []byte {
	var buffer bytes.Buffer
	if err := gob.NewEncoder(&buffer).Encode(value); err != nil {
		return nil
	}
	return buffer.Bytes()
}

func decodeReplayFrom(data []byte) (int, error) {
	var value int
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&value); err != nil {
		return 0, fmt.Errorf("diag: decode replay-from.gob: %w", err)
	}
	if value < 0 {
		return 0, fmt.Errorf("diag: replay-from.gob = %d, want non-negative", value)
	}
	return value, nil
}

func readGob(path string) (*screen.State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("diag: read keyframe.gob: %w", err)
	}
	var state screen.State
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&state); err != nil {
		return nil, fmt.Errorf("diag: decode keyframe.gob: %w", err)
	}
	return &state, nil
}

func normalizeState(state *screen.State) *screen.State {
	clone := cloneState(state)
	for _, attr := range clone.Attrs {
		for key, value := range attr {
			switch typed := value.(type) {
			case int:
				attr[key] = int64(typed)
			case uint:
				attr[key] = int64(typed)
			case int8:
				attr[key] = int64(typed)
			case uint8:
				attr[key] = int64(typed)
			case int16:
				attr[key] = int64(typed)
			case uint16:
				attr[key] = int64(typed)
			case int32:
				attr[key] = int64(typed)
			case uint32:
				attr[key] = int64(typed)
			case uint64:
				attr[key] = int64(typed)
			}
		}
	}
	return clone
}

func cloneState(source *screen.State) *screen.State {
	if source == nil {
		return nil
	}
	clone := *source
	clone.Grids = make(map[int]*screen.Grid, len(source.Grids))
	for gridID, grid := range source.Grids {
		if grid == nil {
			clone.Grids[gridID] = nil
			continue
		}
		gridClone := &screen.Grid{Width: grid.Width, Height: grid.Height, Cells: make([][]screen.Cell, len(grid.Cells))}
		for row := range grid.Cells {
			gridClone.Cells[row] = append([]screen.Cell(nil), grid.Cells[row]...)
		}
		clone.Grids[gridID] = gridClone
	}
	clone.Attrs = make(map[int]screen.Attr, len(source.Attrs))
	for id, attr := range source.Attrs {
		attrClone := make(screen.Attr, len(attr))
		for key, value := range attr {
			attrClone[key] = value
		}
		clone.Attrs[id] = attrClone
	}
	clone.Placements = make(map[int]screen.Placement, len(source.Placements))
	for gridID, placement := range source.Placements {
		clone.Placements[gridID] = placement
	}
	if source.Msg != nil {
		msg := *source.Msg
		clone.Msg = &msg
	}
	return &clone
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}
