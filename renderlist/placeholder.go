package renderlist

import "errors"

var errNotImplemented = errors.New("renderlist: not implemented")

func placeholder() error {
	return errNotImplemented
}
