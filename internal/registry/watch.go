package registry

import "sync"

// WatchEventType 表示注册中心实例变化事件类型。
type WatchEventType string

const (
	WatchEventUpsert WatchEventType = "upsert"
	WatchEventDelete WatchEventType = "delete"
)

// WatchEvent 描述一次已经提交并应用完成的注册中心实例变化。
type WatchEvent struct {
	Revision   uint64         `json:"revision"`
	Type       WatchEventType `json:"type"`
	Namespace  string         `json:"namespace"`
	Service    string         `json:"service"`
	InstanceID string         `json:"instanceId"`
	Value      *Value         `json:"value,omitempty"`
}

// WatchHub 为注册中心实例变化提供进程内订阅能力。
type WatchHub struct {
	mu       sync.RWMutex
	nextID   uint64
	watchers map[uint64]chan WatchEvent
	history  []WatchEvent
}

// NewWatchHub 创建一个新的 watch hub。
func NewWatchHub() *WatchHub {
	return &WatchHub{
		watchers: make(map[uint64]chan WatchEvent),
		history:  make([]WatchEvent, 0, 256),
	}
}

// Subscribe 注册一个 watcher，并返回取消订阅函数。
func (h *WatchHub) Subscribe(buffer int) (uint64, <-chan WatchEvent, func()) {
	id, ch, _, _, unsubscribe := h.SubscribeSince(buffer, 0)
	return id, ch, unsubscribe
}

// SubscribeSince 注册一个 watcher，并尽量返回 since 之后的历史事件。
//
// exact=false 表示现有历史窗口不足以覆盖 since，调用方应退回到 snapshot 再接增量。
func (h *WatchHub) SubscribeSince(buffer int, since uint64) (uint64, <-chan WatchEvent, []WatchEvent, bool, func()) {
	if buffer <= 0 {
		buffer = 64
	}

	h.mu.Lock()
	h.nextID++
	id := h.nextID
	ch := make(chan WatchEvent, buffer)
	h.watchers[id] = ch
	replay, exact := h.replaySinceLocked(since)
	h.mu.Unlock()

	var once sync.Once
	unsubscribe := func() {
		once.Do(func() {
			h.mu.Lock()
			defer h.mu.Unlock()

			watcher, ok := h.watchers[id]
			if !ok {
				return
			}
			delete(h.watchers, id)
			close(watcher)
		})
	}

	return id, ch, replay, exact, unsubscribe
}

// Publish 广播一条事件。
//
// 如果某个 watcher 长时间不消费，当前实现会主动关闭它，让客户端自行重连，
// 避免阻塞已经提交完成的后台事件处理链路。
func (h *WatchHub) Publish(event WatchEvent) {
	h.mu.Lock()
	h.appendHistoryLocked(event)
	watchers := make(map[uint64]chan WatchEvent, len(h.watchers))
	for id, ch := range h.watchers {
		watchers[id] = ch
	}
	h.mu.Unlock()

	slowWatchers := make([]uint64, 0)
	for id, ch := range watchers {
		select {
		case ch <- event:
		default:
			slowWatchers = append(slowWatchers, id)
		}
	}

	if len(slowWatchers) == 0 {
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	for _, id := range slowWatchers {
		ch, ok := h.watchers[id]
		if !ok {
			continue
		}
		delete(h.watchers, id)
		close(ch)
	}
}

func (h *WatchHub) replaySinceLocked(since uint64) ([]WatchEvent, bool) {
	if since == 0 || len(h.history) == 0 {
		return nil, false
	}

	oldest := h.history[0].Revision
	latest := h.history[len(h.history)-1].Revision
	if since > latest {
		return nil, true
	}
	if since+1 < oldest {
		return nil, false
	}

	replay := make([]WatchEvent, 0, len(h.history))
	for _, event := range h.history {
		if event.Revision > since {
			replay = append(replay, event)
		}
	}

	return replay, true
}

func (h *WatchHub) appendHistoryLocked(event WatchEvent) {
	h.history = append(h.history, event)
	if len(h.history) > 2048 {
		h.history = append([]WatchEvent(nil), h.history[len(h.history)-2048:]...)
	}
}
