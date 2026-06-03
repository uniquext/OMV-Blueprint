package eventbus

import "sync"

const subscriberBufferSize = 64

type Event struct {
	Type       string         `json:"type"`
	SnapshotID uint64         `json:"snapshot_id"`
	Data       map[string]any `json:"data"`
}

type Bus struct {
	mu          sync.Mutex
	snapshotID  uint64
	nextSubID   uint64
	subscribers map[uint64]chan Event
}

func New() *Bus {
	return &Bus{
		subscribers: make(map[uint64]chan Event),
	}
}

func (b *Bus) Publish(eventType string, data map[string]any) Event {
	eventData := cloneData(data)

	b.mu.Lock()
	b.snapshotID++
	event := Event{
		Type:       eventType,
		SnapshotID: b.snapshotID,
		Data:       eventData,
	}
	for _, ch := range b.subscribers {
		select {
		case ch <- event:
		default:
		}
	}
	b.mu.Unlock()

	return event
}

func cloneData(data map[string]any) map[string]any {
	if data == nil {
		return nil
	}

	cloned := make(map[string]any, len(data))
	for key, value := range data {
		cloned[key] = value
	}
	return cloned
}

func (b *Bus) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, subscriberBufferSize)

	b.mu.Lock()
	b.nextSubID++
	subID := b.nextSubID
	b.subscribers[subID] = ch
	b.mu.Unlock()

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			b.mu.Lock()
			delete(b.subscribers, subID)
			close(ch)
			b.mu.Unlock()
		})
	}

	return ch, cancel
}

func (b *Bus) SnapshotID() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.snapshotID
}
