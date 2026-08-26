package request

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/chaptarr"
)

func authorRecord(id int, foreignID, name string, images ...chaptarr.Image) chaptarr.Author {
	return chaptarr.Author{
		ID:              id,
		ForeignAuthorID: foreignID,
		AuthorName:      name,
		Images:          images,
	}
}

func authorBook(id, authorID int, foreignID, mediaType string, files int) chaptarr.Book {
	b := chaptarr.Book{
		ID:            id,
		AuthorID:      authorID,
		ForeignBookID: foreignID,
		Title:         foreignID,
		MediaType:     mediaType,
	}
	b.Statistics.BookFileCount = files
	return b
}

// TestBuildLibraryAuthorsCountsTitlesNotRecords is the reason this digest reads
// the books rather than Chaptarr's author statistics: a title owned as both an
// ebook and an audiobook is two records sharing one foreignBookId, and counting
// records tells the user they own twice the books they do.
func TestBuildLibraryAuthorsCountsTitlesNotRecords(t *testing.T) {
	authors := []chaptarr.Author{authorRecord(1, "fa-1", "Martha Wells")}
	books := []chaptarr.Book{
		authorBook(10, 1, "fb-1", "ebook", 1),
		authorBook(11, 1, "fb-1", "audiobook", 1),
		authorBook(12, 1, "fb-2", "ebook", 0),
	}

	got := buildLibraryAuthors(authors, books, 10)
	if len(got) != 1 {
		t.Fatalf("authors = %+v, want one", got)
	}
	if got[0].TitleCount != 2 {
		t.Errorf("TitleCount = %d, want 2 distinct titles from 3 records", got[0].TitleCount)
	}
	if got[0].AvailableCount != 1 {
		t.Errorf("AvailableCount = %d, want 1", got[0].AvailableCount)
	}
}

// TestBuildLibraryAuthorsDropsAuthorsWithNoBooks: a failed or pending metadata
// import leaves an author record with nothing behind it. A card that opens onto
// an empty page is worse than no card.
func TestBuildLibraryAuthorsDropsAuthorsWithNoBooks(t *testing.T) {
	authors := []chaptarr.Author{
		authorRecord(1, "fa-1", "Has Books"),
		authorRecord(2, "fa-2", "Import Never Finished"),
	}
	books := []chaptarr.Book{authorBook(10, 1, "fb-1", "ebook", 1)}

	got := buildLibraryAuthors(authors, books, 10)
	if len(got) != 1 || got[0].Name != "Has Books" {
		t.Fatalf("authors = %+v, want only the author with books", got)
	}
}

// TestBuildLibraryAuthorsOrdersByWhatIsOnDisk keeps the browse row's lead cards
// on the authors the user actually has, and makes the order stable: the same
// unchanged library must not reshuffle between fetches.
func TestBuildLibraryAuthorsOrdersByWhatIsOnDisk(t *testing.T) {
	authors := []chaptarr.Author{
		authorRecord(1, "fa-1", "Zed Owns One"),
		authorRecord(2, "fa-2", "Adams Owns Two"),
		authorRecord(3, "fa-3", "Bell Owns None"),
		authorRecord(4, "fa-4", "Ames Owns None"),
	}
	books := []chaptarr.Book{
		authorBook(10, 1, "fb-1", "ebook", 1),
		authorBook(11, 2, "fb-2", "ebook", 1),
		authorBook(12, 2, "fb-3", "ebook", 1),
		authorBook(13, 3, "fb-4", "ebook", 0),
		authorBook(14, 4, "fb-5", "ebook", 0),
	}

	want := []string{"Adams Owns Two", "Zed Owns One", "Ames Owns None", "Bell Owns None"}
	for run := 0; run < 2; run++ {
		got := buildLibraryAuthors(authors, books, 10)
		if len(got) != len(want) {
			t.Fatalf("authors = %+v, want %d", got, len(want))
		}
		for i, name := range want {
			if got[i].Name != name {
				t.Fatalf("order = %v, want %v", authorNames(got), want)
			}
		}
	}
}

func authorNames(items []LibraryAuthor) []string {
	names := make([]string, 0, len(items))
	for _, a := range items {
		names = append(names, a.Name)
	}
	return names
}

func TestBuildLibraryAuthorsRespectsLimit(t *testing.T) {
	var authors []chaptarr.Author
	var books []chaptarr.Book
	for i := 1; i <= 12; i++ {
		authors = append(authors, authorRecord(i, "fa", "Author"))
		books = append(books, authorBook(100+i, i, "fb", "ebook", 1))
	}

	if got := buildLibraryAuthors(authors, books, 5); len(got) != 5 {
		t.Errorf("len = %d, want 5", len(got))
	}
}

