package websocket

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/windoze95/cantinarr-server/internal/db"
	"github.com/windoze95/cantinarr-server/internal/lidarr"
)

// lidarrBackend is a minimal Lidarr API double with a mutable queue.
type lidarrBackend struct {
	mu    sync.Mutex
	queue string
}

func (b *lidarrBackend) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b.mu.Lock()
		defer b.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/queue" {
			fmt.Fprint(w, queueEnvelope(b.queue))
			return
		}
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
	}
}

func (b *lidarrBackend) setQueue(records string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.queue = records
}

func drainBroadcasts(t *testing.T, h *Hub) []Event {
	t.Helper()
	var events []Event
	for {
		select {
		case msg := <-h.broadcast:
			var e Event
			if err := json.Unmarshal(msg.data, &e); err != nil {
				t.Fatalf("decode broadcast: %v", err)
			}
			events = append(events, e)
		default:
			return events
		}
	}
}

// TestPollLidarrPingsOnCompositionChangeWithoutAnnouncing pins the wave-one
// music poller contract: a queue whose composition changes broadcasts exactly
// the arr_queue_changed invalidation ping, and nothing is ever announced or
// dispatched — no music push category exists yet, and remediation's music
// support is deliberately fail-closed.
func TestPollLidarrPingsOnCompositionChangeWithoutAnnouncing(t *testing.T) {
	backend := &lidarrBackend{}
	srv := httptest.NewServer(backend.handler())
	t.Cleanup(srv.Close)
	client := lidarr.NewClient(srv.URL, "test-key")

	content := &recordingContent{}
	hub := NewHub(nil, nil, nil, nil, content, nil)

	backend.setQueue(`[
		{"id":100,"albumId":9,"status":"downloading","sizeleft":1000},
		{"id":101,"albumId":0,"status":"queued","sizeleft":3000}
	]`)
	hub.pollLidarrInstance("music-a", client)
	if events := drainBroadcasts(t, hub); len(events) != 0 {
		t.Fatalf("first poll broadcast %v, want none (no previous composition)", events)
	}
	if _, tracked := hub.prevLidarrQueue["music-a"][9]; !tracked {
		t.Fatal("album 9 was not witnessed into the queue membership")
	}
	if _, tracked := hub.prevLidarrQueue["music-a"][0]; tracked {
		t.Fatal("unmapped queue row (albumId 0) entered the membership")
	}

	backend.setQueue(`[]`)
	hub.pollLidarrInstance("music-a", client)
	events := drainBroadcasts(t, hub)
	if len(events) != 1 || events[0].Type != "arr_queue_changed" {
		t.Fatalf("broadcasts = %v, want exactly one arr_queue_changed", events)
	}
	if data := events[0].Data; data["service_type"] != "lidarr" || data["instance_id"] != "music-a" {
		t.Fatalf("ping payload = %#v", data)
	}
	if len(hub.prevLidarrQueue["music-a"]) != 0 {
		t.Fatalf("membership after departure = %v, want empty", hub.prevLidarrQueue["music-a"])
	}
	// Nothing announces: the freshness ping is the whole wave-one job.
	if calls := content.calls(); len(calls) != 0 {
		t.Fatalf("music poll announced books %v", calls)
	}
	if calls := content.movieCalls(); len(calls) != 0 {
		t.Fatalf("music poll announced movies %v", calls)
	}

	// An unchanged empty queue stays quiet.
	hub.pollLidarrInstance("music-a", client)
	if events := drainBroadcasts(t, hub); len(events) != 0 {
		t.Fatalf("unchanged queue re-broadcast %v", events)
	}
}

// TestLidarrQueueWitnessSurvivesRestart pins the durable-membership story the
// future music push category will build on: the membership one process saves
// is restored by the next, keyed under service type lidarr.
func TestLidarrQueueWitnessSurvivesRestart(t *testing.T) {
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	backend := &lidarrBackend{}
	srv := httptest.NewServer(backend.handler())
	t.Cleanup(srv.Close)
	client := lidarr.NewClient(srv.URL, "test-key")

	first := NewHub(nil, nil, nil, database, &recordingContent{}, nil)
	backend.setQueue(`[{"id":100,"albumId":9,"status":"downloading","sizeleft":1000}]`)
	first.pollLidarrInstance("music-a", client)
	drainBroadcasts(t, first)

	second := NewHub(nil, nil, nil, database, &recordingContent{}, nil)
	second.restoreQueueWitness()
	if _, tracked := second.prevLidarrQueue["music-a"][9]; !tracked {
		t.Fatalf("restored membership = %v, want album 9", second.prevLidarrQueue["music-a"])
	}
	if second.restoredWitness["music-a"].IsZero() || time.Since(second.restoredWitness["music-a"]) > time.Minute {
		t.Fatalf("restored observation time = %v", second.restoredWitness["music-a"])
	}
}
