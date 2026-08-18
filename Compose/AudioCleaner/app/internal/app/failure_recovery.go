package app

import (
	"context"
	"database/sql"
	"errors"
	"os"

	"omv-blueprint/compose/audiocleaner/internal/api"
	"omv-blueprint/compose/audiocleaner/internal/failure"
	"omv-blueprint/compose/audiocleaner/internal/pipeline"
	"omv-blueprint/compose/audiocleaner/internal/repository"
)

type RecoveryItem struct {
	FileID         int64                          `json:"file_id"`
	Path           string                         `json:"path"`
	Issue          *repository.FailureIssue       `json:"issue,omitempty"`
	Journal        *repository.ReplacementJournal `json:"journal,omitempty"`
	BackupPath     string                         `json:"backup_path"`
	OutputPath     string                         `json:"output_path"`
	OriginalExists bool                           `json:"original_exists"`
	BackupExists   bool                           `json:"backup_exists"`
	OutputExists   bool                           `json:"output_exists"`
	Recommendation string                         `json:"recommendation"`
	Risk           string                         `json:"risk"`
	Actions        []string                       `json:"actions"`
	Audit          []repository.RecoveryAudit     `json:"audit"`
}

func (s *Service) RetryHistoryContext(ctx context.Context, id int64) (any, error) {
	job, err := s.db.JobByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if job.Result != repository.JobResultFailed || job.FailureCode == "manual_recovery" {
		return nil, api.ErrJobNotActionable
	}
	file, err := s.db.FileByID(ctx, job.FileID)
	if err != nil {
		return nil, err
	}
	issue, issueErr := s.db.ActiveFailureIssueByFile(ctx, file.ID)
	if issueErr != nil && !errors.Is(issueErr, sql.ErrNoRows) {
		return nil, issueErr
	}
	return s.retryContext(file, job, nullableIssue(issue, issueErr), "manual_history", issueErr == nil), nil
}

func (s *Service) RetryFileContext(ctx context.Context, id int64) (any, error) {
	file, issue, original, hasIssue, err := s.retryFileFacts(ctx, id, false)
	if err != nil {
		return nil, err
	}
	return s.retryContext(file, original, nullableIssue(issue, absentIssueError(hasIssue)), "manual_file", hasIssue), nil
}

func (s *Service) RetryRecoveryContext(ctx context.Context, id int64) (any, error) {
	file, issue, original, _, err := s.retryFileFacts(ctx, id, true)
	if err != nil {
		return nil, err
	}
	return s.retryContext(file, original, issue, "recovery_center", true), nil
}

func (s *Service) RetryRecovery(ctx context.Context, id int64) (any, error) {
	file, issue, _, _, err := s.retryFileFacts(ctx, id, true)
	if err != nil {
		return nil, err
	}
	jobID, err := s.retryFailedFile(ctx, file, issue, "recovery_center")
	if err != nil {
		return nil, err
	}
	s.logf("recovery retry queued job_id=%d file_id=%d path=%s", jobID, file.ID, file.Path)
	if s.events != nil {
		s.events.Publish("job.retry_queued", map[string]any{
			"job_id": jobID, "file_id": file.ID, "path": file.Path, "source": "recovery_center",
		})
	}
	return map[string]any{"status": "queued", "job_id": jobID, "file_id": file.ID, "path": file.Path}, nil
}

func (s *Service) retryFailedFile(ctx context.Context, file repository.FileFacts, issue repository.FailureIssue, source string) (int64, error) {
	s.workerIntakeMu.Lock()
	defer s.workerIntakeMu.Unlock()
	if !s.canEnqueueLocked(file.Path) || s.queue.Contains(file.Path) {
		return 0, api.ErrProcessing
	}
	original, err := s.loadJobByID(ctx, issue.OriginJobID)
	if err != nil {
		return 0, err
	}
	_, retry, err := s.db.StartManualRetryJob(ctx, file, original.ID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, api.ErrFileNotProcessable
		}
		return 0, err
	}
	if !s.enqueueExistingJob(file.Path, pipeline.JobSourceManual, retry.ID) {
		_, _ = s.db.FinishJob(ctx, retry.ID, repository.JobResultFailed, "queue_error: unable to enqueue manual retry")
		return 0, api.ErrProcessing
	}
	_, err = s.db.CreateRecoveryAudit(ctx, repository.RecoveryAudit{
		FileID: file.ID, JobID: retry.ID, OriginalJobID: original.ID,
		OriginalFailureCode: original.FailureCode, OriginalFailure: original.FinalError, Actor: "local_operator",
		Action: "retry", Source: source, Result: "queued", Detail: "failure suppression bypassed once",
	})
	if err != nil {
		s.queue.Done(file.Path)
		_, _ = s.db.FinishJob(ctx, retry.ID, repository.JobResultFailed, "audit_error: "+err.Error())
		return 0, err
	}
	if err := s.db.ConfirmFailureAttempt(ctx, file.ID, source); err != nil && !errors.Is(err, sql.ErrNoRows) {
		s.logf("confirm retry issue job_id=%d source=%s: %v", retry.ID, source, err)
	}
	return retry.ID, nil
}

