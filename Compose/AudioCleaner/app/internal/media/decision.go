package media

import (
	"path/filepath"
	"strings"
)

type Action string

const (
	ActionUnsupported       Action = "unsupported"
	ActionAlreadyCompatible Action = "already_compatible"
	ActionTranscode         Action = "transcode"
)

type DecisionConfig struct {
	Extensions         []string
	IncompatibleCodecs []string
}

type AudioPlan struct {
	InputAudioOrdinal int
	StreamIndex       int
	Codec             string
	Channels          int
	Transcode         bool
	Bitrate           string
}

type Decision struct {
	Action     Action
	AudioPlans []AudioPlan
	Reason     string
}

func Decide(path string, probe ProbeData, cfg DecisionConfig) Decision {
	if !containsLower(cfg.Extensions, filepath.Ext(path)) {
		return Decision{
			Action: ActionUnsupported,
			Reason: "unsupported extension",
		}
	}

	audioStreams := probe.AudioStreams()
	audioPlans := make([]AudioPlan, 0, len(audioStreams))
	transcode := false

	for ordinal, stream := range audioStreams {
		codec := strings.ToLower(stream.CodecName)
		needsTranscode := containsLower(cfg.IncompatibleCodecs, codec)
		transcode = transcode || needsTranscode

		audioPlans = append(audioPlans, AudioPlan{
			InputAudioOrdinal: ordinal,
			StreamIndex:       stream.Index,
			Codec:             codec,
			Channels:          stream.Channels,
			Transcode:         needsTranscode,
			Bitrate:           AACBitrate(stream.Channels),
		})
	}

	if transcode {
		return Decision{
			Action:     ActionTranscode,
			AudioPlans: audioPlans,
		}
	}

	return Decision{
		Action:     ActionAlreadyCompatible,
		AudioPlans: audioPlans,
	}
}

func AACBitrate(channels int) string {
	if channels <= 2 {
		return "256k"
	}
	if channels <= 6 {
		return "640k"
	}
	return "768k"
}

func containsLower(values []string, value string) bool {
	needle := strings.ToLower(value)
	for _, candidate := range values {
		if strings.ToLower(candidate) == needle {
			return true
		}
	}
	return false
}
