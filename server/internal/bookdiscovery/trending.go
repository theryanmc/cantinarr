package bookdiscovery

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/hardcover"
	"github.com/windoze95/cantinarr-server/internal/instance"
)

// Trending is the one live book discovery feed: Hardcover's trending list,
// fetched with the token an admin connected to the requester's Chaptarr
// instance. Everything else in this package is a retired Open Library route.
//
// The feed is instance-scoped because the credential is: an admin connects
// Hardcover per Chaptarr instance, so a requester sees the list only through
// an instance they hold a grant on, and never learns whether a sibling
// instance is connected. Books carry no content rating, so kids accounts see
// the same list as everyone else who holds the grant.

// TrendingLimit is how many books the row fetches. Hardcover's `books_trending`
// pages by offset; one call of this size returned a full list live.
const TrendingLimit = 50

// trendingTTL bounds Hardcover traffic: the list is two calls per refresh and
// Hardcover's rate-limit budget is ten per window, so the row is served from
// this cache and refreshed at most once per TTL per instance, never per view.
const trendingTTL = 30 * time.Minute

// TrendingSource is what the handler needs from Hardcover (the real client,
// or a test double).
type TrendingSource interface {
	Trending(ctx context.Context, token string, limit int) ([]hardcover.Book, error)
}

type trendingEntry struct {
	books   []hardcover.Book
	fetched time.Time
}

// TrendingHandler serves GET /api/discover/books/trending.
type TrendingHandler struct {
	store  *instance.Store
	source TrendingSource
	now    func() time.Time
	ttl    time.Duration

	mu    sync.Mutex
	cache map[string]*trendingEntry // by instance id
	// inflight coalesces concurrent misses for one instance so a busy screen
	// never spends two Hardcover calls where one would do.
	inflight map[string]*sync.WaitGroup
}

// NewTrendingHandler wires the feed to the instance store (grants + tokens)
// and a Hardcover source.
func NewTrendingHandler(store *instance.Store, source TrendingSource) *TrendingHandler {
	return &TrendingHandler{
		store:    store,
		source:   source,
		now:      time.Now,
		ttl:      trendingTTL,
		cache:    map[string]*trendingEntry{},
		inflight: map[string]*sync.WaitGroup{},
	}
}

// Invalidate drops the cached list for an instance, e.g. after its Hardcover
// token changes.
func (h *TrendingHandler) Invalidate(instanceID string) {
	h.mu.Lock()
	delete(h.cache, instanceID)
	h.mu.Unlock()
}

type trendingResponse struct {
	InstanceID string `json:"instance_id"`
	// Connected is false when the instance holds no Hardcover token. The row
	// then shows an admin the way to Settings instead of an empty list.
	Connected bool             `json:"connected"`
	Source    string           `json:"source"`
	Scope     string           `json:"scope"`
	Books     []hardcover.Book `json:"books"`
}

// Trending answers the row. Authorization mirrors the other book reads: the
// caller needs media:discover and, unless an admin, a grant on the resolved
// Chaptarr instance. Admins also need an instance here -- the token lives on
// it -- so admin catalog browsing without an instance does not apply.
func (h *TrendingHandler) Trending(w http.ResponseWriter, r *http.Request) {
	id, ok := h.authorize(w, r, r.URL.Query().Get("instance_id"))
	if !ok {
		return
	}
	token, err := h.store.HardcoverToken(id)
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "could not read the Hardcover connection")
		return
	}
	resp := trendingResponse{InstanceID: id, Source: "Hardcover", Scope: "Trending on Hardcover right now", Books: []hardcover.Book{}}
	if token == "" {
		writeJSON(w, resp)
		return
	}
	resp.Connected = true
	books, err := h.trending(r.Context(), id, token)
	if err != nil {
		if errors.Is(err, hardcover.ErrUnauthorized) {
			// The connected token no longer works. Say so rather than
			// rendering an empty row that looks like a quiet day.
			fail(w, http.StatusBadGateway, "Hardcover no longer accepts the connected API token; reconnect it in the instance settings")
			return
		}
		log.Printf("bookdiscovery: hardcover trending for %s failed: %v", id, err)
		fail(w, http.StatusBadGateway, "could not reach Hardcover for the trending list")
		return
	}
	// The grant is re-checked after any wait on the provider or the cache.
	if _, ok := h.authorize(w, r, id); !ok {
		return
	}
	resp.Books = books
	writeJSON(w, resp)
}

func (h *TrendingHandler) trending(ctx context.Context, instanceID, token string) ([]hardcover.Book, error) {
	for {
		h.mu.Lock()
		if entry, ok := h.cache[instanceID]; ok && h.now().Sub(entry.fetched) < h.ttl {
			books := entry.books
			h.mu.Unlock()
			return books, nil
		}
		if wg, busy := h.inflight[instanceID]; busy {
			h.mu.Unlock()
			wg.Wait()
			continue
		}
		wg := &sync.WaitGroup{}
		wg.Add(1)
		h.inflight[instanceID] = wg
		h.mu.Unlock()

		books, err := h.source.Trending(ctx, token, TrendingLimit)

		h.mu.Lock()
		delete(h.inflight, instanceID)
		if err == nil {
			if books == nil {
				books = []hardcover.Book{}
			}
			h.cache[instanceID] = &trendingEntry{books: books, fetched: h.now()}
		}
		h.mu.Unlock()
		wg.Done()
		return books, err
	}
}

// authorize resolves and checks the Chaptarr instance the caller may read
// through. It is the same rule the other requester book reads apply.
func (h *TrendingHandler) authorize(w http.ResponseWriter, r *http.Request, id string) (string, bool) {
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		fail(w, http.StatusUnauthorized, "unauthorized")
		return "", false
	}
	if !auth.HasPermission(claims.Role, auth.PermissionMediaDiscover) {
		fail(w, http.StatusForbidden, "discovery is not available to you")
		return "", false
	}
	if h.store == nil {
		fail(w, http.StatusForbidden, "books are not available to you")
		return "", false
	}
	admin := claims.Role == auth.RoleAdmin
	if id == "" {
		var err error
		id, err = h.store.EffectiveDefaultInstanceID(claims.UserID, "chaptarr")
		if err != nil {
			fail(w, http.StatusServiceUnavailable, "could not check book access")
			return "", false
		}
	}
	if id == "" {
		fail(w, http.StatusForbidden, "books are not available to you")
		return "", false
	}
	if !admin {
		allowed, err := h.store.UserCanAccessInstance(claims.UserID, id, "chaptarr")
		if err != nil {
			fail(w, http.StatusServiceUnavailable, "could not check book access")
			return "", false
		}
		if !allowed {
			fail(w, http.StatusForbidden, "books are not available to you")
			return "", false
		}
	}
	inst, err := h.store.Get(id)
	if err != nil || inst == nil || inst.ServiceType != "chaptarr" {
		fail(w, http.StatusForbidden, "books are not available to you")
		return "", false
	}
	return id, true
}

func fail(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

func writeJSON(w http.ResponseWriter, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(body)
}
