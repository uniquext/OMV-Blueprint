package media

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

const QualitySchemaVersion = 1

type QualityGate string
type QualityStatus string

const (
	QualityStructure QualityGate = "structure"
	QualityVideo     QualityGate = "video"
	QualityAudio     QualityGate = "audio"
	QualityDuration  QualityGate = "duration"
	QualitySize      QualityGate = "size"

	QualityPassed QualityStatus = "passed"
	QualityFailed QualityStatus = "failed"
	QualityNotRun QualityStatus = "not_run"
)

type QualityCheck struct {
	Gate   QualityGate   `json:"gate"`
	Status QualityStatus `json:"status"`
	Detail string        `json:"detail"`
}

type QualityAssessment struct {
	SchemaVersion int            `json:"schema_version"`
	Passed        bool           `json:"passed"`
	Checks        []QualityCheck `json:"checks"`
	EvaluatedAt   time.Time      `json:"evaluated_at"`
}

type ValidationRules struct {
	IncompatibleCodecs       []string
	DurationToleranceSeconds int
	MaxSizeRatio             float64
	MaxSizeIncreaseBytes     int64
	OriginalSize             int64
	OutputSize               int64
}

func ValidateOutput(original ProbeData, output ProbeData, decision Decision, rules ValidationRules) error {
	report := EvaluateOutputQuality(original, output, decision, rules)
	for _, check := range report.Checks {
		if check.Status == QualityFailed {
			return errors.New(check.Detail)
		}
	}
	return nil
}

func EvaluateOutputQuality(original ProbeData, output ProbeData, decision Decision, rules ValidationRules) QualityAssessment {
	structureErr := error(nil)
	if rules.OutputSize <= 0 {
		structureErr = errors.New("output file is empty")
	} else {
		structureErr = validateStreamCounts(original, output)
	}

	checks := make([]QualityCheck, 0, 5)
	checks = append(checks, qualityResult(QualityStructure, "output structure is complete", structureErr))
	if structureErr != nil {
		checks = append(checks,
			QualityCheck{Gate: QualityVideo, Status: QualityNotRun, Detail: "not run because structure validation failed"},
			QualityCheck{Gate: QualityAudio, Status: QualityNotRun, Detail: "not run because structure validation failed"},
		)
	} else {
		checks = append(checks,
			qualityResult(QualityVideo, "video streams preserve codec and dimensions", validateVideoStreams(original.OrdinaryVideoStreams(), output.OrdinaryVideoStreams())),
			qualityResult(QualityAudio, "audio streams preserve order and metadata", validateAudioPlans(original.AudioStreams(), output.AudioStreams(), decision.AudioPlans, rules.IncompatibleCodecs)),
		)
	}
	checks = append(checks,
		qualityResult(QualityDuration, "duration is within tolerance", validateDuration(original, output, rules.DurationToleranceSeconds)),
		qualityResult(QualitySize, "output size is within limits", validateOutputSize(rules)),
	)

	passed := true
	for _, check := range checks {
		if check.Status != QualityPassed {
			passed = false
			break
		}
	}
	return QualityAssessment{
		SchemaVersion: QualitySchemaVersion,
		Passed:        passed,
		Checks:        checks,
		EvaluatedAt:   time.Now(),
	}
}

func qualityResult(gate QualityGate, successDetail string, err error) QualityCheck {
	if err != nil {
		return QualityCheck{Gate: gate, Status: QualityFailed, Detail: err.Error()}
	}
	return QualityCheck{Gate: gate, Status: QualityPassed, Detail: successDetail}
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
