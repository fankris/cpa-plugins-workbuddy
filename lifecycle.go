// lifecycle.go implements credit-based auth lifecycle for workbuddy:
//   - CN exhausted → disable auth record, re-enable after check-in restores credits
//   - Legacy WorkBuddy-service accounts retain exhausted auth records disabled
//   - Unknown credits → no-op (never mis-kill)
//   - Hard credit errors from executor → recheck credits then apply policy
//   - Soft rate limits → never change auth lifecycle state
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

var (
	lifecycleState   sync.Map // auth_id (auth.ID) -> lifecycleStateEntry
	lifecycleSaveTTL = 30 * time.Second

	pluginRootCtx, pluginRootCancel = context.WithCancel(context.Background())
	pluginLifecycleMu               sync.Mutex
	pluginQuiescingState            bool
	pluginWorkers                   sync.WaitGroup
	pluginHostCallbacks             sync.WaitGroup
	pluginStreams                   = make(map[*pluginAsyncStream]struct{})
)

var errPluginQuiescing = fmt.Errorf("workbuddy plugin is quiescing")

type pluginAsyncStream struct {
	streamID          string
	cancel            context.CancelFunc
	closeOnce         sync.Once
	doneOnce          sync.Once
	closerMu          sync.Mutex
	closer            func()
	upstreamClosed    bool
	upstreamCloseOnce sync.Once
}

func pluginQuiescing() bool {
	pluginLifecycleMu.Lock()
	defer pluginLifecycleMu.Unlock()
	return pluginQuiescingState
}

func pluginContext() context.Context {
	pluginLifecycleMu.Lock()
	defer pluginLifecycleMu.Unlock()
	return pluginRootCtx
}

func pluginWorkerStart() bool {
	pluginLifecycleMu.Lock()
	defer pluginLifecycleMu.Unlock()
	if pluginQuiescingState {
		return false
	}
	pluginWorkers.Add(1)
	return true
}

func pluginWorkerDone() { pluginWorkers.Done() }

func hostCallbackStart(allowCleanup bool) bool {
	pluginLifecycleMu.Lock()
	defer pluginLifecycleMu.Unlock()
	if pluginQuiescingState && !allowCleanup {
		return false
	}
	pluginHostCallbacks.Add(1)
	return true
}

func hostCallbackDone() { pluginHostCallbacks.Done() }

func registerAsyncStream(streamID string, cancel context.CancelFunc) (*pluginAsyncStream, bool) {
	s := &pluginAsyncStream{streamID: streamID, cancel: cancel}
	pluginLifecycleMu.Lock()
	defer pluginLifecycleMu.Unlock()
	if pluginQuiescingState {
		return nil, false
	}
	pluginWorkers.Add(1)
	pluginStreams[s] = struct{}{}
	return s, true
}

func (s *pluginAsyncStream) setCloser(closer func()) {
	if s == nil || closer == nil {
		return
	}
	s.closerMu.Lock()
	if s.closer != nil {
		s.closerMu.Unlock()
		return
	}
	s.closer = closer
	closed := s.upstreamClosed
	s.closerMu.Unlock()
	if closed {
		s.upstreamCloseOnce.Do(closer)
	}
}

func (s *pluginAsyncStream) closeUpstream() {
	if s == nil {
		return
	}
	s.closerMu.Lock()
	s.upstreamClosed = true
	closer := s.closer
	s.closerMu.Unlock()
	if closer != nil {
		s.upstreamCloseOnce.Do(closer)
	}
}

func (s *pluginAsyncStream) closeHostStream() {
	if s != nil {
		s.closeOnce.Do(func() { streamClose(s.streamID) })
	}
}

func (s *pluginAsyncStream) finish() {
	if s == nil {
		return
	}
	s.doneOnce.Do(func() {
		pluginLifecycleMu.Lock()
		delete(pluginStreams, s)
		pluginLifecycleMu.Unlock()
		pluginWorkers.Done()
	})
}

