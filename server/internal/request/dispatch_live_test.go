package request

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/db"
	"github.com/windoze95/cantinarr-server/internal/httpx"
	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/secrets"
)

// This explicitly opted-in journey mutates disposable local services. The
// manifest must point at empty instances with writable root folders, profiles,
// and no indexers or download clients. It is never used by the default suite.
var catalogCanary = flag.String("catalog-canary", "", "private JSON manifest for disposable loopback Chaptarr/Lidarr instances")

type catalogCanaryInstance struct{ URL, Key, Container string }

func TestLiveDisposableCatalogDelivery(t *testing.T) {
	if *catalogCanary == "" {
		t.Skip("pass -catalog-canary with disposable service credentials")
	}
	body, err := os.ReadFile(*catalogCanary)
	if err != nil {
		t.Fatal(err)
	}
	var configs map[string]catalogCanaryInstance
	if json.Unmarshal(body, &configs) != nil {
		t.Fatal("invalid canary manifest")
	}
	for _, serviceType := range []string{"lidarr", "chaptarr"} {
		t.Run(serviceType, func(t *testing.T) {
			cfg := configs[serviceType]
			target, err := url.Parse(cfg.URL)
			if err != nil || target.Scheme != "http" || target.Hostname() != "127.0.0.1" || cfg.Key == "" {
				t.Fatal("canary must use a disposable loopback service")
			}
			proxy := httputil.NewSingleHostReverseProxy(target)
			proxy.Transport = httpx.Internal()
			var outage atomic.Bool
			outage.Store(serviceType == "lidarr")
			gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if outage.Load() {
					w.Header().Set("Retry-After", "600")
					w.WriteHeader(503)
					return
				}
				proxy.ServeHTTP(w, r)
			}))
			defer gateway.Close()
			cipher, _ := secrets.NewCipher(bytes.Repeat([]byte{0x37}, 32))
			databasePath := filepath.Join(t.TempDir(), "requests.db")
			database, err := db.Open(databasePath)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { database.Close() }()
			store := instance.NewStore(database, cipher)
			inst := &instance.Instance{ServiceType: serviceType, Name: "Disposable canary", URL: gateway.URL, APIKey: cfg.Key}
			if err = store.Create(inst); err != nil {
				t.Fatal(err)
			}
			result, err := database.Exec(`INSERT INTO users(username,password_hash,role) VALUES('canary','','user')`)
			if err != nil {
				t.Fatal(err)
			}
			uid, _ := result.LastInsertId()
			if err = store.SetUserDefault(uid, serviceType, inst.ID); err != nil {
				t.Fatal(err)
			}
			s := NewService(database, instance.NewRegistry(store), nil, nil)
			req := &CreateRequest{MediaType: "music", Title: "Nevermind", InstanceID: inst.ID, CatalogRef: &CatalogRef{Provider: "musicbrainz", ID: "1b022e01-4da6-387b-8658-8678046e4cef"}}
			if serviceType == "chaptarr" {
				req = &CreateRequest{MediaType: "book", Title: "The Subtle Art of Not Giving a Fuck", BookFormat: "both", InstanceID: inst.ID, CatalogRef: &CatalogRef{Provider: "openlibrary", ID: "OL17590212W"}}
			}
			// Refuse to run against a populated library, even on loopback.
			outage.Store(false)
			if serviceType == "chaptarr" {
				client, _, _ := s.resolveChaptarr(uid, inst.ID)
				books, e := client.GetAllBooks()
				if e != nil || len(books) != 0 {
					t.Fatal("canary book library is not empty or readable")
				}
			} else {
				client, _, _ := s.resolveLidarr(uid, inst.ID)
				albums, e := client.GetAllAlbums()
				if e != nil || len(albums) != 0 {
					t.Fatal("canary music library is not empty or readable")
				}
			}
			outage.Store(serviceType == "lidarr")
			out, err := s.CreateMediaRequest(uid, req)
			if err != nil || out.RequestID == 0 {
				t.Fatalf("request was not saved: %v", err)
			}
			s.SweepDispatch(context.Background())
			states, err := s.deliveryStates(out.RequestID)
			if err != nil || len(states) == 0 {
				t.Fatal("delivery intent lost")
			}
			if serviceType == "lidarr" {
				if states[0].State != "retry" || states[0].NextAttemptAt == nil {
					t.Fatalf("outage was not retained: %+v", states)
				}
				// Reopen the actual database and rebuild both service and registry.
				database.Close()
				database, err = db.Open(databasePath)
				if err != nil {
					t.Fatal(err)
				}
				s = NewService(database, instance.NewRegistry(instance.NewStore(database, cipher)), nil, nil)
				outage.Store(false)
				database.Exec(`UPDATE request_dispatch SET next_attempt_at=0`)
				s.SweepDispatch(context.Background())
				states, _ = s.deliveryStates(out.RequestID)
				if states[0].State != "complete" {
					t.Fatalf("live Lidarr delivery did not complete: %+v", states)
				}
				client, _, _ := s.resolveLidarr(uid, inst.ID)
				albums, e := client.GetAllAlbums()
				if e != nil {
					t.Fatal(e)
				}
				matches := 0
				for _, album := range albums {
					if album.ForeignAlbumID == req.CatalogRef.ID {
						matches++
					}
				}
				if matches != 1 {
					t.Fatalf("expected one exact live album, got %d", matches)
				}
				t.Log("saved through HTTP 503, reopened database, delivered one exact release group to live Lidarr")
			} else {
				for _, state := range states {
					if state.State != "retry" && state.State != "needs_match" && state.State != "waiting_library" && state.State != "complete" {
						t.Fatalf("unexpected live book outcome: %+v", states)
					}
					t.Logf("live Chaptarr: %s retained as %s (code %s)", state.Format, state.State, state.Code)
				}
				if states[0].State != "complete" {
					if _, err = s.DeliveryAction(context.Background(), uid, out.RequestID, "cancel", ""); err != nil {
						t.Fatal(err)
					}
				}
			}
		})
	}
}
