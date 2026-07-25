package remediation

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/windoze95/cantinarr-server/internal/arr"
	"github.com/windoze95/cantinarr-server/internal/chaptarr"
	"github.com/windoze95/cantinarr-server/internal/db"
	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/secrets"
)

// bookFileState drives the fake Chaptarr's bookfile + import-history responses
// so tests can flip the exact-file witness between "missing" and "imported".
type bookFileState struct {
	fileID           int // 0 = no file on disk
	importDownloadID string
	importDate       time.Time
}

func setupBookObservationService(t *testing.T, state *bookFileState) (*Service, *fakeNotifier) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/queue":
			fmt.Fprint(w, `{"totalRecords":0,"records":[]}`)
		case "/api/v1/bookfile":
			if r.URL.Query().Get("bookId") != "123" {
				fmt.Fprint(w, `[]`)
				return
			}
			if state.fileID == 0 {
				fmt.Fprint(w, `[]`)
			} else {
				fmt.Fprintf(w, `[{"id":%d,"authorId":456,"bookId":123}]`, state.fileID)
			}
		case "/api/v1/history":
			if state.importDownloadID == "" {
				fmt.Fprint(w, `{"page":1,"pageSize":20,"totalRecords":0,"records":[]}`)
			} else {
				// Readarr-lineage forks emit "bookFileImported" (their event 3) for a
				// completed import — never Radarr/Sonarr's "downloadFolderImported".
				fmt.Fprintf(w, `{"page":1,"pageSize":20,"totalRecords":1,"records":[{"id":88,"eventType":"bookFileImported","downloadId":%q,"date":%q,"authorId":456,"bookId":123,"book":{"id":123,"title":"Example Book"}}]}`,
					state.importDownloadID, state.importDate.Format(time.RFC3339))
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	cipher, _ := secrets.NewCipher(bytes.Repeat([]byte{0x37}, 32))
	store := instance.NewStore(database, cipher)
	if err := store.Create(&instance.Instance{
		ID: "chaptarr-observe", ServiceType: "chaptarr", Name: "Books", URL: server.URL, APIKey: "key",
	}); err != nil {
		t.Fatal(err)
	}
	notifier := &fakeNotifier{}
	return NewService(database, instance.NewRegistry(store), nil, notifier), notifier
}

func observedBookProblem(downloadID string, queueID int) arr.QueueObservation {
	signal := arr.QueueSignal{
		TrackedDownloadStatus: "error", TrackedDownloadState: "importPending",
		ErrorMessage: "The download is stalled with no connections", Size: 100, SizeLeft: 100,
	}
	return arr.QueueObservation{
		DownloadID: downloadID,
		Media:      arr.QueueMediaContext{QueueID: queueID, Title: "Example Book", AuthorID: 456, BookID: 123},
		Signal:     signal, Diagnosis: arr.Diagnose(signal),
	}
}

