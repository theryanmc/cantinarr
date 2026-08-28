// Package mediaaccess provisions and tracks user accounts on media servers
// (Jellyfin today, Emby next). Eligibility is the instance grant: a granted
// user creates their own account, a revoked grant switches the account off,
// and a returning grant switches it back on. Cantinarr never stores the
// password it hands the server and never deletes an account it did not just
// create.
package mediaaccess

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/mediaserver"
)

// ProviderFactory builds the client for a media-server instance. In
// production it is instance.NewMediaServerProvider.
type ProviderFactory func(inst *instance.Instance) (mediaserver.Provider, error)

var (
	// ErrNotAvailable is the one answer for every "not for you" case —
	// unknown instance, not a media server, no grant — so the endpoint is
	// never an existence oracle.
	ErrNotAvailable         = errors.New("media server is not available to this user")
	ErrAccountExists        = errors.New("user already has an account on this media server")
	ErrNameTaken            = errors.New("account name is already taken on the media server")
	ErrInvalidName          = errors.New("username is not a valid media server account name")
	ErrConfigInvalid        = errors.New("media server configuration is unreadable; re-save the instance")
	ErrInstanceNotFound     = errors.New("instance not found")
	ErrNotMediaServer       = errors.New("not a media server instance")
	ErrUserNotFound         = errors.New("user not found")
	ErrRemoteUserNotFound   = errors.New("remote user not found")
	ErrAdministratorAccount = errors.New("administrator accounts can't be linked")
	ErrRemoteAlreadyLinked  = errors.New("remote account is already linked to another user")
	ErrNoAccount            = errors.New("no linked account")
	// ErrUpstream wraps a media-server failure. The wrapped text is host-free
	// by the Provider contract; handlers still answer with fixed bodies.
	ErrUpstream = errors.New("media server request failed")
)

const (
	verifyTimeout    = 3 * time.Second
	createTimeout    = 30 * time.Second
	reconcileTimeout = 10 * time.Second
	// driftSweepInterval paces the retry of switch-offs that could not reach
	// the media server. A pass costs nothing when nothing is drifted: the
	// candidate query is answered entirely from Cantinarr's own tables.
	driftSweepInterval = 5 * time.Minute
	// libraryPropagationBudget caps the whole re-scope pass. It runs inside
	// the admin's save, so a media server that accepts the library list and
	// then answers policy writes slowly must not hold the request open for
	// one timeout per account.
	libraryPropagationBudget = 30 * time.Second
)

// Service owns the user_media_server_accounts table and every remote action.
type Service struct {
	db        *sql.DB
	store     *instance.Store
	providers ProviderFactory
	logger    *slog.Logger

	mu    sync.Mutex
	locks map[string]*sync.Mutex
	// sweepMu serializes drift sweeps so a slow pass cannot overlap the next
	// tick and reconcile the same user twice at once.
	sweepMu sync.Mutex
}

// NewService wires the service. providers is called per request: media
// server clients are stateless, and the store hands back decrypted keys.
func NewService(db *sql.DB, store *instance.Store, providers ProviderFactory, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{db: db, store: store, providers: providers, logger: logger, locks: map[string]*sync.Mutex{}}
}

// AccountView is a user's own account on one server as the guide shows it.
type AccountView struct {
	Username string `json:"username"`
	Disabled bool   `json:"disabled"`
	// Verified is true when the server confirmed the account just now; false
	// means the answer came from Cantinarr's record because the server could
	// not be reached (blindness, said as such — never mistaken for absence).
	Verified bool `json:"verified"`
}

// ServerView is one media server a user is granted, with their account
// state. It carries the admin-typed public address and nothing else about
// the instance.
type ServerView struct {
	InstanceID    string       `json:"instance_id"`
	ServiceType   string       `json:"service_type"`
	Name          string       `json:"name"`
	PublicAddress string       `json:"public_address"`
	Account       *AccountView `json:"account"`
}

// CreatedAccount is what a user gets back after creating their account.
type CreatedAccount struct {
	Username      string `json:"username"`
	PublicAddress string `json:"public_address"`
}

