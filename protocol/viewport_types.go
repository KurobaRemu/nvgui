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

type MouseOn struct{}
type MouseOff struct{}
type SetTitle struct{ Title string }

func (MouseOn) EventName() string  { return "mouse_on" }
func (MouseOff) EventName() string { return "mouse_off" }
func (SetTitle) EventName() string { return "set_title" }

func ParseWinViewport(event Event) (WinViewport, bool, error) {
	return parseWinViewport(event)
}

func ParseWinViewportMargins(event Event) (WinViewportMargins, bool, error) {
	return parseWinViewportMargins(event)
}

func ParseBusyEvent(event Event) (busy bool, ok bool) {
	return parseBusyEvent(event)
}

func ParseMouseEvent(event Event) (enabled bool, ok bool) {
	return parseMouseEvent(event)
}

func ParseOptionSet(event Event) (name string, value any, ok bool) {
	return parseOptionSet(event)
}

func ParseMouseMoveEventOption(event Event) (enabled bool, ok bool) {
	return parseMouseMoveEventOption(event)
}

func ParseSetTitle(event Event) (SetTitle, bool, error) {
	return parseSetTitle(event)
}
