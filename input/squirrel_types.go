package input

type SquirrelAppOption struct {
	BundleID  string `json:"bundle_id"`
	ASCIIMode *bool  `json:"ascii_mode,omitempty"`
	VIMMode   *bool  `json:"vim_mode,omitempty"`
	NoInline  *bool  `json:"no_inline,omitempty"`
}

type SquirrelBundleStatus struct {
	BundleID    string            `json:"bundle_id"`
	Matched     bool              `json:"matched"`
	Option      SquirrelAppOption `json:"option,omitempty"`
	CLIMismatch bool              `json:"cli_mismatch"`
	Warning     string            `json:"warning,omitempty"`
}

func ParseSquirrelAppOptions(data []byte) (map[string]SquirrelAppOption, error) {
	return parseSquirrelAppOptions(data)
}

func CheckSquirrelBundle(bundleID string, launchPath string, opts map[string]SquirrelAppOption) SquirrelBundleStatus {
	return checkSquirrelBundle(bundleID, launchPath, opts)
}
