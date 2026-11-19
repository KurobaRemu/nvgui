package screen

// placeholder

import (
	"errors"

	"nvgui/protocol"
)

var errNotImplemented = errors.New("not implemented")

type Reducer struct{}

func NewReducer() *Reducer { return &Reducer{} }

func (r *Reducer) Apply(events []protocol.Event) ([]*State, error) {
	return nil, errNotImplemented
}

func (r *Reducer) Latest() *State { return nil }

type Layer struct {
	Grid                 int
	ScreenRow, ScreenCol int
}

func Layers(s *State) []Layer { return nil }

func NewReducerFrom(s *State) *Reducer { return &Reducer{} }
