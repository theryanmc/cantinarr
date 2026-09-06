package musicdiscovery

import (
	"math"
	"regexp"
	"strings"
)

const pageSize = 20

// Protect offset arithmetic; music providers do not share TMDB's 500-page cap.
const maxPage = math.MaxInt / pageSize

var mbidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func validID(id string) bool { return mbidPattern.MatchString(id) }

// Album identity is a release GROUP, never a release, title, or numeric TMDB id.
type Album struct {
	ForeignID      string `json:"foreign_id"`
	Title          string `json:"title"`
	Artist         string `json:"artist"`
	ReleaseDate    string `json:"release_date,omitempty"`
	ReleaseType    string `json:"release_type"`
	Artwork        string `json:"artwork,omitempty"`
	Disambiguation string `json:"disambiguation,omitempty"`
}

func albumType(s string) string {
	switch strings.ToLower(s) {
	case "album":
		return "Album"
	case "ep":
		return "EP"
	default:
		return ""
	}
}

func artworkPath(id string) string { return "/api/discover/music/artwork/" + id }

type Page struct {
	Results      []Album `json:"results"`
	Page         int     `json:"page"`
	NextPage     int     `json:"next_page,omitempty"`
	Source       string  `json:"source"`
	Scope        string  `json:"scope"`
	EmptyMessage string  `json:"empty_message,omitempty"`
}

type Genre struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Tag  string `json:"tag"`
}

var Genres = []Genre{
	{"pop", "Pop", "pop"}, {"rock", "Rock", "rock"},
	{"hip-hop", "Hip-Hop", "hip hop"}, {"r-and-b", "R&B", "rhythm and blues"},
	{"electronic", "Electronic", "electronic"}, {"jazz", "Jazz", "jazz"},
	{"classical", "Classical", "classical"}, {"metal", "Metal", "metal"},
	{"country", "Country", "country"}, {"folk", "Folk", "folk"},
	{"blues", "Blues", "blues"}, {"reggae", "Reggae", "reggae"},
}

func genreByID(id string) (Genre, bool) {
	for _, g := range Genres {
		if g.ID == id {
			return g, true
		}
	}
	return Genre{}, false
}
