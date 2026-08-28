package instance

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// newFakeJellyfin serves the two endpoints the instance layer needs, gated on
// the API key, and reports libraries with server paths that must never make
// it past this layer.
func newFakeJellyfin(t *testing.T, apiKey string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	authed := func(w http.ResponseWriter, r *http.Request) bool {
		if r.Header.Get("X-Emby-Token") != apiKey {
			w.WriteHeader(http.StatusUnauthorized)
			return false
		}
		return true
	}
	mux.HandleFunc("/System/Info", func(w http.ResponseWriter, r *http.Request) {
		if !authed(w, r) {
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ServerName": "Home", "Version": "10.11.0", "Id": "srv-1"})
	})
	mux.HandleFunc("/Library/VirtualFolders", func(w http.ResponseWriter, r *http.Request) {
		if !authed(w, r) {
			return
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"Name": "Movies", "CollectionType": "movies", "ItemId": "lib-movies", "Locations": []string{"/srv/media/movies"}},
			{"Name": "Shows", "CollectionType": "tvshows", "ItemId": "lib-shows", "Locations": []string{"/srv/media/shows"}},
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestMediaServerConfigPreserveClearAndValidate(t *testing.T) {
	h := NewHandler(newTestStore(t), nil)

	// Non-media types may only send an empty config.
	radarr := &Instance{ServiceType: "radarr"}
	if err := h.applyMediaServerConfig(radarr, &MediaServerConfig{PublicAddress: "https://x"}, nil); err == nil {
		t.Fatal("radarr accepted a media server config")
	}
	if err := h.applyMediaServerConfig(radarr, &MediaServerConfig{}, nil); err != nil {
		t.Fatalf("radarr rejected an empty config: %v", err)
	}

	// Omitted on create = share everything, no address.
	created := &Instance{ServiceType: "jellyfin"}
	if err := h.applyMediaServerConfig(created, nil, nil); err != nil {
		t.Fatal(err)
	}
	if created.MediaServerConfig.PublicAddress != "" || len(created.MediaServerConfig.LibraryIDs) != 0 || created.MediaServerConfig.LibraryIDs == nil {
		t.Fatalf("omitted create config = %+v, want empty address and [] ids", created.MediaServerConfig)
	}

	// Omitted on update keeps the stored config.
	existing := &Instance{ServiceType: "jellyfin", MediaServerConfig: MediaServerConfig{PublicAddress: "https://jf.example.com", LibraryIDs: []string{"a"}}}
	updated := &Instance{ServiceType: "jellyfin"}
	if err := h.applyMediaServerConfig(updated, nil, existing); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(updated.MediaServerConfig, existing.MediaServerConfig) {
		t.Fatalf("omitted update config = %+v, want %+v", updated.MediaServerConfig, existing.MediaServerConfig)
	}

	// Present replaces, normalized: trailing slash trimmed, ids tidied.
	provided := &MediaServerConfig{PublicAddress: " https://jf.example.com/ ", LibraryIDs: []string{" b ", "", "a", "b"}}
	if err := h.applyMediaServerConfig(updated, provided, existing); err != nil {
		t.Fatal(err)
	}
	want := MediaServerConfig{PublicAddress: "https://jf.example.com", LibraryIDs: []string{"a", "b"}}
	if !reflect.DeepEqual(updated.MediaServerConfig, want) {
		t.Fatalf("normalized config = %+v, want %+v", updated.MediaServerConfig, want)
	}

	// Explicit clear.
	if err := h.applyMediaServerConfig(updated, &MediaServerConfig{}, existing); err != nil {
		t.Fatal(err)
	}
	if updated.MediaServerConfig.PublicAddress != "" || len(updated.MediaServerConfig.LibraryIDs) != 0 {
		t.Fatalf("explicit clear left %+v", updated.MediaServerConfig)
	}

	for _, bad := range []MediaServerConfig{
		{PublicAddress: "jf.example.com"},
		{PublicAddress: "ftp://jf.example.com"},
		{PublicAddress: "https://user:pass@jf.example.com"},
		{PublicAddress: "https://jf.example.com/?x=1"},
		{PublicAddress: "https://jf.example.com/#frag"},
		{LibraryIDs: []string{"has space"}},
		{LibraryIDs: []string{strings.Repeat("x", 129)}},
	} {
		if err := h.applyMediaServerConfig(&Instance{ServiceType: "jellyfin"}, &bad, nil); err == nil {
			t.Errorf("config %+v accepted, want rejection", bad)
		}
	}
	// A path in the address is fine: reverse proxies mount Jellyfin under one.
	withPath := &Instance{ServiceType: "jellyfin"}
	if err := h.applyMediaServerConfig(withPath, &MediaServerConfig{PublicAddress: "https://home.example.com/jellyfin/"}, nil); err != nil {
		t.Fatalf("address with path rejected: %v", err)
	}
	if withPath.MediaServerConfig.PublicAddress != "https://home.example.com/jellyfin" {
		t.Fatalf("address with path = %q", withPath.MediaServerConfig.PublicAddress)
	}
}

func TestMediaServerLibrariesEndpoint(t *testing.T) {
	const storedKey = "stored-jellyfin-secret"
	jf := newFakeJellyfin(t, storedKey)
	s := newTestStore(t)
	stored := &Instance{ServiceType: "jellyfin", Name: "Home", URL: jf.URL, APIKey: storedKey}
	if err := s.Create(stored); err != nil {
		t.Fatal(err)
	}
	radarr := &Instance{ServiceType: "radarr", Name: "Movies", URL: jf.URL, APIKey: storedKey}
	if err := s.Create(radarr); err != nil {
		t.Fatal(err)
	}

	h := NewHandler(s, nil)
	router := chi.NewRouter()
	router.Post("/instances/media-server/libraries", h.MediaServerLibraries)
	do := func(body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest("POST", "/instances/media-server/libraries", strings.NewReader(body))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	rec := do(`{"service_type":"jellyfin","url":"` + jf.URL + `","api_key":"` + storedKey + `"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("candidate = %d %s", rec.Code, rec.Body.String())
	}
	var resp mediaServerLibrariesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.ServerName != "Home" || resp.Version != "10.11.0" || len(resp.Libraries) != 2 ||
		resp.Libraries[0].ID != "lib-movies" || resp.Libraries[1].CollectionType != "tvshows" {
		t.Fatalf("response = %+v", resp)
	}
	if strings.Contains(rec.Body.String(), "/srv/media") {
		t.Fatalf("server paths leaked: %s", rec.Body.String())
	}

	// Editing: the stored key is used when the form leaves it blank.
	rec = do(`{"id":"` + stored.ID + `","url":"` + jf.URL + `","api_key":""}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("stored credentials = %d %s", rec.Code, rec.Body.String())
	}
	rec = do(`{"service_type":"jellyfin","url":"` + jf.URL + `","api_key":"wrong"}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "connection test failed") {
		t.Fatalf("wrong key = %d %s", rec.Code, rec.Body.String())
	}
	rec = do(`{"id":"` + radarr.ID + `","url":"` + jf.URL + `"}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "media server type") {
		t.Fatalf("radarr = %d %s, want 400 not a media server", rec.Code, rec.Body.String())
	}
	if rec := do(`{"id":"jellyfin-missing","url":"` + jf.URL + `"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown id = %d, want 404", rec.Code)
	}
}

func TestJellyfinInstanceHTTPWritesCarryMediaServerConfig(t *testing.T) {
	const key = "jellyfin-secret"
	jf := newFakeJellyfin(t, key)
	store := newTestStore(t)
	h := NewHandler(store, NewRegistry(store))
	router := chi.NewRouter()
	router.Post("/instances", h.Create)
	router.Put("/instances/{instanceID}", h.Update)
	do := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	rec := do("POST", "/instances", `{"service_type":"jellyfin","name":"Home","url":"`+jf.URL+`","api_key":"`+key+`","is_default":true,
		"media_server_config":{"public_address":"https://jf.example.com/","library_ids":["lib-shows","lib-movies"]}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", rec.Code, rec.Body.String())
	}
	var created instanceResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.IsDefault {
		t.Fatal("jellyfin instance must never be the global default")
	}
	if created.MediaServerConfig == nil || created.MediaServerConfig.PublicAddress != "https://jf.example.com" ||
		!reflect.DeepEqual(created.MediaServerConfig.LibraryIDs, []string{"lib-movies", "lib-shows"}) {
		t.Fatalf("created config = %+v", created.MediaServerConfig)
	}
	if strings.Contains(rec.Body.String(), key) {
		t.Fatal("create response echoed the API key")
	}

	// An old client that omits the field keeps the stored config.
	rec = do("PUT", "/instances/"+created.ID, `{"name":"Renamed","url":"`+jf.URL+`","api_key":""}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("omitted update = %d %s", rec.Code, rec.Body.String())
	}
	stored, err := store.Get(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Name != "Renamed" || stored.MediaServerConfig.PublicAddress != "https://jf.example.com" || len(stored.MediaServerConfig.LibraryIDs) != 2 {
		t.Fatalf("omitted update changed config: %+v", stored.MediaServerConfig)
	}

	// An explicit empty config clears it.
	rec = do("PUT", "/instances/"+created.ID, `{"name":"Renamed","url":"`+jf.URL+`","api_key":"","media_server_config":{"public_address":"","library_ids":[]}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("clearing update = %d %s", rec.Code, rec.Body.String())
	}
	stored, _ = store.Get(created.ID)
	if stored.MediaServerConfig.PublicAddress != "" || len(stored.MediaServerConfig.LibraryIDs) != 0 {
		t.Fatalf("explicit clear left %+v", stored.MediaServerConfig)
	}

	// Non-media types refuse the field; non-media responses omit it.
	rec = do("POST", "/instances", `{"service_type":"radarr","name":"Movies","url":"`+jf.URL+`","api_key":"`+key+`","media_server_config":{"public_address":"https://x.example.com"}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("radarr with media_server_config = %d, want 400", rec.Code)
	}
	rec = do("POST", "/instances", `{"service_type":"floppy","name":"x","url":"`+jf.URL+`","api_key":"k"}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "'jellyfin'") {
		t.Fatalf("unknown type = %d %s, want the type list naming jellyfin", rec.Code, rec.Body.String())
	}
}

func TestGrantEndpointsFireObserverForMediaServerTypes(t *testing.T) {
	s := newTestStore(t)
	h := NewHandler(s, nil)
	var calls [][]int64
	h.SetGrantObserver(func(userIDs []int64) { calls = append(calls, userIDs) })
	router := newUsersRouter(h)
	alice := createUser(t, s, "alice")
	bob := createUser(t, s, "bob")
	jf := mkInstance(t, s, "jellyfin", "Home")
	radarr := mkInstance(t, s, "radarr", "Movies")
	do := func(method, path, body string) {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s %s = %d %s", method, path, rec.Code, rec.Body.String())
		}
	}
	ids := func(users ...int64) []int64 { return users }

	do("PUT", "/instances/"+jf+"/grant-users", `{"user_ids":[`+itoa(alice)+`]}`)
	// Replacing alice with bob affects both: alice loses, bob gains.
	do("PUT", "/instances/"+jf+"/grant-users", `{"user_ids":[`+itoa(bob)+`]}`)
	// Arr grants never involve the observer.
	do("PUT", "/instances/"+radarr+"/grant-users", `{"user_ids":[`+itoa(alice)+`]}`)
	do("PUT", "/users/"+itoa(alice)+"/instance-grants", `{"jellyfin":["`+jf+`"]}`)
	do("PUT", "/users/"+itoa(alice)+"/instance-grants", `{"radarr":["`+radarr+`"]}`)

	want := [][]int64{ids(alice), ids(alice, bob), ids(alice)}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("observer calls = %v, want %v", calls, want)
	}
}

func TestPinsRejectMediaServerTypes(t *testing.T) {
	s := newTestStore(t)
	h := NewHandler(s, nil)
	router := newUsersRouter(h)
	alice := createUser(t, s, "alice")
	jf := mkInstance(t, s, "jellyfin", "Home")
	do := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	if rec := do("PUT", "/users/"+itoa(alice)+"/default-instances", `{"jellyfin":"`+jf+`"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("pin via user defaults = %d, want 400", rec.Code)
	}
	if rec := do("PUT", "/instances/"+jf+"/users", `{"user_ids":[`+itoa(alice)+`]}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("pin via instance users = %d, want 400", rec.Code)
	}
	rec := do("GET", "/instances/"+jf+"/users", "")
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Fatalf("GET pins = %d %q, want 200 []", rec.Code, rec.Body.String())
	}
	if pins, _ := s.ListUserDefaults(alice); len(pins) != 0 {
		t.Fatalf("a pin was stored: %v", pins)
	}
}

func itoa(id int64) string {
	return strconv.FormatInt(id, 10)
}
