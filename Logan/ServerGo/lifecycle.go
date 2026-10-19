package main

import (
	"context"
	hconfig "github.com/cloudwego/hertz/pkg/common/config"
	"github.com/cloudwego/hertz/pkg/network"
	"github.com/cloudwego/hertz/pkg/network/standard"
	"net"
	"sync"
)

// Connections carry cancellation so forced shutdown interrupts streamed requests.
type connections struct {
	mu      sync.Mutex
	closed  bool
	entries map[net.Conn]context.CancelFunc
	done    chan struct{}
}

func (r *connections) transport(o *hconfig.Options) network.Transporter {
	return &trackedTransport{Transporter: standard.NewTransporter(o), registry: r}
}

type trackedTransport struct {
	network.Transporter
	registry *connections
}

// Listener preserves Hertz startup readiness checks through the wrapper.
func (t *trackedTransport) Listener() net.Listener {
	if v, ok := t.Transporter.(interface{ Listener() net.Listener }); ok {
		return v.Listener()
	}
	return nil
}

func (t *trackedTransport) ListenAndServe(on network.OnData) error {
	return t.Transporter.ListenAndServe(func(ctx context.Context, c interface{}) error {
		conn, ok := c.(net.Conn)
		if !ok {
			return on(ctx, c)
		}
		ctx, cancel := context.WithCancel(ctx)
		r := t.registry
		r.mu.Lock()
		if r.closed {
			r.mu.Unlock()
			cancel()
			conn.Close()
			return nil
		}
		if r.entries == nil {
			r.entries = make(map[net.Conn]context.CancelFunc)
		}
		r.entries[conn] = cancel
		r.mu.Unlock()
		defer func() {
			cancel()
			r.mu.Lock()
			delete(r.entries, conn)
			if r.closed && len(r.entries) == 0 {
				close(r.done)
				r.done = nil
			}
			r.mu.Unlock()
		}()
		return on(ctx, c)
	})
}
func (r *connections) closeAll() {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	r.closed = true
	r.done = make(chan struct{})
	for c, cancel := range r.entries {
		cancel()
		_ = c.Close()
	}
	if len(r.entries) == 0 {
		close(r.done)
		r.done = nil
	}
	r.mu.Unlock()
}

// wait joins handlers after closeAll has canceled and closed all connections.
func (r *connections) wait() {
	r.mu.Lock()
	done := r.done
	r.mu.Unlock()
	if done != nil {
		<-done
	}
}
