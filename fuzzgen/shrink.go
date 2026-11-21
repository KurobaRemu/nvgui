package fuzzgen

import (
	"fmt"
	"slices"
)

const defaultMaxAttempts = 256

func shrinkScript(script *Script, cfg ShrinkConfig, fails func(*Script) bool) (*ShrinkResult, error) {
	if script == nil {
		return nil, fmt.Errorf("fuzzgen: script is nil")
	}
	if fails == nil {
		return nil, fmt.Errorf("fuzzgen: fails predicate is nil")
	}
	maxAttempts := cfg.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = defaultMaxAttempts
	}
	minimal := cloneScript(script)
	result := &ShrinkResult{OriginalSteps: len(script.Steps)}
	result.Attempts++
	if !fails(cloneScript(minimal)) {
		return nil, fmt.Errorf("fuzzgen: initial script does not trigger failure")
	}
	if len(minimal.Steps) == 0 {
		return finishShrink(result, minimal), nil
	}

	try := func(candidate []Step) bool {
		if result.Attempts >= maxAttempts {
			return false
		}
		result.Attempts++
		proposed := cloneScript(minimal)
		proposed.Steps = slices.Clone(candidate)
		if !fails(cloneScript(proposed)) {
			return false
		}
		minimal = proposed
		return true
	}

	for chunk := len(minimal.Steps) / 2; chunk >= 1 && result.Attempts < maxAttempts; {
		if chunk > len(minimal.Steps) {
			chunk = len(minimal.Steps) / 2
			if chunk < 1 {
				chunk = 1
			}
		}
		for start := 0; start < len(minimal.Steps) && result.Attempts < maxAttempts; {
			end := min(start+chunk, len(minimal.Steps))
			candidate := slices.Concat(minimal.Steps[:start], minimal.Steps[end:])
			if try(candidate) {
				if chunk > len(minimal.Steps) {
					chunk = len(minimal.Steps) / 2
					if chunk < 1 {
						chunk = 1
					}
				}
				continue
			}
			start = end
		}
		if chunk == 1 {
			break
		}
		chunk /= 2
	}

	low, high := 0, len(minimal.Steps)
	for low < high && result.Attempts < maxAttempts {
		length := low + (high-low)/2
		if length >= len(minimal.Steps) {
			break
		}
		if try(slices.Clone(minimal.Steps[:length])) {
			high = len(minimal.Steps)
		} else {
			low = length + 1
		}
	}

	for result.Attempts < maxAttempts {
		progress := false
		for index := 0; index < len(minimal.Steps) && result.Attempts < maxAttempts; {
			candidate := slices.Concat(minimal.Steps[:index], minimal.Steps[index+1:])
			if try(candidate) {
				progress = true
				continue
			}
			index++
		}
		if !progress {
			break
		}
	}

	return finishShrink(result, minimal), nil
}

func finishShrink(result *ShrinkResult, minimal *Script) *ShrinkResult {
	result.MinimalSteps = len(minimal.Steps)
	result.Minimal = minimal
	return result
}

func cloneScript(script *Script) *Script {
	clone := *script
	clone.Steps = slices.Clone(script.Steps)
	return &clone
}