func absentIssueError(hasIssue bool) error {
	if hasIssue {
		return nil
	}
	return sql.ErrNoRows
}

func (s *Service) retryContext(file repository.FileFacts, original repository.JobRecord, issue any, source string, bypassesSuppression bool) map[string]any {
	changed := false
	if info, statErr := os.Stat(file.Path); statErr == nil {
		changed = info.Size() != file.Size || info.ModTime().UnixNano() != file.MTimeNS
	}
	policyChanged := false
	if value, ok := issue.(repository.FailureIssue); ok {
		policyChanged = value.PolicyVersion != s.config().Audio.Version
	}
	return map[string]any{
		"job_id": original.ID, "original_job_id": original.ID, "file_id": file.ID, "path": file.Path,
		"failure_code": original.FailureCode, "final_error": original.FinalError, "issue": issue,
		"file_changed": changed, "policy_changed": policyChanged, "source": source,
		"bypasses_suppression_once": bypassesSuppression,
	}
}

func (s *Service) retryFileFacts(ctx context.Context, id int64, recoveryOnly bool) (repository.FileFacts, repository.FailureIssue, repository.JobRecord, bool, error) {
	file, err := s.db.FileByID(ctx, id)
	if err != nil {
		return repository.FileFacts{}, repository.FailureIssue{}, repository.JobRecord{}, false, err
	}
	issue, issueErr := s.db.ActiveFailureIssueByFile(ctx, id)
	hasIssue := issueErr == nil
	if issueErr != nil && !errors.Is(issueErr, sql.ErrNoRows) {
		return repository.FileFacts{}, repository.FailureIssue{}, repository.JobRecord{}, false, issueErr
	}
	if file.BackupFile != "" || (!hasIssue && file.ComplianceStatus != repository.ComplianceNoncompliant) || (recoveryOnly && !hasIssue) {
		return repository.FileFacts{}, repository.FailureIssue{}, repository.JobRecord{}, false, api.ErrFileNotProcessable
	}
	if journal, journalErr := s.db.ReplacementJournalByFile(ctx, id); journalErr == nil && journal.JobID != 0 {
		return repository.FileFacts{}, repository.FailureIssue{}, repository.JobRecord{}, false, api.ErrFileNotProcessable
	} else if journalErr != nil && !errors.Is(journalErr, sql.ErrNoRows) {
		return repository.FileFacts{}, repository.FailureIssue{}, repository.JobRecord{}, false, journalErr
	}
	if hasIssue && issue.Category == string(failure.Recovery) {
		return repository.FileFacts{}, repository.FailureIssue{}, repository.JobRecord{}, false, api.ErrFileNotProcessable
	}
	var original repository.JobRecord
	if hasIssue {
		original, err = s.db.JobByID(ctx, issue.OriginJobID)
		if err != nil {
			return repository.FileFacts{}, repository.FailureIssue{}, repository.JobRecord{}, false, err
		}
	}
	return file, issue, original, hasIssue, nil
}

func nullableIssue(issue repository.FailureIssue, err error) any {
	if err != nil {
		return nil
	}
	return issue
}

