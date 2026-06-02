package media

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

type ValidationRules struct {
	IncompatibleCodecs       []string
	DurationToleranceSeconds int
	MaxSizeRatio             float64
	MaxSizeIncreaseBytes     int64
	OriginalSize             int64
	OutputSize               int64
}

func ValidateOutput(original ProbeData, output ProbeData, decision Decision, rules ValidationRules) error {
	if rules.OutputSize <= 0 {
		return fmt.Errorf("output file is empty")
	}

	if err := validateStreamCounts(original, output); err != nil {
		return err
	}

	if err := validateVideoStreams(original.OrdinaryVideoStreams(), output.OrdinaryVideoStreams()); err != nil {
		return err
	}

	originalAudio := original.AudioStreams()
	outputAudio := output.AudioStreams()
	if err := validateAudioPlans(originalAudio, outputAudio, decision.AudioPlans, rules.IncompatibleCodecs); err != nil {
		return err
	}

	if err := validateOutputSize(rules); err != nil {
		return err
	}

	return validateDuration(original, output, rules.DurationToleranceSeconds)
}

func validateStreamCounts(original ProbeData, output ProbeData) error {
	if got, want := len(output.OrdinaryVideoStreams()), len(original.OrdinaryVideoStreams()); got != want {
		return fmt.Errorf("ordinary video stream count = %d, want %d", got, want)
	}
	if got, want := len(output.AudioStreams()), len(original.AudioStreams()); got != want {
		return fmt.Errorf("audio stream count = %d, want %d", got, want)
	}
	if got, want := len(output.SubtitleStreams()), len(original.SubtitleStreams()); got < want {
		return fmt.Errorf("subtitle stream count = %d, want at least %d", got, want)
	}
	if got, want := len(output.AttachmentStreams()), len(original.AttachmentStreams()); got < want {
		return fmt.Errorf("attachment stream count = %d, want at least %d", got, want)
	}
	return nil
}

func validateVideoStreams(originalVideo []Stream, outputVideo []Stream) error {
	for ordinal, originalStream := range originalVideo {
		outputStream := outputVideo[ordinal]

		if !strings.EqualFold(outputStream.CodecName, originalStream.CodecName) {
			return fmt.Errorf("ordinary video stream %d codec = %q, want %q", ordinal, outputStream.CodecName, originalStream.CodecName)
		}
		if originalStream.Width <= 0 || originalStream.Height <= 0 || outputStream.Width <= 0 || outputStream.Height <= 0 {
			return fmt.Errorf("ordinary video stream %d dimensions must be positive: output %dx%d, original %dx%d", ordinal, outputStream.Width, outputStream.Height, originalStream.Width, originalStream.Height)
		}
		if outputStream.Width != originalStream.Width || outputStream.Height != originalStream.Height {
			return fmt.Errorf("ordinary video stream %d dimensions = %dx%d, want %dx%d", ordinal, outputStream.Width, outputStream.Height, originalStream.Width, originalStream.Height)
		}
	}
	return nil
}

func validateAudioPlans(originalAudio []Stream, outputAudio []Stream, audioPlans []AudioPlan, incompatibleCodecs []string) error {
	if !validAudioPlans(originalAudio, audioPlans) {
		return fmt.Errorf("audio plans do not match original audio streams")
	}

	for outputOrdinal, outputStream := range outputAudio {
		if containsLower(incompatibleCodecs, outputStream.CodecName) {
			return fmt.Errorf("output audio stream %d uses incompatible codec %q", outputOrdinal, outputStream.CodecName)
		}

		plan := audioPlans[outputOrdinal]
		if plan.InputAudioOrdinal != outputOrdinal {
			return fmt.Errorf("audio reorder is not supported: output audio stream %d maps input audio stream %d", outputOrdinal, plan.InputAudioOrdinal)
		}

		source := originalAudio[plan.InputAudioOrdinal]
		if plan.Transcode {
			if !containsLower(incompatibleCodecs, source.CodecName) {
				return fmt.Errorf("source codec for output audio stream %d = %q, want incompatible codec for transcode plan", outputOrdinal, source.CodecName)
			}
			if !strings.EqualFold(outputStream.CodecName, "aac") {
				return fmt.Errorf("output audio stream %d codec = %q, want aac", outputOrdinal, outputStream.CodecName)
			}
		} else if !strings.EqualFold(outputStream.CodecName, source.CodecName) {
			return fmt.Errorf("output audio stream %d codec = %q, want %q", outputOrdinal, outputStream.CodecName, source.CodecName)
		}
		if outputStream.Channels != source.Channels {
			return fmt.Errorf("output audio stream %d channels = %d, want %d", outputOrdinal, outputStream.Channels, source.Channels)
		}

		if err := validateAudioMetadata(outputOrdinal, source, outputStream); err != nil {
			return err
		}
	}
	return nil
}

func validateAudioMetadata(outputOrdinal int, original Stream, output Stream) error {
	if want := original.Tags["language"]; want != "" && output.Tags["language"] != want {
		return fmt.Errorf("output audio stream %d language = %q, want %q", outputOrdinal, output.Tags["language"], want)
	}
	if want := original.Tags["title"]; want != "" && output.Tags["title"] != want {
		return fmt.Errorf("output audio stream %d title = %q, want %q", outputOrdinal, output.Tags["title"], want)
	}
	if want, got := original.Disposition.String(), output.Disposition.String(); got != want {
		return fmt.Errorf("output audio stream %d disposition = %q, want %q", outputOrdinal, got, want)
	}
	return nil
}

func validateOutputSize(rules ValidationRules) error {
	maxByRatio := float64(rules.OriginalSize) * rules.MaxSizeRatio
	maxByIncrease := float64(rules.OriginalSize) + float64(rules.MaxSizeIncreaseBytes)
	maxAllowed := math.Max(maxByRatio, maxByIncrease)
	if float64(rules.OutputSize) > maxAllowed {
		return fmt.Errorf("output size = %d, max allowed %.0f", rules.OutputSize, maxAllowed)
	}
	return nil
}

func validateDuration(original ProbeData, output ProbeData, toleranceSeconds int) error {
	originalDuration, hasOriginalDuration := positiveDurationSeconds(original)
	if !hasOriginalDuration {
		return nil
	}

	outputDuration, hasOutputDuration := positiveDurationSeconds(output)
	if !hasOutputDuration {
		return fmt.Errorf("output duration is missing or invalid")
	}

	delta := math.Abs(outputDuration - originalDuration)
	if delta > float64(toleranceSeconds) {
		return fmt.Errorf("duration delta = %.3f seconds, max allowed %d seconds", delta, toleranceSeconds)
	}
	return nil
}

func positiveDurationSeconds(probe ProbeData) (float64, bool) {
	duration, err := strconv.ParseFloat(probe.Format.Duration, 64)
	if err != nil || duration <= 0 || math.IsNaN(duration) || math.IsInf(duration, 0) {
		return 0, false
	}
	return duration, true
}
