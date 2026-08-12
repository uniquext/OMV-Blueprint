package repository

import (
	"encoding/json"
	"time"

	"omv-blueprint/compose/audiocleaner/internal/compatibility"
	"omv-blueprint/compose/audiocleaner/internal/media"
	"omv-blueprint/compose/audiocleaner/internal/replacement"
)

type DiscoverySource string
type JobResult string
type ComplianceStatus string
type ReplacementPhase = replacement.Phase

const (
	DiscoveryScan     DiscoverySource = "scan"
	DiscoveryWatchdog DiscoverySource = "watchdog"
	DiscoveryManual   DiscoverySource = "manual"

	ReplacementPrepared        = replacement.PhasePrepared
	ReplacementBackupReady     = replacement.PhaseBackupReady
	ReplacementInstalling      = replacement.PhaseInstalling
	ReplacementOutputInstalled = replacement.PhaseOutputInstalled
	ReplacementRestoring       = replacement.PhaseRestoring
	ReplacementManualRecovery  = replacement.PhaseManualRecovery
	ReplacementCommitted       = replacement.PhaseCommitted

	JobResultProcessing JobResult = "processing"
	JobResultCompatible JobResult = "compatible"
	JobResultSucceeded  JobResult = "succeeded"
	JobResultFailed     JobResult = "failed"

	ComplianceUnknown      ComplianceStatus = ""
	ComplianceCompliant    ComplianceStatus = "compliant"
	ComplianceNoncompliant ComplianceStatus = "noncompliant"
)

type FileFacts struct {
	ID                      int64                     `json:"id"`
	Path                    string                    `json:"path"`
	Size                    int64                     `json:"size"`
	MTimeNS                 int64                     `json:"mtime_ns"`
	AudioSignature          string                    `json:"audio_signature"`
	VideoSignature          string                    `json:"video_signature"`
	ComplianceStatus        ComplianceStatus          `json:"compliance_status"`
	AudioPolicyVersion      int                       `json:"audio_policy_version"`
	BackupFile              string                    `json:"backup_file"`
	MissingAt               *time.Time                `json:"missing_at,omitempty"`
	CompatibilityAssessment *compatibility.Assessment `json:"compatibility_assessment,omitempty"`
	CreatedAt               time.Time                 `json:"created_at"`
	UpdatedAt               time.Time                 `json:"updated_at"`
}

type ReplacementJournal struct {
	JobID                  int64                    `json:"job_id"`
	FileID                 int64                    `json:"file_id"`
	OriginalPath           string                   `json:"original_path"`
	TemporaryBackupPath    string                   `json:"temporary_backup_path"`
	OutputPath             string                   `json:"output_path"`
	Phase                  ReplacementPhase         `json:"phase"`
	OriginalSize           int64                    `json:"original_size"`
	OriginalMTimeNS        int64                    `json:"original_mtime_ns"`
	OriginalAudioSignature string                   `json:"original_audio_signature"`
	OriginalVideoSignature string                   `json:"original_video_signature"`
	OutputSize             int64                    `json:"output_size"`
	OutputMTimeNS          int64                    `json:"output_mtime_ns"`
	OutputAudioSignature   string                   `json:"output_audio_signature"`
	OutputVideoSignature   string                   `json:"output_video_signature"`
	QualityAssessment      *media.QualityAssessment `json:"quality_assessment,omitempty"`
	LastError              string                   `json:"last_error"`
	CreatedAt              time.Time                `json:"created_at"`
	UpdatedAt              time.Time                `json:"updated_at"`
}

