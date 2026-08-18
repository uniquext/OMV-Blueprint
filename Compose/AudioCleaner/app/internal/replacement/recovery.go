package replacement

type Phase string

const (
	PhasePrepared        Phase = "prepared"
	PhaseBackupReady     Phase = "backup_ready"
	PhaseInstalling      Phase = "installing"
	PhaseOutputInstalled Phase = "output_installed"
	PhaseRestoring       Phase = "restoring"
	PhaseManualRecovery  Phase = "manual_recovery"
	PhaseCommitted       Phase = "committed"
)

type Evidence struct {
	Size           int64  `json:"size"`
	MTimeNS        int64  `json:"mtime_ns"`
	AudioSignature string `json:"audio_signature"`
	VideoSignature string `json:"video_signature"`
}

func (e Evidence) Matches(other Evidence) bool {
	return e.complete() && other.complete() &&
		e.Size == other.Size &&
		matchesObservedMTime(e.MTimeNS, other.MTimeNS) &&
		e.AudioSignature == other.AudioSignature &&
		e.VideoSignature == other.VideoSignature
}

func matchesObservedMTime(expected, observed int64) bool {
	if expected == observed {
		return true
	}
	const second = int64(1_000_000_000)
	return observed%second == 0 && expected/second == observed/second
}

func (e Evidence) complete() bool {
	return e.Size > 0 && e.MTimeNS > 0 && e.AudioSignature != "" && e.VideoSignature != ""
}

type Observation struct {
	Exists   bool
	Evidence Evidence
}

type RecoveryAction string

const (
	RecoveryCleanup         RecoveryAction = "cleanup"
	RecoveryAbort           RecoveryAction = "abort"
	RecoveryCommitInstalled RecoveryAction = "commit_installed"
	RecoveryRestore         RecoveryAction = "restore"
	RecoveryRestored        RecoveryAction = "restored"
	RecoveryPreserve        RecoveryAction = "preserve"
)

func DecideRecovery(phase Phase, current Observation, original Evidence, output Evidence) RecoveryAction {
	switch phase {
	case PhaseCommitted:
		return RecoveryCleanup
	case PhaseManualRecovery:
		return RecoveryPreserve
	case PhasePrepared, PhaseBackupReady:
		return RecoveryAbort
	}

	matchesOriginal := current.Exists && original.Matches(current.Evidence)
	matchesOutput := current.Exists && output.Matches(current.Evidence)
	switch phase {
	case PhaseInstalling:
		if matchesOutput {
			return RecoveryCommitInstalled
		}
		if matchesOriginal {
			return RecoveryAbort
		}
	case PhaseOutputInstalled:
		if matchesOutput {
			return RecoveryCommitInstalled
		}
		if matchesOriginal {
			return RecoveryRestored
		}
	case PhaseRestoring:
		if matchesOriginal {
			return RecoveryRestored
		}
		if matchesOutput {
			return RecoveryRestore
		}
	}
	return RecoveryPreserve
}
