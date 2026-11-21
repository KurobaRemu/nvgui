package fuzzgen

import (
	"context"
	"errors"
	"time"
)

var errNotImplemented = errors.New("fuzzgen: not implemented")

func generateScript(int64, Config) (*Script, error) {
	return nil, errNotImplemented
}

func runScript(context.Context, *Script, time.Duration) (*RunResult, error) {
	return nil, errNotImplemented
}

func shrinkScript(*Script, ShrinkConfig, func(*Script) bool) (*ShrinkResult, error) {
	return nil, errNotImplemented
}