// Account is an admin-facing row: which Cantinarr user is which remote
// account on which server.
type Account struct {
	UserID             int64     `json:"user_id"`
	InstanceID         string    `json:"instance_id"`
	InstanceName       string    `json:"instance_name"`
	ServiceType        string    `json:"service_type"`
	RemoteUserID       string    `json:"remote_user_id"`
	Username           string    `json:"username"`
	CreatedByCantinarr bool      `json:"created_by_cantinarr"`
	Disabled           bool      `json:"disabled"`
	CreatedAt          time.Time `json:"created_at"`
}

func (s *Service) lock(userID int64, instanceID string) func() {
	key := fmt.Sprintf("%d:%s", userID, instanceID)
	s.mu.Lock()
	l := s.locks[key]
	if l == nil {
		l = &sync.Mutex{}
		s.locks[key] = l
	}
	s.mu.Unlock()
	l.Lock()
	return l.Unlock
}

// grantedMediaServers returns the media-server instance ids a user holds a
// grant on, in the store's deterministic order. Grants only: a pin is never
// media-server eligibility.
func (s *Service) grantedMediaServers(userID int64) ([]string, error) {
	grants, err := s.store.ListUserGrants(userID)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, serviceType := range instance.MediaServerTypes() {
		ids = append(ids, grants[serviceType]...)
	}
	return ids, nil
}

func contains(ids []string, id string) bool {
	for _, candidate := range ids {
		if candidate == id {
			return true
		}
	}
	return false
}

// ListForUser lists the media servers a user is granted with their account
// on each, confirming each existing account against the live server with a
// short timeout, concurrently, so a dead server costs one wait rather than
// one per server.
func (s *Service) ListForUser(ctx context.Context, userID int64) ([]ServerView, error) {
	ids, err := s.grantedMediaServers(userID)
	if err != nil {
		return nil, err
	}
	views := make([]ServerView, 0, len(ids))
	type pending struct {
		index int
		inst  *instance.Instance
		row   *accountRow
	}
	var checks []pending
	for _, id := range ids {
		inst, err := s.store.Get(id)
		if err != nil {
			return nil, err
		}
		if inst == nil || !instance.IsMediaServerType(inst.ServiceType) {
			continue
		}
		row, err := s.getAccount(userID, id)
		if err != nil {
			return nil, err
		}
		views = append(views, ServerView{
			InstanceID:    inst.ID,
			ServiceType:   inst.ServiceType,
			Name:          inst.Name,
			PublicAddress: inst.MediaServerConfig.PublicAddress,
		})
		if row != nil {
			checks = append(checks, pending{index: len(views) - 1, inst: inst, row: row})
		}
	}

	var wg sync.WaitGroup
	results := make([]*AccountView, len(views))
	for _, check := range checks {
		wg.Add(1)
		go func(check pending) {
			defer wg.Done()
			results[check.index] = s.verifyAccount(ctx, check.inst, check.row)
		}(check)
	}
	wg.Wait()
	for i := range views {
		views[i].Account = results[i]
	}
	return views, nil
}

// verifyAccount reads the live account. A confirmed 404 is definitive
// absence and reads as no account; an unreachable server falls back to the
// stored row with verified=false.
func (s *Service) verifyAccount(ctx context.Context, inst *instance.Instance, row *accountRow) *AccountView {
	stored := &AccountView{Username: row.RemoteUsername, Disabled: row.DisabledAt.Valid, Verified: false}
	provider, err := s.providers(inst)
	if err != nil {
		return stored
	}
	ctx, cancel := context.WithTimeout(ctx, verifyTimeout)
	defer cancel()
	live, err := provider.GetUser(ctx, row.RemoteUserID)
	if errors.Is(err, mediaserver.ErrUserNotFound) {
		s.logger.Info("mediaaccess: linked account no longer exists on the server", "user_id", row.UserID, "instance_id", inst.ID)
		return nil
	}
	if err != nil {
		s.logger.Warn("mediaaccess: could not confirm account", "err", err, "user_id", row.UserID, "instance_id", inst.ID)
		return stored
	}
	return &AccountView{Username: live.Name, Disabled: live.IsDisabled, Verified: true}
}

