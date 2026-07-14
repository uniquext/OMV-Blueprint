package repository

import (
	"encoding/json"
	"time"
)

type DiscoverySource string
type BackupAvailability string
type JobKind string
type JobResult string

const (
	DiscoveryScan     DiscoverySource = "scan"
	DiscoveryWatchdog DiscoverySource = "watchdog"
	DiscoveryManual   DiscoverySource = "manual"

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

type FileFacts struct {
	ID             int64     `json:"id"`
	Path           string    `json:"path"`
	Size           int64     `json:"size"`
	MTimeNS        int64     `json:"mtime_ns"`
	AudioSignature string    `json:"audio_signature"`
	VideoSignature string    `json:"video_signature"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type FileBaseline struct {
	File      FileFacts
	LatestJob *JobRecord
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

type JobHistoryRecord struct {
	ID            int64           `json:"id"`
	FileID        int64           `json:"file_id"`
	Path          string          `json:"path"`
	Kind          JobKind         `json:"kind"`
	TriggerSource DiscoverySource `json:"trigger_source"`
	Result        JobResult       `json:"result"`
	FinalError    string          `json:"final_error"`
	StartedAt     time.Time       `json:"started_at"`
	FinishedAt    time.Time       `json:"finished_at"`
}

func (j JobHistoryRecord) MarshalJSON() ([]byte, error) {
	type jobHistoryJSON struct {
		ID            int64   `json:"id"`
		FileID        int64   `json:"file_id"`
		Path          string  `json:"path"`
		Kind          JobKind `json:"kind"`
		TriggerSource string  `json:"trigger_source"`
		Result        string  `json:"result"`
		FinalError    string  `json:"final_error"`
		StartedAt     string  `json:"started_at"`
		FinishedAt    string  `json:"finished_at"`
	}
	return json.Marshal(jobHistoryJSON{
		ID:            j.ID,
		FileID:        j.FileID,
		Path:          j.Path,
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

type OutcomeCounts struct {
	Compatible int
	Processed  int
	Failed     int
	Restored   int
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
