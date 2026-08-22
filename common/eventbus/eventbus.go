package eventbus

import (
	"sync"
)

const (
	// TopicHostStatus 在巡检或手动测试完成后发布主机状态变化。
	TopicHostStatus = "host.status"
	// TopicHostChanged 在主机增删改后发布主机数据变化。
	TopicHostChanged = "host.changed"
	// TopicCredentialChanged 在凭据增删改后发布凭据数据变化。
	TopicCredentialChanged = "credential.changed"
	// TopicAuditCreated 在新的操作日志写入后发布。
	TopicAuditCreated = "audit.created"
)

// Event 事件。
type Event struct {
	Type string
	Data interface{}
}

// Subscriber 订阅者回调。
type Subscriber func(Event)

type subscriber struct {
	id int
	fn Subscriber
}

// Bus 事件总线。
type Bus struct {
	mu     sync.RWMutex
	subs   map[string][]subscriber
	nextID int
}

// New 创建事件总线。
func New() *Bus {
	return &Bus{subs: make(map[string][]subscriber)}
}

// Subscribe 订阅事件，返回订阅 id（用于 Unsubscribe）。
func (b *Bus) Subscribe(topic string, fn Subscriber) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.nextID++
	id := b.nextID
	b.subs[topic] = append(b.subs[topic], subscriber{id: id, fn: fn})
	return id
}

// Unsubscribe 取消指定 topic 下的某个订阅。
func (b *Bus) Unsubscribe(topic string, id int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	subs := b.subs[topic]
	for i, s := range subs {
		if s.id == id {
			b.subs[topic] = append(subs[:i], subs[i+1:]...)
			break
		}
	}
	if len(b.subs[topic]) == 0 {
		delete(b.subs, topic)
	}
}

// Publish 发布事件。
func (b *Bus) Publish(topic string, data interface{}) {
	b.mu.RLock()
	subs := append([]subscriber(nil), b.subs[topic]...)
	b.mu.RUnlock()
	ev := Event{Type: topic, Data: data}
	for _, s := range subs {
		go s.fn(ev)
	}
}
