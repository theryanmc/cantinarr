package push

import (
	"reflect"
	"sort"
	"testing"
)

func TestPrefsGetDefaultsForMissingRow(t *testing.T) {
	database, err := dbOpen(t)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (1, 'alice', '', 'user')")

	store := NewPrefsStore(database)
	got, err := store.Get(1)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	want := Prefs{RequestDecision: false, RequestPending: true, NewMovie: true, NewEpisode: true, NewBook: true, IssueCreated: true, AgentActionPending: true, PlexAccessRequest: true, PlexInviteSent: true}
	if got != want {
		t.Errorf("default prefs = %+v, want %+v", got, want)
	}
}

func TestPrefsSetThenGet(t *testing.T) {
	database, err := dbOpen(t)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (1, 'alice', '', 'user')")

	store := NewPrefsStore(database)
	want := Prefs{RequestDecision: true, RequestPending: false, NewMovie: false, NewEpisode: true, NewBook: true, PlexAccessRequest: true}
	if err := store.Set(1, want); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := store.Get(1)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != want {
		t.Errorf("after Set, prefs = %+v, want %+v", got, want)
	}

	// Set is an upsert: a second call replaces the row.
	want2 := Prefs{RequestDecision: false, RequestPending: true, NewMovie: true, NewEpisode: false}
	if err := store.Set(1, want2); err != nil {
		t.Fatalf("Set (upsert): %v", err)
	}
	got2, err := store.Get(1)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got2 != want2 {
		t.Errorf("after upsert, prefs = %+v, want %+v", got2, want2)
	}
}

// PUSH-010: Admin-category recipient queries remain role-scoped.
func TestUsersOptedIntoDefaultBehavior(t *testing.T) {
	database, err := dbOpen(t)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	// alice: no row (defaults). bob: opts out of new_movie. An admin with no
	// row is opted into request_pending by default; a regular user never is.
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (1, 'alice', '', 'user')")
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (2, 'bob', '', 'user')")
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (3, 'admin', '', 'admin')")
	mustExec(t, database, "INSERT INTO notification_prefs (user_id, new_movie) VALUES (2, 0)")

	store := NewPrefsStore(database)

	// new_movie on by default => alice + admin included, bob excluded.
	got, err := store.usersOptedInto(CategoryNewMovie)
	if err != nil {
		t.Fatalf("usersOptedInto(new_movie): %v", err)
	}
	if !equalIDs(got, []int64{1, 3}) {
		t.Errorf("new_movie opted-in = %v, want [1 3]", got)
	}

	// new_episode on by default and untouched => everyone included.
	got, err = store.usersOptedInto(CategoryNewEpisode)
	if err != nil {
		t.Fatalf("usersOptedInto(new_episode): %v", err)
	}
	if !equalIDs(got, []int64{1, 2, 3}) {
		t.Errorf("new_episode opted-in = %v, want [1 2 3]", got)
	}

	// request_pending: admin-only, on by default => just the admin.
	got, err = store.usersOptedInto(CategoryRequestPending)
	if err != nil {
		t.Fatalf("usersOptedInto(request_pending): %v", err)
	}
	if !equalIDs(got, []int64{3}) {
		t.Errorf("request_pending opted-in = %v, want [3]", got)
	}

	// request_decision: off by default => nobody without an explicit opt-in.
	got, err = store.usersOptedInto(CategoryRequestDecision)
	if err != nil {
		t.Fatalf("usersOptedInto(request_decision): %v", err)
	}
	if len(got) != 0 {
		t.Errorf("request_decision opted-in = %v, want none", got)
	}
}

