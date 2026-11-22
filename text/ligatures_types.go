package text

func DisableLigaturesFeatures(features string) string {
	return disableLigaturesFeatures(features)
}

func WithLigatures(font Font, enabled bool) Font {
	return withLigatures(font, enabled)
}
