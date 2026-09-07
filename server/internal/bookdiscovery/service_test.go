package bookdiscovery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/windoze95/cantinarr-server/internal/chaptarr"
	"github.com/windoze95/cantinarr-server/internal/instance"
)

func testService(t *testing.T, fn http.HandlerFunc) *Service {
	t.Helper()
	srv := httptest.NewServer(fn)
	t.Cleanup(srv.Close)
	s := NewService()
	s.provider.base = srv.URL
	s.provider.interval = 0
	return s
}

func TestEnglishPresentationKeepsWorkIdentityAndFallsBack(t *testing.T) {
	for _, tc := range []struct {
		name, raw, title string
		cover            int64
	}{
		{"english", `{"key":"/works/OL1W","title":"Titre","author_name":[" Author "],"cover_i":10,"editions":{"docs":[{"key":"/books/OL2M","title":"English title","cover_i":20,"language":["eng"]}]}}`, "English title", 20},
		{"other language", `{"key":"/works/OL1W","title":"Titre","cover_i":10,"editions":{"docs":[{"title":"Other","cover_i":20,"language":["fre"]}]}}`, "Titre", 10},
		{"missing edition title", `{"key":"/works/OL1W","title":"Titre","cover_i":10,"editions":{"docs":[{"cover_i":-1,"language":["eng"]}]}}`, "Titre", 10},
		{"no editions", `{"key":"/works/OL1W","title":"Titre","cover_i":-1}`, "Titre", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var doc searchDoc
			if err := json.Unmarshal([]byte(tc.raw), &doc); err != nil {
				t.Fatal(err)
			}
			b, err := doc.book()
			if err != nil || b.ForeignID != "ol:OL1W" || b.Title != tc.title || b.CoverID != tc.cover || b.Authors == nil {
				t.Fatalf("normalization: %+v %v", b, err)
			}
		})
	}
}

func TestFeedQueriesPaginationIdentityAndCacheTTLs(t *testing.T) {
	var hits atomic.Int32
	s := testService(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		q := r.URL.Query()
		if q.Get("lang") != "en" || q.Get("limit") != "20" || q.Get("offset") != "20" || !strings.Contains(q.Get("fields"), "editions.language") {
			t.Errorf("params: %v", q)
		}
		if !strings.Contains(r.Header.Get("User-Agent"), "github.com/windoze95/cantinarr/issues") {
			t.Error("missing contact")
		}
		if q.Get("sort") == "readinglog" {
			if q.Get("q") != "readinglog_count:[1 TO *]" {
				t.Error("popularity query")
			}
		} else if q.Get("q") != `(subject:"biography" OR subject:"memoir")` {
			t.Errorf("genre query: %s", q.Get("q"))
		}
		w.Write([]byte(`{"start":20,"numFound":43,"docs":[{"key":"/works/OL1W","title":"Same"},{"key":"/works/OL1W","title":"Repeated"},{"key":"/works/OL2W","title":"Same"}]}`))
	})
	for _, feed := range []string{"popular", "genre"} {
		for range 2 {
			data, err := s.Feed(context.Background(), feed, "biography-memoir", 2)
			if err != nil {
				t.Fatal(err)
			}
			var page Page
			json.Unmarshal(data, &page)
			if page.Page != 2 || page.NextPage != 3 || page.Total != 43 || len(page.Results) != 2 || page.Results[0].Title != "Same" || page.Results[1].Title != "Same" {
				t.Fatalf("page %+v", page)
			}
		}
	}
	if hits.Load() != 2 {
		t.Fatalf("cache hits %d", hits.Load())
	}
	s.cache.mu.Lock()
	defer s.cache.mu.Unlock()
	for key, e := range s.cache.entries {
		want := time.Hour
		if strings.Contains(key, "genre") {
			want = 6 * time.Hour
		}
		if remaining := time.Until(e.expires); remaining < want-time.Minute || remaining > want {
			t.Errorf("ttl %s %v", key, remaining)
		}
	}
}

