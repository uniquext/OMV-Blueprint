package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path"
	"strings"
	"time"
)

type Config struct {
	Media      MediaConfig      `json:"media"`
	Audio      AudioConfig      `json:"audio"`
	Pipeline   PipelineConfig   `json:"pipeline"`
	Validation ValidationConfig `json:"validation"`
	UI         UIConfig         `json:"ui"`
	Scan       ScanConfig       `json:"scan"`
}

type MediaConfig struct {
	Roots           []string `json:"roots"`
	Extensions      []string `json:"extensions"`
	ExcludeDirs     []string `json:"exclude_dirs"`
	ExcludePatterns []string `json:"exclude_patterns"`
}

type AudioConfig struct {
	Version            int      `json:"version"`
	IncompatibleCodecs []string `json:"incompatible_codecs"`
}

type PipelineConfig struct {
	Workers           int `json:"workers"`
	MaxRetries        int `json:"max_retries"`
	RetryDelaySeconds int `json:"retry_delay_seconds"`
	StatQuietSeconds  int `json:"stat_quiet_seconds"`
	JobTimeoutMinutes int `json:"job_timeout_minutes"`
}

type ValidationConfig struct {
	MaxSizeRatio            float64 `json:"max_size_ratio"`
	MaxSizeIncreaseMegabyte int64   `json:"max_size_increase_mb"`
	DurationToleranceSec    int     `json:"duration_tolerance_seconds"`
}

type UIConfig struct {
	Language string `json:"language"`
}

type ScanConfig struct {
	StartupScanEnabled         bool `json:"startup_scan_enabled"`
	WatchdogEnabled            bool `json:"watchdog_enabled"`
	ReconciliationIntervalMins int  `json:"reconciliation_interval_minutes"`
}

func Default() Config {
	return Config{
		Media: MediaConfig{
			Roots:           []string{"/media"},
			Extensions:      []string{".mkv", ".mp4", ".mov", ".m4v", ".ts", ".m2ts"},
			ExcludeDirs:     []string{"@eaDir", ".stfolder", "#recycle"},
			ExcludePatterns: []string{},
		},
		Audio: AudioConfig{
			Version:            1,
			IncompatibleCodecs: []string{"dts", "truehd", "eac3", "ac3", "dca", "dtshd"},
		},
		Pipeline: PipelineConfig{
			Workers:           2,
			MaxRetries:        2,
			RetryDelaySeconds: 300,
			StatQuietSeconds:  10,
			JobTimeoutMinutes: 120,
		},
		Validation: ValidationConfig{
			MaxSizeRatio:            1.1,
			MaxSizeIncreaseMegabyte: 500,
			DurationToleranceSec:    2,
		},
		UI: UIConfig{
			Language: "zh-CN",
		},
		Scan: ScanConfig{
			StartupScanEnabled:         true,
			WatchdogEnabled:            true,
			ReconciliationIntervalMins: 15,
		},
	}
}

