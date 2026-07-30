package serversettings

import (
	"testing"

	"github.com/windoze95/cantinarr-server/internal/db"
)

func newTestService(t *testing.T) *Service {
	t.Helper()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return NewService(database)
}

// TestGetDefaultsToATrendingSource pins the shipped default: a server nobody
// has configured still serves a short-window feed, not TMDB's lifetime
// popularity ranking.
func TestGetDefaultsToATrendingSource(t *testing.T) {
	s := newTestService(t)
	got := s.Get()
	if got.DiscoverySource != DiscoverySourceTMDBTrending {
		t.Errorf("DiscoverySource = %q, want %q", got.DiscoverySource, DiscoverySourceTMDBTrending)
	}
	if got.DiscoveryEnglishOnly {
		t.Error("DiscoveryEnglishOnly = true, want false — hiding titles is opt-in")
	}
}

// TestSettersPreserveEachOthersFields is the regression guard for the one blob:
// each setter knows only its own fields, so a whole-struct write would silently
// clear whatever the caller did not know about.
func TestSettersPreserveEachOthersFields(t *testing.T) {
	s := newTestService(t)

	if _, err := s.SetManagementURL("http://tower.local/Docker"); err != nil {
		t.Fatalf("SetManagementURL: %v", err)
	}
	if _, err := s.SetDiscovery(DiscoverySourceTraktTrending, true); err != nil {
		t.Fatalf("SetDiscovery: %v", err)
	}

	got := s.Get()
	if got.ManagementURL != "http://tower.local/Docker" {
		t.Errorf("ManagementURL = %q, want it preserved through the discovery write", got.ManagementURL)
	}
	if got.DiscoverySource != DiscoverySourceTraktTrending || !got.DiscoveryEnglishOnly {
		t.Errorf("discovery = (%q, %t), want (%q, true)",
			got.DiscoverySource, got.DiscoveryEnglishOnly, DiscoverySourceTraktTrending)
	}

	// And the other direction.
	if _, err := s.SetManagementURL("https://portainer.example.com"); err != nil {
		t.Fatalf("SetManagementURL: %v", err)
	}
	got = s.Get()
	if got.DiscoverySource != DiscoverySourceTraktTrending || !got.DiscoveryEnglishOnly {
		t.Errorf("discovery = (%q, %t) after a management-URL write, want it preserved",
			got.DiscoverySource, got.DiscoveryEnglishOnly)
	}
}

// TestSetDiscoveryRejectsUnknownSources fails a typo loudly instead of storing
// it and quietly reverting to the default on the next read.
func TestSetDiscoveryRejectsUnknownSources(t *testing.T) {
	s := newTestService(t)
	if _, err := s.SetDiscovery("netflix_top_10", false); err == nil {
		t.Fatal("SetDiscovery accepted an unknown source, want an error")
	}
	if got := s.Get().DiscoverySource; got != DiscoverySourceTMDBTrending {
		t.Errorf("DiscoverySource = %q, want the rejected write to have changed nothing", got)
	}
}

// TestSetDiscoveryAcceptsEmptyAsTheDefault lets a client clear the choice
// without having to know which source is currently the default.
func TestSetDiscoveryAcceptsEmptyAsTheDefault(t *testing.T) {
	s := newTestService(t)
	if _, err := s.SetDiscovery(DiscoverySourceTMDBPopular, false); err != nil {
		t.Fatalf("SetDiscovery: %v", err)
	}
	if _, err := s.SetDiscovery("", false); err != nil {
		t.Fatalf("SetDiscovery(\"\"): %v", err)
	}
	if got := s.Get().DiscoverySource; got != DefaultDiscoverySource {
		t.Errorf("DiscoverySource = %q, want %q", got, DefaultDiscoverySource)
	}
}

// TestDiscoveryChosenTracksARealDecision covers what the setup checklist keys
// on. Every discovery answer is valid, so the checklist item can only ask
// whether the admin decided — which means "chosen" has to survive the fact that
// the default and a deliberate pick of the default look identical through Get.
func TestDiscoveryChosenTracksARealDecision(t *testing.T) {
	s := newTestService(t)
	if s.DiscoveryChosen() {
		t.Error("DiscoveryChosen = true on a fresh server, want false")
	}

	// Saving the value the screen already loaded is still a decision, so an
	// admin who is happy with the defaults can finish the step in one tap.
	if _, err := s.SetDiscovery(DefaultDiscoverySource, false); err != nil {
		t.Fatalf("SetDiscovery: %v", err)
	}
	if !s.DiscoveryChosen() {
		t.Error("DiscoveryChosen = false after saving the default, want true")
	}
}

// TestManagementURLWriteIsNotADiscoveryDecision is the regression guard for the
// read-modify-write: a setter that round-trips the normalized blob would stamp
// a discovery source nobody picked and silently tick the checklist item off.
func TestManagementURLWriteIsNotADiscoveryDecision(t *testing.T) {
	s := newTestService(t)
	if _, err := s.SetManagementURL("http://tower.local/Docker"); err != nil {
		t.Fatalf("SetManagementURL: %v", err)
	}
	if s.DiscoveryChosen() {
		t.Error("DiscoveryChosen = true after a management-URL write, want false")
	}
	if got := s.Get().DiscoverySource; got != DefaultDiscoverySource {
		t.Errorf("DiscoverySource = %q, want reads to still serve %q", got, DefaultDiscoverySource)
	}
}

// TestGetNormalizesAnUnrecognizedStoredSource keeps a hand-edited or
// downgraded database serving a working row rather than an empty one.
func TestGetNormalizesAnUnrecognizedStoredSource(t *testing.T) {
	s := newTestService(t)
	if _, err := s.db.Exec(
		"INSERT OR REPLACE INTO settings (key, value) VALUES (?, ?)",
		settingsKey, `{"discovery_source":"from_a_newer_build"}`,
	); err != nil {
		t.Fatalf("seed settings: %v", err)
	}
	if got := s.Get().DiscoverySource; got != DefaultDiscoverySource {
		t.Errorf("DiscoverySource = %q, want the default %q", got, DefaultDiscoverySource)
	}
}
