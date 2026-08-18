package compatibility

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"omv-blueprint/compose/audiocleaner/internal/media"
)

const CurrentSchemaVersion = 1

type Source string

const (
	SourceProbe Source = "source_probe"
	SourceCache Source = "cache"
)

type TrackAction string

const (
	TrackActionCopy      TrackAction = "copy"
	TrackActionTranscode TrackAction = "transcode"
)

type Policy struct {
	Version            int
	IncompatibleCodecs []string
}

type AudioTrackEvidence struct {
	StreamIndex int    `json:"stream_index"`
	Codec       string `json:"codec"`
	Channels    int    `json:"channels"`
	Language    string `json:"language,omitempty"`
	Title       string `json:"title,omitempty"`
	Disposition string `json:"disposition,omitempty"`
}

type RuleMatch struct {
	Codec         string `json:"codec"`
	StreamIndexes []int  `json:"stream_indexes"`
}

type AudioPlanEvidence struct {
	StreamIndex int         `json:"stream_index"`
	Action      TrackAction `json:"action"`
	TargetCodec string      `json:"target_codec,omitempty"`
	Bitrate     string      `json:"bitrate,omitempty"`
}

type ProcessingEvidence struct {
	PolicyVersion int                  `json:"policy_version"`
	AudioTracks   []AudioTrackEvidence `json:"audio_tracks"`
	MatchedRules  []RuleMatch          `json:"matched_rules"`
	Action        media.Action         `json:"action"`
	AudioPlans    []AudioPlanEvidence  `json:"audio_plans"`
	Reason        string               `json:"reason"`
	AssessedAt    time.Time            `json:"assessed_at"`
}

type Assessment struct {
	SchemaVersion            int                  `json:"schema_version"`
	Source                   Source               `json:"source"`
	PolicyVersion            int                  `json:"policy_version"`
	PolicyIncompatibleCodecs []string             `json:"policy_incompatible_codecs"`
	AudioTracks              []AudioTrackEvidence `json:"audio_tracks"`
	MatchedRules             []RuleMatch          `json:"matched_rules"`
	Action                   media.Action         `json:"action"`
	AudioPlans               []AudioPlanEvidence  `json:"audio_plans"`
	Reason                   string               `json:"reason"`
	AssessedAt               time.Time            `json:"assessed_at"`
	ReusedAt                 time.Time            `json:"reused_at,omitempty"`
	LastProcessing           *ProcessingEvidence  `json:"last_processing,omitempty"`
}

func ProcessingEvidenceFrom(assessment Assessment) *ProcessingEvidence {
	if assessment.Action != media.ActionTranscode {
		return nil
	}
	return &ProcessingEvidence{
		PolicyVersion: assessment.PolicyVersion,
		AudioTracks:   append([]AudioTrackEvidence(nil), assessment.AudioTracks...),
		MatchedRules:  cloneRuleMatches(assessment.MatchedRules),
		Action:        assessment.Action,
		AudioPlans:    append([]AudioPlanEvidence(nil), assessment.AudioPlans...),
		Reason:        assessment.Reason,
		AssessedAt:    assessment.AssessedAt,
	}
}

func cloneRuleMatches(matches []RuleMatch) []RuleMatch {
	cloned := make([]RuleMatch, len(matches))
	for index, match := range matches {
		cloned[index] = match
		cloned[index].StreamIndexes = append([]int(nil), match.StreamIndexes...)
	}
	return cloned
}

func BuildAssessment(probe media.ProbeData, decision media.Decision, policy Policy, assessedAt time.Time) Assessment {
	audioStreams := probe.AudioStreams()
	tracks := make([]AudioTrackEvidence, 0, len(audioStreams))
	for _, stream := range audioStreams {
		tracks = append(tracks, AudioTrackEvidence{
			StreamIndex: stream.Index,
			Codec:       strings.ToLower(stream.CodecName),
			Channels:    stream.Channels,
			Language:    stream.Tags["language"],
			Title:       stream.Tags["title"],
			Disposition: stream.Disposition.String(),
		})
	}

	plans := make([]AudioPlanEvidence, 0, len(decision.AudioPlans))
	matches := make(map[string][]int)
	for _, plan := range decision.AudioPlans {
		evidence := AudioPlanEvidence{StreamIndex: plan.StreamIndex, Action: TrackActionCopy}
		if plan.Transcode {
			codec := strings.ToLower(plan.Codec)
			evidence.Action = TrackActionTranscode
			evidence.TargetCodec = "aac"
			evidence.Bitrate = plan.Bitrate
			matches[codec] = append(matches[codec], plan.StreamIndex)
		}
		plans = append(plans, evidence)
	}

	matchedRules := make([]RuleMatch, 0, len(matches))
	for codec, streamIndexes := range matches {
		matchedRules = append(matchedRules, RuleMatch{Codec: codec, StreamIndexes: streamIndexes})
	}
	sort.Slice(matchedRules, func(i, j int) bool { return matchedRules[i].Codec < matchedRules[j].Codec })

	return Assessment{
		SchemaVersion:            CurrentSchemaVersion,
		Source:                   SourceProbe,
		PolicyVersion:            policy.Version,
		PolicyIncompatibleCodecs: normalizeCodecs(policy.IncompatibleCodecs),
		AudioTracks:              tracks,
		MatchedRules:             matchedRules,
		Action:                   decision.Action,
		AudioPlans:               plans,
		Reason:                   assessmentReason(decision, lenMatchedTracks(matchedRules)),
		AssessedAt:               assessedAt,
	}
}

