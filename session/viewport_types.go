package session

import "nvgui/screen"

func (s *Session) LatestMeta() *screen.FrameMeta { return sessionLatestMeta(s) }
func (s *Session) LatestSnapshot() (*screen.State, *screen.FrameMeta) {
	return sessionLatestSnapshot(s)
}
