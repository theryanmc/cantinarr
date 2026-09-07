package bookdiscovery

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

// NormalizeISBN validates both checksums and represents ISBN-10 as ISBN-13.
// Invalid identifiers are never usable evidence, even when two sources agree.
func NormalizeISBN(raw string) string {
	s := strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(strings.TrimSpace(raw)))
	if len(s) == 10 {
		sum := 0
		for i, c := range s {
			n := int(c - '0')
			if i == 9 && c == 'X' {
				n = 10
			} else if c < '0' || c > '9' {
				return ""
			}
			sum += (10 - i) * n
		}
		if sum%11 != 0 {
			return ""
		}
		s = "978" + s[:9]
		sum = 0
		for i, c := range s {
			sum += int(c-'0') * (1 + 2*(i%2))
		}
		return s + string(rune('0'+(10-sum%10)%10))
	}
	if len(s) != 13 || (!strings.HasPrefix(s, "978") && !strings.HasPrefix(s, "979")) {
		return ""
	}
	sum := 0
	for i, c := range s {
		if c < '0' || c > '9' {
			return ""
		}
		sum += int(c-'0') * (1 + 2*(i%2))
	}
	if sum%10 != 0 {
		return ""
	}
	return s
}

func WorkID(raw string) string { return workID(raw) }

type SourceEdition struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Format    string   `json:"format,omitempty"`
	Languages []string `json:"languages,omitempty"`
	ISBNs     []string `json:"isbns"`
}

// Identity fetches bounded edition evidence. This is metadata, never a stored
// claim about what any Chaptarr instance contains. A partial edition scan does
// not prove that no other ISBN or matching service record exists.
func (s *Service) Identity(ctx context.Context, id string) (Book, error) {
	var book Book
	if workID(id) == "" {
		return book, errMalformed
	}
	body, err := s.Book(ctx, id)
	if err != nil {
		return book, err
	}
	if err = json.Unmarshal(body, &book); err != nil {
		return book, err
	}
	body, err = s.cache.get(ctx, "editions:"+id, 24*time.Hour, func(ctx context.Context) ([]byte, error) {
		var result struct {
			Size    int `json:"size"`
			Entries []struct {
				Key       string   `json:"key"`
				Title     string   `json:"title"`
				Format    string   `json:"physical_format"`
				ISBN10    []string `json:"isbn_10"`
				ISBN13    []string `json:"isbn_13"`
				Languages []struct {
					Key string `json:"key"`
				} `json:"languages"`
				Works []struct {
					Key string `json:"key"`
				} `json:"works"`
			} `json:"entries"`
		}
		if err := s.provider.get(ctx, "/works/"+id+"/editions.json?limit=200", &result); err != nil {
			return nil, err
		}
		if result.Entries == nil || len(result.Entries) > 200 {
			return nil, errMalformed
		}
		editions := []SourceEdition{}
		for _, entry := range result.Entries {
			belongs := false
			for _, work := range entry.Works {
				belongs = belongs || workID(work.Key) == id
			}
			if !belongs || !strings.HasPrefix(entry.Key, "/books/OL") {
				continue
			}
			ed := SourceEdition{ID: strings.TrimPrefix(entry.Key, "/books/"), Title: entry.Title, Format: entry.Format, ISBNs: []string{}}
			seen := map[string]bool{}
			for _, raw := range append(entry.ISBN13, entry.ISBN10...) {
				if isbn := NormalizeISBN(raw); isbn != "" && !seen[isbn] {
					ed.ISBNs = append(ed.ISBNs, isbn)
					seen[isbn] = true
				}
			}
			for _, lang := range entry.Languages {
				ed.Languages = append(ed.Languages, strings.TrimPrefix(lang.Key, "/languages/"))
			}
			editions = append(editions, ed)
		}
		return json.Marshal(editions)
	})
	if err == nil {
		err = json.Unmarshal(body, &book.Editions)
	}
	return book, err
}
