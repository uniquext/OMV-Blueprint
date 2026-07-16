package config

import (
	"sort"
	"strings"
)

const maxInt = int(^uint(0) >> 1)

func AudioContentEqual(left AudioConfig, right AudioConfig) bool {
	leftCodecs := normalizedCodecs(left.IncompatibleCodecs)
	rightCodecs := normalizedCodecs(right.IncompatibleCodecs)
	if len(leftCodecs) != len(rightCodecs) {
		return false
	}
	for index := range leftCodecs {
		if leftCodecs[index] != rightCodecs[index] {
			return false
		}
	}
	return true
}

func NextAudioVersion(version int) int {
	if version == maxInt {
		return 1
	}
	return version + 1
}

func normalizedCodecs(codecs []string) []string {
	unique := make(map[string]struct{}, len(codecs))
	for _, codec := range codecs {
		unique[strings.ToLower(codec)] = struct{}{}
	}
	normalized := make([]string, 0, len(unique))
	for codec := range unique {
		normalized = append(normalized, codec)
	}
	sort.Strings(normalized)
	return normalized
}
