package computer

import (
	"sync"
	"time"
)

// LeaseRecord says which turn owns a desktop right now.
type LeaseRecord struct {
	TurnID    string
	Slug      string
	ExpiresAt time.Time
}

// Lease is a short, renewable ownership fence for one desktop. Runtime
// events keep an active turn's lease alive; a dead provider cannot pin the
// desktop forever. Every method is synchronous so lifecycle routes and turn
// dispatch can claim their side of a race before either awaits anything.
type Lease struct {
	mu     sync.Mutex
	record *LeaseRecord
	ttl    time.Duration
}

// NewLease returns a lease with the given TTL.
func NewLease(ttl time.Duration) *Lease {
	if ttl <= 0 {
		ttl = 90 * time.Second
	}
	return &Lease{ttl: ttl}
}

// Current returns the live owner, or nil. isBusy lets a lease end early
// when its owning bot is no longer running a turn.
func (l *Lease) Current(isBusy func(slug string) bool, now time.Time) *LeaseRecord {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.currentLocked(isBusy, now)
}

func (l *Lease) currentLocked(isBusy func(slug string) bool, now time.Time) *LeaseRecord {
	if l.record != nil && (!l.record.ExpiresAt.After(now) || (isBusy != nil && !isBusy(l.record.Slug))) {
		l.record = nil
	}
	if l.record == nil {
		return nil
	}
	copy := *l.record
	return &copy
}

// Claim takes the lease for turnID unless another live turn holds it.
func (l *Lease) Claim(turnID, slug string, isBusy func(slug string) bool, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if cur := l.currentLocked(isBusy, now); cur != nil && cur.TurnID != turnID {
		return false
	}
	l.record = &LeaseRecord{TurnID: turnID, Slug: slug, ExpiresAt: now.Add(l.ttl)}
	return true
}

// Touch renews the lease when turnID still owns it.
func (l *Lease) Touch(turnID string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.record != nil && !l.record.ExpiresAt.After(now) {
		l.record = nil
		return
	}
	if l.record != nil && l.record.TurnID == turnID {
		l.record.ExpiresAt = now.Add(l.ttl)
	}
}

// Release drops the lease when turnID owns it.
func (l *Lease) Release(turnID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.record != nil && l.record.TurnID == turnID {
		l.record = nil
	}
}

// LeasePool keys independent leases by target so separate desktops never
// block each other while each desktop stays a strict singleton.
type LeasePool struct {
	mu     sync.Mutex
	leases map[string]*Lease
	ttl    time.Duration
}

// NewLeasePool returns a pool whose leases share one TTL.
func NewLeasePool(ttl time.Duration) *LeasePool {
	return &LeasePool{ttl: ttl}
}

// For returns the lease for a target key, creating it on first use.
func (p *LeasePool) For(key string) *Lease {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.leases == nil {
		p.leases = map[string]*Lease{}
	}
	l, ok := p.leases[key]
	if !ok {
		l = NewLease(p.ttl)
		p.leases[key] = l
	}
	return l
}

// IdleTimer is a renewable idle deadline for one desktop. Activity resets
// the full window; when it expires with no turn running, suspend runs. A
// busy desktop or a failed suspend simply gets another full window.
type IdleTimer struct {
	mu      sync.Mutex
	timer   *time.Timer
	idle    time.Duration
	isBusy  func() bool
	suspend func() error
}

// NewIdleTimer builds an idle timer; Touch arms it.
func NewIdleTimer(idle time.Duration, isBusy func() bool, suspend func() error) *IdleTimer {
	return &IdleTimer{idle: idle, isBusy: isBusy, suspend: suspend}
}

// Touch restarts the idle window.
func (t *IdleTimer) Touch() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.timer != nil {
		t.timer.Stop()
	}
	t.timer = time.AfterFunc(t.idle, t.expire)
}

// Cancel disarms the timer.
func (t *IdleTimer) Cancel() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.timer != nil {
		t.timer.Stop()
		t.timer = nil
	}
}

func (t *IdleTimer) expire() {
	if t.isBusy != nil && t.isBusy() {
		t.Touch()
		return
	}
	if err := t.suspend(); err != nil {
		t.Touch()
	}
}
