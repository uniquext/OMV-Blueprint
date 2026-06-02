package pipeline

import "sync"

type Queue struct {
	mu     sync.Mutex
	paths  []string
	active map[string]struct{}
}

func NewQueue() *Queue {
	return &Queue{
		active: make(map[string]struct{}),
	}
}

func (q *Queue) Enqueue(path string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()

	if _, ok := q.active[path]; ok {
		return false
	}
	q.active[path] = struct{}{}
	q.paths = append(q.paths, path)
	return true
}

func (q *Queue) Next() (string, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if len(q.paths) == 0 {
		return "", false
	}
	path := q.paths[0]
	copy(q.paths, q.paths[1:])
	q.paths[len(q.paths)-1] = ""
	q.paths = q.paths[:len(q.paths)-1]
	return path, true
}

func (q *Queue) Done(path string) {
	q.mu.Lock()
	defer q.mu.Unlock()

	delete(q.active, path)
}