func quiescePlugin() {
	pluginLifecycleMu.Lock()
	if !pluginQuiescingState {
		pluginQuiescingState = true
		pluginRootCancel()
	}
	streams := make([]*pluginAsyncStream, 0, len(pluginStreams))
	for s := range pluginStreams {
		streams = append(streams, s)
	}
	pluginLifecycleMu.Unlock()
	for _, s := range streams {
		if s.cancel != nil {
			s.cancel()
		}
		s.closeUpstream()
		s.closeHostStream()
	}
	pluginWorkers.Wait()
	pluginHostCallbacks.Wait()
}

func shutdownPlugin() {
	quiescePlugin()
	hostAPIMu.Lock()
	hostAPI = nil
	hostAPIMu.Unlock()
}

// resetPluginLifecycleForTest restores a fresh, non-quiescing lifecycle state.
//
// It cancels the previous root context first. The long-lived scheduler holds a
// pluginWorkers slot for the whole process lifetime and only releases it when
// its context is cancelled; replacing the context without cancelling leaves that
// worker running, so the next quiescePlugin() blocks forever on a worker nobody
// asked to stop. Production never replaces the context (only quiesce cancels
// it), but tests call this between cases — without the cancel, every later
// quiesce test hangs.
func resetPluginLifecycleForTest() {
	pluginLifecycleMu.Lock()
	oldCancel := pluginRootCancel
	pluginRootCtx, pluginRootCancel = context.WithCancel(context.Background())
	pluginQuiescingState = false
	pluginStreams = make(map[*pluginAsyncStream]struct{})
	pluginLifecycleMu.Unlock()
	if oldCancel != nil {
		oldCancel()
	}
}

type lifecycleStateEntry struct {
	disabled bool
	note     string
	at       time.Time
}

func lifecycleStateUnchanged(authID string, disabled bool, note string) bool {
	v, ok := lifecycleState.Load(authID)
	if !ok {
		return false
	}
	e := v.(*lifecycleStateEntry)
	if e.disabled != disabled || e.note != note {
		return false
	}
	return time.Since(e.at) < lifecycleSaveTTL
}

func rememberLifecycleState(authID string, disabled bool, note string) {
	lifecycleState.Store(authID, &lifecycleStateEntry{disabled: disabled, note: note, at: time.Now()})
}

// pruneLifecycleState removes entries for auth indices that no longer exist
// or whose TTL has expired. Called from dashboard prune to prevent unbounded growth.
func pruneLifecycleState() {
	files, err := hostAuthList()
	if err != nil {
		return
	}
	live := make(map[string]struct{}, len(files))
	for _, f := range files {
		live[f.ID] = struct{}{}
	}
	lifecycleState.Range(func(key, value any) bool {
		idx, _ := key.(string)
		if _, ok := live[idx]; !ok {
			lifecycleState.Delete(key)
			return true
		}
		if e, ok := value.(*lifecycleStateEntry); ok && time.Since(e.at) > 10*time.Minute {
			lifecycleState.Delete(key)
		}
		return true
	})
}

// disableAuth writes disabled:true for a CN (or fallback) account.
func disableAuth(authIndex, authID string, sa *storedAuth, cr *creditsSummary, reason string) error {
	mu := checkinLockFor(authIndex)
	mu.Lock()
	defer mu.Unlock()

	note := displayNote(sa, cr, true)
	if reason != "" && !strings.Contains(note, reason) && len(note)+len(reason) < 75 {
		note += " · " + reason
	}
	if lifecycleStateUnchanged(authID, true, note) {
		return nil
	}
	phys, err := hostAuthGetPhysical(authIndex)
	if err != nil {
		return err
	}
	name := authFileNameForPhysical(sa, phys)
	if err := saveAuthState(name, phys.JSON, sa, true, note, nil); err != nil {
		return err
	}
	rememberLifecycleState(authID, true, note)
	accountCache.Delete(authID)
	return nil
}

