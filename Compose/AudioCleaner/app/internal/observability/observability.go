package observability

import (
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const DefaultReserveBytes int64 = 64 * 1024 * 1024

type Reason struct {
	Code               string `json:"code"`
	Summary            string `json:"summary"`
	AffectedCapability string `json:"affected_capability"`
	Advice             string `json:"advice"`
}

type SpaceProvider interface {
	AvailableBytes(path string) (int64, error)
}

type CapabilityChecker interface {
	CheckCapability(path, capability string) error
}

type CapacityRequest struct {
	SourceBytes    int64
	MaxOutputBytes int64
	ReserveBytes   int64
	BackupPath     string
	WorkPath       string
	MediaPath      string
}

type VolumeCapacity struct {
	Capability     string `json:"capability"`
	Path           string `json:"path"`
	AvailableBytes int64  `json:"available_bytes"`
	RequiredBytes  int64  `json:"required_bytes"`
	Ready          bool   `json:"ready"`
	Code           string `json:"code,omitempty"`
	Error          string `json:"error,omitempty"`
}

type CapacityResult struct {
	Ready    bool             `json:"ready"`
	Volumes  []VolumeCapacity `json:"volumes"`
	Blocking []Reason         `json:"blocking_reasons"`
}

func EvaluateCapacity(provider SpaceProvider, request CapacityRequest) CapacityResult {
	reserve := request.ReserveBytes
	if reserve < 0 {
		reserve = 0
	}
	targets := []struct {
		capability string
		path       string
		payload    int64
	}{
		{capability: "backup", path: request.BackupPath, payload: request.SourceBytes},
		{capability: "work", path: request.WorkPath, payload: request.MaxOutputBytes},
		{capability: "media", path: request.MediaPath, payload: request.SourceBytes},
	}
	result := CapacityResult{Ready: true, Volumes: make([]VolumeCapacity, 0, len(targets)), Blocking: []Reason{}}
	for _, target := range targets {
		volume := VolumeCapacity{Capability: target.capability, Path: target.path}
		required, valid := addBytes(target.payload, reserve)
		volume.RequiredBytes = required
		if !valid || provider == nil || target.path == "" {
			volume.Code = "capacity_" + target.capability + "_invalid"
			volume.Error = "capacity requirement or path is invalid"
		} else {
			available, err := provider.AvailableBytes(target.path)
			volume.AvailableBytes = available
			switch {
			case err != nil:
				volume.Code = "capacity_" + target.capability + "_unavailable"
				volume.Error = err.Error()
			case available < required:
				volume.Code = "capacity_" + target.capability + "_insufficient"
			default:
				if checker, ok := provider.(CapabilityChecker); ok {
					if err := checker.CheckCapability(target.path, target.capability); err != nil {
						volume.Code = "capacity_" + target.capability + "_unavailable"
						volume.Error = err.Error()
						break
					}
				}
				volume.Ready = true
			}
		}
		if !volume.Ready {
			result.Ready = false
			result.Blocking = append(result.Blocking, capacityReason(volume))
		}
		result.Volumes = append(result.Volumes, volume)
	}
	return result
}

func addBytes(payload, reserve int64) (int64, bool) {
	if payload < 0 || reserve < 0 || payload > math.MaxInt64-reserve {
		return 0, false
	}
	return payload + reserve, true
}

func capacityReason(volume VolumeCapacity) Reason {
	label := capabilityLabel(volume.Capability)
	summary := label + "配置无效"
	advice := "检查" + label + "路径和容量策略配置。"
	switch {
	case strings.HasSuffix(volume.Code, "_insufficient"):
		summary = label + "可用空间不足"
		advice = "释放" + label + "空间后系统将自动重试。"
	case strings.HasSuffix(volume.Code, "_unavailable"):
		summary = label + "不可用"
		advice = "检查" + label + "的挂载状态和读写权限后，系统将自动重试。"
	}
	return Reason{
		Code: volume.Code, Summary: summary, AffectedCapability: "new_transcode",
		Advice: advice,
	}
}

func capabilityLabel(capability string) string {
	switch capability {
	case "backup":
		return "备份位置"
	case "work":
		return "工作位置"
	default:
		return "媒体位置"
	}
}

type BusinessStatus string

const (
	StatusNormal         BusinessStatus = "normal"
	StatusDegraded       BusinessStatus = "degraded"
	StatusIntakeStopped  BusinessStatus = "intake_stopped"
	StatusManualRecovery BusinessStatus = "manual_recovery"
)

type HealthInput struct {
	ProcessStatus       string
	CapacityReady       bool
	CapacityReasons     []Reason
	WatcherStatus       string
	WatcherError        string
	ManualRecoveryCount int
}

type Health struct {
	Status        BusinessStatus `json:"status"`
	ProcessStatus string         `json:"process_status"`
	Reasons       []Reason       `json:"health_reasons"`
}

func EvaluateHealth(input HealthInput) Health {
	health := Health{Status: StatusNormal, ProcessStatus: input.ProcessStatus, Reasons: []Reason{}}
	if input.ProcessStatus != "" && input.ProcessStatus != "running" {
		health.Status = StatusDegraded
		health.Reasons = append(health.Reasons, Reason{Code: "process_" + input.ProcessStatus, Summary: "服务进程状态异常", AffectedCapability: "new_transcode", Advice: "检查服务日志并确认重启结果。"})
	}
	if input.WatcherStatus == "error" {
		health.Status = StatusDegraded
		summary := "目录监听异常"
		if input.WatcherError != "" {
			summary += ": " + input.WatcherError
		}
		health.Reasons = append(health.Reasons, Reason{Code: "watcher_error", Summary: summary, AffectedCapability: "automatic_discovery", Advice: "检查媒体目录连接和权限；周期对账仍会继续。"})
	}
	if !input.CapacityReady {
		health.Status = StatusIntakeStopped
		health.Reasons = append(health.Reasons, input.CapacityReasons...)
	}
	if input.ManualRecoveryCount > 0 {
		health.Status = StatusManualRecovery
		health.Reasons = append([]Reason{{Code: "manual_recovery_pending", Summary: "存在需要人工处理的恢复事项", AffectedCapability: "new_transcode", Advice: "前往恢复中心完成恢复或保留决策。"}}, health.Reasons...)
	}
	return health
}

type Progress struct {
	PositionSeconds float64   `json:"position_seconds"`
	Speed           float64   `json:"speed"`
	OutputBytes     int64     `json:"output_bytes"`
	ETASeconds      float64   `json:"eta_seconds"`
	LastProgressAt  time.Time `json:"last_progress_at"`
	Complete        bool      `json:"complete"`
}

type ProgressParser struct {
	duration float64
	progress Progress
}

func NewProgressParser(durationSeconds float64) *ProgressParser {
	if durationSeconds < 0 || math.IsNaN(durationSeconds) || math.IsInf(durationSeconds, 0) {
		durationSeconds = 0
	}
	return &ProgressParser{duration: durationSeconds}
}

func (p *ProgressParser) Accept(line string, observedAt time.Time) {
	key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
	if !ok {
		return
	}
	changed := false
	switch key {
	case "out_time_us", "out_time_ms":
		micros, err := strconv.ParseInt(value, 10, 64)
		seconds := float64(micros) / 1_000_000
		if err == nil && seconds >= p.progress.PositionSeconds {
			p.progress.PositionSeconds = seconds
			changed = true
		}
	case "speed":
		speed, err := strconv.ParseFloat(strings.TrimSuffix(value, "x"), 64)
		if err == nil && speed >= 0 && !math.IsNaN(speed) && !math.IsInf(speed, 0) {
			p.progress.Speed = speed
			changed = true
		}
	case "total_size":
		size, err := strconv.ParseInt(value, 10, 64)
		if err == nil && size >= p.progress.OutputBytes {
			p.progress.OutputBytes = size
			changed = true
		}
	case "progress":
		if value == "end" {
			p.progress.Complete = true
			changed = true
		} else if value == "continue" {
			changed = true
		}
	}
	if changed {
		p.progress.LastProgressAt = observedAt
		p.progress.ETASeconds = estimateETA(p.duration, p.progress.PositionSeconds, p.progress.Speed)
	}
}

func (p *ProgressParser) Snapshot() Progress { return p.progress }

func estimateETA(duration, position, speed float64) float64 {
	if duration <= 0 || position < 0 || position >= duration || speed <= 0 {
		return 0
	}
	return (duration - position) / speed
}

func LimitDiagnostic(value string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(value) <= maxBytes {
		return value
	}
	const suffix = "..."
	if maxBytes <= len(suffix) {
		return suffix[:maxBytes]
	}
	limit := maxBytes - len(suffix)
	cut := limit
	for cut > 0 && !utf8.ValidString(value[:cut]) {
		cut--
	}
	return value[:cut] + suffix
}

type RetentionItem struct {
	ID        int64
	Timestamp time.Time
	Protected bool
}

func SelectExpired(items []RetentionItem, cutoff time.Time, maxCount int) []int64 {
	if cutoff.IsZero() && maxCount <= 0 {
		return nil
	}
	ordered := append([]RetentionItem(nil), items...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Timestamp.Equal(ordered[j].Timestamp) {
			return ordered[i].ID > ordered[j].ID
		}
		return ordered[i].Timestamp.After(ordered[j].Timestamp)
	})
	expired := make([]int64, 0)
	for index, item := range ordered {
		overAge := !cutoff.IsZero() && item.Timestamp.Before(cutoff)
		overCount := maxCount > 0 && index >= maxCount
		if !item.Protected && (overAge || overCount) {
			expired = append(expired, item.ID)
		}
	}
	sort.Slice(expired, func(i, j int) bool { return expired[i] < expired[j] })
	return expired
}
