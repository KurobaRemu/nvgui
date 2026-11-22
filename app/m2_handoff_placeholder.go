package app

import (
	"context"
	"errors"
)

var errM2HandoffNotImplemented = errors.New("app: m2 handoff not implemented")

type instanceServerImpl struct{}

func parseCLILaunch(_ []string, _ []string, _ string) (CLILaunch, error) {
	return CLILaunch{}, errM2HandoffNotImplemented
}

func parseOpenInPolicy(_ string) (OpenInPolicy, bool) {
	return "", false
}

func formatDropCommand(_ []string) (string, error) {
	return "", errM2HandoffNotImplemented
}

func resolveLoginPATH(_ context.Context, env []string, _ PathProbeOptions) ([]string, bool) {
	return append([]string(nil), env...), false
}

func defaultInstanceSocketPath(_ []string) string {
	return ""
}

func listenInstance(_ string, _ func(context.Context, HandoffRequest) (HandoffResult, error)) (*InstanceServer, error) {
	return nil, errM2HandoffNotImplemented
}

func instanceServerSocketPath(_ *InstanceServer) string {
	return ""
}

func instanceServerClose(_ *InstanceServer) error {
	return errM2HandoffNotImplemented
}

func forwardInstance(_ context.Context, _ string, _ HandoffRequest) (HandoffResult, bool, error) {
	return HandoffResult{}, false, errM2HandoffNotImplemented
}

func workspaceOpenInPolicy(_ *Workspace) OpenInPolicy {
	return ""
}

func workspaceSetOpenInPolicy(_ *Workspace, _ OpenInPolicy) {}

func workspaceHandleHandoff(_ *Workspace, _ context.Context, _ HandoffRequest) (HandoffResult, error) {
	return HandoffResult{}, errM2HandoffNotImplemented
}

func workspaceOpenFiles(_ *Workspace, _ context.Context, _ []string, _ string, _ LaunchSource) (HandoffResult, error) {
	return HandoffResult{}, errM2HandoffNotImplemented
}

func shellDropFiles(_ *Shell, _ context.Context, _ []string) error {
	return errM2HandoffNotImplemented
}
