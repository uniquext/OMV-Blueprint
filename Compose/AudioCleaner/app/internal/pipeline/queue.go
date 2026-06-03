package pipeline

import "sync"

type Queue struct {
	mu     sync.Mutex
	jobs   []QueueJob
	active map[string]struct{}
}

type JobSource string

const (
	JobSourceDefault JobSource = ""
	JobSourceScan    JobSource = "scan"
)

type QueueJob struct {
	Path   string
	Source JobSource
}

func NewQueue() *Queue {
	return &Queue{
		active: make(map[string]struct{}),
	}
}

func (q *Queue) Enqueue(path string) bool {
	return q.EnqueueWithSource(path, JobSourceDefault)
}

func (q *Queue) EnqueueWithSource(path string, source JobSource) bool {
	q.mu.Lock()
	defer q.mu.Unlock()

	if _, ok := q.active[path]; ok {
		return false
	}
	q.active[path] = struct{}{}
	q.jobs = append(q.jobs, QueueJob{Path: path, Source: source})
	return true
}

func (q *Queue) Next() (string, bool) {
	job, ok := q.NextJob()
	return job.Path, ok
}

func (q *Queue) NextJob() (QueueJob, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if len(q.jobs) == 0 {
		return QueueJob{}, false
	}
	job := q.jobs[0]
	copy(q.jobs, q.jobs[1:])
	q.jobs[len(q.jobs)-1] = QueueJob{}
	q.jobs = q.jobs[:len(q.jobs)-1]
	return job, true
}

func (q *Queue) Done(path string) {
	q.mu.Lock()
	defer q.mu.Unlock()

	delete(q.active, path)
}

func (q *Queue) Contains(path string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()

	_, ok := q.active[path]
	return ok
}

func (q *Queue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()

	return len(q.jobs)
}
