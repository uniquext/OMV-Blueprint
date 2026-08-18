package failure

import (
	"strings"
	"time"
)

type Category string
type Strategy string

const (
	PermanentMedia    Category = "permanent_media"
	UnsupportedPolicy Category = "unsupported_policy"
	SourceChanged     Category = "source_changed"
	TemporaryStorage  Category = "temporary_storage"
	Capacity          Category = "capacity"
	Timeout           Category = "timeout"
	Verification      Category = "verification"
	Recovery          Category = "recovery"

	Suppress    Strategy = "suppress"
	Backoff     Strategy = "backoff"
	Conditional Strategy = "conditional"
	Manual      Strategy = "manual_recovery"
)

type Classification struct {
	Stage           string
	Category        Category
	Code            string
	Summary         string
	Advice          string
	Strategy        Strategy
	UnlockCondition string
	AutoRetry       bool
}

type Fingerprint struct {
	Size          int64
	MTimeNS       int64
	PolicyVersion int
}

func (f Fingerprint) Unchanged(size, mtimeNS int64, policyVersion int) bool {
	return f.Size == size && f.MTimeNS == mtimeNS && f.PolicyVersion == policyVersion
}

func Classify(code, detail string) Classification {
	lower := strings.ToLower(detail)
	if strings.Contains(lower, "permission denied") || strings.Contains(lower, "operation not permitted") {
		return classification("storage", TemporaryStorage, "permission_denied", "媒体或存储路径权限不足", "检查媒体、备份和工作路径的读写权限后重新分析。", Backoff, "permission_restored", true)
	}
	switch code {
	case "ffprobe_error":
		return classification("analysis", PermanentMedia, code, "媒体无法解析", "修复或替换文件后重新分析。", Suppress, "file_changed", false)
	case "unsupported":
		return classification("decision", UnsupportedPolicy, code, "当前策略不支持此媒体", "调整音频策略或文件后重新分析。", Suppress, "file_or_policy_changed", false)
	case "source_changed":
		return classification("stability", SourceChanged, code, "分析期间文件发生变化", "等待文件写入稳定，后续扫描会重新分析，也可手动重试。", Conditional, "file_stable", true)
	case "timeout":
		return classification("processing", Timeout, code, "处理超时", "系统将在退避后自动重试。", Backoff, "retry_time", true)
	case "verification_failed":
		return classification("verification", Verification, code, "输出校验失败", "检查源文件或策略，确认后手动重试。", Suppress, "file_or_policy_changed", false)
	case "manual_recovery":
		return classification("replacement", Recovery, code, "替换状态需要人工确认", "请在恢复中心选择恢复或保留候选。", Manual, "recovery_action", false)
	case "interrupted_replacement":
		return classification("replacement", Recovery, code, "替换事务已中断并保护原文件", "原文件已确认安全，可重新分析该文件。", Suppress, "manual_retry", false)
	case "capacity_exhausted":
		return classification("storage", Capacity, code, "存储容量不足", "释放对应存储位置空间后系统将自动重试。", Conditional, "capacity_available", true)
	case "permission_denied":
		return classification("storage", TemporaryStorage, code, "媒体或存储路径权限不足", "检查媒体、备份和工作路径的读写权限后重新分析。", Backoff, "permission_restored", true)
	case "restore_stat_error", "restore_probe_error":
		return classification("recovery", TemporaryStorage, code, "恢复介质暂时不可用", "检查存储连接，系统将在退避后重试。", Backoff, "retry_time", true)
	}
	if strings.Contains(lower, "no space") || strings.Contains(lower, "disk full") || strings.Contains(lower, "容量") {
		return classification("storage", Capacity, "capacity_exhausted", "存储容量不足", "释放空间后等待系统重试或手动重新分析。", Conditional, "capacity_available", true)
	}
	return classification("processing", TemporaryStorage, normalizedCode(code), "处理所需存储暂时不可用", "检查文件权限和存储连接，系统将在退避后重试。", Backoff, "retry_time", true)
}

func classification(stage string, category Category, code, summary, advice string, strategy Strategy, unlock string, retry bool) Classification {
	return Classification{Stage: stage, Category: category, Code: code, Summary: summary, Advice: advice, Strategy: strategy, UnlockCondition: unlock, AutoRetry: retry}
}

func normalizedCode(code string) string {
	if code == "" || code == "failed" {
		return "temporary_storage"
	}
	return code
}

func RetryDelay(base time.Duration, attempt int) time.Duration {
	if base <= 0 {
		base = time.Second
	}
	if attempt < 1 {
		attempt = 1
	}
	delay := base
	for i := 1; i < attempt && delay < 30*time.Minute; i++ {
		delay *= 2
	}
	if delay > 30*time.Minute {
		return 30 * time.Minute
	}
	return delay
}
