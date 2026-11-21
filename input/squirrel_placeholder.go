package input

import "errors"

var errSquirrelNotImplemented = errors.New("input: squirrel app_options not implemented")

func parseSquirrelAppOptions([]byte) (map[string]SquirrelAppOption, error) {
	return nil, errSquirrelNotImplemented
}

func checkSquirrelBundle(string, string, map[string]SquirrelAppOption) SquirrelBundleStatus {
	return SquirrelBundleStatus{}
}
