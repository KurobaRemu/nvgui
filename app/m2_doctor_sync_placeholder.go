package app

import (
	"context"
	"errors"

	"nvgui/render"
)

var errM2DoctorSyncNotImplemented = errors.New("app: m2 doctor sync not implemented")

func collectDoctor(_ context.Context, _ DoctorOptions) DoctorReport {
	return DoctorReport{}
}

func formatDoctorReportText(_ DoctorReport) string {
	return ""
}

func formatDoctorReportJSON(_ DoctorReport) ([]byte, error) {
	return nil, errM2DoctorSyncNotImplemented
}

func shellNvimConfig(_ *Shell) NvimConfigSnapshot {
	return NvimConfigSnapshot{}
}

func shellSyncNvimConfig(_ *Shell, _ context.Context) (NvimConfigSnapshot, error) {
	return NvimConfigSnapshot{}, errM2DoctorSyncNotImplemented
}

func shellSetOpenInPolicy(_ *Shell, _ OpenInPolicy) {}

func shellOpenInPolicy(_ *Shell) OpenInPolicy {
	return ""
}

func shellApplyAnimationConfigAndSync(_ *Shell, _ context.Context, _ render.AnimationConfig) error {
	return errM2DoctorSyncNotImplemented
}
