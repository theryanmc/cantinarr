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