// CreateAccount creates the caller's account on a granted media server,
// named after their Cantinarr username, restricted to the shared libraries.
// The password is handed to the server once and never kept.
func (s *Service) CreateAccount(ctx context.Context, userID int64, instanceID, password string) (CreatedAccount, error) {
	unlock := s.lock(userID, instanceID)
	defer unlock()
	ctx, cancel := context.WithTimeout(ctx, createTimeout)
	defer cancel()

	inst, err := s.store.Get(instanceID)
	if err != nil {
		return CreatedAccount{}, err
	}
	if inst == nil || !instance.IsMediaServerType(inst.ServiceType) {
		return CreatedAccount{}, ErrNotAvailable
	}
	granted, err := s.grantedMediaServers(userID)
	if err != nil {
		return CreatedAccount{}, err
	}
	if !contains(granted, instanceID) {
		return CreatedAccount{}, ErrNotAvailable
	}
	if inst.MediaServerConfigInvalid {
		return CreatedAccount{}, ErrConfigInvalid
	}
	provider, err := s.providers(inst)
	if err != nil {
		return CreatedAccount{}, ErrNotAvailable
	}

	if row, err := s.getAccount(userID, instanceID); err != nil {
		return CreatedAccount{}, err
	} else if row != nil {
		// A row is a claim, not proof: confirm it before refusing. A server
		// that no longer has the account lets the user create a fresh one.
		_, liveErr := provider.GetUser(ctx, row.RemoteUserID)
		switch {
		case liveErr == nil:
			return CreatedAccount{}, ErrAccountExists
		case errors.Is(liveErr, mediaserver.ErrUserNotFound):
			if _, err := s.deleteAccount(userID, instanceID); err != nil {
				return CreatedAccount{}, err
			}
		default:
			return CreatedAccount{}, fmt.Errorf("%w: %v", ErrUpstream, liveErr)
		}
	}

	var username string
	if err := s.db.QueryRow("SELECT username FROM users WHERE id = ?", userID).Scan(&username); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return CreatedAccount{}, ErrUserNotFound
		}
		return CreatedAccount{}, fmt.Errorf("load user: %w", err)
	}

	remote, err := provider.CreateUser(ctx, username, password, inst.MediaServerConfig.LibraryIDs)
	switch {
	case errors.Is(err, mediaserver.ErrInvalidName):
		return CreatedAccount{}, ErrInvalidName
	case errors.Is(err, mediaserver.ErrUserExists):
		return CreatedAccount{}, ErrNameTaken
	case err != nil:
		return CreatedAccount{}, fmt.Errorf("%w: %v", ErrUpstream, err)
	}

	inserted, err := s.insertAccount(accountRow{
		UserID: userID, InstanceID: instanceID,
		RemoteUserID: remote.ID, RemoteUsername: remote.Name, CreatedByCantinarr: true,
	}, true)
	if err != nil || !inserted {
		// The grant vanished, the instance was deleted, or a concurrent link
		// claimed the slot while the server was creating the account. Undo
		// the remote side so nothing unrestricted survives.
		s.rollbackCreate(ctx, provider, remote.ID, userID, instanceID)
		switch {
		case errors.Is(err, errAccountConflict), errors.Is(err, errRemoteConflict):
			return CreatedAccount{}, ErrAccountExists
		case err != nil && !errors.Is(err, errRowReference):
			return CreatedAccount{}, err
		}
		return CreatedAccount{}, ErrNotAvailable
	}
	return CreatedAccount{Username: remote.Name, PublicAddress: inst.MediaServerConfig.PublicAddress}, nil
}

func (s *Service) rollbackCreate(ctx context.Context, provider mediaserver.Provider, remoteID string, userID int64, instanceID string) {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), reconcileTimeout)
	defer cancel()
	if err := provider.DeleteUser(cleanupCtx, remoteID); err != nil {
		s.logger.Error("mediaaccess: could not roll back a half-created account", "err", err, "user_id", userID, "instance_id", instanceID)
	}
}

