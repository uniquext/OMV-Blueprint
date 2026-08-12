package pipeline

import (
	"context"
	"sort"
	"sync"
	"time"
)

type RuntimePhase string

const (
	RuntimeQueued       RuntimePhase = "queued"
	RuntimeRetryWait    RuntimePhase = "retry_wait"
	RuntimeCapacityWait RuntimePhase = "capacity_wait"
	RuntimeChecking     RuntimePhase = "checking"
	RuntimeTranscoding  RuntimePhase = "transcoding"
	RuntimeVerifying    RuntimePhase = "verifying"
	RuntimeBackingUp    RuntimePhase = "backing_up"
	RuntimeReplacing    RuntimePhase = "replacing"
)

type JobSource string

const (
	JobSourceDefault  JobSource = ""
	JobSourceScan     JobSource = "scan"
	JobSourceWatchdog JobSource = "watchdog"
	JobSourceManual   JobSource = "manual"
)

type QueueJob struct {
	Path          string
	Source        JobSource
	JobID         int64
	AttemptNumber int
}

type RuntimeTask struct {
	Path                 string       `json:"path"`
	Source               JobSource    `json:"source,omitempty"`
	Phase                RuntimePhase `json:"phase"`
	StartedAt            time.Time    `json:"started_at"`
	PhaseStartedAt       time.Time    `json:"phase_started_at"`
	ElapsedSeconds       int64        `json:"elapsed_seconds"`
	PhaseElapsedSeconds  int64        `json:"phase_elapsed_seconds"`
	MediaPositionSeconds float64      `json:"media_position_seconds,omitempty"`
	Speed                float64      `json:"speed,omitempty"`
	OutputBytes          int64        `json:"output_bytes,omitempty"`
	ETASeconds           float64      `json:"eta_seconds,omitempty"`
	WaitReason           string       `json:"wait_reason,omitempty"`
	UnlockCondition      string       `json:"unlock_condition,omitempty"`
	LastProgressAt       time.Time    `json:"last_progress_at,omitempty"`
	Stalled              bool         `json:"stalled"`
}

type RuntimeProgress struct {
	PositionSeconds float64
	Speed           float64
	OutputBytes     int64
	ETASeconds      float64
	LastProgressAt  time.Time
}

type RuntimeTaskSnapshot struct {
	Waiting []RuntimeTask `json:"waiting_tasks"`
	Active  []RuntimeTask `json:"active_tasks"`
}

type RemovedWaitingTask struct {
	RuntimeTask
	JobID          int64
	LastRetryError string
}

type runtimeTask struct {
	RuntimeTask
	active          bool
	queued          bool
	cancel          context.CancelFunc
	order           uint64
	jobID           int64
	retryCount      int
	attemptNumber   int
	retryTimer      *time.Timer
	retryGeneration uint64
	lastRetryError  string
}

type Queue struct {
	mu         sync.Mutex
	jobs       []QueueJob
	tasks      map[string]*runtimeTask
	nextOrder  uint64
	now        func() time.Time
	stallAfter time.Duration
}

func NewQueue() *Queue {
	return newQueueWithClock(time.Now, 2*time.Minute)
}

func newQueueWithClock(now func() time.Time, stallAfter time.Duration) *Queue {
	if now == nil {
		now = time.Now
	}
	if stallAfter < 0 {
		stallAfter = 0
	}
	return &Queue{tasks: make(map[string]*runtimeTask), now: now, stallAfter: stallAfter}
}

func (q *Queue) Enqueue(path string) bool {
	return q.EnqueueWithSource(path, JobSourceDefault)
}

func (q *Queue) EnqueueWithSource(path string, source JobSource) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.enqueueLocked(path, source, 0)
}

