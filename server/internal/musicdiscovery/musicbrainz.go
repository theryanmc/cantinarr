package musicdiscovery

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type releaseGroup struct {
	ID             string `json:"id"`
	Title          string `json:"title"`
	Type           string `json:"primary-type"`
	Date           string `json:"first-release-date"`
	Disambiguation string `json:"disambiguation"`
	Credits        []struct {
		Name   string `json:"name"`
		Join   string `json:"joinphrase"`
		Artist struct {
			Name string `json:"name"`
		} `json:"artist"`
	} `json:"artist-credit"`
}

func (r releaseGroup) album() (Album, error) {
	if !validID(r.ID) || strings.TrimSpace(r.Title) == "" {
		return Album{}, errors.New("invalid MusicBrainz release group")
	}
	var artist strings.Builder
	for _, credit := range r.Credits {
		name := credit.Name
		if name == "" {
			name = credit.Artist.Name
		}
		artist.WriteString(name)
		artist.WriteString(credit.Join)
	}
	return Album{ForeignID: r.ID, Title: r.Title, Artist: artist.String(),
		ReleaseDate: r.Date, ReleaseType: albumType(r.Type),
		Disambiguation: r.Disambiguation, Artwork: artworkPath(r.ID)}, nil
}

type groupSearch struct {
	Count  *int           `json:"count"`
	Offset *int           `json:"offset"`
	Groups []releaseGroup `json:"release-groups"`
}

func (s *Service) search(ctx context.Context, query string, offset, limit int) (groupSearch, error) {
	var result groupSearch
	params := url.Values{"query": {query}, "fmt": {"json"}, "limit": {strconv.Itoa(limit)}, "offset": {strconv.Itoa(offset)}}
	err := s.mb.get(ctx, "/release-group/?"+params.Encode(), &result)
	if err != nil {
		return result, err
	}
	if result.Count == nil || *result.Count < 0 || result.Offset == nil || *result.Offset != offset ||
		result.Groups == nil || len(result.Groups) > limit ||
		(len(result.Groups) == 0 && offset < *result.Count) {
		return result, errors.New("incomplete MusicBrainz search response")
	}
	return result, nil
}

// One OR query enriches a whole chart page. Missing IDs fail the page, rather
// than turning a provider failure into an apparently complete shorter chart.
func (s *Service) enrich(ctx context.Context, ids []string) (map[string]Album, error) {
	result := make(map[string]Album, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	query := "rgid:(" + strings.Join(ids, " OR ") + ")"
	raw, err := s.cache.get(ctx, "batch:"+query, 24*time.Hour, func(ctx context.Context) ([]byte, error) {
		search, err := s.search(ctx, query, 0, len(ids))
		if err != nil {
			return nil, err
		}
		byID := make(map[string]Album, len(ids))
		for _, rg := range search.Groups {
			album, err := rg.album()
			if err != nil {
				return nil, err
			}
			byID[album.ForeignID] = album
		}
		for _, id := range ids {
			if _, ok := byID[id]; !ok {
				return nil, errors.New("MusicBrainz could not resolve every chart entry; please retry")
			}
		}
		return json.Marshal(byID)
	})
	if err == nil {
		err = json.Unmarshal(raw, &result)
	}
	return result, err
}

func (s *Service) Album(ctx context.Context, id string) ([]byte, error) {
	if !validID(id) {
		return nil, errors.New("invalid MusicBrainz release-group ID")
	}
	return s.cache.get(ctx, "album:"+id, 24*time.Hour, func(ctx context.Context) ([]byte, error) {
		var rg releaseGroup
		if err := s.mb.get(ctx, "/release-group/"+id+"?inc=artists&fmt=json", &rg); err != nil {
			return nil, err
		}
		if rg.ID != id {
			return nil, errors.New("MusicBrainz returned a different album identity")
		}
		album, err := rg.album()
		if err != nil {
			return nil, err
		}
		if album.ReleaseType == "" {
			return nil, errors.New("this release is not an album or EP")
		}
		return json.Marshal(album)
	})
}

func (s *Service) genre(ctx context.Context, genre Genre, page int) (Page, error) {
	result := Page{Page: page, Results: []Album{}, Source: "MusicBrainz", Scope: genre.Name + " albums and EPs, in MusicBrainz matching order"}
	offset := (page - 1) * pageSize
	search, err := s.search(ctx, "tag:\""+genre.Tag+"\" AND (primarytype:album OR primarytype:ep)", offset, pageSize)
	if err != nil {
		return result, err
	}
	seen := map[string]bool{}
	for _, rg := range search.Groups {
		album, err := rg.album()
		if err != nil {
			return result, err
		}
		if album.ReleaseType != "" && !seen[album.ForeignID] {
			result.Results = append(result.Results, album)
			seen[album.ForeignID] = true
		}
	}
	if offset+len(search.Groups) < *search.Count && page < maxPage {
		result.NextPage = page + 1
	}
	return result, nil
}