// ListAccounts returns every linked account for the admin Users screen.
func (s *Service) ListAccounts() ([]Account, error) {
	return s.listAccounts()
}

func (s *Service) mediaServerInstance(instanceID string) (*instance.Instance, error) {
	inst, err := s.store.Get(instanceID)
	if err != nil {
		return nil, err
	}
	if inst == nil {
		return nil, ErrInstanceNotFound
	}
	if !instance.IsMediaServerType(inst.ServiceType) {
		return nil, ErrNotMediaServer
	}
	return inst, nil
}

// RemoteUsers lists the accounts on a media server, for the admin link picker.
func (s *Service) RemoteUsers(ctx context.Context, instanceID string) ([]mediaserver.RemoteUser, error) {
	inst, err := s.mediaServerInstance(instanceID)
	if err != nil {
		return nil, err
	}
	provider, err := s.providers(inst)
	if err != nil {
		return nil, ErrNotMediaServer
	}
	ctx, cancel := context.WithTimeout(ctx, reconcileTimeout)
	defer cancel()
	users, err := provider.Users(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUpstream, err)
	}
	if users == nil {
		users = []mediaserver.RemoteUser{}
	}
	return users, nil
}

// LinkAccount records that a Cantinarr user is an existing remote account,
// grants them the instance if they lack it, and brings the account's
// disabled state in line with that grant. The account's libraries are left
// exactly as the admin configured them on the server.
func (s *Service) LinkAccount(ctx context.Context, userID int64, instanceID, remoteUserID string) (Account, error) {
	unlock := s.lock(userID, instanceID)
	defer unlock()
	ctx, cancel := context.WithTimeout(ctx, createTimeout)
	defer cancel()

	inst, err := s.mediaServerInstance(instanceID)
	if err != nil {
		return Account{}, err
	}
	var exists int
	if err := s.db.QueryRow("SELECT 1 FROM users WHERE id = ?", userID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Account{}, ErrUserNotFound
		}
		return Account{}, fmt.Errorf("load user: %w", err)
	}
	provider, err := s.providers(inst)
	if err != nil {
		return Account{}, ErrNotMediaServer
	}
	remote, err := provider.GetUser(ctx, remoteUserID)
	switch {
	case errors.Is(err, mediaserver.ErrUserNotFound):
		return Account{}, ErrRemoteUserNotFound
	case err != nil:
		return Account{}, fmt.Errorf("%w: %v", ErrUpstream, err)
	case remote.IsAdministrator:
		return Account{}, ErrAdministratorAccount
	}

	_, err = s.insertAccount(accountRow{
		UserID: userID, InstanceID: instanceID,
		RemoteUserID: remote.ID, RemoteUsername: remote.Name, CreatedByCantinarr: false,
	}, false)
	switch {
	case errors.Is(err, errAccountConflict):
		return Account{}, ErrAccountExists
	case errors.Is(err, errRemoteConflict):
		return Account{}, ErrRemoteAlreadyLinked
	case errors.Is(err, errRowReference):
		return Account{}, ErrInstanceNotFound
	case err != nil:
		return Account{}, err
	}

	grants, err := s.store.ListUserGrants(userID)
	if err != nil {
		return Account{}, err
	}
	if !contains(grants[inst.ServiceType], instanceID) {
		if err := s.store.SetUserGrants(userID, map[string][]string{
			inst.ServiceType: append(grants[inst.ServiceType], instanceID),
		}); err != nil {
			return Account{}, err
		}
	}
	s.reconcileUser(ctx, userID)

	accounts, err := s.listAccounts()
	if err != nil {
		return Account{}, err
	}
	for _, a := range accounts {
		if a.UserID == userID && a.InstanceID == instanceID {
			return a, nil
		}
	}
	return Account{}, ErrNoAccount
}