func (c Config) Validate() error {
	if len(c.Media.Roots) == 0 {
		return errors.New("media.roots must not be empty")
	}
	for index, root := range c.Media.Roots {
		clean := path.Clean(root)
		if root == "" || (clean != "/media" && !strings.HasPrefix(clean, "/media/")) {
			return fmt.Errorf("media.roots[%d] %q must be /media or a directory below it", index, root)
		}
	}
	if len(c.Media.Extensions) == 0 {
		return errors.New("media.extensions must not be empty")
	}
	for index, ext := range c.Media.Extensions {
		if len(ext) == 0 || ext[0] != '.' {
			return fmt.Errorf("media.extensions[%d] %q must start with dot", index, ext)
		}
	}
	for index, dir := range c.Media.ExcludeDirs {
		if dir == "" {
			return fmt.Errorf("media.exclude_dirs[%d] must not be empty", index)
		}
		if strings.ContainsAny(dir, `/\\`) {
			return fmt.Errorf("media.exclude_dirs[%d] %q must be a directory name without path separators", index, dir)
		}
	}
	for index, pattern := range c.Media.ExcludePatterns {
		if pattern == "" {
			return fmt.Errorf("media.exclude_patterns[%d] must not be empty", index)
		}
		if _, err := path.Match(pattern, "candidate"); err != nil {
			return fmt.Errorf("media.exclude_patterns[%d] %q is invalid: %w", index, pattern, err)
		}
	}
	if c.Audio.Version < 1 {
		return errors.New("audio.version must be at least 1")
	}
	if len(c.Audio.IncompatibleCodecs) == 0 {
		return errors.New("audio.incompatible_codecs must not be empty")
	}
	for index, codec := range c.Audio.IncompatibleCodecs {
		if codec == "" {
			return fmt.Errorf("audio.incompatible_codecs[%d] must not be empty", index)
		}
	}
	if c.Pipeline.Workers < 1 || c.Pipeline.Workers > 64 {
		return errors.New("pipeline.workers must be between 1 and 64")
	}
	if c.Pipeline.MaxRetries < 0 || c.Pipeline.MaxRetries > 20 {
		return errors.New("pipeline.max_retries must be between 0 and 20")
	}
	if c.Pipeline.RetryDelaySeconds < 0 || c.Pipeline.RetryDelaySeconds > 86400 {
		return errors.New("pipeline.retry_delay_seconds must be between 0 and 86400")
	}
	if c.Pipeline.StatQuietSeconds < 1 || c.Pipeline.StatQuietSeconds > 3600 {
		return errors.New("pipeline.stat_quiet_seconds must be between 1 and 3600")
	}
	if c.Pipeline.JobTimeoutMinutes < 1 || c.Pipeline.JobTimeoutMinutes > 10080 {
		return errors.New("pipeline.job_timeout_minutes must be between 1 and 10080")
	}
	if c.Scan.ReconciliationIntervalMins < 1 || c.Scan.ReconciliationIntervalMins > 10080 {
		return errors.New("scan.reconciliation_interval_minutes must be between 1 and 10080")
	}
	if math.IsNaN(c.Validation.MaxSizeRatio) || math.IsInf(c.Validation.MaxSizeRatio, 0) || c.Validation.MaxSizeRatio < 1 || c.Validation.MaxSizeRatio > 10 {
		return errors.New("validation.max_size_ratio must be a finite number between 1 and 10")
	}
	if c.Validation.MaxSizeIncreaseMegabyte < 0 || c.Validation.MaxSizeIncreaseMegabyte > 1048576 {
		return errors.New("validation.max_size_increase_mb must be between 0 and 1048576")
	}
	if c.Validation.DurationToleranceSec < 0 || c.Validation.DurationToleranceSec > 3600 {
		return errors.New("validation.duration_tolerance_seconds must be between 0 and 3600")
	}
	switch c.UI.Language {
	case "zh-CN", "en-US":
	default:
		return fmt.Errorf("ui.language %q is not supported", c.UI.Language)
	}
	return nil
}

func (c *Config) Normalize() {
	c.Media.Roots = normalizeStrings(c.Media.Roots, false)
	c.Media.Extensions = normalizeStrings(c.Media.Extensions, true)
	c.Media.ExcludeDirs = normalizeStrings(c.Media.ExcludeDirs, false)
	c.Media.ExcludePatterns = normalizeStrings(c.Media.ExcludePatterns, false)
	c.Audio.IncompatibleCodecs = normalizeStrings(c.Audio.IncompatibleCodecs, true)
}

func normalizeStrings(values []string, lower bool) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if lower {
			value = strings.ToLower(value)
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func (p PipelineConfig) StatQuietDuration() time.Duration {
	return time.Duration(p.StatQuietSeconds) * time.Second
}

func (p PipelineConfig) RetryDelay() time.Duration {
	return time.Duration(p.RetryDelaySeconds) * time.Second
}

func (p PipelineConfig) JobTimeout() time.Duration {
	return time.Duration(p.JobTimeoutMinutes) * time.Minute
}

func LoadOrCreate(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			cfg := Default()
			if err := cfg.Validate(); err != nil {
				return Config{}, err
			}
			if err := AtomicWriteJSON(path, cfg); err != nil {
				return Config{}, err
			}
			return cfg, nil
		}
		return Config{}, err
	}

	cfg := Default()
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, err
	}
	cfg.Normalize()
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}