// reenableAuth writes disabled:false when CN has credits again.
func reenableAuth(authIndex, authID string, sa *storedAuth, cr *creditsSummary) error {
	mu := checkinLockFor(authIndex)
	mu.Lock()
	defer mu.Unlock()

	if !shouldReenableCN(true, cr) {
		return nil
	}
	note := displayNote(sa, cr, false)
	if lifecycleStateUnchanged(authID, false, note) {
		return nil
	}
	phys, err := hostAuthGetPhysical(authIndex)
	if err != nil {
		return err
	}
	name := authFileNameForPhysical(sa, phys)
	if err := saveAuthState(name, phys.JSON, sa, false, note, nil); err != nil {
		return err
	}
	rememberLifecycleState(authID, false, note)
	accountCache.Delete(authID)
	return nil
}

// deleteAuth retains an exhausted legacy WorkBuddy-service auth record while
// disabling it. The historical function name remains for policy compatibility;
// it clears plugin caches and selected state but never removes a physical auth file.
func deleteAuth(authIndex, authID string, sa *storedAuth) error {
	mu := checkinLockFor(authIndex)
	mu.Lock()
	defer mu.Unlock()

	phys, err := hostAuthGetPhysical(authIndex)
	if err != nil {
		return err
	}
	name := authFileNameForPhysical(sa, phys)
	note := appendAuthNote(existingNote(phys.JSON), displayNote(sa, nil, true))
	note = appendAuthNote(note, "CPA 管理端可清理")
	if err := saveAuthState(name, phys.JSON, nil, true, note, nil); err != nil {
		return err
	}
	rememberLifecycleState(authID, true, note)
	accountCache.Delete(authID)
	clearActiveAuthIfMatch(authID)
	return nil
}

// applyExhaustedPolicy disables exhausted CN accounts or retains and disables legacy WorkBuddy-service accounts.
func applyExhaustedPolicy(authIndex, authID string, sa *storedAuth, cr *creditsSummary, reason string) error {
	if !lifecycleEnabled() {
		return nil
	}
	action := lifecycleActionFor(accountServiceRegion(sa), cr)
	switch action {
	case lifecycleDelete:
		return deleteAuth(authIndex, authID, sa)
	case lifecycleDisable:
		return disableAuth(authIndex, authID, sa, cr, reason)
	default:
		return nil
	}
}

// syncAuthNote writes note without changing disabled state.
func syncAuthNote(authIndex, authID string, sa *storedAuth, cr *creditsSummary, disabled bool) error {
	if sa == nil {
		return nil
	}
	note := displayNote(sa, cr, disabled)
	if lifecycleStateUnchanged(authID, disabled, note) {
		return nil
	}
	mu := checkinLockFor(authIndex)
	mu.Lock()
	defer mu.Unlock()
	phys, err := hostAuthGetPhysical(authIndex)
	if err != nil {
		return err
	}
	name := authFileNameForPhysical(sa, phys)
	// Re-read disabled from disk as source of truth.
	disabled = phys.Disabled
	note = displayNote(sa, cr, disabled)
	if lifecycleStateUnchanged(authID, disabled, note) {
		return nil
	}
	if err := saveAuthState(name, phys.JSON, sa, disabled, note, nil); err != nil {
		return err
	}

	rememberLifecycleState(authID, disabled, note)
	return nil
}

