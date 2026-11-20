package drawlist

import (
	"errors"

	"nvgui/screen"
)

var errNotImplemented = errors.New("drawlist: not implemented")

func marshalJSON(_ *List) ([]byte, error) {
	return nil, errNotImplemented
}

func Build(_ *screen.State) (*List, error) {
	return nil, errNotImplemented
}