func (s *Service) ListRecovery(ctx context.Context) (any, error) {
	files, err := s.db.Files(ctx)
	if err != nil {
		return nil, err
	}
	issues, err := s.db.ActiveFailureIssues(ctx)
	if err != nil {
		return nil, err
	}
	journals, err := s.db.ReplacementJournals(ctx)
	if err != nil {
		return nil, err
	}
	items := buildRecoveryItems(files, issues, journals)
	result := make([]RecoveryItem, 0, len(items))
	for _, item := range items {
		item.OriginalExists = pathExists(item.Path)
		item.BackupExists = pathExists(item.BackupPath)
		item.OutputExists = pathExists(item.OutputPath)
		item.Audit, err = s.db.RecoveryAudits(ctx, item.FileID)
		if err != nil {
			return nil, err
		}
		result = append(result, *item)
	}
	return result, nil
}

func pathExists(path string) bool {
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

func (s *Service) ListFailures(ctx context.Context) (any, error) {
	return s.db.ActiveFailureIssues(ctx)
}

func (s *Service) RestoreRecovery(ctx context.Context, id int64) (result any, err error) {
	result, err = s.RestoreFileBackup(ctx, id)
	if err != nil {
		s.auditRecoveryFailure(ctx, id, "restore", err)
	}
	return result, err
}

func (s *Service) DeleteRecoveryBackup(ctx context.Context, id int64) (result any, err error) {
	result, err = s.DeleteFileBackup(ctx, id)
	if err != nil {
		s.auditRecoveryFailure(ctx, id, "delete", err)
	}
	return result, err
}

func (s *Service) auditRecoveryFailure(ctx context.Context, fileID int64, action string, actionErr error) {
	if _, err := s.db.CreateRecoveryAudit(ctx, repository.RecoveryAudit{FileID: fileID, Actor: "local_operator", Action: action, Source: "recovery_center", Result: "failed", Detail: actionErr.Error()}); err != nil {
		s.logf("record failed recovery action file_id=%d action=%s: %v", fileID, action, err)
	}
}

func buildRecoveryItems(files []repository.FileFacts, issues []repository.FailureIssue, journals []repository.ReplacementJournal) map[int64]*RecoveryItem {
	items := make(map[int64]*RecoveryItem)
	for _, file := range files {
		if file.BackupFile != "" {
			items[file.ID] = &RecoveryItem{FileID: file.ID, Path: file.Path, BackupPath: file.BackupFile, Recommendation: "恢复已验证的原始副本", Risk: "删除副本可能导致原始内容无法恢复", Actions: []string{"restore", "retain", "delete"}}
		}
	}
	for i := range journals {
		journal := journals[i]
		if journal.TemporaryBackupPath == "" && journal.OutputPath == "" {
			continue
		}
		item := items[journal.FileID]
		if item == nil {
			item = &RecoveryItem{FileID: journal.FileID, Path: journal.OriginalPath}
			items[journal.FileID] = item
		}
		item.Journal = &journal
		item.BackupPath = journal.TemporaryBackupPath
		item.OutputPath = journal.OutputPath
		if journal.Phase == repository.ReplacementManualRecovery {
			item.Recommendation = "恢复已验证的原始副本"
			item.Risk = "替换结果不确定，必须保留至少一个可验证候选"
			item.Actions = []string{"restore", "retain"}
		}
	}
	for i := range issues {
		issue := issues[i]
		item := items[issue.FileID]
		if item == nil {
			continue
		}
		item.Issue = &issue
		if issue.Category == string(failure.Recovery) {
			item.Recommendation = issue.Advice
			item.Risk = "原路径状态不确定，禁止直接重试或删除"
			item.Actions = []string{"restore", "retain"}
		}
	}
	return items
}

func (s *Service) RetainRecovery(ctx context.Context, id int64) (any, error) {
	file, err := s.db.FileByID(ctx, id)
	if err != nil {
		return nil, err
	}
	journal, journalErr := s.db.ReplacementJournalByFile(ctx, id)
	if journalErr != nil && !errors.Is(journalErr, sql.ErrNoRows) {
		return nil, journalErr
	}
	hasJournalCandidate := journalErr == nil && (journal.TemporaryBackupPath != "" || journal.OutputPath != "")
	if file.BackupFile == "" && !hasJournalCandidate {
		return nil, api.ErrFileNotProcessable
	}
	if err := s.db.AddRecoveryAudit(ctx, repository.RecoveryAudit{FileID: id, Action: "retain", Source: "recovery_center", Result: "retained", Detail: "candidates intentionally retained"}); err != nil {
		return nil, err
	}
	return map[string]any{"status": "retained", "file_id": id}, nil
}
