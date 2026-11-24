package mail

import (
	"context"
	"errors"
	"sync"
	"time"
)

const (
	maxClockRecheck   = 30 * time.Second
	minSendInterval   = 2 * time.Second
	defaultQuotaDayTZ = "Asia/Shanghai"
)

// DeliverFunc 执行单条已认领投递；调用方负责 SMTP 与阶段落盘细节之外的业务终态由 dispatcher 写回。
// 返回 status 必须是 accepted/failed/unknown 之一。
type DeliverFunc func(ctx context.Context, item SubmissionItem, snap PreparedSnapshot, sub Submission) (status, reason string, err error)

// AccountGate 在发信前检查账号是否仍可用；disabled/missing 时 dispatcher 标 failed 并释放额度预占责任由 Deliver 内部处理。
type AccountGate func(ctx context.Context, accountID string) (ok bool, reason string)

// Dispatcher 是独立一次性投递 worker；不依赖 automation。
type Dispatcher struct {
	store   *Store
	clock   Clock
	deliver DeliverFunc
	gate    AccountGate

	mu       sync.Mutex
	started  bool
	holds    map[string]int
	wake     chan struct{}
	cancel   context.CancelFunc
	done     chan struct{}
	lastSent time.Time
}

// NewDispatcher 构造未启动的派发器；Start 才开循环。
func NewDispatcher(store *Store, clock Clock, deliver DeliverFunc, gate AccountGate) *Dispatcher {
	if clock == nil {
		clock = SystemClock{}
	}
	if gate == nil {
		gate = func(context.Context, string) (bool, string) { return true, "" }
	}
	return &Dispatcher{
		store:   store,
		clock:   clock,
		deliver: deliver,
		gate:    gate,
		holds:   map[string]int{},
		wake:    make(chan struct{}, 1),
		done:    make(chan struct{}),
	}
}

// Start 恢复崩溃中的 sending→unknown 后进入循环。
func (d *Dispatcher) Start(ctx context.Context) error {
	d.mu.Lock()
	if d.started {
		d.mu.Unlock()
		return nil
	}
	d.started = true
	ctx, d.cancel = context.WithCancel(ctx)
	d.mu.Unlock()

	now := d.clock.Now()
	if err := d.store.Recover(ctx, now); err != nil {
		d.abortStart()
		return err
	}
	go d.loop(ctx)
	return nil
}

func (d *Dispatcher) abortStart() {
	d.mu.Lock()
	cancel := d.cancel
	d.cancel = nil
	d.started = false
	d.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// Stop 取消循环并等待退出。
func (d *Dispatcher) Stop() {
	d.mu.Lock()
	if d.cancel != nil {
		d.cancel()
	}
	d.mu.Unlock()
	<-d.done
}

// Wake 立即重核到期项。
func (d *Dispatcher) Wake() {
	select {
	case d.wake <- struct{}{}:
	default:
	}
}

// HoldAdmission 阻止新认领直至 release。
func (d *Dispatcher) HoldAdmission(reason string) (release func()) {
	d.mu.Lock()
	d.holds[reason]++
	d.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			d.mu.Lock()
			if d.holds[reason] > 0 {
				d.holds[reason]--
				if d.holds[reason] == 0 {
					delete(d.holds, reason)
				}
			}
			d.mu.Unlock()
			d.Wake()
		})
	}
}

func (d *Dispatcher) admissionBlocked() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.holds) > 0
}

// ExitSummary 返回待发（queued/scheduled/sending）计数。
func (d *Dispatcher) ExitSummary(ctx context.Context, owners []string) (pending int, err error) {
	return d.store.CountPendingDelivery(ctx, owners)
}

func (d *Dispatcher) loop(ctx context.Context) {
	defer close(d.done)
	for {
		d.dispatchOnce(ctx)
		wait := d.waitDuration(ctx)
		timer := d.clock.After(wait)
		select {
		case <-ctx.Done():
			return
		case <-d.wake:
		case <-timer:
		}
	}
}

