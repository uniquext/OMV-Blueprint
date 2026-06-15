package repository

import (
	"encoding/json"
	"time"
)

type Status string
type DiscoverySource string
type PipelinePhase string
type FailureCause string
type EventKind string
type EventCode string
type EventOutcome string

const (
	StatusCompatible Status = "compatible"
	StatusProcessing Status = "processing"
	StatusProcessed  Status = "processed"
	StatusFailed     Status = "failed"
	StatusRestored   Status = "restored"
	StatusIgnored    Status = "ignored"

	DiscoveryScan     DiscoverySource = "scan"
	DiscoveryWatchdog DiscoverySource = "watchdog"
	DiscoveryManual   DiscoverySource = "manual"

	PipelinePhasePending     PipelinePhase = "pending"
	PipelinePhaseQueued      PipelinePhase = "queued"
	PipelinePhaseRetryWait   PipelinePhase = "retry_wait"
	PipelinePhaseChecking    PipelinePhase = "checking"
	PipelinePhaseTranscoding PipelinePhase = "transcoding"
	PipelinePhaseVerifying   PipelinePhase = "verifying"
	PipelinePhaseBackingUp   PipelinePhase = "backing_up"
	PipelinePhaseReplacing   PipelinePhase = "replacing"

	CauseFailed                      FailureCause = "failed"
	CauseUnsupported                 FailureCause = "unsupported"
	CauseFFProbeError                FailureCause = "ffprobe_error"
	CauseVerificationFailed          FailureCause = "verification_failed"
	CauseTimeout                     FailureCause = "timeout"
	CauseRestoreStatError            FailureCause = "restore_stat_error"
	CauseRestoreProbeError           FailureCause = "restore_probe_error"
	CauseRestoredRequiresTranscoding FailureCause = "restored_requires_transcoding"

	EventKindPhaseTransition EventKind = "phase_transition"
	EventKindStatusChange    EventKind = "status_change"
	EventKindOperation       EventKind = "operation"
	EventKindDiagnostic      EventKind = "diagnostic"

	EventCodeQueued                   EventCode = "queued"
	EventCodeChecking                 EventCode = "checking"
	EventCodeTranscoding              EventCode = "transcoding"
	EventCodeVerifying                EventCode = "verifying"
	EventCodeBackingUp                EventCode = "backing_up"
	EventCodeReplacing                EventCode = "replacing"
	EventCodeRetryWait                EventCode = "retry_wait"
	EventCodeCompatible               EventCode = "compatible"
	EventCodeProcessed                EventCode = "processed"
	EventCodeFailed                   EventCode = "failed"
	EventCodeIgnored                  EventCode = "ignored"
	EventCodeRestore                  EventCode = "restore"
	EventCodeFfmpegDataStreamFallback EventCode = "ffmpeg_data_stream_fallback"

	OutcomeTranscoded  EventOutcome = "transcoded"
	OutcomeFailed      EventOutcome = "failed"
	OutcomeUnsupported EventOutcome = "unsupported"
	OutcomeRestored    EventOutcome = "restored"
)

type FileRecord struct {
	ID              int64           `json:"id"`
	Path            string          `json:"path"`
	Status          Status          `json:"status"`
	DiscoverySource DiscoverySource `json:"discovery_source"`
	FailureCause    FailureCause    `json:"failure_cause"`
	PipelinePhase   PipelinePhase   `json:"pipeline_phase"`
	Fingerprint     string          `json:"fingerprint"`
	Size            int64           `json:"size"`
	MTimeNS         int64           `json:"mtime_ns"`
	AudioSignature  string          `json:"audio_signature"`
	VideoSignature  string          `json:"video_signature"`
	Attempts        int             `json:"attempts"`
	LastError       string          `json:"last_error"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
}

type JobEvent struct {
	ID         int64         `json:"id"`
	FileID     int64         `json:"file_id"`
	EventKind  EventKind     `json:"event_kind"`
	EventCode  EventCode     `json:"event_code"`
	Phase      PipelinePhase `json:"phase"`
	Status     Status        `json:"status"`
	Outcome    EventOutcome  `json:"outcome"`
	Attempt    int           `json:"attempt"`
	Command    string        `json:"command"`
	Message    string        `json:"message"`
	Error      string        `json:"error"`
	StartedAt  time.Time     `json:"started_at"`
	FinishedAt time.Time     `json:"finished_at"`
}

type BackupRecord struct {
	ID                int64     `json:"id"`
	FileID            int64     `json:"file_id"`
	OriginalPath      string    `json:"original_path"`
	BackupPath        string    `json:"backup_path"`
	OriginalSize      int64     `json:"original_size"`
	OriginalMTimeNS   int64     `json:"original_mtime_ns"`
	CreatedAt         time.Time `json:"created_at"`
	ExpiresAt         time.Time `json:"expires_at"`
	RestoredAt        time.Time `json:"restored_at"`
	RestoreSafetyPath string    `json:"restore_safety_path"`
	Missing           bool      `json:"missing"`
}

func (b BackupRecord) MarshalJSON() ([]byte, error) {
	type backupRecordJSON struct {
		ID                int64  `json:"id"`
		FileID            int64  `json:"file_id"`
		OriginalPath      string `json:"original_path"`
		BackupPath        string `json:"backup_path"`
		OriginalSize      int64  `json:"original_size"`
		OriginalMTimeNS   int64  `json:"original_mtime_ns"`
		CreatedAt         string `json:"created_at"`
		ExpiresAt         string `json:"expires_at"`
		RestoredAt        string `json:"restored_at"`
		RestoreSafetyPath string `json:"restore_safety_path"`
		Missing           bool   `json:"missing"`
	}
	return json.Marshal(backupRecordJSON{
		ID:                b.ID,
		FileID:            b.FileID,
		OriginalPath:      b.OriginalPath,
		BackupPath:        b.BackupPath,
		OriginalSize:      b.OriginalSize,
		OriginalMTimeNS:   b.OriginalMTimeNS,
		CreatedAt:         formatTime(b.CreatedAt),
		ExpiresAt:         formatTime(b.ExpiresAt),
		RestoredAt:        formatTime(b.RestoredAt),
		RestoreSafetyPath: b.RestoreSafetyPath,
		Missing:           b.Missing,
	})
}

type TranscodeStats struct {
	Succeeded int
	Failed    int
}

type PageRequest struct {
	Page     int
	PageSize int
}

type PageResult[T any] struct {
	Items    []T `json:"items"`
	Page     int `json:"page"`
	PageSize int `json:"page_size"`
	Total    int `json:"total"`
}

func (r PageRequest) Normalize() PageRequest {
	if r.Page < 1 {
		r.Page = 1
	}
	if r.PageSize < 1 {
		r.PageSize = 20
	}
	if r.PageSize > 1000 {
		r.PageSize = 1000
	}
	return r
}

func (r PageRequest) Offset() int {
	r = r.Normalize()
	return (r.Page - 1) * r.PageSize
}