// reconcileOneAccount refreshes credits and applies lifecycle for one auth.
// authIndex is used for host RPC (host.auth.get), authID (auth.ID) is used
// for cache keys (accountCache/lifecycleState) so it matches the scheduler's
// SchedulerAuthCandidate.ID.
// force ignores short-circuit only for credit fetch (uses force on cache via caller).
func reconcileOneAccount(authIndex, authID string, force bool) (action lifecycleAction, err error) {
	if !lifecycleEnabled() {
		return lifecycleNone, nil
	}
	// Single host.auth.get (A-19): previous hostAuthGet + hostAuthGetPhysical
	// doubled RPC on every reconcile tick (21 accounts × 2).
	sa, phys, err := hostAuthGetBundle(authIndex)
	if err != nil {
		return lifecycleNone, err
	}
	disabled := false
	if phys != nil {
		disabled = phys.Disabled
	}

	// Credits: use force path via fetchUserResource always when force,
	// else try cache first.
	var cr *creditsSummary
	if !force {
		if v, ok := accountCache.Load(authID); ok {
			if e, ok2 := v.(*accountCacheEntry); ok2 && e.credits != nil && time.Since(e.fetched) < accountCacheTTL {
				cr = e.credits
			}
		}
	}
	if cr == nil {
		// Route credits fetch through cachedAccountDetails so singleflight
		// serializes concurrent writers for the same authID (P0-2 fix: the
		// previous Load→Store sequence here had a check-then-act window
		// where a concurrent dashboard cachedAccountDetails write could
		// overwrite our merge with newer plan/checkin values).
		_, _, cr2, _ := cachedAccountDetails(authID, sa, true)
		cr = cr2
		if cr == nil {
			return lifecycleNone, nil
		}
	}

	region := accountServiceRegion(sa)
	if region == regionCN && disabled {
		if shouldReenableCN(true, cr) {
			if err := reenableAuth(authIndex, authID, sa, cr); err != nil {
				return lifecycleReenable, err
			}
			return lifecycleReenable, nil
		}
		// still disabled: refresh note
		_ = syncAuthNote(authIndex, authID, sa, cr, true)
		return lifecycleNone, nil
	}

	act := lifecycleActionFor(region, cr)
	switch act {
	case lifecycleDelete:
		// Confirm before retaining a legacy WorkBuddy-service account as disabled:
		// a transient 402 must not alter the auth record.
		cr2, err2 := fetchUserResource(sa)
		if err2 != nil || !isCreditsExhausted(cr2) {
			return lifecycleNone, nil
		}
		return lifecycleDelete, deleteAuth(authIndex, authID, sa)

	case lifecycleDisable:
		return lifecycleDisable, disableAuth(authIndex, authID, sa, cr, "耗尽")
	default:
		// healthy: keep note fresh (throttled)
		_ = syncAuthNote(authIndex, authID, sa, cr, false)
		return lifecycleNone, nil
	}
}

// reconcileAllAccounts walks workbuddy auths and applies lifecycle.
func reconcileAllAccounts(force bool) []map[string]any {
	if !lifecycleEnabled() {
		return nil
	}
	files, err := hostAuthList()
	if err != nil {
		return []map[string]any{{"error": err.Error()}}
	}
	out := make([]map[string]any, 0, len(files))
	for _, f := range files {
		act, err := reconcileOneAccount(f.AuthIndex, f.ID, force)
		row := map[string]any{"auth_index": f.AuthIndex, "action": act.String()}
		if err != nil {
			row["error"] = err.Error()
		}
		if act != lifecycleNone || err != nil {
			out = append(out, row)
		}
	}
	return out
}

// reconcileAfterExecutorError triggers lifecycle when upstream reports hard credit failure.
// AuthID from the executor may be the credential ID (UID) rather than runtime auth_index;
// we resolve via host.auth.list when direct get fails.
func reconcileAfterExecutorError(authID string, status int, body string) {
	if !lifecycleEnabled() || strings.TrimSpace(authID) == "" {
		return
	}
	if isSoftRateLimit(status, body) && !isHardCreditError(status, body) {
		return
	}
	if !isHardCreditError(status, body) {
		return
	}
	startPluginWorker(func() {
		idx, id := resolveAuthIndexAndID(authID)
		if idx == "" {
			return
		}
		_, _ = reconcileOneAccount(idx, id, true)
	})
}

func startPluginWorker(fn func()) bool {
	if fn == nil || !pluginWorkerStart() {
		return false
	}
	go func() {
		defer pluginWorkerDone()
		fn()
	}()
	return true
}