func assessmentReason(decision media.Decision, matchedTracks int) string {
	switch decision.Action {
	case media.ActionAlreadyCompatible:
		return "all audio tracks are compatible"
	case media.ActionTranscode:
		return fmt.Sprintf("%d audio track matches incompatible codec rules", matchedTracks)
	default:
		if decision.Reason != "" {
			return decision.Reason
		}
		return "media is unsupported"
	}
}

func lenMatchedTracks(matches []RuleMatch) int {
	total := 0
	for _, match := range matches {
		total += len(match.StreamIndexes)
	}
	return total
}

type CacheInput struct {
	KnownCompatible  bool
	HasRecoveryIssue bool
	StoredSize       int64
	StoredMTimeNS    int64
	CurrentSize      int64
	CurrentMTimeNS   int64
	Policy           Policy
	Assessment       Assessment
	Now              time.Time
}

type CacheResult struct {
	Reuse      bool
	Reason     string
	Assessment Assessment
}

func EvaluateCache(input CacheInput) CacheResult {
	if input.HasRecoveryIssue {
		return CacheResult{Reason: "file has an unresolved recovery issue"}
	}
	if !input.KnownCompatible {
		return CacheResult{Reason: "stored result is not compatible"}
	}
	if input.StoredSize != input.CurrentSize {
		return CacheResult{Reason: "file size changed"}
	}
	if input.StoredMTimeNS != input.CurrentMTimeNS {
		return CacheResult{Reason: "file modification time changed"}
	}
	if input.Assessment.SchemaVersion != CurrentSchemaVersion {
		return CacheResult{Reason: "stored assessment is missing or unsupported"}
	}
	if input.Assessment.Action != media.ActionAlreadyCompatible {
		return CacheResult{Reason: "stored assessment is not compatible"}
	}

	currentRules := normalizeCodecs(input.Policy.IncompatibleCodecs)
	storedRules := normalizeCodecs(input.Assessment.PolicyIncompatibleCodecs)
	if input.Assessment.PolicyVersion != input.Policy.Version || !equalStrings(storedRules, currentRules) {
		difference := symmetricDifference(storedRules, currentRules)
		trackCodecs := make(map[string]struct{}, len(input.Assessment.AudioTracks))
		for _, track := range input.Assessment.AudioTracks {
			trackCodecs[strings.ToLower(track.Codec)] = struct{}{}
		}
		for _, codec := range difference {
			if _, affected := trackCodecs[codec]; affected {
				return CacheResult{Reason: "audio policy change affects codec " + codec}
			}
		}
		return reused(input, currentRules, "audio policy change does not affect this file")
	}

	return reused(input, currentRules, "file facts and audio policy are unchanged")
}

func reused(input CacheInput, currentRules []string, reason string) CacheResult {
	assessment := input.Assessment
	assessment.Source = SourceCache
	assessment.PolicyVersion = input.Policy.Version
	assessment.PolicyIncompatibleCodecs = append([]string(nil), currentRules...)
	assessment.ReusedAt = input.Now
	assessment.Reason = reason
	return CacheResult{Reuse: true, Reason: reason, Assessment: assessment}
}

func normalizeCodecs(codecs []string) []string {
	set := make(map[string]struct{}, len(codecs))
	for _, codec := range codecs {
		codec = strings.ToLower(strings.TrimSpace(codec))
		if codec != "" {
			set[codec] = struct{}{}
		}
	}
	normalized := make([]string, 0, len(set))
	for codec := range set {
		normalized = append(normalized, codec)
	}
	sort.Strings(normalized)
	return normalized
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func symmetricDifference(left, right []string) []string {
	leftSet := make(map[string]struct{}, len(left))
	rightSet := make(map[string]struct{}, len(right))
	for _, value := range left {
		leftSet[value] = struct{}{}
	}
	for _, value := range right {
		rightSet[value] = struct{}{}
	}
	difference := make([]string, 0)
	for value := range leftSet {
		if _, exists := rightSet[value]; !exists {
			difference = append(difference, value)
		}
	}
	for value := range rightSet {
		if _, exists := leftSet[value]; !exists {
			difference = append(difference, value)
		}
	}
	sort.Strings(difference)
	return difference
}

func PolicyFingerprint(tracks []AudioTrackEvidence, incompatibleCodecs []string) string {
	rules := make(map[string]struct{})
	for _, codec := range normalizeCodecs(incompatibleCodecs) {
		rules[codec] = struct{}{}
	}
	codecs := make(map[string]struct{})
	for _, track := range tracks {
		codec := strings.ToLower(strings.TrimSpace(track.Codec))
		if codec != "" {
			codecs[codec] = struct{}{}
		}
	}
	ordered := make([]string, 0, len(codecs))
	for codec := range codecs {
		ordered = append(ordered, codec)
	}
	sort.Strings(ordered)
	parts := make([]string, 0, len(ordered))
	for _, codec := range ordered {
		_, affected := rules[codec]
		parts = append(parts, fmt.Sprintf("%s=%t", codec, affected))
	}
	return strings.Join(parts, ",")
}
