package auth

import (
	"sync"
	"time"
)

// QREvent 是推送给桌面端的扫码状态事件。Name 对应 SSE 的 event 字段。
type QREvent struct {
	Name string // scanned / confirmed / expired / failed
	Data any
}

// QRHub 在进程内维护「会话 ID → SSE 订阅者」的映射。
//
// 单机部署下不需要 Redis 之类的外部 broker；多实例时把 Publish 换成
// 消息总线即可，其余逻辑不变。
type QRHub struct {
	mu   sync.Mutex
	subs map[string]chan QREvent
}

// NewQRHub 构造二维码事件中心。
func NewQRHub() *QRHub {
	return &QRHub{subs: make(map[string]chan QREvent)}
}

// Subscribe 订阅某个会话的事件。同一会话只保留一个订阅者：
// 桌面端刷新页面会重新订阅，旧连接必须被顶掉，否则 leaked goroutine。
func (h *QRHub) Subscribe(sessionID string) (<-chan QREvent, func()) {
	const buf = 4

	h.mu.Lock()
	defer h.mu.Unlock()

	if old, ok := h.subs[sessionID]; ok {
		close(old)
	}
	ch := make(chan QREvent, buf)
	h.subs[sessionID] = ch

	cancel := func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if cur, ok := h.subs[sessionID]; ok && cur == ch {
			delete(h.subs, sessionID)
		}
		close(ch)
	}
	return ch, cancel
}

// Publish 推送事件。订阅端不在或缓冲满时直接丢弃：
// 状态已落库，客户端重连后仍能通过轮询缺口看到结果，不必阻塞扫码端。
func (h *QRHub) Publish(sessionID string, ev QREvent) {
	h.mu.Lock()
	defer h.mu.Unlock()

	ch, ok := h.subs[sessionID]
	if !ok {
		return
	}
	select {
	case ch <- ev:
	default:
	}
}

// heartbeatInterval 心跳间隔，低于常见网关 30s 空闲超时。
const heartbeatInterval = 15 * time.Second