// resolveAuthIndexAndID maps executor AuthID (index, file id, or account UID)
// to host auth_index AND auth.ID. Returns ("", "") if not found.
func resolveAuthIndexAndID(authID string) (string, string) {
	authID = strings.TrimSpace(authID)
	if authID == "" {
		return "", ""
	}
	// Fast path: already an auth_index the host understands.
	if _, err := hostAuthGet(authID); err == nil {
		// Find the matching file entry to get auth.ID.
		if files, err := hostAuthList(); err == nil {
			for _, f := range files {
				if f.AuthIndex == authID {
					return authID, f.ID
				}
			}
		}
		return authID, ""
	}
	files, err := hostAuthList()
	if err != nil {
		return "", ""
	}
	// Prefer O(list) name/id match before per-account host.auth.get (A-22).
	// Try all canonical and legacy realm-qualified names before the slower
	// content scan for records whose list metadata lacks a UID.
	wantNames := authFileNameCandidates(authID)
	for _, f := range files {
		if f.AuthIndex == authID || f.ID == authID || f.Name == authID {
			return f.AuthIndex, f.ID
		}
		if listEntryMatchesAnyUID(f, authID, wantNames...) {
			return f.AuthIndex, f.ID
		}
	}
	// Slow path: only when list metadata lacks uid (rare legacy shapes).
	for _, f := range files {
		sa, err := hostAuthGet(f.AuthIndex)
		if err != nil {
			continue
		}
		if strings.TrimSpace(sa.Account.UID) == authID {
			return f.AuthIndex, f.ID
		}
	}
	return "", ""
}

// reconcileByUID finds workbuddy auth by account UID and applies executor-error lifecycle.
func reconcileByUID(uid string, status int, body string) {
	uid = strings.TrimSpace(uid)
	if uid == "" || !lifecycleEnabled() {
		return
	}
	if !isHardCreditError(status, body) {
		return
	}
	idx, id := resolveAuthIndexAndID(uid)
	if idx == "" {
		return
	}
	_, _ = reconcileOneAccount(idx, id, true)
}

// invalidateAccountCredits drops cached credits so the next panel/reconcile
// fetch hits upstream. Call after a successful chat completion — otherwise a
// short TTL cache makes "used" look frozen while the user is burning credits.
func invalidateAccountCredits(authID, authUID string) {
	// Invalidate credits only — keep plan/checkin in cache.
	invalidateCredits := func(id string) {
		if v, ok := accountCache.Load(id); ok {
			if e, ok2 := v.(*accountCacheEntry); ok2 {
				fresh := *e
				fresh.credits = nil
				fresh.fetched = time.Now()
				accountCache.Store(id, &fresh)
			}
		}
	}
	if authID != "" {
		invalidateCredits(authID)
	}
	if authUID == "" {
		return
	}
	// Also drop any cache keyed by auth_index that maps to this UID. Do not
	// return merely because authID equals the UID: CN/Global/Intl records can
	// legitimately share a UID while having distinct physical auth paths.
	files, err := hostAuthList()
	if err != nil {
		return
	}
	wantNames := authFileNameCandidates(authUID)
	matchedByName := false
	for _, f := range files {
		if f.AuthIndex == authID || f.ID == authID || f.Name == authID {
			invalidateCredits(f.ID)
			continue
		}
		if listEntryMatchesAnyUID(f, authUID, wantNames...) {
			invalidateCredits(f.ID)
			matchedByName = true
		}
	}
	if matchedByName {
		return
	}
	// Slow path: legacy names without uid in list metadata.
	for _, f := range files {
		if f.AuthIndex == authID {
			continue
		}
		sa, err := hostAuthGet(f.AuthIndex)
		if err != nil {
			continue
		}
		if strings.TrimSpace(sa.Account.UID) == authUID {
			invalidateCredits(f.ID)
		}
	}
}

