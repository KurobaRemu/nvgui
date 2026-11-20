package screen

func NewReducerFrom(s *State) *Reducer {
	if s == nil {
		return NewReducer()
	}
	working := cloneState(s)
	reducer := &Reducer{working: working, seq: s.Seq}
	reducer.latest.Store(cloneState(s))
	return reducer
}