func TestMalformedResponsesNeverBecomeEmptyResults(t *testing.T) {
	for _, raw := range []string{`{}`, `null`, `{"start":0,"numFound":0,"docs":null}`, `{"start":0,"numFound":1,"docs":[]}`, `{"start":1,"numFound":1,"docs":[]}`, `{"start":0,"numFound":1,"docs":[{"key":"/works/OL1M","title":"Book"}]}`, `{"start":0,"numFound":1,"docs":[{"key":"/works/OL1W","title":""}]}`, `{"start":0,"numFound":1,"docs":[{"key":"/works/OL1W","title":"Book","editions":{"docs":"bad"}}]}`} {
		t.Run(raw, func(t *testing.T) {
			s := testService(t, func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(raw)) })
			if body, err := s.Feed(context.Background(), "popular", "", 1); err == nil {
				t.Fatalf("accepted %s", body)
			}
			if len(s.cache.entries) != 0 {
				t.Fatal("cached a failure")
			}
		})
	}
	s := testService(t, func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(`{"start":0,"numFound":0,"docs":[]}`)) })
	body, err := s.Feed(context.Background(), "popular", "", 1)
	if err != nil || !strings.Contains(string(body), "This does not search your library") {
		t.Fatalf("empty scope: %s %v", body, err)
	}
}

