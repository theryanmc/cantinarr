package bookdiscovery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

var errNotFound = errors.New("this work was not found on Open Library")
var errMalformed = errors.New("Open Library returned an invalid response; please retry")

type Service struct {
	provider    *provider
	cache       memo
	targets     memo
	lookupSlots chan struct{}
}

func NewService() *Service {
	return &Service{provider: newProvider("https://openlibrary.org"), lookupSlots: make(chan struct{}, 4)}
}

type searchDoc struct {
	Key      string   `json:"key"`
	Title    string   `json:"title"`
	Authors  []string `json:"author_name"`
	Year     int      `json:"first_publish_year"`
	Cover    int64    `json:"cover_i"`
	Language []string `json:"language"`
	Editions struct {
		Docs []searchDoc `json:"docs"`
	} `json:"editions"`
}

type searchResponse struct {
	Total *int        `json:"numFound"`
	Start *int        `json:"start"`
	Docs  []searchDoc `json:"docs"`
}

func (d searchDoc) book() (Book, error) {
	id := workID(d.Key)
	if id == "" || strings.TrimSpace(d.Title) == "" {
		return Book{}, errMalformed
	}
	b := Book{ForeignID: "ol:" + id, Title: strings.TrimSpace(d.Title), Authors: []string{}, Year: d.Year, CoverID: validCover(d.Cover)}
	for _, a := range d.Authors {
		if a = strings.TrimSpace(a); a != "" {
			b.Authors = append(b.Authors, a)
		}
	}
	for _, ed := range d.Editions.Docs {
		english := false
		for _, lang := range ed.Language {
			english = english || lang == "eng"
		}
		if !english {
			continue
		}
		if title := strings.TrimSpace(ed.Title); title != "" {
			b.Title = title
		}
		if cover := validCover(ed.Cover); cover != 0 {
			b.CoverID = cover
		}
		break
	}
	return b, nil
}

func (s *Service) search(ctx context.Context, q, sort string, offset, limit int) (searchResponse, error) {
	params := url.Values{"q": {q}, "lang": {"en"}, "limit": {fmt.Sprint(limit)}, "offset": {fmt.Sprint(offset)},
		"fields": {"key,title,author_name,first_publish_year,cover_i,editions,editions.key,editions.title,editions.language,editions.cover_i"}}
	if sort != "" {
		params.Set("sort", sort)
	}
	var result searchResponse
	if err := s.provider.get(ctx, "/search.json?"+params.Encode(), &result); err != nil {
		return result, err
	}
	if result.Total == nil || *result.Total < 0 || result.Start == nil || *result.Start != offset || result.Docs == nil || len(result.Docs) > limit || (len(result.Docs) == 0 && offset < *result.Total) {
		return result, errMalformed
	}
	return result, nil
}

func (s *Service) Feed(ctx context.Context, feed, genreID string, page int) ([]byte, error) {
	q, sort, scope, ttl := "readinglog_count:[1 TO *]", "readinglog", "Popular on Open Library", time.Hour
	if feed == "genre" {
		genre, ok := genreByID(genreID)
		if !ok {
			return nil, errMalformed
		}
		parts := make([]string, len(genre.Subjects))
		for i, subject := range genre.Subjects {
			parts[i] = `subject:"` + subject + `"`
		}
		q, sort, scope, ttl = "("+strings.Join(parts, " OR ")+")", "", genre.Name+" on Open Library", 6*time.Hour
	} else if feed != "popular" {
		return nil, errMalformed
	}
	if page < 1 || page > maxPage {
		return nil, errMalformed
	}
	key := fmt.Sprintf("feed:%s:%s:%d", feed, genreID, page)
	return s.cache.get(ctx, key, ttl, func(ctx context.Context) ([]byte, error) {
		result, err := s.search(ctx, q, sort, (page-1)*pageSize, pageSize)
		if err != nil {
			return nil, err
		}
		out := Page{Results: []Book{}, Page: page, Total: *result.Total, Scope: scope}
		// Pagination follows provider offsets, never normalized/deduplicated size.
		if *result.Start+len(result.Docs) < *result.Total && page < maxPage {
			out.NextPage = page + 1
		}
		seen := map[string]bool{}
		for _, doc := range result.Docs {
			book, err := doc.book()
			if err != nil {
				return nil, err
			}
			if !seen[book.ForeignID] {
				out.Results = append(out.Results, book)
				seen[book.ForeignID] = true
			}
		}
		if len(out.Results) == 0 {
			out.EmptyMessage = "No books found on this page of " + scope + ". This does not search your library."
		}
		return json.Marshal(out)
	})
}

func (s *Service) Book(ctx context.Context, id string) ([]byte, error) {
	return s.cache.get(ctx, "book:"+id, 24*time.Hour, func(ctx context.Context) ([]byte, error) {
		result, err := s.search(ctx, "key:/works/"+id, "", 0, 1)
		if err != nil {
			return nil, err
		}
		if len(result.Docs) == 0 {
			return nil, errNotFound
		}
		book, err := result.Docs[0].book()
		if err != nil || book.ForeignID != "ol:"+id {
			return nil, errMalformed
		}
		var work struct {
			Key         string          `json:"key"`
			Description json.RawMessage `json:"description"`
		}
		if err := s.provider.get(ctx, "/works/"+id+".json", &work); err != nil {
			return nil, err
		}
		if workID(work.Key) != id {
			return nil, errMalformed
		}
		if len(work.Description) > 0 && string(work.Description) != "null" {
			if json.Unmarshal(work.Description, &book.Description) != nil {
				var description struct {
					Value string `json:"value"`
				}
				if json.Unmarshal(work.Description, &description) != nil {
					return nil, errMalformed
				}
				book.Description = description.Value
			}
		}
		return json.Marshal(book)
	})
}
