package session

import "nvgui/screen"

func sessionLatestMeta(*Session) *screen.FrameMeta { return nil }
func sessionLatestSnapshot(*Session) (*screen.State, *screen.FrameMeta) {
	return nil, nil
}
