package push

import "testing"

// TestClaimContentAlertDedupes pins the new-content dedupe: the queue-poll
// witness and the arr webhook receiver can both report the same import (and a
// season pack arrives as one webhook per file), but only the first claim in
// the window may send. Distinct content is never suppressed.
func TestClaimContentAlertDedupes(t *testing.T) {
	n := &Notifier{}

	if !n.claimContentAlert(CategoryNewEpisode, "tv", "700", "Gappy Show") {
		t.Fatal("first claim must send")
	}
	if n.claimContentAlert(CategoryNewEpisode, "tv", "700", "Gappy Show") {
		t.Error("duplicate claim within the window must be suppressed")
	}
	if !n.claimContentAlert(CategoryNewMovie, "movie", "700", "Gappy Show") {
		t.Error("a different category is different content")
	}
	if !n.claimContentAlert(CategoryNewEpisode, "tv", "0", "Other Show") {
		t.Error("tmdb 0 with a different title is different content")
	}
	if n.claimContentAlert(CategoryNewEpisode, "tv", "0", "Other Show") {
		t.Error("tmdb 0 duplicates of the same title must still dedupe")
	}

	// Books key on foreignBookId plus format: a title's ebook and audiobook
	// are separate records that may import minutes apart, so one format's
	// alert must never swallow the other's.
	if !n.claimContentAlert(CategoryNewBook, "book", "29749107|ebook", "Ahsoka") {
		t.Error("first book claim must send")
	}
	if n.claimContentAlert(CategoryNewBook, "book", "29749107|ebook", "Ahsoka") {
		t.Error("duplicate book format claim must be suppressed")
	}
	if !n.claimContentAlert(CategoryNewBook, "book", "29749107|audiobook", "Ahsoka") {
		t.Error("the sibling format of the same book is different content")
	}
}
