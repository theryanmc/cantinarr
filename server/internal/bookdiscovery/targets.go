package bookdiscovery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/windoze95/cantinarr-server/internal/transporterr"
	"strings"
	"time"
	"unicode"

	"github.com/windoze95/cantinarr-server/internal/chaptarr"
	"github.com/windoze95/cantinarr-server/internal/instance"
)

var errCatalog = errors.New("could not check this book in your library catalog; please retry")

type Target struct {
	ForeignID string `json:"foreign_id"`
	Title     string `json:"title"`
	Author    string `json:"author"`
	Year      int    `json:"year,omitempty"`
	Evidence  string `json:"evidence,omitempty"`
}

type Targets struct {
	State        string   `json:"state,omitempty"`
	Suggestions  []Target `json:"suggestions,omitempty"`
	Code         string   `json:"code,omitempty"`
	RetryAfter   int64    `json:"retry_after,omitempty"`
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
	return s.targets.get(ctx, inst.ID+":"+id, 0, func(ctx context.Context) ([]byte, error) {
		result, err := s.Resolve(ctx, inst, id)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	})
}

// Resolve separates verified identity from suggestions. Each search is bounded;
// a failed read is recorded even when another path returned usable suggestions.
func (s *Service) Resolve(ctx context.Context, inst *instance.Instance, id string) (Targets, error) {
	if inst == nil {
		return Targets{}, errMalformed
	}
	return s.ResolveClient(ctx, chaptarr.NewClient(inst.URL, inst.APIKey), id)
}