func TestOptedInSingleUser(t *testing.T) {
	database, err := dbOpen(t)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (1, 'alice', '', 'user')")
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (2, 'bob', '', 'user')")
	mustExec(t, database, "INSERT INTO notification_prefs (user_id, request_decision) VALUES (1, 1)")

	store := NewPrefsStore(database)

	if !store.optedIn(1, CategoryRequestDecision) {
		t.Error("alice opted into request_decision, want true")
	}
	// bob has no row: request_decision defaults off.
	if store.optedIn(2, CategoryRequestDecision) {
		t.Error("bob has no row, request_decision defaults off, want false")
	}
	// new_movie is on by default for a user without a row.
	if !store.optedIn(2, CategoryNewMovie) {
		t.Error("bob has no row, new_movie defaults on, want true")
	}
	// Unknown category fails closed.
	if store.optedIn(1, "bogus") {
		t.Error("unknown category should be false")
	}
}

func TestUsersOptedIntoUnknownCategory(t *testing.T) {
	database, err := dbOpen(t)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	store := NewPrefsStore(database)
	if _, err := store.usersOptedInto("bogus"); err == nil {
		t.Error("expected error for unknown category")
	}
	// new_book must go through the instance-scoped query: an unscoped audience
	// would leak book alerts to users without any chaptarr grant.
	if _, err := store.usersOptedInto(CategoryNewBook); err == nil {
		t.Error("expected error for unscoped new_book recipient query")
	}
}

// A new_book audience is the intersection of the opt-in and the instance's
// visibility: admins always see every instance; a non-admin only via the
// per-user default row that doubles as the chaptarr access grant.
func TestUsersOptedIntoNewBookScopesToInstanceAccess(t *testing.T) {
	database, err := dbOpen(t)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	// admin(1): no grant row needed. alice(2): granted books-a. bob(3):
	// granted books-a but opted out. carol(4): granted sibling books-b.
	// dave(5): no chaptarr grant at all.
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (1, 'admin', '', 'admin')")
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (2, 'alice', '', 'user')")
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (3, 'bob', '', 'user')")
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (4, 'carol', '', 'user')")
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (5, 'dave', '', 'user')")
	mustExec(t, database, "INSERT INTO user_default_instances (user_id, service_type, instance_id) VALUES (2, 'chaptarr', 'books-a')")
	mustExec(t, database, "INSERT INTO user_default_instances (user_id, service_type, instance_id) VALUES (3, 'chaptarr', 'books-a')")
	mustExec(t, database, "INSERT INTO user_default_instances (user_id, service_type, instance_id) VALUES (4, 'chaptarr', 'books-b')")
	mustExec(t, database, "INSERT INTO notification_prefs (user_id, new_book) VALUES (3, 0)")

	store := NewPrefsStore(database)

	got, err := store.usersOptedIntoNewBook("books-a")
	if err != nil {
		t.Fatalf("usersOptedIntoNewBook(books-a): %v", err)
	}
	if !equalIDs(got, []int64{1, 2}) {
		t.Errorf("books-a audience = %v, want [1 2] (admin + granted alice)", got)
	}

	// The sibling instance reaches its own grantee (and admins), never books-a's.
	got, err = store.usersOptedIntoNewBook("books-b")
	if err != nil {
		t.Fatalf("usersOptedIntoNewBook(books-b): %v", err)
	}
	if !equalIDs(got, []int64{1, 4}) {
		t.Errorf("books-b audience = %v, want [1 4] (admin + granted carol)", got)
	}

	// An admin who opts out is excluded like anyone else.
	mustExec(t, database, "INSERT INTO notification_prefs (user_id, new_book) VALUES (1, 0)")
	got, err = store.usersOptedIntoNewBook("books-a")
	if err != nil {
		t.Fatalf("usersOptedIntoNewBook(books-a): %v", err)
	}
	if !equalIDs(got, []int64{2}) {
		t.Errorf("books-a audience after admin opt-out = %v, want [2]", got)
	}
}

// equalIDs compares two id slices order-independently.
func equalIDs(got, want []int64) bool {
	g := append([]int64(nil), got...)
	w := append([]int64(nil), want...)
	sort.Slice(g, func(i, j int) bool { return g[i] < g[j] })
	sort.Slice(w, func(i, j int) bool { return w[i] < w[j] })
	return reflect.DeepEqual(g, w)
}
