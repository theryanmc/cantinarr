package webhooks

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/instance"
)

// addLidarr attaches a Lidarr instance to an existing fixture and returns its
// id and webhook credential.
func (f *fixture) addLidarr(t *testing.T) (string, string) {
	t.Helper()
	inst := &instance.Instance{ServiceType: "lidarr", Name: "Music", URL: "http://lidarr.invalid", APIKey: "key"}
	if err := f.store.Create(inst); err != nil {
		t.Fatalf("create lidarr: %v", err)
	}
	token, err := f.store.WebhookToken(inst.ID)
	if err != nil {
		t.Fatalf("lidarr token: %v", err)
	}
	return inst.ID, token
}

func (f *fixture) postMusic(t *testing.T, id, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	return f.post(t, "/api/webhooks/arr/"+id, body, func(r *http.Request) {
		r.SetBasicAuth("cantinarr", token)
	})
}

// TestLidarrImportInvalidatesAndPingsWithoutAlerting pins the wave-one music
// webhook contract: an import (Lidarr serializes it as "Download") drops the
// music digests and pings arr_queue_changed so every music surface refetches —
// and announces nothing, because no music push category exists yet. The
// freshness story is the whole job.
func TestLidarrImportInvalidatesAndPingsWithoutAlerting(t *testing.T) {
	f := newFixture(t, "http://radarr.invalid", "http://sonarr.invalid")
	id, token := f.addLidarr(t)

	rec := f.postMusic(t, id, token, `{"eventType":"Download","albums":[{"id":9,"mbId":"mb-1"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if len(f.requests.musicIDs) != 1 || f.requests.musicIDs[0] != id {
		t.Fatalf("music invalidations = %v", f.requests.musicIDs)
	}
	if len(f.hub.events) == 0 || f.hub.events[0].Type != "arr_queue_changed" {
		t.Fatalf("broadcasts = %v, want an arr_queue_changed invalidation ping", f.hub.events)
	}
	if data := f.hub.events[0].Data; data["service_type"] != "lidarr" || data["instance_id"] != id {
		t.Fatalf("ping payload = %#v", data)
	}
	// Music has no TMDB id; request_status_changed would collide across every
	// album at tmdb 0.
	for _, e := range f.hub.events {
		if e.Type == "request_status_changed" {
			t.Error("a music event emitted request_status_changed")
		}
	}
	// No music push category exists in this wave: nothing may announce.
	if len(f.content.books) != 0 || len(f.content.movies) != 0 || len(f.content.episodes) != 0 {
		t.Errorf("music import announced content: books=%v movies=%v episodes=%v", f.content.books, f.content.movies, f.content.episodes)
	}
}

func TestLidarrLibraryEventsInvalidateWithoutAlerting(t *testing.T) {
	f := newFixture(t, "http://radarr.invalid", "http://sonarr.invalid")
	id, token := f.addLidarr(t)

	for _, event := range []string{"Grab", "AlbumDelete", "ArtistAdd", "ArtistDelete", "Retag"} {
		rec := f.postMusic(t, id, token, `{"eventType":"`+event+`"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d", event, rec.Code)
		}
	}
	if len(f.requests.musicIDs) != 5 {
		t.Fatalf("music invalidations = %v", f.requests.musicIDs)
	}
}

// TestLidarrTestAndUnknownEventsAckWithoutSideEffects: the Test button must
// succeed silently, and an event name this build does not know must be
// acknowledged (never 4xx — that would make Lidarr disable the webhook) with
// no cache churn.
func TestLidarrTestAndUnknownEventsAckWithoutSideEffects(t *testing.T) {
	f := newFixture(t, "http://radarr.invalid", "http://sonarr.invalid")
	id, token := f.addLidarr(t)

	for _, body := range []string{`{"eventType":"Test"}`, `{"eventType":"Health"}`, `{"eventType":"SomethingNew"}`} {
		rec := f.postMusic(t, id, token, body)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d for %s", rec.Code, body)
		}
	}
	if len(f.requests.musicIDs) != 0 || len(f.hub.events) != 0 {
		t.Fatalf("ack-only events had side effects: invalidations=%v events=%v", f.requests.musicIDs, f.hub.events)
	}
}

func TestLidarrWebhookRejectsBadToken(t *testing.T) {
	f := newFixture(t, "http://radarr.invalid", "http://sonarr.invalid")
	id, _ := f.addLidarr(t)

	rec := f.post(t, "/api/webhooks/arr/"+id, `{"eventType":"Download"}`, func(r *http.Request) {
		r.SetBasicAuth("cantinarr", "wrong")
	})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if len(f.requests.musicIDs) != 0 {
		t.Fatalf("unauthenticated call invalidated caches: %v", f.requests.musicIDs)
	}
}
