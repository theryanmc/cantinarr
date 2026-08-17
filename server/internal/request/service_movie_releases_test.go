package request

import (
	"testing"
	"time"
)

// TestMovieStatusCarriesReleaseDates pins the release dates a movie's status
// response hands the detail screen: they come from the Radarr record the status
// call already reads, so a title that reads "Requested" can say it simply isn't
// out yet instead of looking like a stalled download.
func TestMovieStatusCarriesReleaseDates(t *testing.T) {
	f := &fakeRadarr{
		libraryJSON: `[{"id":1,"title":"Dune: Part Three","tmdbId":550,"year":2026,
			"hasFile":false,"monitored":true,
			"inCinemas":"2026-07-03T00:00:00Z","digitalRelease":"2026-09-12T00:00:00Z"}]`,
	}
	srv := newFakeRadarrServer(t, f)
	s, uid := newHistoryTestService(t, srv.URL, "", "")

	resp, err := s.GetUserStatus(uid, 550, "movie")
	if err != nil {
		t.Fatalf("GetUserStatus: %v", err)
	}
	if resp.Status != StatusRequested {
		t.Fatalf("status = %q, want %q", resp.Status, StatusRequested)
	}
	if resp.Releases == nil {
		t.Fatal("releases = nil, want the movie's dates")
	}
	if resp.Releases.InCinemas != "2026-07-03" {
		t.Errorf("in_cinemas = %q, want 2026-07-03", resp.Releases.InCinemas)
	}
	if resp.Releases.Digital != "2026-09-12" {
		t.Errorf("digital = %q, want 2026-09-12", resp.Releases.Digital)
	}
}

// TestMovieReleaseDatesDoNotShiftZones is the regression pin for the trap that
// dogs every arr date: a movie release is a calendar date with no meaningful
// time-of-day, so localising Radarr's midnight timestamp moves it onto the
// previous day for every viewer west of UTC. The zone is forced here so the
// test fails on a UTC CI box too, where the bug would otherwise hide.
func TestMovieReleaseDatesDoNotShiftZones(t *testing.T) {
	f := &fakeRadarr{
		libraryJSON: `[{"id":1,"title":"Dune: Part Three","tmdbId":550,
			"hasFile":false,"monitored":true,
			"inCinemas":"2026-07-03T00:00:00Z","digitalRelease":"2026-09-12T00:00:00Z"}]`,
	}
	srv := newFakeRadarrServer(t, f)
	// Built before the zone is forced: the SQLite driver parses stored
	// timestamps against time.Local, so moving it mid-setup breaks the fixture
	// rather than the thing under test.
	s, uid := newHistoryTestService(t, srv.URL, "", "")

	original := time.Local
	time.Local = time.FixedZone("UTC-8", -8*60*60)
	t.Cleanup(func() { time.Local = original })

	resp, err := s.GetUserStatus(uid, 550, "movie")
	if err != nil {
		t.Fatalf("GetUserStatus: %v", err)
	}
	if resp.Releases == nil {
		t.Fatal("releases = nil, want the movie's dates")
	}
	if resp.Releases.InCinemas != "2026-07-03" || resp.Releases.Digital != "2026-09-12" {
		t.Errorf("dates = %q/%q, want 2026-07-03/2026-09-12 (a zone conversion slipped in)",
			resp.Releases.InCinemas, resp.Releases.Digital)
	}
}

// TestMovieStatusReleasesOmittedWhenUnknown keeps the field absent rather than
// empty when Radarr knows neither date, so a client never renders a blank
// release line.
func TestMovieStatusReleasesOmittedWhenUnknown(t *testing.T) {
	f := &fakeRadarr{
		libraryJSON: `[{"id":1,"title":"Fight Club","tmdbId":550,"hasFile":false,"monitored":true}]`,
	}
	srv := newFakeRadarrServer(t, f)
	s, uid := newHistoryTestService(t, srv.URL, "", "")

	resp, err := s.GetUserStatus(uid, 550, "movie")
	if err != nil {
		t.Fatalf("GetUserStatus: %v", err)
	}
	if resp.Releases != nil {
		t.Errorf("releases = %+v, want nil when neither date is known", resp.Releases)
	}
}

// TestMovieStatusReleasesOnlyPartiallyKnown covers the common pre-theatrical
// shape: a cinema date is set and the digital date has not been announced.
func TestMovieStatusReleasesOnlyPartiallyKnown(t *testing.T) {
	f := &fakeRadarr{
		libraryJSON: `[{"id":1,"title":"Fight Club","tmdbId":550,"hasFile":false,"monitored":true,
			"inCinemas":"2026-07-03T00:00:00Z"}]`,
	}
	srv := newFakeRadarrServer(t, f)
	s, uid := newHistoryTestService(t, srv.URL, "", "")

	resp, err := s.GetUserStatus(uid, 550, "movie")
	if err != nil {
		t.Fatalf("GetUserStatus: %v", err)
	}
	if resp.Releases == nil || resp.Releases.InCinemas != "2026-07-03" {
		t.Fatalf("releases = %+v, want the cinema date alone", resp.Releases)
	}
	if resp.Releases.Digital != "" {
		t.Errorf("digital = %q, want empty (not announced)", resp.Releases.Digital)
	}
}

// TestMovieStatusReleasesRideAvailableTitles keeps the dates on an available
// movie: a file can land before the digital date, and deciding whether a
// milestone still matters is the client's call, not the server's.
func TestMovieStatusReleasesRideAvailableTitles(t *testing.T) {
	f := &fakeRadarr{
		libraryJSON: `[{"id":1,"title":"Fight Club","tmdbId":550,"hasFile":true,"monitored":true,
			"digitalRelease":"2026-09-12T00:00:00Z"}]`,
	}
	srv := newFakeRadarrServer(t, f)
	s, uid := newHistoryTestService(t, srv.URL, "", "")

	resp, err := s.GetUserStatus(uid, 550, "movie")
	if err != nil {
		t.Fatalf("GetUserStatus: %v", err)
	}
	if resp.Status != StatusAvailable {
		t.Fatalf("status = %q, want %q", resp.Status, StatusAvailable)
	}
	if resp.Releases == nil || resp.Releases.Digital != "2026-09-12" {
		t.Errorf("releases = %+v, want the digital date to ride along", resp.Releases)
	}
}

// TestMovieStatusUnaddedTitleHasNoReleases pins the scope: dates come from the
// arr record, so a title nobody has added carries none.
func TestMovieStatusUnaddedTitleHasNoReleases(t *testing.T) {
	f := &fakeRadarr{}
	srv := newFakeRadarrServer(t, f)
	s, uid := newHistoryTestService(t, srv.URL, "", "")

	resp, err := s.GetUserStatus(uid, 550, "movie")
	if err != nil {
		t.Fatalf("GetUserStatus: %v", err)
	}
	if resp.Status != StatusUnavailable {
		t.Fatalf("status = %q, want %q", resp.Status, StatusUnavailable)
	}
	if resp.Releases != nil {
		t.Errorf("releases = %+v, want nil for a title not in the library", resp.Releases)
	}
}
