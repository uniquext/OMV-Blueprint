package pipeline

import (
	"path/filepath"
	"strings"
)

const audioCleanerTempMarker = ".audiocleaner-"

func IsAudioCleanerTempOutputPath(path string) bool {
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	if ext == "" {
		return false
	}
	stem := base[:len(base)-len(ext)]
	markerIndex := strings.LastIndex(stem, audioCleanerTempMarker)
	if markerIndex < 0 {
		return false
	}
	token := stem[markerIndex+len(audioCleanerTempMarker):]
	if token == "" {
		return false
	}
	for _, ch := range token {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return true
}
