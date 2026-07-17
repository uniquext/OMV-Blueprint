package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"
)

type Config struct {
	Media         MediaConfig         `json:"media"`
	Audio         AudioConfig         `json:"audio"`
	Pipeline      PipelineConfig      `json:"pipeline"`
	Validation    ValidationConfig    `json:"validation"`
	UI            UIConfig            `json:"ui"`
	Scan          ScanConfig          `json:"scan"`
	Notifications NotificationsConfig `json:"notifications"`
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
	StartupScanEnabled bool `json:"startup_scan_enabled"`
	WatchdogEnabled    bool `json:"watchdog_enabled"`
}

type NotificationsConfig struct {
	Enabled bool     `json:"enabled"`
	Targets []string `json:"targets"`
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
			StartupScanEnabled: true,
			WatchdogEnabled:    true,
		},
		Notifications: NotificationsConfig{
			Enabled: false,
			Targets: []string{},
		},
	}
}

func (c Config) Validate() error {
	if len(c.Media.Roots) == 0 {
		return errors.New("media roots must not be empty")
	}
	if len(c.Media.Extensions) == 0 {
		return errors.New("media extensions must not be empty")
	}
	for _, ext := range c.Media.Extensions {
		if len(ext) == 0 || ext[0] != '.' {
			return fmt.Errorf("media extension %q must start with dot", ext)
		}
	}
	if c.Audio.Version < 1 {
		return errors.New("audio version must be at least 1")
	}
	if c.Pipeline.Workers < 1 {
		return errors.New("pipeline workers must be at least 1")
	}
	if c.Pipeline.MaxRetries < 0 {
		return errors.New("pipeline max retries must not be negative")
	}
	if c.Pipeline.RetryDelaySeconds < 0 {
		return errors.New("pipeline retry delay seconds must not be negative")
	}
	if c.Pipeline.StatQuietSeconds < 1 {
		return errors.New("pipeline stat quiet seconds must be at least 1")
	}
	if c.Pipeline.JobTimeoutMinutes < 1 {
		return errors.New("pipeline job timeout minutes must be at least 1")
	}
	if c.Validation.MaxSizeRatio < 1 {
		return errors.New("validation max size ratio must be at least 1")
	}
	if c.Validation.MaxSizeIncreaseMegabyte < 0 {
		return errors.New("validation max size increase megabyte must not be negative")
	}
	if c.Validation.DurationToleranceSec < 0 {
		return errors.New("validation duration tolerance seconds must not be negative")
	}
	switch c.UI.Language {
	case "zh-CN", "en-US":
	default:
		return fmt.Errorf("ui language %q is not supported", c.UI.Language)
	}
	return nil
}

func (c *Config) Normalize() {
	defaults := Default()
	if len(c.Media.Roots) == 0 {
		c.Media.Roots = defaults.Media.Roots
	}
	if c.UI.Language == "" {
		c.UI.Language = defaults.UI.Language
	}
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
