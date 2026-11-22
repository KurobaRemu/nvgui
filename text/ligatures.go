package text

import "strings"

const disabledLigatureFeatures = "calt=0,liga=0,clig=0,dlig=0"

var ligatureFeatureTags = [...]string{"calt", "liga", "clig", "dlig"}

func disableLigaturesFeatures(features string) string {
	if features == "" {
		return disabledLigatureFeatures
	}
	kept := filterLigatureFeatures(normalizeFeatures(features))
	kept = append(kept, "calt=0", "liga=0", "clig=0", "dlig=0")
	return strings.Join(kept, ",")
}

func withLigatures(font Font, enabled bool) Font {
	if !enabled {
		font.Features = disableLigaturesFeatures(font.Features)
		return font
	}
	if font.Features == "" {
		return font
	}
	if featuresAreNormalized(font.Features) && !hasDisabledLigatureFeatures(font.Features) {
		return font
	}
	if !featuresAreNormalized(font.Features) {
		font.Features = normalizeFeatures(font.Features)
	}
	if hasDisabledLigatureFeatures(font.Features) {
		font.Features = enabledLigatureFeatures(font.Features)
	}
	return font
}

func featuresAreNormalized(features string) bool {
	if features == "" {
		return true
	}
	for len(features) > 0 {
		feature, rest, hasComma := strings.Cut(features, ",")
		if feature == "" || strings.TrimSpace(feature) != feature ||
			strings.HasPrefix(feature, "-") || strings.HasPrefix(feature, "+") {
			return false
		}
		if before, after, hasValue := strings.Cut(feature, "="); hasValue &&
			(strings.TrimSpace(before) != before || strings.TrimSpace(after) != after) {
			return false
		}
		if !hasComma {
			return true
		}
		features = rest
	}
	return false
}

func enabledLigatureFeatures(features string) string {
	if features == "" {
		return ""
	}
	parts := strings.Split(features, ",")
	kept := make([]string, 0, len(parts))
	for _, feature := range parts {
		if !disabledLigatureFeature(feature) {
			kept = append(kept, feature)
		}
	}
	return strings.Join(kept, ",")
}

func filterLigatureFeatures(features string) []string {
	if features == "" {
		return make([]string, 0, 4)
	}
	parts := strings.Split(features, ",")
	kept := make([]string, 0, len(parts))
	for _, feature := range parts {
		tag, _, _ := strings.Cut(feature, "=")
		if isLigatureTag(strings.TrimSpace(tag)) {
			continue
		}
		kept = append(kept, feature)
	}
	return kept
}

func hasDisabledLigatureFeatures(features string) bool {
	for len(features) > 0 {
		feature, rest, hasComma := strings.Cut(features, ",")
		if disabledLigatureFeature(feature) {
			return true
		}
		if !hasComma {
			return false
		}
		features = rest
	}
	return false
}

func disabledLigatureFeature(feature string) bool {
	tag, value, hasValue := strings.Cut(feature, "=")
	if !isLigatureTag(strings.TrimSpace(tag)) || !hasValue {
		return false
	}
	value = strings.TrimSpace(value)
	for _, char := range value {
		if char != '0' {
			return false
		}
	}
	return value != ""
}

func isLigatureTag(tag string) bool {
	for _, candidate := range ligatureFeatureTags {
		if tag == candidate {
			return true
		}
	}
	return false
}
