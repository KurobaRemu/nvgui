package app

import "nvgui/text"

type DisplayRecoveryReason string

const (
	DisplayRecoveryEnterFullScreen   DisplayRecoveryReason = "enter-fullscreen"
	DisplayRecoveryLeaveFullScreen   DisplayRecoveryReason = "leave-fullscreen"
	DisplayRecoverySpaceReturn       DisplayRecoveryReason = "space-return"
	DisplayRecoveryWindowReactivated DisplayRecoveryReason = "window-reactivated"
	DisplayRecoveryGeometryJump      DisplayRecoveryReason = "geometry-jump"
)

type DisplayMetrics struct {
	Scale        float32       `json:"scale"`
	ScaleFactor  float32       `json:"scale_factor"`
	Cell         text.CellSize `json:"cell"`
	FullScreen   bool          `json:"fullscreen"`
	RebuildCount uint64        `json:"rebuild_count"`
	RepaintCount uint64        `json:"repaint_count"`
}

func (s *Shell) Scale() float32 {
	return shellScale(s)
}

func (s *Shell) SetScale(scale float32) error {
	return shellSetScale(s, scale)
}

func (s *Shell) ScaleFactor() float32 {
	return shellScaleFactor(s)
}

func (s *Shell) SetScaleFactor(factor float32) error {
	return shellSetScaleFactor(s, factor)
}

func (s *Shell) ZoomIn() error {
	return shellZoomIn(s)
}

func (s *Shell) ZoomOut() error {
	return shellZoomOut(s)
}

func (s *Shell) ZoomReset() error {
	return shellZoomReset(s)
}

func (s *Shell) Title() string {
	return shellTitle(s)
}

func (s *Shell) DisplayMetrics() DisplayMetrics {
	return shellDisplayMetrics(s)
}

func (s *Shell) NotifyDisplayRecovery(reason DisplayRecoveryReason) {
	shellNotifyDisplayRecovery(s, reason)
}
