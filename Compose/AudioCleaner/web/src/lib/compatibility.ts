import type { AudioPlanEvidence, AudioTrackEvidence, CompatibilityAssessment, RuleMatch } from './types'

export function assessmentSourceKey(assessment: Pick<CompatibilityAssessment, 'source'> | undefined): string {
  switch (assessment?.source) {
    case 'source_probe':
      return 'filesAssessmentSourceProbe'
    case 'cache':
      return 'filesAssessmentSourceCache'
    default:
      return 'filesAssessmentSourceUnknown'
  }
}

export function assessmentActionKey(assessment: Pick<CompatibilityAssessment, 'action'> | undefined): string {
  switch (assessment?.action) {
    case 'already_compatible':
      return 'filesAssessmentActionCompatible'
    case 'transcode':
      return 'filesAssessmentActionTranscode'
    case 'unsupported':
      return 'filesAssessmentActionUnsupported'
    default:
      return 'filesAssessmentActionUnknown'
  }
}

export function assessmentReasonKey(assessment: Pick<CompatibilityAssessment, 'action' | 'reason'> | undefined): string | undefined {
  if (!assessment) return 'filesEmptyValue'
  switch (assessment.reason) {
    case 'file facts and audio policy are unchanged':
      return 'filesAssessmentReasonUnchanged'
    case 'audio policy change does not affect this file':
      return 'filesAssessmentReasonPolicyUnaffected'
    case 'all audio tracks are compatible':
      return 'filesAssessmentReasonCompatible'
  }
  if (assessment.action === 'transcode') return 'filesAssessmentReasonTranscode'
  return undefined
}

export function audioTrackSummary(track: AudioTrackEvidence): string {
  const parts = [`#${track.stream_index}`, track.codec ? track.codec.toUpperCase() : 'unknown']
  if (track.channels > 0) parts.push(`${track.channels}ch`)
  if (track.language) parts.push(track.language)
  if (track.title) parts.push(track.title)
  if (track.disposition) parts.push(track.disposition)
  return parts.join(' · ')
}

export function ruleMatchSummary(rule: RuleMatch): string {
  const streams = rule.stream_indexes.map((index) => `#${index}`).join(', ')
  return [rule.codec.toUpperCase(), streams].filter(Boolean).join(' · ')
}

export function audioPlanSummary(plan: AudioPlanEvidence): string {
  const action = plan.action === 'transcode' && plan.target_codec
    ? `${plan.action} → ${plan.target_codec.toUpperCase()}`
    : plan.action
  return [`#${plan.stream_index}`, action, plan.bitrate].filter(Boolean).join(' · ')
}
