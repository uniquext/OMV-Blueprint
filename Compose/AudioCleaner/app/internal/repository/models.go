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
type BackupAvailability string
type JobKind string
type JobResult string

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

	BackupAvailable BackupAvailability = "available"
	BackupMissing   BackupAvailability = "missing"
	BackupRestored  BackupAvailability = "restored"
	BackupExpired   BackupAvailability = "expired"

	JobKindProcess JobKind = "process"
	JobKindRestore JobKind = "restore"

	JobResultProcessing JobResult = "processing"
	JobResultCompatible JobResult = "compatible"
	JobResultSucceeded  JobResult = "succeeded"
	JobResultFailed     JobResult = "failed"
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

type HistoryRecord struct {
	ID              int64           `json:"id"`
	Path            string          `json:"path"`
	Status          Status          `json:"status"`
	DiscoverySource DiscoverySource `json:"discovery_source"`
	AudioSignature  string          `json:"audio_signature"`
	VideoSignature  string          `json:"video_signature"`
	LastError       string          `json:"last_error"`
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

func (e JobEvent) MarshalJSON() ([]byte, error) {
	type jobEventJSON struct {
		ID         int64   `json:"id"`
		FileID     int64   `json:"file_id"`
		EventKind  string  `json:"event_kind"`
		EventCode  string  `json:"event_code"`
		Phase      *string `json:"phase"`
		Status     *string `json:"status"`
		Outcome    *string `json:"outcome"`
		Attempt    int     `json:"attempt"`
		Command    string  `json:"command"`
		Message    string  `json:"message"`
		Error      string  `json:"error"`
		StartedAt  string  `json:"started_at"`
		FinishedAt string  `json:"finished_at"`
	}
	return json.Marshal(jobEventJSON{
		ID:         e.ID,
		FileID:     e.FileID,
		EventKind:  string(e.EventKind),
		EventCode:  string(e.EventCode),
		Phase:      optionalString(e.Phase),
		Status:     optionalString(e.Status),
		Outcome:    optionalString(e.Outcome),
		Attempt:    e.Attempt,
		Command:    e.Command,
		Message:    e.Message,
		Error:      e.Error,
		StartedAt:  formatTime(e.StartedAt),
		FinishedAt: formatTime(e.FinishedAt),
	})
}

type JobRecord struct {
	ID            int64           `json:"id"`
	FileID        int64           `json:"file_id"`
	Kind          JobKind         `json:"kind"`
	TriggerSource DiscoverySource `json:"trigger_source"`
	Result        JobResult       `json:"result"`
	FinalError    string          `json:"final_error"`
	StartedAt     time.Time       `json:"started_at"`
	FinishedAt    time.Time       `json:"finished_at"`
}

func (j JobRecord) MarshalJSON() ([]byte, error) {
	type jobRecordJSON struct {
		ID            int64   `json:"id"`
		FileID        int64   `json:"file_id"`
		Kind          JobKind `json:"kind"`
		TriggerSource string  `json:"trigger_source"`
		Result        string  `json:"result"`
		FinalError    string  `json:"final_error"`
		StartedAt     string  `json:"started_at"`
		FinishedAt    string  `json:"finished_at"`
	}
	return json.Marshal(jobRecordJSON{
		ID:            j.ID,
		FileID:        j.FileID,
		Kind:          j.Kind,
		TriggerSource: string(j.TriggerSource),
		Result:        string(j.Result),
		FinalError:    j.FinalError,
		StartedAt:     formatTime(j.StartedAt),
		FinishedAt:    formatTime(j.FinishedAt),
	})
}

type BackupRecord struct {
	ID                int64     `json:"id"`
	CreatedByJobID    int64     `json:"created_by_job_id"`
	BackupPath        string    `json:"backup_path"`
	CreatedAt         time.Time `json:"created_at"`
	RestoredByJobID   int64     `json:"restored_by_job_id"`
	RestoreSafetyPath string    `json:"restore_safety_path"`
}

func (b BackupRecord) MarshalJSON() ([]byte, error) {
	type backupRecordJSON struct {
		ID                int64  `json:"id"`
		CreatedByJobID    int64  `json:"created_by_job_id"`
		BackupPath        string `json:"backup_path"`
		CreatedAt         string `json:"created_at"`
		RestoredByJobID   int64  `json:"restored_by_job_id"`
		RestoreSafetyPath string `json:"restore_safety_path"`
	}
	return json.Marshal(backupRecordJSON{
		ID:                b.ID,
		CreatedByJobID:    b.CreatedByJobID,
		BackupPath:        b.BackupPath,
		CreatedAt:         formatTime(b.CreatedAt),
		RestoredByJobID:   b.RestoredByJobID,
		RestoreSafetyPath: b.RestoreSafetyPath,
	})
}

type BackupView struct {
	BackupRecord
	FileID       int64              `json:"file_id"`
	OriginalPath string             `json:"original_path"`
	RestoredAt   time.Time          `json:"restored_at"`
	Availability BackupAvailability `json:"availability,omitempty"`
}

func (b BackupView) MarshalJSON() ([]byte, error) {
	type backupViewJSON struct {
		ID                int64              `json:"id"`
		FileID            int64              `json:"file_id"`
		CreatedByJobID    int64              `json:"created_by_job_id"`
		OriginalPath      string             `json:"original_path"`
		BackupPath        string             `json:"backup_path"`
		CreatedAt         string             `json:"created_at"`
		RestoredAt        string             `json:"restored_at"`
		RestoredByJobID   int64              `json:"restored_by_job_id"`
		RestoreSafetyPath string             `json:"restore_safety_path"`
		Availability      BackupAvailability `json:"availability,omitempty"`
	}
	return json.Marshal(backupViewJSON{
		ID:                b.ID,
		FileID:            b.FileID,
		CreatedByJobID:    b.CreatedByJobID,
		OriginalPath:      b.OriginalPath,
		BackupPath:        b.BackupPath,
		CreatedAt:         formatTime(b.CreatedAt),
		RestoredAt:        formatTime(b.RestoredAt),
		RestoredByJobID:   b.RestoredByJobID,
		RestoreSafetyPath: b.RestoreSafetyPath,
		Availability:      b.Availability,
	})
}

func optionalString[T ~string](value T) *string {
	if value == "" {
		return nil
	}
	text := string(value)
	return &text
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
