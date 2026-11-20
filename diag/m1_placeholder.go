package diag

import "errors"

var errM1NotImplemented = errors.New("diag: M1 bundle is not implemented")

func (r *Recorder) RecordInput(_ TraceEvent) {}

func ReadBundle(dir string) (*Bundle, error) {
	return nil, errM1NotImplemented
}

func ReplayBundle(dir string) (*Bundle, error) {
	return nil, errM1NotImplemented
}
