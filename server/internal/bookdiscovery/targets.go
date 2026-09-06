package bookdiscovery

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/windoze95/cantinarr-server/internal/chaptarr"
	"github.com/windoze95/cantinarr-server/internal/instance"
)

var errCatalog = errors.New("could not check this book in your library catalog; please retry")

type Target struct {
	ForeignID string `json:"foreign_id"`
	Title     string `json:"title"`
	Author    string `json:"author"`
}

type Targets struct {
	Candidates   []Target `json:"candidates"`
	Scope        string   `json:"scope"`
	EmptyMessage string   `json:"empty_message,omitempty"`
}

// A provider-prefixed ID term is an exact Chaptarr fetch. A single canonical
// work returned by that fetch declares an alias, as in request.lookupCanonicalAlias.
// Distinct canonical results need explicit identity evidence; titles never
// prove a match. Format siblings share one canonical request target.
func requestTargets(id string, results []chaptarr.LookupResult) (Targets, error) {
	out := Targets{Candidates: []Target{}, Scope: "Open Library work ID lookup in the selected Chaptarr catalog"}
	canonical := map[string]bool{}
	for _, result := range results {
		key := strings.TrimSpace(result.ForeignBookID)
		if key == "" || strings.TrimSpace(result.Title) == "" {
			return out, errCatalog
		}
		canonical[key] = true
	}
	seen := map[string]bool{}
	for _, result := range results {
		key := strings.TrimSpace(result.ForeignBookID)
		providerID := workID(result.OpenLibraryWorkID)
		// Explicit contradictory provider evidence wins over an inferred alias.
		if result.OpenLibraryWorkID != "" && providerID != id {
			continue
		}
		if providerID != id && workID(key) != id && len(canonical) != 1 {
			continue
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		author := result.AuthorName
		if result.Author != nil && result.Author.AuthorName != "" {
			author = result.Author.AuthorName
		}
		out.Candidates = append(out.Candidates, Target{ForeignID: key, Title: result.Title, Author: author})
	}
	if len(out.Candidates) == 0 {
		out.EmptyMessage = "No verified match for this Open Library work ID in your library catalog. The book may still be found by searching its title and author."
	}
	return out, nil
}

func (s *Service) RequestTargets(ctx context.Context, inst *instance.Instance, id string) ([]byte, error) {
	// Coalesce simultaneous reads but keep no server-side mapping snapshot:
	// requests/webhooks can immediately change catalog identity. Visible clients
	// retain mappings for at most 60 seconds and invalidate them on library events.
	return s.targets.get(ctx, inst.ID+":"+id, 0, func(ctx context.Context) ([]byte, error) {
		select {
		case s.lookupSlots <- struct{}{}:
			defer func() { <-s.lookupSlots }()
		case <-ctx.Done():
			return nil, errCatalog
		}
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		results, err := chaptarr.NewClient(inst.URL, inst.APIKey).LookupBookContext(ctx, "ol:"+id)
		if err != nil {
			return nil, errCatalog
		}
		targets, err := requestTargets(id, results)
		if err != nil {
			return nil, err
		}
		return json.Marshal(targets)
	})
}