// UnlinkAccount forgets the row. The remote account and the grant stay as
// they are: unlinking is "stop managing this", not revocation.
func (s *Service) UnlinkAccount(userID int64, instanceID string) error {
	deleted, err := s.deleteAccount(userID, instanceID)
	if err != nil {
		return err
	}
	if !deleted {
		return ErrNoAccount
	}
	return nil
}

// OnGrantsChanged is the instance handler's grant observer: every affected
// user's accounts are reconciled against their grants.
func (s *Service) OnGrantsChanged(userIDs []int64) {
	for _, userID := range userIDs {
		ctx, cancel := context.WithTimeout(context.Background(), reconcileTimeout)
		s.reconcileUser(ctx, userID)
		cancel()
	}
}

// SweepAccountDrift retries the account switch-offs and switch-ons that never
// reached the media server. A grant write reconciles synchronously and, by
// design, does not fail when the server is down — which used to mean a grant
// revoked during an outage was applied to Cantinarr and never to the server,
// leaving the account signed-in-able forever with only a WARN line to say so.
// Each pass re-derives the intent from the grants (the account rows whose
// disabled stamp disagrees) and reconciles those users, so the switch-off
// lands as soon as the server is reachable again.
func (s *Service) SweepAccountDrift(ctx context.Context) {
	s.sweepMu.Lock()
	defer s.sweepMu.Unlock()

	userIDs, err := s.listDriftedAccountUsers()
	if err != nil {
		s.logger.Error("mediaaccess: drift sweep: list candidates", "err", err)
		return
	}
	if len(userIDs) == 0 {
		return
	}
	s.logger.Info("mediaaccess: retrying media server account changes that did not land", "users", len(userIDs))
	for _, userID := range userIDs {
		if ctx.Err() != nil {
			return
		}
		passCtx, cancel := context.WithTimeout(ctx, reconcileTimeout)
		s.reconcileUser(passCtx, userID)
		cancel()
	}
}

// StartAccountMaintenance sweeps once now — a switch-off can be owed from
// before this process started — and then on a fixed cadence until ctx ends.
func (s *Service) StartAccountMaintenance(ctx context.Context) {
	go func() {
		s.SweepAccountDrift(ctx)
		ticker := time.NewTicker(driftSweepInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.SweepAccountDrift(ctx)
			}
		}
	}()
}

// OnSharedLibrariesChanged is the instance handler's shared-libraries
// observer: it re-applies the instance's library selection to the accounts
// Cantinarr created there, so unticking a library actually takes it away from
// the people who already have accounts instead of only from future ones.
// Accounts an admin linked are left alone — Cantinarr never edits a policy it
// did not write — and so is every other server. Failures are logged and the
// next save retries them; the admin has just read this server's library list
// to reach this screen, so a server that answers the list and then refuses
// the write is the narrow case.
func (s *Service) OnSharedLibrariesChanged(instanceID string, libraryIDs []string) {
	inst, err := s.mediaServerInstance(instanceID)
	if err != nil {
		s.logger.Error("mediaaccess: shared libraries changed: load instance", "err", err, "instance_id", instanceID)
		return
	}
	rows, err := s.listAccountsCreatedOn(instanceID)
	if err != nil {
		s.logger.Error("mediaaccess: shared libraries changed: list accounts", "err", err, "instance_id", instanceID)
		return
	}
	if len(rows) == 0 {
		return
	}
	provider, err := s.providers(inst)
	if err != nil {
		s.logger.Error("mediaaccess: shared libraries changed: build client", "err", err, "instance_id", instanceID)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), libraryPropagationBudget)
	defer cancel()
	for i, row := range rows {
		if ctx.Err() != nil {
			s.logger.Error("mediaaccess: shared libraries changed: out of time",
				"instance_id", instanceID, "not_rescoped", len(rows)-i, "of", len(rows))
			return
		}
		if err := provider.SetLibraries(ctx, row.RemoteUserID, libraryIDs); err != nil {
			s.logger.Error("mediaaccess: shared libraries changed: re-scope account",
				"err", err, "user_id", row.UserID, "instance_id", instanceID)
		}
	}
}

