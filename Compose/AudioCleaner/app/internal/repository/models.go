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
	ID                  int64
	Path                string
	Status              Status
	QualificationSource QualificationSource
	Phase               Phase
	UnqualifiedReason   UnqualifiedReason
	Fingerprint         string
	Size                int64
	MTimeNS             int64
	AudioSignature      string
	VideoSignature      string
	Attempts            int
	LastError           string
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

type JobEvent struct {
	ID         int64
	FileID     int64
	EventType  string
	Phase      Phase
	Attempt    int
	Command    string
	Message    string
	Error      string
	StartedAt  time.Time
	FinishedAt time.Time
}

type BackupRecord struct {
	ID                int64
	FileID            int64
	OriginalPath      string
	BackupPath        string
	OriginalSize      int64
	OriginalMTimeNS   int64
	CreatedAt         time.Time
	ExpiresAt         time.Time
	RestoredAt        time.Time
	RestoreSafetyPath string
	Missing           bool
}

type TranscodeStats struct {
	Succeeded int
	Failed    int
}
