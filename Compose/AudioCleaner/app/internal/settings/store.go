package settings

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"
	"sync"

	"omv-blueprint/compose/audiocleaner/internal/config"
)

type Module string

const (
	Media      Module = "media"
	Audio      Module = "audio"
	Pipeline   Module = "pipeline"
	Validation Module = "validation"
	Scan       Module = "scan"
	UI         Module = "ui"
)

var moduleOrder = []Module{Media, Audio, Pipeline, Validation, Scan, UI}

type ApplyMode string

const (
	HotReload      ApplyMode = "hot_reload"
	ModuleReload   ApplyMode = "module_reload"
	ServiceRestart ApplyMode = "service_restart"
)

type Status string

const (
	Active     Status = "active"
	Restarting Status = "restarting"
)

type Metadata struct {
	Revision  string    `json:"revision"`
	ApplyMode ApplyMode `json:"apply_mode"`
}

type View struct {
	Revision string              `json:"revision"`
	Modules  config.Config       `json:"modules"`
	Metadata map[Module]Metadata `json:"metadata"`
}

type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

type Result struct {
	Module       Module        `json:"module"`
	Changed      bool          `json:"changed"`
	ApplyMode    ApplyMode     `json:"apply_mode"`
	Status       Status        `json:"status"`
	Revision     string        `json:"revision"`
	Config       any           `json:"config"`
	Notices      []string      `json:"notices"`
	BeforeConfig config.Config `json:"-"`
	FullConfig   config.Config `json:"-"`
}

type ValidationError struct {
	Module      Module       `json:"module"`
	FieldErrors []FieldError `json:"field_errors"`
}

func (e *ValidationError) Error() string {
	if len(e.FieldErrors) == 0 {
		return "configuration validation failed"
	}
	return e.FieldErrors[0].Message
}

type ConflictError struct {
	Module   Module `json:"module"`
	Revision string `json:"revision"`
	Config   any    `json:"config"`
}

func (e *ConflictError) Error() string { return "configuration changed" }

type ApplyError struct {
	Module Module `json:"module"`
	Cause  error  `json:"-"`
}

func (e *ApplyError) Error() string {
	return fmt.Sprintf("apply %s configuration: %v", e.Module, e.Cause)
}
func (e *ApplyError) Unwrap() error { return e.Cause }

type Applicator interface {
	Apply(ctx context.Context, module Module, before, after config.Config) error
	Rollback(ctx context.Context, module Module, before config.Config) error
}

type Store struct {
	mu         sync.RWMutex
	cfg        config.Config
	path       string
	applicator Applicator
}

func NewStore(cfg config.Config, path string, applicator Applicator) *Store {
	cfg = cloneConfig(cfg)
	cfg.Normalize()
	return &Store{cfg: cfg, path: path, applicator: applicator}
}

func Modules() []Module {
	return append([]Module(nil), moduleOrder...)
}

func ApplyModeFor(module Module) ApplyMode {
	switch module {
	case Media, Pipeline:
		return ModuleReload
	case Scan:
		return ServiceRestart
	case Audio, Validation, UI:
		return HotReload
	default:
		return ""
	}
}

func IsModule(module Module) bool { return ApplyModeFor(module) != "" }

func (s *Store) View() View {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return makeView(cloneConfig(s.cfg))
}

func (s *Store) Defaults() View {
	cfg := config.Default()
	cfg.Normalize()
	return makeView(cfg)
}

func (s *Store) Restore(cfg config.Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg = cloneConfig(cfg)
	if s.path != "" {
		if err := config.AtomicWriteJSON(s.path, cfg); err != nil {
			return err
		}
	}
	s.cfg = cfg
	return nil
}

