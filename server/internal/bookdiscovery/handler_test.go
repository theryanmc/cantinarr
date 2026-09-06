package bookdiscovery

import (
	"bytes"
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/contentpolicy"
	"github.com/windoze95/cantinarr-server/internal/db"
	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/secrets"
)

type accessEnv struct {
	h           *Handler
	router      http.Handler
	db          *sql.DB
	a, b, wrong string
	hits        atomic.Int32
}

func newAccessEnv(t *testing.T) *accessEnv {
	t.Helper()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	for _, user := range []struct{ name, role string }{{"requester", "user"}, {"ungranted", "user"}, {"kid", "user"}, {"ungranted-kid", "user"}, {"admin", "admin"}} {
		if _, err := database.Exec("INSERT INTO users(username,password_hash,role) VALUES (?,'',?)", user.name, user.role); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []int64{3, 4} {
		if err := contentpolicy.NewStore(database).Set(id, contentpolicy.Policy{MaxMovieRating: "G", MaxTVRating: "TV-Y", RatingRegion: "US", BlockUnrated: true}); err != nil {
			t.Fatal(err)
		}
	}
	cipher, err := secrets.NewCipher(bytes.Repeat([]byte{42}, 32))
	if err != nil {
		t.Fatal(err)
	}
	store := instance.NewStore(database, cipher)
	create := func(kind, name string) string {
		inst := &instance.Instance{ServiceType: kind, Name: name, URL: "http://not-contacted.invalid", APIKey: "test"}
		if err := store.Create(inst); err != nil {
			t.Fatal(err)
		}
		return inst.ID
	}
	e := &accessEnv{db: database, a: create("chaptarr", "A"), b: create("chaptarr", "B"), wrong: create("radarr", "Movies")}
	for _, user := range []int64{1, 3} {
		if err := store.SetUserGrants(user, map[string][]string{"chaptarr": {e.a}}); err != nil {
			t.Fatal(err)
		}
	}
	e.h = NewHandler(store)
	e.h.service = testService(t, func(w http.ResponseWriter, r *http.Request) {
		e.hits.Add(1)
		if r.URL.Path == "/search.json" {
			w.Write([]byte(`{"start":0,"numFound":1,"docs":[{"key":"/works/OL1W","title":"Book"}]}`))
		} else {
			w.Write([]byte(`{"key":"/works/OL1W","description":"A book"}`))
		}
	})
	catalog := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"foreignBookId":"gr:1","title":"Book"}]`))
	}))
	t.Cleanup(catalog.Close)
	for _, id := range []string{e.a, e.b} {
		inst, _ := store.Get(id)
		inst.URL = catalog.URL
		if err := store.Update(inst); err != nil {
			t.Fatal(err)
		}
	}
	r := chi.NewRouter()
	r.Get("/api/discover/books/{feed}", e.h.Feed)
	r.Get("/api/media/book/{workId}/request-target", e.h.RequestTarget)
	r.Get("/api/media/book/{workId}", e.h.Book)
	r.Get("/api/genres/book", e.h.Genres)
	e.router = r
	return e
}

func (e *accessEnv) get(user int64, role, path, inst string) *httptest.ResponseRecorder {
	if inst != "" {
		sep := "?"
		if strings.Contains(path, "?") {
			sep = "&"
		}
		path += sep + "instance_id=" + inst
	}
	req := httptest.NewRequest("GET", path, nil)
	if user > 0 {
		req = req.WithContext(context.WithValue(req.Context(), auth.ClaimsKey, &auth.Claims{UserID: user, Role: role}))
	}
	w := httptest.NewRecorder()
	e.router.ServeHTTP(w, req)
	return w
}

func TestEveryBookEndpointChecksGrantsBeforeAndAfterWarmCache(t *testing.T) {
	e := newAccessEnv(t)
	paths := []string{"/api/discover/books/popular", "/api/media/book/OL1W", "/api/genres/book", "/api/media/book/OL1W/request-target"}
	for _, path := range paths {
		for _, tc := range []struct {
			user       int64
			role, inst string
			status     int
		}{
			{1, "user", e.a, 200}, {3, "user", e.a, 200}, {1, "user", "", 200},
			{2, "user", e.a, 403}, {4, "user", e.a, 403}, {2, "user", "", 403},
			{1, "user", e.b, 403}, {1, "user", e.wrong, 403}, {0, "", "", 401},
			{1, "unknown", e.a, 403}, {5, "admin", e.b, 200}, {5, "admin", "", 200},
			{5, "admin", e.wrong, 403}, {5, "admin", "missing", 403},
		} {
			w := e.get(tc.user, tc.role, path, tc.inst)
			if w.Code != tc.status {
				t.Fatalf("%s user %d instance %s: %d %s", path, tc.user, tc.inst, w.Code, w.Body)
			}
			if w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("private data can be shared cached")
			}
		}
	}
	// Metadata was shared between authorized accounts/instances; access wasn't.
	if e.hits.Load() != 3 {
		t.Fatalf("metadata/artwork cache wasn't shared: %d", e.hits.Load())
	}
	if err := e.h.store.SetUserGrants(1, map[string][]string{"chaptarr": nil}); err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		if w := e.get(1, "user", path, e.a); w.Code != 403 {
			t.Fatalf("warm cache bypass after revoke: %s %d", path, w.Code)
		}
		if w := e.get(3, "user", path, e.a); w.Code != 200 {
			t.Fatalf("other user's grant lost: %s %d", path, w.Code)
		}
	}
	if e.hits.Load() != 3 {
		t.Fatal("denied caller reached provider")
	}
}

func TestGrantRevocationWhileProviderIsLoading(t *testing.T) {
	e := newAccessEnv(t)
	started, release := make(chan struct{}), make(chan struct{})
	e.h.service = testService(t, func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		w.Write([]byte(`{"start":0,"numFound":1,"docs":[{"key":"/works/OL1W","title":"Book"}]}`))
	})
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- e.get(1, "user", "/api/discover/books/popular", e.a) }()
	<-started
	if err := e.h.store.SetUserGrants(1, map[string][]string{"chaptarr": nil}); err != nil {
		t.Fatal(err)
	}
	close(release)
	if w := <-done; w.Code != 403 || strings.Contains(w.Body.String(), "Book") {
		t.Fatalf("revoked grant received data: %d %s", w.Code, w.Body)
	}
}

func TestBookInputValidationDoesNotCallProviders(t *testing.T) {
	e := newAccessEnv(t)
	for _, path := range []string{
		"/api/discover/books/popular?page=0", "/api/discover/books/popular?page=999999999999999999999",
		"/api/discover/books/genre?genre=science%22%20OR%20*", "/api/discover/books/popular?genre=romance",
		"/api/discover/books/unknown", "/api/media/book/OL1M", "/api/media/book/ol:invalid/request-target",
	} {
		if w := e.get(1, "user", path, e.a); w.Code != 400 && w.Code != 404 {
			t.Fatalf("%s: %d", path, w.Code)
		}
	}
	if e.hits.Load() != 0 {
		t.Fatal("invalid query reached upstream")
	}
}

func TestGrantRevocationWhileRequestTargetIsLoading(t *testing.T) {
	e := newAccessEnv(t)
	started, release := make(chan struct{}), make(chan struct{})
	catalog := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		w.Write([]byte(`[{"foreignBookId":"gr:1","title":"Private title"}]`))
	}))
	t.Cleanup(catalog.Close)
	inst, err := e.h.store.Get(e.a)
	if err != nil {
		t.Fatal(err)
	}
	inst.URL = catalog.URL
	if err := e.h.store.Update(inst); err != nil {
		t.Fatal(err)
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- e.get(1, "user", "/api/media/book/OL1W/request-target", e.a) }()
	<-started
	err = e.h.store.SetUserGrants(1, map[string][]string{"chaptarr": nil})
	close(release)
	if err != nil {
		t.Fatal(err)
	}
	if w := <-done; w.Code != 403 || strings.Contains(w.Body.String(), "Private title") {
		t.Fatalf("revoked grant received target: %d %s", w.Code, w.Body)
	}
}