type FailureIssue struct {
	ID                int64      `json:"id"`
	FileID            int64      `json:"file_id"`
	Path              string     `json:"path"`
	OriginJobID       int64      `json:"origin_job_id"`
	Stage             string     `json:"stage"`
	Category          string     `json:"category"`
	Code              string     `json:"code"`
	Summary           string     `json:"summary"`
	Advice            string     `json:"advice"`
	RetryStrategy     string     `json:"retry_strategy"`
	UnlockCondition   string     `json:"unlock_condition"`
	NextRetryAt       *time.Time `json:"next_retry_at,omitempty"`
	FileSize          int64      `json:"file_size"`
	FileMTimeNS       int64      `json:"file_mtime_ns"`
	PolicyVersion     int        `json:"policy_version"`
	OccurrenceCount   int        `json:"occurrence_count"`
	AttemptNumber     int        `json:"attempt_number"`
	MaxAttempts       int        `json:"max_attempts"`
	LastAttemptSource string     `json:"last_attempt_source"`
	ConfirmedAt       *time.Time `json:"confirmed_at,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	LastSeenAt        time.Time  `json:"last_seen_at"`
	ResolvedAt        *time.Time `json:"resolved_at,omitempty"`
}

type RecoveryAudit struct {
	ID                  int64     `json:"id"`
	FileID              int64     `json:"file_id"`
	JobID               int64     `json:"job_id"`
	OriginalJobID       int64     `json:"original_job_id"`
	OriginalFailureCode string    `json:"original_failure_code"`
	OriginalFailure     string    `json:"original_failure"`
	Actor               string    `json:"actor"`
	Action              string    `json:"action"`
	Source              string    `json:"source"`
	Result              string    `json:"result"`
	Detail              string    `json:"detail"`
	CreatedAt           time.Time `json:"created_at"`
}

type FailureDetails struct {
	Stage           string
	Category        string
	Code            string
	Summary         string
	Advice          string
	RetryStrategy   string
	UnlockCondition string
}

func (j ReplacementJournal) OriginalEvidence() replacement.Evidence {
	return replacement.Evidence{
		Size: j.OriginalSize, MTimeNS: j.OriginalMTimeNS,
		AudioSignature: j.OriginalAudioSignature, VideoSignature: j.OriginalVideoSignature,
	}
}

func (j ReplacementJournal) OutputEvidence() replacement.Evidence {
	return replacement.Evidence{
		Size: j.OutputSize, MTimeNS: j.OutputMTimeNS,
		AudioSignature: j.OutputAudioSignature, VideoSignature: j.OutputVideoSignature,
	}
}

type JobRecord struct {
	ID                int64                    `json:"id"`
	FileID            int64                    `json:"file_id"`
	ParentJobID       int64                    `json:"parent_job_id"`
	TriggerSource     DiscoverySource          `json:"trigger_source"`
	Result            JobResult                `json:"result"`
	FinalError        string                   `json:"final_error"`
	FailureCode       string                   `json:"failure_code"`
	FailureStage      string                   `json:"failure_stage"`
	FailureCategory   string                   `json:"failure_category"`
	FailureSummary    string                   `json:"failure_summary"`
	FailureAdvice     string                   `json:"failure_advice"`
	RetryStrategy     string                   `json:"retry_strategy"`
	UnlockCondition   string                   `json:"unlock_condition"`
	QualityAssessment *media.QualityAssessment `json:"quality_assessment,omitempty"`
	StartedAt         time.Time                `json:"started_at"`
	FinishedAt        time.Time                `json:"finished_at"`
	Ignored           bool                     `json:"-"`
}

func (j JobRecord) MarshalJSON() ([]byte, error) {
	type jobRecordJSON struct {
		ID                int64                    `json:"id"`
		FileID            int64                    `json:"file_id"`
		ParentJobID       int64                    `json:"parent_job_id"`
		TriggerSource     string                   `json:"trigger_source"`
		Result            string                   `json:"result"`
		FinalError        string                   `json:"final_error"`
		FailureCode       string                   `json:"failure_code"`
		FailureStage      string                   `json:"failure_stage"`
		FailureCategory   string                   `json:"failure_category"`
		FailureSummary    string                   `json:"failure_summary"`
		FailureAdvice     string                   `json:"failure_advice"`
		RetryStrategy     string                   `json:"retry_strategy"`
		UnlockCondition   string                   `json:"unlock_condition"`
		QualityAssessment *media.QualityAssessment `json:"quality_assessment,omitempty"`
		StartedAt         string                   `json:"started_at"`
		FinishedAt        string                   `json:"finished_at"`
	}
	return json.Marshal(jobRecordJSON{
		ID:                j.ID,
		FileID:            j.FileID,
		ParentJobID:       j.ParentJobID,
		TriggerSource:     string(j.TriggerSource),
		Result:            string(j.Result),
		FinalError:        j.FinalError,
		FailureCode:       j.FailureCode,
		FailureStage:      j.FailureStage,
		FailureCategory:   j.FailureCategory,
		FailureSummary:    j.FailureSummary,
		FailureAdvice:     j.FailureAdvice,
		RetryStrategy:     j.RetryStrategy,
		UnlockCondition:   j.UnlockCondition,
		QualityAssessment: j.QualityAssessment,
		StartedAt:         formatTime(j.StartedAt),
		FinishedAt:        formatTime(j.FinishedAt),
	})
}

type JobHistoryRecord struct {
	ID                int64                    `json:"id"`
	FileID            int64                    `json:"file_id"`
	ParentJobID       int64                    `json:"parent_job_id"`
	Path              string                   `json:"path"`
	TriggerSource     DiscoverySource          `json:"trigger_source"`
	Result            JobResult                `json:"result"`
	FinalError        string                   `json:"final_error"`
	FailureCode       string                   `json:"failure_code"`
	FailureStage      string                   `json:"failure_stage"`
	FailureCategory   string                   `json:"failure_category"`
	FailureSummary    string                   `json:"failure_summary"`
	FailureAdvice     string                   `json:"failure_advice"`
	RetryStrategy     string                   `json:"retry_strategy"`
	UnlockCondition   string                   `json:"unlock_condition"`
	QualityAssessment *media.QualityAssessment `json:"quality_assessment,omitempty"`
	StartedAt         time.Time                `json:"started_at"`
	FinishedAt        time.Time                `json:"finished_at"`
}

func (j JobHistoryRecord) MarshalJSON() ([]byte, error) {
	type jobHistoryJSON struct {
		ID                int64                    `json:"id"`
		FileID            int64                    `json:"file_id"`
		Path              string                   `json:"path"`
		TriggerSource     string                   `json:"trigger_source"`
		Result            string                   `json:"result"`
		FinalError        string                   `json:"final_error"`
		FailureCode       string                   `json:"failure_code"`
		ParentJobID       int64                    `json:"parent_job_id"`
		FailureStage      string                   `json:"failure_stage"`
		FailureCategory   string                   `json:"failure_category"`
		FailureSummary    string                   `json:"failure_summary"`
		FailureAdvice     string                   `json:"failure_advice"`
		RetryStrategy     string                   `json:"retry_strategy"`
		UnlockCondition   string                   `json:"unlock_condition"`
		QualityAssessment *media.QualityAssessment `json:"quality_assessment,omitempty"`
		StartedAt         string                   `json:"started_at"`
		FinishedAt        string                   `json:"finished_at"`
	}
	return json.Marshal(jobHistoryJSON{
		ID:                j.ID,
		FileID:            j.FileID,
		Path:              j.Path,
		TriggerSource:     string(j.TriggerSource),
		Result:            string(j.Result),
		FinalError:        j.FinalError,
		FailureCode:       j.FailureCode,
		ParentJobID:       j.ParentJobID,
		FailureStage:      j.FailureStage,
		FailureCategory:   j.FailureCategory,
		FailureSummary:    j.FailureSummary,
		FailureAdvice:     j.FailureAdvice,
		RetryStrategy:     j.RetryStrategy,
		UnlockCondition:   j.UnlockCondition,
		QualityAssessment: j.QualityAssessment,
		StartedAt:         formatTime(j.StartedAt),
		FinishedAt:        formatTime(j.FinishedAt),
	})
}

type OutcomeCounts struct {
	Compatible int
	Processed  int
	Failed     int
}

type ProcessJobStats struct {
	Succeeded int
	Failed    int
}

type RetentionPolicy struct {
	HistoryMaxAge       time.Duration
	HistoryMaxCount     int
	MissingFileMaxAge   time.Duration
	MissingFileMaxCount int
}

type RetentionResult struct {
	DeletedJobs  int `json:"deleted_jobs"`
	DeletedFiles int `json:"deleted_files"`
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
