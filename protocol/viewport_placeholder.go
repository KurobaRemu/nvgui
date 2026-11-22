package protocol

func parseWinViewport(Event) (WinViewport, bool, error) {
	return WinViewport{}, false, nil
}

func parseWinViewportMargins(Event) (WinViewportMargins, bool, error) {
	return WinViewportMargins{}, false, nil
}

func parseBusyEvent(Event) (bool, bool) {
	return false, false
}

func parseOptionSet(Event) (string, any, bool) {
	return "", nil, false
}
