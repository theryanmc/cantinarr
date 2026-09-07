package bookdiscovery

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/instance"
)

type Handler struct {
	store   *instance.Store
	service Catalog
}

func NewHandler(store *instance.Store) *Handler {
	return &Handler{store: store, service: NewService()}
}

// authorize requires a real Chaptarr instance for request-target resolution.
// Chaptarr is grant-only for every requester, including kids accounts. Admins
// may select any configured Chaptarr; there is no requester global fallback.
func (h *Handler) authorize(w http.ResponseWriter, r *http.Request, id string) (string, bool) {
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		fail(w, 401, "unauthorized")
		return "", false
	}
	if !auth.HasPermission(claims.Role, auth.PermissionMediaDiscover) {
		fail(w, 403, "discovery is not available to you")
		return "", false
	}
	if h.store == nil {
		fail(w, 403, "books are not available to you")
		return "", false
	}
	admin := claims.Role == auth.RoleAdmin
	if id == "" {
		var err error
		id, err = h.store.EffectiveDefaultInstanceID(claims.UserID, "chaptarr")
		if err != nil {
			fail(w, 503, "could not check book access")
			return "", false
		}
		if id == "" && admin {
			inst, err := h.store.GetDefault("chaptarr")
			if err != nil {
				fail(w, 503, "could not check book access")
				return "", false
			}
			if inst != nil {
				id = inst.ID
			}
		}
	}
	if id == "" {
		fail(w, 403, "books are not available to you")
		return "", false
	}
	if !admin {
		allowed, err := h.store.UserCanAccessInstance(claims.UserID, id, "chaptarr")
		if err != nil {
			fail(w, 503, "could not check book access")
			return "", false
		}
		if !allowed {
			fail(w, 403, "books are not available to you")
			return "", false
		}
	}
	inst, err := h.store.Get(id)
	if err != nil || inst == nil || inst.ServiceType != "chaptarr" {
		fail(w, 403, "books are not available to you")
		return "", false
	}
	return id, true
}

// authorizeMetadata permits admins to browse external catalogs before setup.
// An explicit instance remains validated; requester grants never fall back.
func (h *Handler) authorizeMetadata(w http.ResponseWriter, r *http.Request, id string) (string, bool) {
	claims := auth.GetClaims(r.Context())
	if claims != nil && claims.Role == auth.RoleAdmin && auth.HasPermission(claims.Role, auth.PermissionMediaDiscover) && id == "" {
		return "", true
	}
	return h.authorize(w, r, id)
}

func fail(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}

func (h *Handler) serve(w http.ResponseWriter, r *http.Request, id string, body []byte, err error, metadata bool) {
	authorize := h.authorize
	if metadata {
		authorize = h.authorizeMetadata
	}
	// Check the grant again after waiting on a provider or shared cache fill.
	if _, ok := authorize(w, r, id); !ok {
		return
	}
	if err != nil {
		code := http.StatusBadGateway
		if errors.Is(err, errNotFound) {
			code = http.StatusNotFound
		}
		fail(w, code, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if !metadata {
		var result Targets
		if json.Unmarshal(body, &result) == nil && result.State == "unavailable" {
			if result.RetryAfter > 0 {
				w.Header().Set("Retry-After", strconv.FormatInt(result.RetryAfter, 10))
			}
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}
	w.Write(body)
}

func (h *Handler) Feed(w http.ResponseWriter, r *http.Request) {
	id, ok := h.authorizeMetadata(w, r, r.URL.Query().Get("instance_id"))
	if !ok {
		return
	}
	feed, genre := chi.URLParam(r, "feed"), r.URL.Query().Get("genre")
	switch feed {
	case "popular":
		if genre != "" {
			fail(w, 400, "genre is only supported by the genre feed")
			return
		}
	case "genre":
		if _, ok := genreByID(genre); !ok {
			fail(w, 400, "choose a supported book genre")
			return
		}
	default:
		fail(w, 404, "book feed not found")
		return
	}
	page := 1
	if raw := r.URL.Query().Get("page"); raw != "" {
		var err error
		page, err = strconv.Atoi(raw)
		if err != nil || page < 1 || page > maxPage {
			fail(w, 400, "invalid book page")
			return
		}
	}
	body, err := h.service.Feed(r.Context(), feed, genre, page)
	h.serve(w, r, id, body, err, true)
}

func (h *Handler) Genres(w http.ResponseWriter, r *http.Request) {
	id, ok := h.authorizeMetadata(w, r, r.URL.Query().Get("instance_id"))
	if !ok {
		return
	}
	body, err := json.Marshal(map[string]any{"genres": Genres})
	h.serve(w, r, id, body, err, true)
}

func (h *Handler) Book(w http.ResponseWriter, r *http.Request)          { h.book(w, r, false) }
func (h *Handler) RequestTarget(w http.ResponseWriter, r *http.Request) { h.book(w, r, true) }

func (h *Handler) book(w http.ResponseWriter, r *http.Request, target bool) {
	authorize := h.authorizeMetadata
	if target {
		authorize = h.authorize
	}
	id, ok := authorize(w, r, r.URL.Query().Get("instance_id"))
	if !ok {
		return
	}
	work := workID(chi.URLParam(r, "workId"))
	if work == "" {
		fail(w, 400, "invalid Open Library work ID")
		return
	}
	var body []byte
	var err error
	if target {
		inst, readErr := h.store.Get(id)
		if readErr != nil || inst == nil {
			fail(w, 503, "could not check book access")
			return
		}
		body, err = h.service.RequestTargets(r.Context(), inst, work)
	} else {
		body, err = h.service.Book(r.Context(), work)
	}
	h.serve(w, r, id, body, err, !target)
}

func (h *Handler) Search(w http.ResponseWriter, r *http.Request) {
	id, ok := h.authorizeMetadata(w, r, r.URL.Query().Get("instance_id"))
	if !ok {
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("query"))
	page := 1
	if raw := r.URL.Query().Get("page"); raw != "" {
		var err error
		page, err = strconv.Atoi(raw)
		if err != nil {
			fail(w, 400, "invalid search page")
			return
		}
	}
	if query == "" || len(query) > 300 || page < 1 || page > maxPage {
		fail(w, 400, "enter a search query and valid page")
		return
	}
	body, err := h.service.Search(r.Context(), query, page)
	h.serve(w, r, id, body, err, true)
}

func NewHandlerWithService(store *instance.Store, service Catalog) *Handler {
	if service == nil {
		service = NewService()
	}
	return &Handler{store: store, service: service}
}
