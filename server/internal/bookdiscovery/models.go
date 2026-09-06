package bookdiscovery

import (
	"regexp"
	"strings"
)

const pageSize = 20
const maxPage = 50

// Book is shared external metadata. A catalog's request identity and library
// state never belong here; an edition changes presentation, never identity.
type Book struct {
	ForeignID   string   `json:"foreign_id"`
	Title       string   `json:"title"`
	Authors     []string `json:"authors"`
	Year        int      `json:"year,omitempty"`
	Description string   `json:"description,omitempty"`
	CoverID     int64    `json:"cover_id,omitempty"`
}

type Page struct {
	Results      []Book `json:"results"`
	Page         int    `json:"page"`
	NextPage     int    `json:"next_page,omitempty"`
	Total        int    `json:"total_results"`
	Scope        string `json:"scope"`
	EmptyMessage string `json:"empty_message,omitempty"`
}

type Genre struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Subjects []string `json:"subjects"`
}

var Genres = []Genre{
	{"romance", "Romance", []string{"romance"}},
	{"fantasy", "Fantasy", []string{"fantasy"}},
	{"science-fiction", "Science Fiction", []string{"science fiction"}},
	{"mystery", "Mystery", []string{"mystery"}},
	{"thriller", "Thriller", []string{"thriller"}},
	{"horror", "Horror", []string{"horror"}},
	{"historical-fiction", "Historical Fiction", []string{"historical fiction"}},
	{"literary-fiction", "Literary Fiction", []string{"literary fiction"}},
	{"biography-memoir", "Biography & Memoir", []string{"biography", "memoir"}},
	{"history", "History", []string{"history"}},
	{"science", "Science", []string{"science"}},
	{"self-help", "Self-Help", []string{"self-help"}},
}

func genreByID(id string) (Genre, bool) {
	for _, g := range Genres {
		if g.ID == id {
			return g, true
		}
	}
	return Genre{}, false
}

var workPattern = regexp.MustCompile(`^OL[1-9][0-9]{0,11}W$`)

func workID(raw string) string {
	id := strings.TrimPrefix(strings.TrimPrefix(raw, "ol:"), "/works/")
	if !workPattern.MatchString(id) {
		return ""
	}
	return id
}

func validCover(id int64) int64 {
	if id > 0 && id <= 9007199254740991 {
		return id
	}
	return 0
}