// reconcileUser makes each of a user's linked accounts enabled exactly when
// the user holds the instance's grant. It compares against the LIVE state,
// not the row's stamp, so an account an admin re-enabled or disabled on the
// server side converges too. Failures are logged (ids only) and skipped: a
// grant write must never fail because a media server is down.
func (s *Service) reconcileUser(ctx context.Context, userID int64) {
	rows, err := s.listAccountsForUser(userID)
	if err != nil {
		s.logger.Error("mediaaccess: reconcile: list accounts", "err", err, "user_id", userID)
		return
	}
	if len(rows) == 0 {
		return
	}
	granted, err := s.grantedMediaServers(userID)
	if err != nil {
		s.logger.Error("mediaaccess: reconcile: list grants", "err", err, "user_id", userID)
		return
	}
	for _, row := range rows {
		inst, err := s.store.Get(row.InstanceID)
		if err != nil || inst == nil {
			continue
		}
		provider, err := s.providers(inst)
		if err != nil {
			continue
		}
		wantDisabled := !contains(granted, row.InstanceID)
		live, err := provider.GetUser(ctx, row.RemoteUserID)
		if err != nil {
			s.logger.Warn("mediaaccess: reconcile: read account", "err", err, "user_id", userID, "instance_id", row.InstanceID)
			continue
		}
		if live.IsAdministrator {
			s.logger.Warn("mediaaccess: reconcile: linked account is an administrator; leaving it alone", "user_id", userID, "instance_id", row.InstanceID)
			continue
		}
		if live.IsDisabled != wantDisabled {
			if err := provider.SetDisabled(ctx, row.RemoteUserID, wantDisabled); err != nil {
				s.logger.Error("mediaaccess: reconcile: set disabled", "err", err, "user_id", userID, "instance_id", row.InstanceID, "disabled", wantDisabled)
				continue
			}
		}
		if row.DisabledAt.Valid != wantDisabled {
			if err := s.setDisabledAt(userID, row.InstanceID, wantDisabled); err != nil {
				s.logger.Error("mediaaccess: reconcile: stamp", "err", err, "user_id", userID, "instance_id", row.InstanceID)
			}
		}
	}
}

// BeforeUserDelete is the auth handler's delete hook. Called before the
// user is deleted, it snapshots what would need switching off; the returned
// closure does it and must run only after the delete succeeded (the delete
// can still refuse: last admin, self-delete). Rows are gone by cascade at
// that point, which is fine — the snapshot already holds the remote ids.
func (s *Service) BeforeUserDelete(userID int64) (committed func()) {
	rows, err := s.listAccountsForUser(userID)
	if err != nil {
		s.logger.Error("mediaaccess: delete hook: list accounts", "err", err, "user_id", userID)
		return func() {}
	}
	type target struct {
		inst *instance.Instance
		row  accountRow
	}
	var targets []target
	for _, row := range rows {
		inst, err := s.store.Get(row.InstanceID)
		if err != nil || inst == nil {
			continue
		}
		targets = append(targets, target{inst: inst, row: row})
	}
	return func() {
		for _, t := range targets {
			provider, err := s.providers(t.inst)
			if err != nil {
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), reconcileTimeout)
			live, err := provider.GetUser(ctx, t.row.RemoteUserID)
			switch {
			case errors.Is(err, mediaserver.ErrUserNotFound):
			case err != nil:
				s.logger.Warn("mediaaccess: delete hook: read account", "err", err, "user_id", userID, "instance_id", t.inst.ID)
			case live.IsAdministrator:
				s.logger.Warn("mediaaccess: delete hook: linked account is an administrator; leaving it alone", "user_id", userID, "instance_id", t.inst.ID)
			case !live.IsDisabled:
				if err := provider.SetDisabled(ctx, t.row.RemoteUserID, true); err != nil {
					s.logger.Error("mediaaccess: delete hook: disable account", "err", err, "user_id", userID, "instance_id", t.inst.ID)
				}
			}
			cancel()
		}
	}
}