func (d *Dispatcher) waitDuration(ctx context.Context) time.Duration {
	now := d.clock.Now()
	next, err := d.store.NextDueAt(ctx, now)
	if err != nil || next == nil {
		return maxClockRecheck
	}
	delay := next.Sub(now)
	if delay <= 0 {
		return time.Millisecond
	}
	if delay > maxClockRecheck {
		return maxClockRecheck
	}
	return delay
}

func (d *Dispatcher) dispatchOnce(ctx context.Context) {
	if d.admissionBlocked() || d.deliver == nil {
		return
	}
	for {
		if ctx.Err() != nil || d.admissionBlocked() {
			return
		}
		now := d.clock.Now()
		// 串行间隔：不忙轮询，但已认领的项仍继续。
		d.mu.Lock()
		since := now.Sub(d.lastSent)
		d.mu.Unlock()
		if !d.lastSent.IsZero() && since < minSendInterval {
			return
		}
		item, err := d.store.ClaimDueItem(ctx, now)
		if errors.Is(err, ErrNotFound) {
			return
		}
		if errors.Is(err, ErrConflict) {
			continue
		}
		if err != nil {
			return
		}
		d.processClaimed(ctx, item, now)
		d.mu.Lock()
		d.lastSent = d.clock.Now()
		d.mu.Unlock()
	}
}

func (d *Dispatcher) processClaimed(ctx context.Context, item SubmissionItem, now time.Time) {
	finish := func(status, reason string, when time.Time) {
		_ = d.store.FinishItem(ctx, item.ID, status, reason, when)
		_ = d.store.ReconcileSubmission(ctx, item.SubmissionID, when)
	}
	_ = d.store.ReconcileSubmission(ctx, item.SubmissionID, now)

	sub, err := d.store.GetSubmission(ctx, item.SubmissionID, []string{item.OwnerScope})
	if err != nil {
		finish(StatusFailed, "submission_missing", now)
		return
	}
	snap, err := d.store.GetPreparedSnapshot(ctx, sub.SnapshotID, []string{item.OwnerScope})
	if err != nil {
		finish(StatusFailed, "snapshot_missing", now)
		return
	}
	if ok, reason := d.gate(ctx, snap.AccountID); !ok {
		if reason == "" {
			reason = "account_unavailable"
		}
		finish(StatusFailed, reason, now)
		return
	}
	dayKey := quotaDayKey(now)
	if err := d.store.ReserveQuota(ctx, item.OwnerScope, snap.AccountID, dayKey, item.ID, 1, DailyQuotaDefault); err != nil {
		if errors.Is(err, ErrQuotaExceeded) {
			finish(StatusFailed, "quota_exceeded", now)
			return
		}
		finish(StatusFailed, "quota_reserve_failed", now)
		return
	}

	status, reason, deliverErr := d.deliver(ctx, item, snap, sub)
	fin := d.clock.Now()
	switch status {
	case StatusAccepted, StatusUnknown:
		_ = d.store.ConsumeQuota(ctx, item.OwnerScope, item.ID)
		if reason == "" && deliverErr != nil {
			reason = deliverErr.Error()
		}
		finish(status, sanitizeDetail(reason), fin)
	case StatusFailed:
		_ = d.store.ReleaseQuota(ctx, item.OwnerScope, item.ID)
		if reason == "" && deliverErr != nil {
			reason = deliverErr.Error()
		}
		finish(StatusFailed, sanitizeDetail(reason), fin)
	default:
		_ = d.store.ConsumeQuota(ctx, item.OwnerScope, item.ID)
		finish(StatusUnknown, "invalid_delivery_status", fin)
	}
}

func quotaDayKey(now time.Time) string {
	loc, err := time.LoadLocation(defaultQuotaDayTZ)
	if err != nil {
		loc = time.FixedZone("CST", 8*3600)
	}
	return now.In(loc).Format("2006-01-02")
}

func sanitizeDetail(s string) string {
	if len(s) > 500 {
		return s[:500]
	}
	return s
}
