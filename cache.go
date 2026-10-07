// cache.go holds the per-account in-memory cache for plan / checkin / credits
// snapshots and the singleflight machinery that dedups concurrent upstream
// fetches for the same account. The cache is the coordination point between
// the dashboard, reconcile, and scheduler pick paths.
package main

import (
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// the failed field falls back to the previous value instead of being wiped.
type accountCacheEntry struct {
	checkin                         *checkinSummary
	credits                         *creditsSummary
	plan                            string
	fetched                         time.Time
	errs                            []string
	creditsSeq, checkinSeq, planSeq uint64
}

var (
	accountCache    sync.Map // auth_id (auth.ID) -> *accountCacheEntry
	accountCacheTTL = 45 * time.Second
)

// accountDetailFlight is a per-authID singleflight: concurrent dashboard /
// reconcile callers for the same account share one upstream fetch instead of
// stampeding the billing API. Without this, two parallel panel refreshes would
// each spawn 3 goroutines per account → 6× upstream QPS and last-writer-wins
// cache corruption.
var accountDetailFlight sync.Map // authID -> *accountDetailCall

type accountDetailCall struct {
	done chan struct{}
	plan string
	ci   *checkinSummary
	cr   *creditsSummary
	errs []string
}

// cachedAccountDetails fetches plan/checkin/credits concurrently (upstream
// round-trip dominates; 3 serial calls ≈ 3× latency). On any individual
// failure the previous cached value is kept (stale-while-error) so a
// transient upstream 500 does not blank the panel row.
func cachedAccountDetails(authID string, sa *storedAuth, force bool) (plan string, ci *checkinSummary, cr *creditsSummary, errs []string) {
	var prev *accountCacheEntry
	if v, ok := accountCache.Load(authID); ok {
		prev = v.(*accountCacheEntry)
		if !force && time.Since(prev.fetched) < accountCacheTTL && creditSnapshotFresh(prev, time.Now(), accountCacheTTL) {
			// Return cached values. Do NOT mutate prev.credits here — concurrent
			// goroutines (reconcileOneAccount) may read the same entry.
			// FetchedAt is stamped at Store time; if it's empty (legacy entry),
			// the panel can derive it from prev.fetched if needed.
			return prev.plan, supportedCheckinSnapshot(sa, prev.checkin), prev.credits, append([]string(nil), prev.errs...)
		}
	}

	// Singleflight: only one goroutine performs the upstream fetch per authID.
	// Others wait on the in-flight call's done channel and reuse its result.
	// Note: force=true callers DO join the flight (P1-1 trade-off). This is
	// intentional: the flight window is short (~3 concurrent fetches), and
	// skipping it would re-introduce the P0-2 race where concurrent writers
	// overwrite each other's cache entries. The result a force caller gets
	// is still checked for age and credit errors before any lifecycle decision.
	call := &accountDetailCall{done: make(chan struct{})}
	actual, loaded := accountDetailFlight.LoadOrStore(authID, call)
	if loaded {
		// Someone else is fetching — wait for their result.
		other := actual.(*accountDetailCall)
		<-other.done
		// Re-read cache: fetcher already Stored; use whatever won the race.
		if v, ok := accountCache.Load(authID); ok {
			if e, ok2 := v.(*accountCacheEntry); ok2 {
				return e.plan, supportedCheckinSnapshot(sa, e.checkin), e.credits, append([]string(nil), e.errs...)
			}
		}
		return other.plan, other.ci, other.cr, other.errs
	}
	// We are the fetcher. Make sure waiters wake up and the flight entry is
	// released even on panic.
	defer func() {
		call.plan, call.ci, call.cr, call.errs = plan, ci, cr, errs
		close(call.done)
		accountDetailFlight.Delete(authID)
	}()

	seq := nextAccountSnapshot()
	var (
		wg      sync.WaitGroup
		errMu   sync.Mutex
		errList []string
	)
	addErr := func(msg string) {
		errMu.Lock()
		errList = append(errList, msg)
		errMu.Unlock()
	}
	wg.Add(3)
	go func() { defer wg.Done(); plan = fetchPaymentType(sa) }()
	go func() {
		defer wg.Done()
		if !supportsBusiness(sa, "checkin") {
			return
		}
		if c, err := fetchCheckinStatus(sa); err == nil {
			ci = c
		} else {
			addErr("checkin: " + err.Error())
		}
	}()
	go func() {
		defer wg.Done()
		if r, err := fetchUserResource(sa); err == nil {
			cr = r
		} else {
			addErr("credits: " + err.Error())
		}
	}()
	wg.Wait()
	// Merge field-by-field against the current snapshot. An older request
	// cannot overwrite a later request's field, even if it completes last.
	entry := publishAccountSnapshot(authID, accountSnapshotPatch{
		seq: seq, creditsSet: true, credits: cr, checkinSet: true, checkin: ci,
		planSet: true, plan: plan, errors: errList,
	})
	plan, ci, cr, errs = entry.plan, supportedCheckinSnapshot(sa, entry.checkin), entry.credits, append([]string(nil), entry.errs...)

	// Soft cap: if map is huge, drop oldest-looking entries beyond bound.
	pruneAccountCacheSoftCap(accountCacheSoftCap)
	return plan, ci, cr, errs
}

// accountCacheSoftCap limits concurrent cache entries (auth churn / index thrash).
const accountCacheSoftCap = 256

// pruneAccountCacheSoftCap drops excess entries with the oldest fetched time.
// Called after Store; O(n) over map size — fine for dozens of accounts.
func pruneAccountCacheSoftCap(capN int) {
	if capN <= 0 {
		return
	}
	type item struct {
		key string
		at  time.Time
	}
	var items []item
	accountCache.Range(func(key, value any) bool {
		k, _ := key.(string)
		e, ok := value.(*accountCacheEntry)
		if !ok || k == "" {
			accountCache.Delete(key)
			return true
		}
		items = append(items, item{key: k, at: e.fetched})
		return true
	})
	if len(items) <= capN {
		return
	}
	// Sort oldest first (was O(n²) bubble — sort.Slice is O(n log n)).
	sort.Slice(items, func(i, j int) bool { return items[i].at.Before(items[j].at) })
	drop := len(items) - capN
	for i := 0; i < drop; i++ {
		accountCache.Delete(items[i].key)
	}
}

// cachedCheckinToday returns cached today_checked_in when present.
func cachedCheckinToday(authID string) *bool {
	v, ok := accountCache.Load(authID)
	if !ok {
		return nil
	}
	e, ok := v.(*accountCacheEntry)
	if !ok || e == nil || e.checkin == nil {
		return nil
	}
	b := e.checkin.TodayCheckedIn
	return &b
}

func supportedCheckinSnapshot(sa *storedAuth, ci *checkinSummary) *checkinSummary {
	if !supportsBusiness(sa, "checkin") {
		return nil
	}
	return ci
}

// Request generations are allocated BEFORE I/O. CAS publishes immutable,
// per-field snapshots; sync.Map alone would not prevent lost updates.
var accountSnapshotSequence atomic.Uint64

func nextAccountSnapshot() uint64 { return accountSnapshotSequence.Add(1) }

type accountSnapshotPatch struct {
	seq                                         uint64
	creditsSet, checkinSet, planSet, invalidate bool
	credits                                     *creditsSummary
	checkin                                     *checkinSummary
	plan                                        string
	errors                                      []string
}

func publishAccountSnapshot(id string, p accountSnapshotPatch) *accountCacheEntry {
	if p.seq == 0 {
		p.seq = nextAccountSnapshot()
	}
	for {
		old, exists := accountCache.Load(id)
		n := &accountCacheEntry{}
		if exists {
			*n = *old.(*accountCacheEntry)
			n.errs = append([]string(nil), n.errs...)
		}
		mergeErrors := func(field string) {
			kept := make([]string, 0, len(n.errs)+len(p.errors))
			for _, err := range n.errs {
				if !strings.HasPrefix(err, field+":") {
					kept = append(kept, err)
				}
			}
			for _, err := range p.errors {
				if strings.HasPrefix(err, field+":") {
					kept = append(kept, err)
				}
			}
			n.errs = kept
		}
		if p.creditsSet && p.seq >= n.creditsSeq {
			n.creditsSeq = p.seq
			mergeErrors("credits")
			if p.invalidate {
				n.credits = nil
			} else if p.credits != nil {
				cr := *p.credits
				// The reader stamps acquisition time, never the cache merge time.
				if cr.FetchedAt == "" {
					cr.FetchedAt = time.Now().UTC().Format(time.RFC3339Nano)
				}
				n.credits = &cr
			} else if !hasCreditError(n.errs) {
				n.errs = append(n.errs, "credits: unavailable")
			}
		}
		if p.checkinSet && p.seq >= n.checkinSeq {
			n.checkinSeq = p.seq
			mergeErrors("checkin")
			if p.checkin != nil {
				n.checkin = p.checkin
			}
		}
		if p.planSet && p.seq >= n.planSeq {
			n.planSeq = p.seq
			mergeErrors("plan")
			if p.plan != "" {
				n.plan = p.plan
			}
		}
		n.fetched = time.Now() // last cache update, NOT the credits acquisition time
		if exists {
			if accountCache.CompareAndSwap(id, old, n) {
				return n
			}
		} else if _, loaded := accountCache.LoadOrStore(id, n); !loaded {
			return n
		}
	}
}
func hasCreditError(errs []string) bool {
	for _, err := range errs {
		// Legacy unscoped errors are unknown, not positive credit evidence.
		if !strings.HasPrefix(err, "checkin:") && !strings.HasPrefix(err, "plan:") {
			return true
		}
	}
	return false
}
func creditSnapshotFresh(e *accountCacheEntry, now time.Time, age time.Duration) bool {
	if e == nil || e.credits == nil || hasCreditError(e.errs) {
		return false
	}
	at, err := time.Parse(time.RFC3339Nano, e.credits.FetchedAt)
	elapsed := now.Sub(at)
	return err == nil && elapsed >= 0 && elapsed <= age
}
func trustedCachedCredits(id string, now time.Time, age time.Duration) *creditsSummary {
	if v, ok := accountCache.Load(id); ok {
		if e, ok := v.(*accountCacheEntry); ok && creditSnapshotFresh(e, now, age) {
			return e.credits
		}
	}
	return nil
}
func invalidateCachedCredits(id string) {
	// Keep a generation barrier even when a read is still in flight.
	publishAccountSnapshot(id, accountSnapshotPatch{seq: nextAccountSnapshot(), creditsSet: true, invalidate: true})
}
