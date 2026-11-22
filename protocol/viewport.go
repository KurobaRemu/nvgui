package protocol

import (
	"fmt"
	"math"
)

func parseWinViewport(event Event) (WinViewport, bool, error) {
	unknown, ok := event.(Unknown)
	if !ok || unknown.Name != "win_viewport" {
		return WinViewport{}, false, nil
	}
	args := unknown.Args
	if len(args) < 6 {
		return WinViewport{}, false, fmt.Errorf("protocol: win_viewport has %d args, want at least 6", len(args))
	}
	grid, err := asInt(args[0])
	if err != nil {
		return WinViewport{}, false, fmt.Errorf("protocol: win_viewport grid: %w", err)
	}
	window, err := handleID(args[1])
	if err != nil {
		return WinViewport{}, false, fmt.Errorf("protocol: win_viewport win: %w", err)
	}
	topLine, err := asInt(args[2])
	if err != nil {
		return WinViewport{}, false, fmt.Errorf("protocol: win_viewport topline: %w", err)
	}
	botLine, err := asInt(args[3])
	if err != nil {
		return WinViewport{}, false, fmt.Errorf("protocol: win_viewport botline: %w", err)
	}
	curLine, err := asInt(args[4])
	if err != nil {
		return WinViewport{}, false, fmt.Errorf("protocol: win_viewport curline: %w", err)
	}
	curCol, err := asInt(args[5])
	if err != nil {
		return WinViewport{}, false, fmt.Errorf("protocol: win_viewport curcol: %w", err)
	}
	viewport := WinViewport{
		Grid:    int(grid),
		Win:     window,
		TopLine: topLine,
		BotLine: botLine,
		CurLine: curLine,
		CurCol:  curCol,
	}
	if len(args) > 6 {
		if lineCount, err := asInt(args[6]); err == nil {
			viewport.LineCount = lineCount
			viewport.HasLineCount = true
		}
	}
	if len(args) > 7 {
		if scrollDelta, err := asFloat(args[7]); err == nil && finiteFloat64(scrollDelta) {
			viewport.ScrollDelta = scrollDelta
			viewport.HasScrollDelta = true
		}
	}
	return viewport, true, nil
}

func parseWinViewportMargins(event Event) (WinViewportMargins, bool, error) {
	unknown, ok := event.(Unknown)
	if !ok || unknown.Name != "win_viewport_margins" {
		return WinViewportMargins{}, false, nil
	}
	args := unknown.Args
	if len(args) < 6 {
		return WinViewportMargins{}, false, fmt.Errorf("protocol: win_viewport_margins has %d args, want at least 6", len(args))
	}
	grid, err := asInt(args[0])
	if err != nil {
		return WinViewportMargins{}, false, fmt.Errorf("protocol: win_viewport_margins grid: %w", err)
	}
	window, err := handleID(args[1])
	if err != nil {
		return WinViewportMargins{}, false, fmt.Errorf("protocol: win_viewport_margins win: %w", err)
	}
	top, err := asInt(args[2])
	if err != nil {
		return WinViewportMargins{}, false, fmt.Errorf("protocol: win_viewport_margins top: %w", err)
	}
	bottom, err := asInt(args[3])
	if err != nil {
		return WinViewportMargins{}, false, fmt.Errorf("protocol: win_viewport_margins bottom: %w", err)
	}
	left, err := asInt(args[4])
	if err != nil {
		return WinViewportMargins{}, false, fmt.Errorf("protocol: win_viewport_margins left: %w", err)
	}
	right, err := asInt(args[5])
	if err != nil {
		return WinViewportMargins{}, false, fmt.Errorf("protocol: win_viewport_margins right: %w", err)
	}
	return WinViewportMargins{
		Grid:   int(grid),
		Win:    window,
		Top:    int(top),
		Bottom: int(bottom),
		Left:   int(left),
		Right:  int(right),
	}, true, nil
}

func parseBusyEvent(event Event) (bool, bool) {
	unknown, ok := event.(Unknown)
	if !ok {
		return false, false
	}
	switch unknown.Name {
	case "busy_start":
		return true, true
	case "busy_stop":
		return false, true
	default:
		return false, false
	}
}

func parseOptionSet(event Event) (string, any, bool) {
	unknown, ok := event.(Unknown)
	if !ok || unknown.Name != "option_set" || len(unknown.Args) < 2 {
		return "", nil, false
	}
	name, err := asString(unknown.Args[0])
	if err != nil {
		return "", nil, false
	}
	return name, unknown.Args[1], true
}

func finiteFloat64(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