func (q *Queue) EnqueueExistingJob(path string, source JobSource, jobID int64) bool {
	if jobID < 1 {
		return false
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.enqueueLocked(path, source, jobID)
}

func (q *Queue) enqueueLocked(path string, source JobSource, jobID int64) bool {
	if _, exists := q.tasks[path]; exists {
		return false
	}
	now := q.now()
	task := &runtimeTask{
		RuntimeTask: RuntimeTask{
			Path: path, Source: source, Phase: RuntimeQueued, StartedAt: now, PhaseStartedAt: now,
			WaitReason: "等待可用处理槽位", UnlockCondition: "处理槽位可用",
		},
		queued:        true,
		order:         q.nextTaskOrder(),
		jobID:         jobID,
		attemptNumber: 1,
	}
	q.tasks[path] = task
	q.jobs = append(q.jobs, QueueJob{Path: path, Source: source, JobID: jobID, AttemptNumber: 1})
	return true
}

func (q *Queue) Next() (string, bool) {
	job, ok := q.NextJob()
	return job.Path, ok
}

func (q *Queue) NextJob() (QueueJob, bool) {
	return q.ClaimNext(nil)
}

func (q *Queue) ClaimNext(cancel context.CancelFunc) (QueueJob, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()

	for len(q.jobs) > 0 {
		queued := q.jobs[0]
		copy(q.jobs, q.jobs[1:])
		q.jobs[len(q.jobs)-1] = QueueJob{}
		q.jobs = q.jobs[:len(q.jobs)-1]

		task, ok := q.tasks[queued.Path]
		if !ok || !task.queued {
			continue
		}
		task.queued = false
		task.active = true
		task.cancel = cancel
		task.Phase = RuntimeChecking
		task.PhaseStartedAt = q.now()
		task.WaitReason = ""
		task.UnlockCondition = ""
		task.order = q.nextTaskOrder()
		return QueueJob{
			Path:          task.Path,
			Source:        task.Source,
			JobID:         task.jobID,
			AttemptNumber: task.attemptNumber,
		}, true
	}
	return QueueJob{}, false
}

func (q *Queue) BindJob(path string, jobID int64) bool {
	if jobID <= 0 {
		return false
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	task, ok := q.tasks[path]
	if !ok || (task.jobID != 0 && task.jobID != jobID) {
		return false
	}
	task.jobID = jobID
	return true
}

func (q *Queue) ScheduleRetry(path string, delay time.Duration, lastError string, maxRetries int) bool {
	return q.ScheduleRetryWithCondition(path, delay, delay, lastError, maxRetries, nil)
}

func (q *Queue) ScheduleRetryWithCondition(path string, delay, checkInterval time.Duration, lastError string, maxRetries int, condition func() bool) bool {
	return q.ScheduleRetryWithObservation(path, delay, checkInterval, lastError, maxRetries, RuntimeRetryWait, lastError, "等待重试时间", condition)
}

func (q *Queue) ScheduleRetryWithObservation(path string, delay, checkInterval time.Duration, lastError string, maxRetries int, phase RuntimePhase, waitReason, unlockCondition string, condition func() bool) bool {
	q.mu.Lock()
	defer q.mu.Unlock()

	task, ok := q.tasks[path]
	if !ok || !task.active || task.retryCount >= maxRetries || (phase != RuntimeRetryWait && phase != RuntimeCapacityWait) {
		return false
	}
	if task.retryTimer != nil {
		task.retryTimer.Stop()
	}
	task.active = false
	task.queued = false
	task.cancel = nil
	task.Phase = phase
	task.PhaseStartedAt = q.now()
	task.WaitReason = waitReason
	task.UnlockCondition = unlockCondition
	task.retryCount++
	task.attemptNumber = task.retryCount + 1
	task.lastRetryError = lastError
	task.retryGeneration++
	task.order = q.nextTaskOrder()
	generation := task.retryGeneration
	task.retryTimer = time.AfterFunc(delay, func() {
		q.activateRetryWhen(path, generation, checkInterval, condition)
	})
	return true
}

func (q *Queue) activateRetryWhen(path string, generation uint64, checkInterval time.Duration, condition func() bool) {
	if condition != nil && !condition() {
		q.mu.Lock()
		defer q.mu.Unlock()
		task, ok := q.tasks[path]
		if !ok || task.retryGeneration != generation || !isRetryWaitPhase(task.Phase) {
			return
		}
		if checkInterval <= 0 {
			checkInterval = time.Second
		}
		task.retryTimer = time.AfterFunc(checkInterval, func() { q.activateRetryWhen(path, generation, checkInterval, condition) })
		return
	}
	q.activateRetry(path, generation)
}

func (q *Queue) activateRetry(path string, generation uint64) {
	q.mu.Lock()
	defer q.mu.Unlock()
	task, ok := q.tasks[path]
	if !ok || task.retryGeneration != generation || !isRetryWaitPhase(task.Phase) {
		return
	}
	task.retryTimer = nil
	task.queued = true
	task.Phase = RuntimeQueued
	task.PhaseStartedAt = q.now()
	task.WaitReason = "等待可用处理槽位"
	task.UnlockCondition = "处理槽位可用"
	task.order = q.nextTaskOrder()
	q.jobs = append(q.jobs, QueueJob{Path: task.Path, Source: task.Source})
}

func (q *Queue) Done(path string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	task, ok := q.tasks[path]
	if !ok {
		return
	}
	if task.retryTimer != nil {
		task.retryTimer.Stop()
	}
	task.retryGeneration++
	delete(q.tasks, path)
}

func (q *Queue) Contains(path string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	_, ok := q.tasks[path]
	return ok
}

func (q *Queue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	count := 0
	for _, task := range q.tasks {
		if !task.active {
			count++
		}
	}
	return count
}

func (q *Queue) RemoveWaitingIf(remove func(RuntimeTask) bool) []RemovedWaitingTask {
	if remove == nil {
		return nil
	}

	q.mu.Lock()
	defer q.mu.Unlock()

	type orderedRemovedTask struct {
		task  RemovedWaitingTask
		order uint64
	}
	removed := make([]orderedRemovedTask, 0)
	removedPaths := make(map[string]struct{})
	for path, task := range q.tasks {
		if task.active || !remove(task.RuntimeTask) {
			continue
		}
		if task.retryTimer != nil {
			task.retryTimer.Stop()
		}
		task.retryGeneration++
		removed = append(removed, orderedRemovedTask{
			task: RemovedWaitingTask{
				RuntimeTask:    task.RuntimeTask,
				JobID:          task.jobID,
				LastRetryError: task.lastRetryError,
			},
			order: task.order,
		})
		removedPaths[path] = struct{}{}
		delete(q.tasks, path)
	}
	if len(removed) == 0 {
		return nil
	}

	writeIndex := 0
	for _, job := range q.jobs {
		if _, ok := removedPaths[job.Path]; ok {
			continue
		}
		q.jobs[writeIndex] = job
		writeIndex++
	}
	for index := writeIndex; index < len(q.jobs); index++ {
		q.jobs[index] = QueueJob{}
	}
	q.jobs = q.jobs[:writeIndex]

	sort.Slice(removed, func(i, j int) bool { return removed[i].order < removed[j].order })
	result := make([]RemovedWaitingTask, len(removed))
	for index, item := range removed {
		result[index] = item.task
	}
	return result
}

func (q *Queue) UpdatePhase(path string, phase RuntimePhase) {
	q.mu.Lock()
	defer q.mu.Unlock()
	task, ok := q.tasks[path]
	if !ok || !task.active || !isActivePhase(phase) {
		return
	}
	if task.Phase != phase {
		task.Phase = phase
		task.PhaseStartedAt = q.now()
	}
	task.WaitReason = ""
	task.UnlockCondition = ""
}

func (q *Queue) UpdateProgress(path string, progress RuntimeProgress) {
	q.mu.Lock()
	defer q.mu.Unlock()
	task, ok := q.tasks[path]
	if !ok || !task.active {
		return
	}
	if progress.PositionSeconds >= task.MediaPositionSeconds {
		task.MediaPositionSeconds = progress.PositionSeconds
		task.Speed = progress.Speed
		task.ETASeconds = progress.ETASeconds
	}
	if progress.OutputBytes >= task.OutputBytes {
		task.OutputBytes = progress.OutputBytes
	}
	if !progress.LastProgressAt.Before(task.LastProgressAt) {
		task.LastProgressAt = progress.LastProgressAt
	}
}

func (q *Queue) Snapshot() RuntimeTaskSnapshot {
	q.mu.Lock()
	defer q.mu.Unlock()

	type orderedTask struct {
		task  RuntimeTask
		order uint64
	}
	waiting := make([]orderedTask, 0)
	active := make([]orderedTask, 0)
	now := q.now()
	for _, task := range q.tasks {
		snapshot := task.RuntimeTask
		snapshot.ElapsedSeconds = elapsedSeconds(snapshot.StartedAt, now)
		snapshot.PhaseElapsedSeconds = elapsedSeconds(snapshot.PhaseStartedAt, now)
		snapshot.Stalled = task.active && snapshot.Phase == RuntimeTranscoding && q.stallAfter > 0 && !snapshot.LastProgressAt.IsZero() && now.Sub(snapshot.LastProgressAt) >= q.stallAfter
		item := orderedTask{task: snapshot, order: task.order}
		if task.active {
			active = append(active, item)
		} else {
			waiting = append(waiting, item)
		}
	}
	sort.Slice(waiting, func(i, j int) bool { return waiting[i].order < waiting[j].order })
	sort.Slice(active, func(i, j int) bool { return active[i].order < active[j].order })

	snapshot := RuntimeTaskSnapshot{
		Waiting: make([]RuntimeTask, len(waiting)),
		Active:  make([]RuntimeTask, len(active)),
	}
	for index, item := range waiting {
		snapshot.Waiting[index] = item.task
	}
	for index, item := range active {
		snapshot.Active[index] = item.task
	}
	return snapshot
}

func elapsedSeconds(start, now time.Time) int64 {
	if start.IsZero() || now.Before(start) {
		return 0
	}
	return int64(now.Sub(start) / time.Second)
}

func isRetryWaitPhase(phase RuntimePhase) bool {
	return phase == RuntimeRetryWait || phase == RuntimeCapacityWait
}

func (q *Queue) CancelActive(includeCritical bool) {
	q.mu.Lock()
	cancels := make([]context.CancelFunc, 0)
	for _, task := range q.tasks {
		if !task.active || task.cancel == nil {
			continue
		}
		if includeCritical || !isCriticalPhase(task.Phase) {
			cancels = append(cancels, task.cancel)
		}
	}
	q.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

func (q *Queue) HasCriticalActive() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, task := range q.tasks {
		if task.active && isCriticalPhase(task.Phase) {
			return true
		}
	}
	return false
}

func (q *Queue) nextTaskOrder() uint64 {
	q.nextOrder++
	return q.nextOrder
}

func isActivePhase(phase RuntimePhase) bool {
	switch phase {
	case RuntimeChecking, RuntimeTranscoding, RuntimeVerifying, RuntimeBackingUp, RuntimeReplacing:
		return true
	default:
		return false
	}
}

func isCriticalPhase(phase RuntimePhase) bool {
	return phase == RuntimeBackingUp || phase == RuntimeReplacing
}