// A chaptarr snapshot is a first-class observation source: a problem queue row
// creates one book issue that carries the durable Chaptarr author/book ids.
func TestChaptarrSnapshotCreatesBookIssueWithDurableIdentity(t *testing.T) {
	svc, _ := setupBookObservationService(t, &bookFileState{})
	enableAutoDispatch(t, svc, 5)

	base := time.Date(2026, 7, 10, 10, 0, 0, 0, time.UTC)
	if err := svc.observeQueueSnapshot("chaptarr", "chaptarr-observe", []arr.QueueObservation{observedBookProblem("download-1", 9)}, base); err != nil {
		t.Fatal(err)
	}
	issues, err := svc.ListIssues("")
	if err != nil || len(issues) != 1 {
		t.Fatalf("issues=%+v err=%v", issues, err)
	}
	issue, err := svc.GetIssue(issues[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if issue.MediaType != "book" || issue.AuthorID != 456 || issue.BookID != 123 ||
		issue.ArrQueueID != 9 || issue.DownloadID != "download-1" || issue.Status != IssueObserving {
		t.Fatalf("book issue identity = %+v", issue)
	}

	// The same incident on the next poll reconciles instead of duplicating.
	if err := svc.observeQueueSnapshot("chaptarr", "chaptarr-observe", []arr.QueueObservation{observedBookProblem("download-1", 9)}, base.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if again, err := svc.ListIssues(""); err != nil || len(again) != 1 {
		t.Fatalf("post-reconcile issues=%+v err=%v", again, err)
	}
}

// A stalled book download that Chaptarr later imports itself closes with the
// exact-file + import-receipt proof, phrased for books.
func TestBookRecoveryWitnessClosesAutoIssue(t *testing.T) {
	state := &bookFileState{}
	svc, _ := setupBookObservationService(t, state)
	enableAutoDispatch(t, svc, 5)

	base := time.Date(2026, 7, 10, 10, 0, 0, 0, time.UTC)
	if err := svc.observeQueueSnapshot("chaptarr", "chaptarr-observe", []arr.QueueObservation{observedBookProblem("download-1", 9)}, base); err != nil {
		t.Fatal(err)
	}
	// The book imports: file appears, the import history binds download-1 to
	// book 123, and the queue row departs.
	state.fileID = 5
	state.importDownloadID = "download-1"
	state.importDate = base.Add(30 * time.Second)
	if err := svc.observeQueueSnapshot("chaptarr", "chaptarr-observe", nil, base.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := svc.observeQueueSnapshot("chaptarr", "chaptarr-observe", nil, base.Add(11*time.Minute)); err != nil {
		t.Fatal(err)
	}
	issues, err := svc.ListIssues("")
	if err != nil || len(issues) != 1 {
		t.Fatalf("issues=%+v err=%v", issues, err)
	}
	issue := issues[0]
	if issue.Status != IssueResolved || issue.ResolutionKind != ResolutionArrStateCleared {
		t.Fatalf("book recovery close = %+v", issue)
	}
	if issue.Resolution != arrStateClearedBookResolution {
		t.Fatalf("book resolution text = %q", issue.Resolution)
	}
}

// A receipt dated before this download's attempt is a stale import of a reused
// download id, not proof: without exact-file binding the witness requires the
// record to postdate the incident, so the issue escalates instead of closing.
func TestBookRecoveryRejectsStaleReceipt(t *testing.T) {
	state := &bookFileState{}
	svc, _ := setupBookObservationService(t, state)
	enableAutoDispatch(t, svc, 5)

	base := time.Date(2026, 7, 10, 10, 0, 0, 0, time.UTC)
	if err := svc.observeQueueSnapshot("chaptarr", "chaptarr-observe", []arr.QueueObservation{observedBookProblem("download-1", 9)}, base); err != nil {
		t.Fatal(err)
	}
	state.fileID = 5
	state.importDownloadID = "download-1"
	state.importDate = base.Add(-24 * time.Hour) // months/hours before this incident's attempt
	if err := svc.observeQueueSnapshot("chaptarr", "chaptarr-observe", nil, base.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := svc.observeQueueSnapshot("chaptarr", "chaptarr-observe", nil, base.Add(11*time.Minute)); err != nil {
		t.Fatal(err)
	}
	issues, err := svc.ListIssues("")
	if err != nil || len(issues) != 1 {
		t.Fatalf("issues=%+v err=%v", issues, err)
	}
	if issues[0].Status != IssueNeedsAdmin {
		t.Fatalf("stale-receipt book recovery status = %+v", issues[0])
	}
}

// A fresh receipt cannot close a book issue while its queue row still signals a
// problem (a partially imported multi-file audiobook): the receipt binds only
// download + book record, not the exact file, so the incident promotes for
// attention instead of resolving over a live problem.
func TestBookPartialImportDoesNotCloseWhileQueueRowSignals(t *testing.T) {
	state := &bookFileState{}
	svc, _ := setupBookObservationService(t, state)
	enableAutoDispatch(t, svc, 5)

	base := time.Date(2026, 7, 10, 10, 0, 0, 0, time.UTC)
	item := observedBookProblem("download-1", 9)
	if err := svc.observeQueueSnapshot("chaptarr", "chaptarr-observe", []arr.QueueObservation{item}, base); err != nil {
		t.Fatal(err)
	}
	// One part imports (file + receipt appear) but the SAME problem row stays in
	// the queue, unchanged, past the min + quiet windows.
	state.fileID = 5
	state.importDownloadID = "download-1"
	state.importDate = base.Add(30 * time.Second)
	if err := svc.observeQueueSnapshot("chaptarr", "chaptarr-observe", []arr.QueueObservation{item}, base.Add(11*time.Minute)); err != nil {
		t.Fatal(err)
	}
	issues, err := svc.ListIssues("")
	if err != nil || len(issues) != 1 {
		t.Fatalf("issues=%+v err=%v", issues, err)
	}
	if issues[0].Status != IssueOpen {
		t.Fatalf("partial-import book status = %+v, want promoted open (not resolved)", issues[0])
	}
	if issues[0].ClosedAt != nil {
		t.Fatalf("partial-import book issue closed: %+v", issues[0])
	}
}

// Without an import receipt the departed queue row is not proof: the incident
// escalates for a human instead of closing on file presence alone.
func TestBookRecoveryWithoutReceiptEscalates(t *testing.T) {
	state := &bookFileState{}
	svc, _ := setupBookObservationService(t, state)
	enableAutoDispatch(t, svc, 5)

	base := time.Date(2026, 7, 10, 10, 0, 0, 0, time.UTC)
	if err := svc.observeQueueSnapshot("chaptarr", "chaptarr-observe", []arr.QueueObservation{observedBookProblem("download-1", 9)}, base); err != nil {
		t.Fatal(err)
	}
	state.fileID = 5 // file appeared, but history has no matching import receipt
	if err := svc.observeQueueSnapshot("chaptarr", "chaptarr-observe", nil, base.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := svc.observeQueueSnapshot("chaptarr", "chaptarr-observe", nil, base.Add(11*time.Minute)); err != nil {
		t.Fatal(err)
	}
	issues, err := svc.ListIssues("")
	if err != nil || len(issues) != 1 {
		t.Fatalf("issues=%+v err=%v", issues, err)
	}
	if issues[0].Status != IssueNeedsAdmin {
		t.Fatalf("unproven book recovery status = %+v", issues[0])
	}
}

func TestMediaScopeMatchesBookRequiresExactBookRecord(t *testing.T) {
	want := arr.QueueMediaContext{AuthorID: 456, BookID: 123}
	if !mediaScopeMatches(want, arr.QueueMediaContext{BookID: 123}, "book") {
		t.Fatal("exact book record did not match")
	}
	if mediaScopeMatches(want, arr.QueueMediaContext{BookID: 999}, "book") {
		t.Fatal("different book record matched")
	}
	if mediaScopeMatches(want, arr.QueueMediaContext{AuthorID: 456}, "book") {
		t.Fatal("book without record id matched on author alone")
	}
	if mediaScopeMatches(arr.QueueMediaContext{}, arr.QueueMediaContext{BookID: 123}, "book") {
		t.Fatal("identity-less want matched")
	}
}

func TestIncidentScopeKeyBookUsesBookRecordIdentity(t *testing.T) {
	media := arr.QueueMediaContext{BookID: 123, AuthorID: 456}
	first := incidentScopeKey("chaptarr-observe", "chaptarr", "download-1", media)
	replacement := incidentScopeKey("chaptarr-observe", "book", "download-2", media)
	if first == "" || first != replacement {
		t.Fatalf("book incident scope not stable across downloads: %q vs %q", first, replacement)
	}
	fallback := incidentScopeKey("chaptarr-observe", "chaptarr", "download-3", arr.QueueMediaContext{})
	if fallback == "" || fallback == first {
		t.Fatalf("identity-less book row did not fall back to download scope: %q", fallback)
	}
}

func TestChaptarrObservationMapsBookIdentity(t *testing.T) {
	added := time.Date(2026, 7, 10, 9, 0, 0, 0, time.UTC)
	item := chaptarr.DetailedQueueItem{
		ID: 9, Title: "queue title", Added: &added, DownloadID: "download-1",
		Status: "warning", TrackedDownloadStatus: "warning", TrackedDownloadState: "importPending",
		Size: 100, Sizeleft: 100,
		Book:   &chaptarr.BookContext{ID: 123, Title: "Example Book"},
		Author: &chaptarr.AuthorContext{ID: 456, AuthorName: "Author"},
	}
	obs := chaptarrObservation(item)
	if obs.Media.BookID != 123 || obs.Media.AuthorID != 456 || obs.Media.Title != "Example Book" ||
		obs.Media.QueueID != 9 || obs.DownloadID != "download-1" {
		t.Fatalf("chaptarr observation media = %+v", obs.Media)
	}
	if obs.AddedAt == nil || !obs.AddedAt.Equal(added) {
		t.Fatalf("chaptarr observation added = %v", obs.AddedAt)
	}
	if obs.FileIDAtSnapshot != nil {
		t.Fatalf("chaptarr observation file id should be unknown, got %v", *obs.FileIDAtSnapshot)
	}
}

func TestUserBookScopeKeyLeavesMovieKeysUnchanged(t *testing.T) {
	movie := userIncidentScopeKey("radarr-1", "movie", arr.QueueMediaContext{TmdbID: 42})
	movieAgain := userIncidentScopeKey("radarr-1", "movie", arr.QueueMediaContext{TmdbID: 42, BookID: 0})
	if movie != movieAgain {
		t.Fatal("movie scope key changed with zero book identity")
	}
	book := userIncidentScopeKey("chaptarr-1", "book", arr.QueueMediaContext{BookID: 123})
	other := userIncidentScopeKey("chaptarr-1", "book", arr.QueueMediaContext{BookID: 124})
	if book == other {
		t.Fatal("distinct book records share a user scope key")
	}
}

// decode guard: the JSON shapes used by the fake above must match the client's.
func TestFakeChaptarrShapesDecode(t *testing.T) {
	var page chaptarr.HistoryPage
	if err := json.Unmarshal([]byte(`{"page":1,"pageSize":20,"totalRecords":1,"records":[{"id":88,"eventType":"bookFileImported","downloadId":"download-1","authorId":456,"bookId":123}]}`), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Records) != 1 || page.Records[0].DownloadID != "download-1" || page.Records[0].BookID != 123 {
		t.Fatalf("history decode = %+v", page.Records)
	}
}
