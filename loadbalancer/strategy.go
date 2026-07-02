package main

import (
	"sync/atomic"
)

// Backend represents a single chat server that the load balancer can route to.
type Backend struct {
	Addr        string
	ActiveConns atomic.Int64
	Alive       atomic.Bool
}

// Strategy defines how the load balancer picks a backend for a new connection.
type Strategy interface {
	Pick(backends []*Backend) *Backend
}

// RoundRobin cycles through backends in order, skipping dead ones.
type RoundRobin struct {
	counter atomic.Uint64
}

// Pick returns the next alive backend in round-robin order, or nil if none are alive.
func (rr *RoundRobin) Pick(backends []*Backend) *Backend {
	n := len(backends)
	if n == 0 {
		return nil
	}

	start := rr.counter.Add(1) - 1
	for i := 0; i < n; i++ {
		idx := int((start + uint64(i)) % uint64(n))
		if backends[idx].Alive.Load() {
			return backends[idx]
		}
	}
	return nil
}

// LeastConnections picks the alive backend with the fewest active connections.
type LeastConnections struct{}

// Pick returns the alive backend with the lowest ActiveConns, or nil if none are alive.
func (lc *LeastConnections) Pick(backends []*Backend) *Backend {
	var best *Backend
	var bestConns int64

	for _, b := range backends {
		if !b.Alive.Load() {
			continue
		}
		conns := b.ActiveConns.Load()
		if best == nil || conns < bestConns {
			best = b
			bestConns = conns
		}
	}
	return best
}
