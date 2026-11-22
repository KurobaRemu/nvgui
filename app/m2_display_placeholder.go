package app

import "errors"

var errM2DisplayNotImplemented = errors.New("app: m2 display not implemented")

func shellScale(_ *Shell) float32 {
	return 0
}

func shellSetScale(_ *Shell, _ float32) error {
	return errM2DisplayNotImplemented
}

func shellDisplayMetrics(_ *Shell) DisplayMetrics {
	return DisplayMetrics{}
}

func shellNotifyDisplayRecovery(_ *Shell, _ DisplayRecoveryReason) {}