func TestDetailDescriptionAndIdentity(t *testing.T) {
	for _, description := range []string{`"Description"`, `{"type":"/type/text","value":"Description"}`} {
		s := testService(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/search.json" {
				if r.URL.Query().Get("q") != "key:/works/OL1W" {
					t.Error("not an ID query")
				}
				w.Write([]byte(`{"start":0,"numFound":1,"docs":[{"key":"/works/OL1W","title":"Book"}]}`))
			} else {
				fmt.Fprintf(w, `{"key":"/works/OL1W","description":%s}`, description)
			}
		})
		b, err := s.Book(context.Background(), "OL1W")
		if err != nil || !strings.Contains(string(b), `"description":"Description"`) {
			t.Fatalf("%s %v", b, err)
		}
		if time.Until(s.cache.entries["book:OL1W"].expires) < 23*time.Hour {
			t.Fatal("detail ttl")
		}
	}
	s := testService(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"start":0,"numFound":1,"docs":[{"key":"/works/OL2W","title":"Wrong book"}]}`))
	})
	if _, err := s.Book(context.Background(), "OL1W"); err == nil {
		t.Fatal("substituted another work")
	}
}

func TestCoalescingCancellationExpiryAndBounds(t *testing.T) {
	var m memo
	started, release := make(chan struct{}), make(chan struct{})
	var hits atomic.Int32
	load := func(ctx context.Context) ([]byte, error) {
		hits.Add(1)
		close(started)
		<-release
		return []byte("book"), ctx.Err()
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := m.get(ctx, "same", time.Hour, load); done <- err }()
	<-started
	cancel()
	if !errors.Is(<-done, context.Canceled) {
		t.Fatal("cancel did not return")
	}
	other := make(chan error, 1)
	go func() {
		b, err := m.get(context.Background(), "same", time.Hour, load)
		if string(b) != "book" {
			err = fmt.Errorf("wrong result")
		}
		other <- err
	}()
	close(release)
	if err := <-other; err != nil || hits.Load() != 1 {
		t.Fatalf("shared read canceled: %v", err)
	}
	m.mu.Lock()
	e := m.entries["same"]
	e.expires = time.Now().Add(-time.Second)
	m.entries["same"] = e
	m.mu.Unlock()
	fresh := func(context.Context) ([]byte, error) { return []byte("fresh"), nil }
	if b, _ := m.get(context.Background(), "same", time.Hour, fresh); string(b) != "fresh" {
		t.Fatal("expired cache read")
	}
	for i := range 280 {
		if _, err := m.get(context.Background(), fmt.Sprint(i), time.Hour, fresh); err != nil {
			t.Fatal(err)
		}
	}
	if len(m.entries) > 256 {
		t.Fatal("unbounded cache")
	}
}

func TestRateLimitRetriesAndFailure(t *testing.T) {
	var mu sync.Mutex
	var starts []time.Time
	s := testService(t, func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		starts = append(starts, time.Now())
		mu.Unlock()
		w.Write([]byte(`{}`))
	})
	if NewService().provider.interval != time.Second {
		t.Fatal("production rate limit")
	}
	s.provider.interval = 20 * time.Millisecond
	var wg sync.WaitGroup
	for range 5 {
		wg.Go(func() {
			var dst any
			if err := s.provider.get(context.Background(), "/test", &dst); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	for i := 1; i < len(starts); i++ {
		if starts[i].Sub(starts[i-1]) < 15*time.Millisecond {
			t.Fatal("requests started too fast")
		}
	}
	var hits atomic.Int32
	s = testService(t, func(w http.ResponseWriter, _ *http.Request) {
		if hits.Add(1) == 1 {
			w.WriteHeader(503)
		} else {
			w.Write([]byte(`{}`))
		}
	})
	var dst any
	if err := s.provider.get(context.Background(), "/test", &dst); err != nil || hits.Load() != 2 {
		t.Fatalf("retry: %v", err)
	}
	s = testService(t, func(w http.ResponseWriter, _ *http.Request) { w.Header().Set("Retry-After", "120"); w.WriteHeader(429) })
	if err := s.provider.get(context.Background(), "/test", &dst); err == nil || strings.Contains(err.Error(), s.provider.base) {
		t.Fatal("unbounded wait or leaked host")
	}
}

func TestCanonicalTargets(t *testing.T) {
	for _, tc := range []struct {
		name string
		rows []chaptarr.LookupResult
		want int
	}{
		{"exact", []chaptarr.LookupResult{{ForeignBookID: "ol:OL1W", Title: "Book"}}, 1},
		{"canonical alias", []chaptarr.LookupResult{{ForeignBookID: "gr:1", Title: "Different title"}}, 1},
		{"format siblings", []chaptarr.LookupResult{{ForeignBookID: "gr:1", Title: "Book"}, {ForeignBookID: "gr:1", Title: "Book"}}, 1},
		{"ambiguous verified", []chaptarr.LookupResult{{ForeignBookID: "gr:1", OpenLibraryWorkID: "ol:OL1W", Title: "Book"}, {ForeignBookID: "hc:2", OpenLibraryWorkID: "OL1W", Title: "Book"}}, 2},
		{"title alone", []chaptarr.LookupResult{{ForeignBookID: "gr:1", Title: "Book"}, {ForeignBookID: "gr:2", Title: "Book"}}, 0},
		{"contradictory", []chaptarr.LookupResult{{ForeignBookID: "gr:1", OpenLibraryWorkID: "OL2W", Title: "Book"}}, 0},
		{"missing", nil, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := requestTargets("OL1W", tc.rows)
			if err != nil || len(got.Candidates) != tc.want {
				t.Fatalf("%+v %v", got, err)
			}
			if tc.want == 0 && got.EmptyMessage == "" {
				t.Fatal("missing scope")
			}
		})
	}
}

func TestTargetConcurrencyCoalescingAndInstanceIsolation(t *testing.T) {
	var active, maximum, hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := active.Add(1)
		defer active.Add(-1)
		for old := maximum.Load(); n > old && !maximum.CompareAndSwap(old, n); old = maximum.Load() {
		}
		hits.Add(1)
		time.Sleep(20 * time.Millisecond)
		if r.Method != "GET" || r.URL.Path != "/api/v1/book/lookup" || !strings.HasPrefix(r.URL.Query().Get("term"), "ol:OL") {
			t.Error("not a read-only ID lookup")
		}
		fmt.Fprintf(w, `[{"foreignBookId":%q,"title":"Book"}]`, r.Header.Get("X-Api-Key"))
	}))
	t.Cleanup(srv.Close)
	s := NewService()
	var wg sync.WaitGroup
	for i := range 12 {
		wg.Go(func() {
			_, err := s.RequestTargets(context.Background(), &instance.Instance{ID: "one", URL: srv.URL, APIKey: "gr:1"}, fmt.Sprintf("OL%dW", i+1))
			if err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if maximum.Load() != 4 {
		t.Fatalf("max concurrency: %d", maximum.Load())
	}
	for _, id := range []string{"gr:1", "gr:2"} {
		b, err := s.RequestTargets(context.Background(), &instance.Instance{ID: id, URL: srv.URL, APIKey: id}, "OL1W")
		if err != nil || !strings.Contains(string(b), id) {
			t.Fatalf("instance mapping leaked %s %v", b, err)
		}
	}
	if len(s.targets.entries) != 0 {
		t.Fatal("stored a stale server mapping")
	}
}

func TestCatalogFailuresDoNotBecomeUnresolvedMatches(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"upstream failure", 503, `[]`},
		{"null", 200, `null`},
		{"truncated", 200, `[{"foreignBookId":"gr:1"}`},
		{"missing identity", 200, `[{"title":"Book"}]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			service := NewService()
			service.provider.base = srv.URL
			service.provider.interval = 0
			body, err := service.RequestTargets(context.Background(), &instance.Instance{ID: "one", URL: srv.URL}, "OL1W")
			if err != nil || !strings.Contains(string(body), `"state":"unavailable"`) || !strings.Contains(string(body), `"code":"catalog_unavailable"`) {
				t.Fatalf("failed catalog looked empty: %s %v", body, err)
			}
		})
	}
}