func (s *Store) Update(ctx context.Context, module Module, expectedRevision string, payload json.RawMessage) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !IsModule(module) {
		return Result{}, &ValidationError{Module: module, FieldErrors: []FieldError{{Field: string(module), Message: "unknown configuration module"}}}
	}
	before := cloneConfig(s.cfg)
	currentRevision := revisionForModule(module, before)
	if expectedRevision != currentRevision {
		return Result{}, &ConflictError{Module: module, Revision: currentRevision, Config: moduleConfig(module, before)}
	}
	after := cloneConfig(before)
	if err := decodeModule(module, payload, &after); err != nil {
		return Result{}, &ValidationError{Module: module, FieldErrors: []FieldError{{Field: string(module), Message: err.Error()}}}
	}
	after.Normalize()
	if module == Audio && !config.AudioContentEqual(before.Audio, after.Audio) {
		after.Audio.Version = config.NextAudioVersion(before.Audio.Version)
	}
	if err := after.Validate(); err != nil {
		return Result{}, &ValidationError{Module: module, FieldErrors: []FieldError{{Field: validationField(err.Error(), module), Message: err.Error()}}}
	}
	changed := !reflect.DeepEqual(before, after)
	status := Active
	if module == Scan && changed {
		status = Restarting
	}
	if !changed {
		return resultFor(module, before, before, false, status), nil
	}

	if s.path != "" {
		if err := config.AtomicWriteJSON(s.path, after); err != nil {
			return Result{}, err
		}
	}
	if s.applicator != nil {
		if err := s.applicator.Apply(ctx, module, before, after); err != nil {
			rollbackErr := error(nil)
			if s.path != "" {
				rollbackErr = config.AtomicWriteJSON(s.path, before)
			}
			runtimeRollbackErr := s.applicator.Rollback(ctx, module, before)
			if rollbackErr != nil || runtimeRollbackErr != nil {
				err = fmt.Errorf("%w; file rollback: %v; runtime rollback: %v", err, rollbackErr, runtimeRollbackErr)
			}
			return Result{}, &ApplyError{Module: module, Cause: err}
		}
	}
	s.cfg = cloneConfig(after)
	return resultFor(module, before, after, true, status), nil
}

func makeView(cfg config.Config) View {
	metadata := make(map[Module]Metadata, len(moduleOrder))
	for _, module := range moduleOrder {
		metadata[module] = Metadata{Revision: revisionForModule(module, cfg), ApplyMode: ApplyModeFor(module)}
	}
	return View{Revision: revision("config", cfg), Modules: cfg, Metadata: metadata}
}

func resultFor(module Module, before, cfg config.Config, changed bool, status Status) Result {
	return Result{
		Module: module, Changed: changed, ApplyMode: ApplyModeFor(module), Status: status,
		Revision: revisionForModule(module, cfg), Config: moduleConfig(module, cfg), Notices: []string{},
		BeforeConfig: cloneConfig(before), FullConfig: cloneConfig(cfg),
	}
}

func revisionForModule(module Module, cfg config.Config) string {
	return revision(string(module), moduleConfig(module, cfg))
}

func revision(prefix string, value any) string {
	data, _ := json.Marshal(value)
	sum := sha256.Sum256(data)
	return prefix + ":" + hex.EncodeToString(sum[:8])
}

func moduleConfig(module Module, cfg config.Config) any {
	switch module {
	case Media:
		return cfg.Media
	case Audio:
		return cfg.Audio
	case Pipeline:
		return cfg.Pipeline
	case Validation:
		return cfg.Validation
	case Scan:
		return cfg.Scan
	case UI:
		return cfg.UI
	default:
		return nil
	}
}

func decodeModule(module Module, payload []byte, cfg *config.Config) error {
	switch module {
	case Media:
		return strictDecode(payload, &cfg.Media)
	case Audio:
		var request struct {
			IncompatibleCodecs []string `json:"incompatible_codecs"`
		}
		if err := strictDecode(payload, &request); err != nil {
			return err
		}
		cfg.Audio.IncompatibleCodecs = request.IncompatibleCodecs
		return nil
	case Pipeline:
		return strictDecode(payload, &cfg.Pipeline)
	case Validation:
		return strictDecode(payload, &cfg.Validation)
	case Scan:
		return strictDecode(payload, &cfg.Scan)
	case UI:
		return strictDecode(payload, &cfg.UI)
	default:
		return fmt.Errorf("unknown configuration module %q", module)
	}
}

func strictDecode(payload []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("invalid configuration JSON: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("invalid configuration JSON: multiple values")
	}
	return nil
}

func validationField(message string, module Module) string {
	if index := strings.IndexByte(message, ' '); index > 0 {
		candidate := message[:index]
		if strings.Contains(candidate, ".") {
			return candidate
		}
	}
	return string(module)
}

func cloneConfig(cfg config.Config) config.Config {
	cfg.Media.Roots = cloneStrings(cfg.Media.Roots)
	cfg.Media.Extensions = cloneStrings(cfg.Media.Extensions)
	cfg.Media.ExcludeDirs = cloneStrings(cfg.Media.ExcludeDirs)
	cfg.Media.ExcludePatterns = cloneStrings(cfg.Media.ExcludePatterns)
	cfg.Audio.IncompatibleCodecs = cloneStrings(cfg.Audio.IncompatibleCodecs)
	return cfg
}

func cloneStrings(values []string) []string {
	if values == nil {
		return nil
	}
	return append(make([]string, 0, len(values)), values...)
}
