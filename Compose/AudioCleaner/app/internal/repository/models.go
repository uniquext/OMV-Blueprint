package repository

import "time"

type Status string
type QualificationSource string
type Phase string
type UnqualifiedReason string

const (
	StatusQualified   Status = "qualified"
	StatusProcessing  Status = "processing"
	StatusUnqualified Status = "unqualified"

	SourceTranscoded        QualificationSource = "transcoded"
	SourceAlreadyCompatible QualificationSource = "already_compatible"
	SourceObserved          QualificationSource = "observed"
	SourceRestored          QualificationSource = "restored"

	PhaseQueued      Phase = "queued"
	PhaseChecking    Phase = "checking"
	PhaseTranscoding Phase = "transcoding"
	PhaseVerifying   Phase = "verifying"
	PhaseBackingUp   Phase = "backing_up"
	PhaseReplacing   Phase = "replacing"
	PhaseDeferred    Phase = "deferred"
	PhaseRetryWait   Phase = "retry_wait"

	ReasonFailed             UnqualifiedReason = "failed"
	ReasonUnsupported        UnqualifiedReason = "unsupported"
	ReasonFFProbeError       UnqualifiedReason = "ffprobe_error"
	ReasonVerificationFailed UnqualifiedReason = "verification_failed"
	ReasonTimeout            UnqualifiedReason = "timeout"
	ReasonIgnored            UnqualifiedReason = "ignored"
	ReasonRestored           UnqualifiedReason = "restored"
)

type FileRecord struct {
	ID                  int64               `json:"id"`
	Path                string              `json:"path"`
	Status              Status              `json:"status"`
	QualificationSource QualificationSource `json:"qualification_source"`
	Phase               Phase               `json:"phase"`
	UnqualifiedReason   UnqualifiedReason   `json:"unqualified_reason"`
	Fingerprint         string              `json:"fingerprint"`
	Size                int64               `json:"size"`
	MTimeNS             int64               `json:"mtime_ns"`
	AudioSignature      string              `json:"audio_signature"`
	VideoSignature      string              `json:"video_signature"`
	Attempts            int                 `json:"attempts"`
	LastError           string              `json:"last_error"`
	CreatedAt           time.Time           `json:"created_at"`
	UpdatedAt           time.Time           `json:"updated_at"`
}

type JobEvent struct {
	ID         int64     `json:"id"`
	FileID     int64     `json:"file_id"`
	EventType  string    `json:"event_type"`
	Phase      Phase     `json:"phase"`
	Attempt    int       `json:"attempt"`
	Command    string    `json:"command"`
	Message    string    `json:"message"`
	Error      string    `json:"error"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
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