// TestClientReachableAuthorImageNeverLeaksAnArrOrigin: clients must never
// dereference an arr-origin URL, so an absolute arr address falls back to the
// metadata CDN copy and otherwise yields nothing to load.
func TestClientReachableAuthorImageNeverLeaksAnArrOrigin(t *testing.T) {
	cases := []struct {
		name   string
		images []chaptarr.Image
		want   string
	}{
		{
			name: "prefers the poster over other cover types",
			images: []chaptarr.Image{
				{CoverType: "fanart", URL: "/MediaCover/Authors/1/fanart.jpg"},
				{CoverType: "poster", URL: "/MediaCover/Authors/1/poster.jpg"},
			},
			want: "/MediaCover/Authors/1/poster.jpg",
		},
		{
			name:   "falls back to any usable image",
			images: []chaptarr.Image{{CoverType: "fanart", URL: "/MediaCover/Authors/1/fanart.jpg"}},
			want:   "/MediaCover/Authors/1/fanart.jpg",
		},
		{
			name: "an arr-origin absolute url degrades to the metadata cdn copy",
			images: []chaptarr.Image{{
				CoverType: "poster",
				URL:       "http://chaptarr:8787/MediaCover/Authors/1/poster.jpg",
				RemoteURL: "https://images.example.com/poster.jpg",
			}},
			want: "https://images.example.com/poster.jpg",
		},
		{
			name:   "an arr-origin absolute url with no cdn copy yields nothing",
			images: []chaptarr.Image{{CoverType: "poster", URL: "http://chaptarr:8787/MediaCover/Authors/1/poster.jpg"}},
			want:   "",
		},
		{name: "no images", images: nil, want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := clientReachableAuthorImage(authorRecord(1, "fa-1", "Author", tc.images...))
			if got != tc.want {
				t.Errorf("image = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestSortAuthorTitlesPutsUndatedRecordsLast: a record with no release date has
// no year, and sorting it as year zero would be fine — but sorting it as the
// newest would put unreleased-looking noise at the top of every bibliography.
func TestSortAuthorTitlesPutsUndatedRecordsLast(t *testing.T) {
	titles := []LibraryTitle{
		{Title: "Undated", Year: 0},
		{Title: "Older", Year: 2019},
		{Title: "Newer", Year: 2024},
		{Title: "Also 2024", Year: 2024},
	}

	sortAuthorTitles(titles)

	want := []string{"Also 2024", "Newer", "Older", "Undated"}
	for i, name := range want {
		if titles[i].Title != name {
			t.Fatalf("order = %+v, want %v", titles, want)
		}
	}
}

// TestGetLibraryAuthorsWithoutChaptarrGrantReturnsEmpty: a user with no
// Chaptarr access has no row, which is not a failure.
func TestGetLibraryAuthorsWithoutChaptarrGrantReturnsEmpty(t *testing.T) {
	// A nil registry is how resolveChaptarr reports "no client for this user".
	svc := &Service{}

	digest, err := svc.GetLibraryAuthorsForInstance(1, "")
	if err != nil {
		t.Fatalf("err = %v, want nil for an ungranted user", err)
	}
	if digest == nil || digest.Authors == nil {
		t.Fatal("digest must carry an empty list, never nil")
	}
	if len(digest.Authors) != 0 {
		t.Errorf("authors = %+v, want none", digest.Authors)
	}
}

// TestGetLibraryAuthorDetailWithoutChaptarrGrantIsForbiddenNotMissing keeps the
// two empty answers apart: "this library has no such author" and "you cannot
// see this library at all" are different facts, and returning 404 for the
// second would tell a user the library was searched when it never was.
func TestGetLibraryAuthorDetailWithoutChaptarrGrantIsForbiddenNotMissing(t *testing.T) {
	svc := &Service{}

	_, err := svc.GetLibraryAuthorDetailForInstance(1, "fa-1", "")
	if err == nil {
		t.Fatal("err = nil, want a forbidden error")
	}
	if requestErrorStatus(err) != http.StatusForbidden {
		t.Errorf("status = %d, want 403", requestErrorStatus(err))
	}
}

// TestBookAuthorNotFoundMapsTo404 keeps a missing author out of the 500 bucket.
func TestBookAuthorNotFoundMapsTo404(t *testing.T) {
	if got := requestErrorStatus(ErrBookAuthorNotFound); got != http.StatusNotFound {
		t.Errorf("status = %d, want 404", got)
	}
}

// TestGetBookAuthorsReturnsAnArray keeps the JSON body an array, not null, so
// the app's list decode never has to special-case it.
func TestGetBookAuthorsReturnsAnArray(t *testing.T) {
	handler := NewHandler(&Service{})
	req := httptest.NewRequest(http.MethodGet, "/api/requests/book-authors", nil)
	req = req.WithContext(context.WithValue(req.Context(), auth.ClaimsKey,
		&auth.Claims{UserID: 1, Role: auth.RoleUser}))
	resp := httptest.NewRecorder()

	handler.GetBookAuthors(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", resp.Code, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), `"authors":[]`) {
		t.Errorf("body = %s, want an empty authors array", resp.Body.String())
	}
}

// TestGetBookAuthorRequiresAForeignID: without one there is no author to look
// up, and a 200 would imply the library was searched.
func TestGetBookAuthorRequiresAForeignID(t *testing.T) {
	handler := NewHandler(&Service{})
	req := httptest.NewRequest(http.MethodGet, "/api/requests/book-author?foreign_id=%20", nil)
	req = req.WithContext(context.WithValue(req.Context(), auth.ClaimsKey,
		&auth.Claims{UserID: 1, Role: auth.RoleUser}))
	resp := httptest.NewRecorder()

	handler.GetBookAuthor(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", resp.Code, resp.Body.String())
	}
}

// TestBookAuthorEndpointsRequireAuthentication guards the claims checks.
func TestBookAuthorEndpointsRequireAuthentication(t *testing.T) {
	for name, call := range map[string]func(http.ResponseWriter, *http.Request){
		"book-authors": NewHandler(nil).GetBookAuthors,
		"book-author":  NewHandler(nil).GetBookAuthor,
	} {
		t.Run(name, func(t *testing.T) {
			resp := httptest.NewRecorder()
			call(resp, httptest.NewRequest(http.MethodGet, "/api/requests/"+name, nil))
			if resp.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", resp.Code)
			}
		})
	}
}

// TestReduceLibraryKeepsTheYearWhenRecordsCarryNoAuthorObject is the defect a
// real library found: Chaptarr's per-author book list omits the embedded author
// object entirely (includeAuthor=true does not change that), so a reduction that
// only reads the year off an author-carrying record leaves every title undated —
// which silently turns the author page's newest-first order into alphabetical.
func TestReduceLibraryKeepsTheYearWhenRecordsCarryNoAuthorObject(t *testing.T) {
	released := time.Date(2010, 1, 18, 17, 0, 0, 0, time.UTC)
	book := authorBook(10, 1, "fb-1", "ebook", 1)
	book.Title = "Unseen Academicals"
	book.Author = nil // exactly what /book?authorId= returns
	book.ReleaseDate = &released

	digest := reduceLibrary([]chaptarr.Book{book})

	if len(digest.Titles) != 1 {
		t.Fatalf("titles = %+v, want one", digest.Titles)
	}
	if got := digest.Titles[0].Year; got != 2010 {
		t.Errorf("Year = %d, want 2010 from the record's own releaseDate", got)
	}
}

// TestReduceLibraryPrefersTheFirstDatedRecordInAGroup keeps the year stable when
// a title's two format records disagree, matching how the reduction already
// takes the first record that carries metadata.
func TestReduceLibraryPrefersTheFirstDatedRecordInAGroup(t *testing.T) {
	first := time.Date(2019, 6, 1, 0, 0, 0, 0, time.UTC)
	second := time.Date(2021, 6, 1, 0, 0, 0, 0, time.UTC)
	undated := authorBook(10, 1, "fb-1", "ebook", 1)
	ebook := authorBook(11, 1, "fb-1", "ebook", 1)
	ebook.ReleaseDate = &first
	audiobook := authorBook(12, 1, "fb-1", "audiobook", 1)
	audiobook.ReleaseDate = &second

	digest := reduceLibrary([]chaptarr.Book{undated, ebook, audiobook})

	if got := digest.Titles[0].Year; got != 2019 {
		t.Errorf("Year = %d, want the first dated record's 2019", got)
	}
}

// TestStampAuthorNameFillsOnlyWhatTheReductionLeftBlank: the author page looked
// this author up by id, so a blank author on its own titles is noise, not truth.
// A name the reduction did resolve is never overwritten.
func TestStampAuthorNameFillsOnlyWhatTheReductionLeftBlank(t *testing.T) {
	titles := []LibraryTitle{
		{Title: "Unseen Academicals"},
		{Title: "Good Omens", Author: "Terry Pratchett & Neil Gaiman"},
	}

	stampAuthorName(titles, "  Terry Pratchett  ")

	if titles[0].Author != "Terry Pratchett" {
		t.Errorf("Author = %q, want the stamped name", titles[0].Author)
	}
	if titles[1].Author != "Terry Pratchett & Neil Gaiman" {
		t.Errorf("Author = %q, want the resolved name kept", titles[1].Author)
	}
}

// TestSortAuthorTitlesOrdersRealDatesNewestFirst pins the ordering the year fix
// restores: without a year every title falls into the undated bucket and the
// page silently reads alphabetical.
func TestSortAuthorTitlesOrdersRealDatesNewestFirst(t *testing.T) {
	titles := []LibraryTitle{
		{Title: "A Blink of the Screen", Year: 2012},
		{Title: "Zebra Ending", Year: 2021},
		{Title: "A Hat Full of Sky", Year: 2004},
	}

	sortAuthorTitles(titles)

	want := []string{"Zebra Ending", "A Blink of the Screen", "A Hat Full of Sky"}
	for i, name := range want {
		if titles[i].Title != name {
			t.Fatalf("order = %+v, want %v", titles, want)
		}
	}
}