func (s *Service) ResolveClient(ctx context.Context, client *chaptarr.Client, id string) (Targets, error) {
	out := Targets{Candidates: []Target{}, Suggestions: []Target{}, State: "not_found", Scope: "Open Library work ID, up to 200 editions, selected library, bounded ISBN and title/author searches"}
	id = workID(id)
	if id == "" || client == nil {
		return out, errMalformed
	}
	select {
	case s.lookupSlots <- struct{}{}:
		defer func() { <-s.lookupSlots }()
	case <-ctx.Done():
		return out, ctx.Err()
	}
	ctx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	failed := false
	noteFailure := func(err error) {
		failed = true
		out.Code = "catalog_unavailable"
		_, delay := transporterr.Retry(err)
		if seconds := int64(delay.Round(time.Second) / time.Second); seconds > out.RetryAfter {
			out.RetryAfter = seconds
		}
	}
	lookup := func(term string) ([]chaptarr.LookupResult, error) {
		work, stop := context.WithTimeout(ctx, 5*time.Second)
		defer stop()
		return client.LookupBookContext(work, term)
	}
	rows, err := lookup("ol:" + id)
	if err == nil {
		exact, e := requestTargets(id, rows)
		if e != nil {
			noteFailure(e)
		} else if len(exact.Candidates) > 0 {
			exact.State = "matched"
			for i := range exact.Candidates {
				exact.Candidates[i].Evidence = "work_id"
			}
			return exact, nil
		}
	} else {
		noteFailure(err)
		var upstream *transporterr.Upstream
		if errors.As(err, &upstream) && (upstream.Status == 401 || upstream.Status == 403) {
			out.State, out.Code = "unavailable", "catalog_access_denied"
			return out, nil
		}
	}
	book, metadataErr := s.Identity(ctx, id)
	if metadataErr != nil {
		noteFailure(metadataErr)
	}
	isbns := map[string]bool{}
	editions := map[string]bool{}
	ordered := []string{}
	for _, ed := range book.Editions {
		editions[ed.ID] = true
		for _, isbn := range ed.ISBNs {
			if !isbns[isbn] {
				ordered = append(ordered, isbn)
			}
			isbns[isbn] = true
		}
	}
	seen := map[string]bool{}
	suggested := map[string]bool{}
	collect := func(rows []chaptarr.LookupResult, exact bool) {
		for _, row := range rows {
			if row.ForeignBookID == "" || strings.TrimSpace(row.Title) == "" {
				noteFailure(errCatalog)
				continue
			}
			if row.OpenLibraryWorkID != "" && workID(row.OpenLibraryWorkID) != id {
				continue
			}
			evidence := ""
			if workID(row.OpenLibraryWorkID) == id || workID(row.ForeignBookID) == id {
				evidence = "work_id"
			}
			for _, raw := range row.Editions {
				var ed chaptarr.Edition
				if json.Unmarshal(raw, &ed) != nil {
					continue
				}
				if isbn := NormalizeISBN(ed.ISBN13); isbn != "" && isbns[isbn] {
					evidence = "isbn"
				}
				editionID := strings.TrimPrefix(strings.TrimPrefix(ed.ForeignEditionID, "ol:"), "/books/")
				if editions[editionID] {
					evidence = "edition_id"
				}
			}
			author := row.AuthorName
			if row.Author != nil && row.Author.AuthorName != "" {
				author = row.Author.AuthorName
			}
			target := Target{ForeignID: row.ForeignBookID, Title: row.Title, Author: author, Year: row.Year, Evidence: evidence}
			if evidence != "" && !seen[row.ForeignBookID] {
				out.Candidates = append(out.Candidates, target)
				seen[row.ForeignBookID] = true
			}
			if evidence == "" && !exact && plausibleTitle(book.Title, row.Title) && !suggested[row.ForeignBookID] && len(out.Suggestions) < 20 {
				out.Suggestions = append(out.Suggestions, target)
				suggested[row.ForeignBookID] = true
			}
		}
	}
	// Library evidence can still resolve a request while metadata lookup is down.
	work, stop := context.WithTimeout(ctx, 5*time.Second)
	library, libraryErr := client.GetAllBooksContext(work)
	stop()
	if libraryErr != nil {
		noteFailure(libraryErr)
	} else {
		native := make([]chaptarr.LookupResult, 0, len(library))
		for _, row := range library {
			target := chaptarr.LookupResult{ForeignBookID: row.ForeignBookID, Title: row.Title, OpenLibraryWorkID: row.OpenLibraryWorkID}
			if row.Author != nil {
				target.AuthorName = row.Author.AuthorName
			}
			for _, ed := range row.Editions {
				raw, _ := json.Marshal(ed)
				target.Editions = append(target.Editions, raw)
			}
			native = append(native, target)
		}
		collect(native, true)
	}
	if len(out.Candidates) == 0 && ctx.Err() == nil {
		// Never turn a provider outage into dozens of identical failed calls.
		for i, isbn := range ordered {
			if i >= 4 || ctx.Err() != nil {
				break
			}
			lookupFailed := false
			for _, term := range []string{"isbn:" + isbn, isbn} {
				rows, e := lookup(term)
				if e != nil {
					noteFailure(e)
					lookupFailed = true
					break
				}
				collect(rows, true)
				if len(out.Candidates) > 0 {
					break
				}
			}
			if len(out.Candidates) > 0 || lookupFailed {
				break
			}
		}
	}
	if len(out.Candidates) == 0 && book.Title != "" && ctx.Err() == nil {
		terms := []string{book.Title}
		if len(book.Authors) > 0 {
			terms = append([]string{book.Title + " " + book.Authors[0]}, terms...)
		}
		for _, term := range terms {
			rows, e := lookup(term)
			if e != nil {
				noteFailure(e)
				break
			}
			collect(rows, false)
			if len(out.Candidates) > 0 || len(out.Suggestions) > 0 {
				break
			}
		}
	}
	switch {
	case len(out.Candidates) > 0:
		out.State = "matched"
	case len(out.Suggestions) > 0:
		out.State = "needs_match"
		out.EmptyMessage = "Choose the book that matches the title you want."
	case failed:
		out.State = "unavailable"
		out.EmptyMessage = "The library catalog is temporarily unavailable. You can save your request and we will retry automatically."
	default:
		out.EmptyMessage = "No verified match was found in the searched records. You can save this request for help finding a match."
	}
	return out, nil
}

func plausibleTitle(a, b string) bool {
	words := func(s string) []string {
		return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	}
	wanted := map[string]bool{}
	for _, w := range words(a) {
		if len(w) > 2 {
			wanted[w] = true
		}
	}
	matches := 0
	for _, w := range words(b) {
		if wanted[w] {
			matches++
		}
	}
	return matches > 0 || strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}

// Confirm re-reads the source and selected instance. A client cannot manufacture
// a mapping by posting an arbitrary native ID or approving another request.
func (s *Service) Confirm(ctx context.Context, inst *instance.Instance, id, foreignID string) (Target, error) {
	result, err := s.Resolve(ctx, inst, id)
	if err != nil {
		return Target{}, err
	}
	for _, candidate := range append(result.Candidates, result.Suggestions...) {
		if candidate.ForeignID == foreignID {
			return candidate, nil
		}
	}
	return Target{}, fmt.Errorf("that book is not among the current matches; refresh the choices")
}
