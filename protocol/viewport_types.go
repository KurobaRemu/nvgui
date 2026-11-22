package protocol

type WinViewport struct {
	Grid           int
	Win            int
	TopLine        int64
	BotLine        int64
	CurLine        int64
	CurCol         int64
	LineCount      int64
	ScrollDelta    float64
	HasLineCount   bool
	HasScrollDelta bool
}

type WinViewportMargins struct {
	Grid, Win                int
	Top, Bottom, Left, Right int
}

func ParseWinViewport(event Event) (WinViewport, bool, error) {
	return parseWinViewport(event)
}

func ParseWinViewportMargins(event Event) (WinViewportMargins, bool, error) {
	return parseWinViewportMargins(event)
}

func ParseBusyEvent(event Event) (busy bool, ok bool) {
	return parseBusyEvent(event)
}

func ParseOptionSet(event Event) (name string, value any, ok bool) {
	return parseOptionSet(event)
}