// listEntryMatchesUID reports whether host list metadata already encodes the UID
// for one canonical filename. It remains as a small testable helper; callers
// that support both domestic and Intl records use listEntryMatchesAnyUID.
func listEntryMatchesUID(f pluginapi.HostAuthFileEntry, uid, wantName string) bool {
	if uid == "" {
		return false
	}
	if strings.EqualFold(f.Name, wantName) || strings.EqualFold(f.ID, wantName) {
		return true
	}
	base := strings.TrimSuffix(f.Name, ".json")
	wantBase := strings.TrimSuffix(wantName, ".json")
	return strings.EqualFold(base, wantBase)
}

func listEntryMatchesAnyUID(f pluginapi.HostAuthFileEntry, uid string, names ...string) bool {
	for _, name := range names {
		if listEntryMatchesUID(f, uid, name) {
			return true
		}
	}
	return false
}

// enrichAuthMetadata builds Metadata map for AuthData (type/logo/note/disabled).
//
// v0.9.32: the map now also carries the account's identity fields. host.auth.save
// rebuilds the auth record from the FILE JSON (buildAuthFromFileData), where the
// host derives Label from metadata["email"] and falls back to the provider key
// ("workbuddy") when it is absent. Because every persist path in this plugin
// goes through host.auth.save, the account's display name silently degraded to
// "workbuddy" on the first lifecycle write (disable, note sync, keepalive) even
// though the credential file still held the nickname. Writing `email` (and the
// account name fields) into the file keeps the row labelled with the account
// after any save, and gives CPA's auth list a real identity to render.
func enrichAuthMetadata(sa *storedAuth, cr *creditsSummary, disabled bool) map[string]any {
	note := displayNote(sa, cr, disabled)
	meta := map[string]any{
		"type":     providerName,
		"provider": providerName,
		"logo":     pluginLogoURL,
		"note":     note,
		"disabled": disabled,
	}
	// Identity fields. accountNameForAuth is the single source of truth used by
	// both the label and the metadata so they can never disagree.
	if name := accountNameForAuth(sa); name != "" {
		meta["email"] = name
		meta["account_name"] = name
	}
	if sa != nil {
		if uid := strings.TrimSpace(sa.Account.UID); uid != "" {
			meta["uid"] = uid
		}
		if eid := strings.TrimSpace(sa.Account.EnterpriseID); eid != "" {
			meta["enterprise_id"] = eid
		}
		if realm := accountRegion(sa); realm != "" {
			meta["region"] = realm
		}
	}
	return meta
}

// accountNameForAuth resolves the best human-readable identity for an account.
// Order: nickname (the upstream account name) → email claim from the token →
// empty (callers fall back to the provider label). Deliberately never returns
// the bare provider key: that is exactly the "everything is called workbuddy"
// symptom this fixes.
func accountNameForAuth(sa *storedAuth) string {
	if sa == nil {
		return ""
	}
	if nickname := strings.TrimSpace(sa.Account.Nickname); nickname != "" {
		return nickname
	}
	if email := tokenEmailClaim(sa.Auth.AccessToken); email != "" {
		return email
	}
	return ""
}

// tokenEmailClaim extracts an email-shaped claim from the access token. The
// WorkBuddy/CodeBuddy tokens are JWTs whose payload carries the account
// identity; older tokens may omit it, hence the empty return. Decoding is
// best-effort: a malformed token yields "" and never fails the caller.
func tokenEmailClaim(accessToken string) string {
	parts := strings.Split(strings.TrimSpace(accessToken), ".")
	if len(parts) < 2 {
		return ""
	}
	payload := parts[1]
	if pad := len(payload) % 4; pad != 0 {
		payload += strings.Repeat("=", 4-pad)
	}
	raw, err := base64.URLEncoding.DecodeString(payload)
	if err != nil {
		return ""
	}
	var claims struct {
		Email string `json:"email"`
		UPN   string `json:"upn"`
		Sub   string `json:"sub"`
	}
	if json.Unmarshal(raw, &claims) != nil {
		return ""
	}
	for _, candidate := range []string{claims.Email, claims.UPN, claims.Sub} {
		candidate = strings.TrimSpace(candidate)
		if strings.Contains(candidate, "@") {
			return candidate
		}
	}
	return ""
}
