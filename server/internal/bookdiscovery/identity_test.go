package bookdiscovery

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/windoze95/cantinarr-server/internal/instance"
)

func TestISBNChecksumsAndNormalization(t *testing.T) {
	for raw, want := range map[string]string{"0-06-245771-3": "9780062457714", "9780062457714": "9780062457714", "0062641549": "9780062641540", "123456789X": "9781234567897", "9780062457715": "", "97800624577a4": "", "": ""} {
		if got := NormalizeISBN(raw); got != want {
			t.Errorf("NormalizeISBN(%q)=%q, want %q", raw, got, want)
		}
	}
}

func catalogFixture(t *testing.T) *Service {
	t.Helper()
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/search.json":
			fmt.Fprint(w, `{"numFound":1,"start":0,"docs":[{"key":"/works/OL1W","title":"The Subtle Art","author_name":["Mark Manson"]}]}`)
		case "/works/OL1W.json":
			fmt.Fprint(w, `{"key":"/works/OL1W","description":"A book"}`)
		case "/works/OL1W/editions.json":
			fmt.Fprint(w, `{"size":1,"entries":[{"key":"/books/OL2M","title":"The Subtle Art","isbn_10":["0062457713"],"works":[{"key":"/works/OL1W"}],"languages":[{"key":"/languages/eng"}]}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(provider.Close)
	s := NewService()
	s.provider.base = provider.URL
	s.provider.interval = 0
	return s
}

func TestISBNFallbackAfterWorkLookupOutage(t *testing.T) {
	s := catalogFixture(t)
	arr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/book" {
			fmt.Fprint(w, `[]`)
			return
		}
		term := r.URL.Query().Get("term")
		if term == "ol:OL1W" {
			w.Header().Set("Retry-After", "600")
			w.WriteHeader(503)
			return
		}
		if strings.Contains(term, "9780062457714") {
			fmt.Fprint(w, `[{"foreignBookId":"hc:42","title":"The Subtle Art","authorName":"Mark Manson","editions":[{"isbn13":"9780062457714"}]}]`)
			return
		}
		fmt.Fprint(w, `[]`)
	}))
	defer arr.Close()
	out, err := s.Resolve(context.Background(), &instance.Instance{ID: "a", URL: arr.URL}, "OL1W")
	if err != nil || out.State != "matched" || len(out.Candidates) != 1 || out.Candidates[0].ForeignID != "hc:42" || out.Candidates[0].Evidence != "isbn" {
		t.Fatalf("ISBN fallback: %+v %v", out, err)
	}
}

func TestTextMatchesNeedConfirmationAndContradictoryIDsAreRejected(t *testing.T) {
	s := catalogFixture(t)
	arr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Query().Get("term"), "The Subtle Art") {
			fmt.Fprint(w, `[{"foreignBookId":"hc:other","title":"The Subtle Art","authorName":"Someone Else"},{"foreignBookId":"hc:wrong-work","title":"The Subtle Art","openLibraryWorkId":"OL9W","editions":[{"isbn13":"9780062457714"}]}]`)
			return
		}
		fmt.Fprint(w, `[]`)
	}))
	defer arr.Close()
	out, err := s.Resolve(context.Background(), &instance.Instance{ID: "a", URL: arr.URL}, "OL1W")
	if err != nil || out.State != "needs_match" || len(out.Candidates) != 0 || len(out.Suggestions) != 1 || out.Suggestions[0].ForeignID != "hc:other" {
		t.Fatalf("unverified title became identity: %+v %v", out, err)
	}
}

func TestPublicSearchDoesNotCollapseDistinctWorks(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"numFound":2,"start":0,"docs":[{"key":"/works/OL1W","title":"Same","author_name":["A"]},{"key":"/works/OL2W","title":"Same","author_name":["A"]}]}`)
	}))
	defer provider.Close()
	s := NewService()
	s.provider.base = provider.URL
	s.provider.interval = 0
	body, err := s.Search(context.Background(), "Same", 1)
	if err != nil || !strings.Contains(string(body), "ol:OL1W") || !strings.Contains(string(body), "ol:OL2W") {
		t.Fatalf("distinct works lost: %s %v", body, err)
	}
}

var liveCatalog = flag.Bool("books-live", false, "verify public book search and edition evidence against Open Library")

func TestLiveSearchAndEditionEvidence(t *testing.T) {
	if !*liveCatalog {
		t.Skip("pass -books-live for public provider verification")
	}
	s := NewService()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	body, err := s.Search(ctx, "The Subtle Art of Not Giving a Fuck", 1)
	if err != nil {
		t.Fatal(err)
	}
	var page Page
	if json.Unmarshal(body, &page) != nil || len(page.Results) == 0 {
		t.Fatal("public search returned no usable books")
	}
	book, err := s.Identity(ctx, "OL17590212W")
	if err != nil {
		t.Fatal(err)
	}
	isbns := 0
	for _, edition := range book.Editions {
		isbns += len(edition.ISBNs)
	}
	if isbns == 0 {
		t.Fatal("screenshot work has no validated edition ISBNs")
	}
	t.Logf("Open Library search: %d books; screenshot work: %d editions, %d validated ISBN references", len(page.Results), len(book.Editions), isbns)
}
