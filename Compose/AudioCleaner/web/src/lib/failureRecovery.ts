export type RecoveryAction = 'restore' | 'retain' | 'delete' | 'retry'

export interface RetryContextDetails {
  summary: string
  fileChanged: boolean
  policyChanged: boolean
  bypassesSuppressionOnce: boolean
}

export function retryContextDetails(context: Pick<RetryContext, 'issue' | 'file_changed' | 'policy_changed' | 'bypasses_suppression_once'> | Record<string, unknown>, fallback: string): RetryContextDetails {
  const issue = context.issue as Record<string, unknown> | null
  return {
    summary: typeof issue?.summary === 'string' ? issue.summary : fallback,
    fileChanged: context.file_changed === true,
    policyChanged: context.policy_changed === true,
    bypassesSuppressionOnce: context.bypasses_suppression_once === true
  }
}

export function recoveryActionAllowed(actions: RecoveryAction[], action: RecoveryAction): boolean {
  return actions.includes(action)
}
import type { RetryContext } from './types'
