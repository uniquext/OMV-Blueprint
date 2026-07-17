package repository

import (
	"encoding/json"
	"time"
)

type DiscoverySource string
type JobResult string
type ComplianceStatus string
type ReplacementPhase string

const (
	DiscoveryScan     DiscoverySource = "scan"
	DiscoveryWatchdog DiscoverySource = "watchdog"
	DiscoveryManual   DiscoverySource = "manual"

	ReplacementPrepared        ReplacementPhase = "prepared"
	ReplacementBackupReady     ReplacementPhase = "backup_ready"
	ReplacementOutputInstalled ReplacementPhase = "output_installed"
	ReplacementRestoring       ReplacementPhase = "restoring"
	ReplacementCommitted       ReplacementPhase = "committed"

	JobResultProcessing JobResult = "processing"
	JobResultCompatible JobResult = "compatible"
	JobResultSucceeded  JobResult = "succeeded"
	JobResultFailed     JobResult = "failed"

	ComplianceUnknown      ComplianceStatus = ""
	ComplianceCompliant    ComplianceStatus = "compliant"
	ComplianceNoncompliant ComplianceStatus = "noncompliant"
)

type FileFacts struct {
	ID                 int64            `json:"id"`
	Path               string           `json:"path"`
	Size               int64            `json:"size"`
	MTimeNS            int64            `json:"mtime_ns"`
	AudioSignature     string           `json:"audio_signature"`
	VideoSignature     string           `json:"video_signature"`
	ComplianceStatus   ComplianceStatus `json:"compliance_status"`
	AudioPolicyVersion int              `json:"audio_policy_version"`
	BackupFile         string           `json:"backup_file"`
	CreatedAt          time.Time        `json:"created_at"`
	UpdatedAt          time.Time        `json:"updated_at"`
}

type ReplacementJournal struct {
	JobID                  int64            `json:"job_id"`
	FileID                 int64            `json:"file_id"`
	OriginalPath           string           `json:"original_path"`
	TemporaryBackupPath    string           `json:"temporary_backup_path"`
	OutputPath             string           `json:"output_path"`
	Phase                  ReplacementPhase `json:"phase"`
	OriginalSize           int64            `json:"original_size"`
	OriginalMTimeNS        int64            `json:"original_mtime_ns"`
	OriginalAudioSignature string           `json:"original_audio_signature"`
	OriginalVideoSignature string           `json:"original_video_signature"`
	LastError              string           `json:"last_error"`
	CreatedAt              time.Time        `json:"created_at"`
	UpdatedAt              time.Time        `json:"updated_at"`
}

type JobRecord struct {
	ID            int64           `json:"id"`
	FileID        int64           `json:"file_id"`
	TriggerSource DiscoverySource `json:"trigger_source"`
	Result        JobResult       `json:"result"`
	FinalError    string          `json:"final_error"`
	StartedAt     time.Time       `json:"started_at"`
	FinishedAt    time.Time       `json:"finished_at"`
	Ignored       bool            `json:"-"`
}

func (j JobRecord) MarshalJSON() ([]byte, error) {
	type jobRecordJSON struct {
		ID            int64  `json:"id"`
		FileID        int64  `json:"file_id"`
		TriggerSource string `json:"trigger_source"`
		Result        string `json:"result"`
		FinalError    string `json:"final_error"`
		StartedAt     string `json:"started_at"`
		FinishedAt    string `json:"finished_at"`
	}
	return json.Marshal(jobRecordJSON{
		ID:            j.ID,
		FileID:        j.FileID,
		TriggerSource: string(j.TriggerSource),
		Result:        string(j.Result),
		FinalError:    j.FinalError,
		StartedAt:     formatTime(j.StartedAt),
		FinishedAt:    formatTime(j.FinishedAt),
	})
}

type JobHistoryRecord struct {
	ID            int64           `json:"id"`
	FileID        int64           `json:"file_id"`
	Path          string          `json:"path"`
	TriggerSource DiscoverySource `json:"trigger_source"`
	Result        JobResult       `json:"result"`
	FinalError    string          `json:"final_error"`
	StartedAt     time.Time       `json:"started_at"`
	FinishedAt    time.Time       `json:"finished_at"`
}

func (j JobHistoryRecord) MarshalJSON() ([]byte, error) {
	type jobHistoryJSON struct {
		ID            int64  `json:"id"`
		FileID        int64  `json:"file_id"`
		Path          string `json:"path"`
		TriggerSource string `json:"trigger_source"`
		Result        string `json:"result"`
		FinalError    string `json:"final_error"`
		StartedAt     string `json:"started_at"`
		FinishedAt    string `json:"finished_at"`
	}
	return json.Marshal(jobHistoryJSON{
		ID:            j.ID,
		FileID:        j.FileID,
		Path:          j.Path,
		TriggerSource: string(j.TriggerSource),
		Result:        string(j.Result),
		FinalError:    j.FinalError,
		StartedAt:     formatTime(j.StartedAt),
		FinishedAt:    formatTime(j.FinishedAt),
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
