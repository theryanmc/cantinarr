// Package serversettings stores small, admin-editable, server-wide preferences
// in the settings key/value table (mirroring the remediation/request settings
// pattern). It holds the optional management-portal URL that the "update
// available" banner links to, and the discovery preferences that decide which
// feed backs the headline discovery rows.
package serversettings

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

const settingsKey = "server_settings"

// Discovery sources for the headline rows. TMDB's own popularity value is a
// lifetime score, so DiscoverySourceTMDBPopular silts up with decade-old
// procedurals and nightly talk shows; the two trending feeds are short-window
// and self-correcting, which is why one of them is the default.
const (
	// DiscoverySourceTMDBTrending is TMDB's weekly trending feed. It needs no
	// credential beyond the TMDB key discovery already requires.
	DiscoverySourceTMDBTrending = "tmdb_trending"
	// DiscoverySourceTraktTrending is Trakt's trending feed, ranked by how many
	// people are watching right now. Requires Trakt to be configured.
	DiscoverySourceTraktTrending = "trakt_trending"
	// DiscoverySourceTMDBPopular is TMDB's all-time popularity ranking.
	DiscoverySourceTMDBPopular = "tmdb_popular"

	// DefaultDiscoverySource backs the rows when an admin has not chosen one.
	DefaultDiscoverySource = DiscoverySourceTMDBTrending
)

// DiscoverySources lists every valid source, in the order the admin UI offers
// them.
func DiscoverySources() []string {
	return []string{
		DiscoverySourceTMDBTrending,
		DiscoverySourceTraktTrending,
		DiscoverySourceTMDBPopular,
	}
}

// Settings is the server-wide admin preferences blob. It is stored as JSON and
// unmarshalled over the zero value, so adding a field later is migration-free.
type Settings struct {
	// ManagementURL is an optional link to the admin's own container-management
	// portal (e.g. an Unraid or Portainer page). Empty means "not configured".
	ManagementURL string `json:"management_url"`

	// DiscoverySource picks which feed backs the headline discovery rows.
	// Empty is read back as DefaultDiscoverySource.
	DiscoverySource string `json:"discovery_source"`

	// DiscoveryEnglishOnly drops titles whose original language is not English
	// from the discovery rows. Search and detail lookups are never filtered —
	// a title you went looking for is always findable.
	DiscoveryEnglishOnly bool `json:"discovery_english_only"`
}

// Service reads and writes the server settings blob.
type Service struct {
	db *sql.DB
}

// NewService returns a settings service backed by the given database.
func NewService(db *sql.DB) *Service {
	return &Service{db: db}
}

// Get returns the stored settings, or the defaults when none are saved.
func (s *Service) Get() Settings {
	out := s.raw()
	out.ManagementURL = strings.TrimSpace(out.ManagementURL)
	out.DiscoverySource = normalizeDiscoverySource(out.DiscoverySource)
	return out
}

// raw reads the stored blob without filling defaults in, so callers that need
// to tell "never set" from "set to the value that happens to be the default"
// can. Get normalizes on top of this; DiscoveryChosen does not.
func (s *Service) raw() Settings {
	var out Settings
	var v string
	if err := s.db.QueryRow("SELECT value FROM settings WHERE key = ?", settingsKey).Scan(&v); err == nil && v != "" {
		_ = json.Unmarshal([]byte(v), &out)
	}
	return out
}

// DiscoveryChosen reports whether an admin has ever saved a discovery
// preference. Get normalizes an empty source onto the default, which makes an
// untouched install indistinguishable from a deliberate "TMDB trending" — but
// the setup checklist needs exactly that distinction: every discovery answer is
// valid, so the checklist item asks "have you decided", not "did you pick the
// one we like". Any save flips this, which is what keeps the item from nagging
// an admin who is happy with the defaults.
func (s *Service) DiscoveryChosen() bool {
	return strings.TrimSpace(s.raw().DiscoverySource) != ""
}

// SetManagementURL stores the management-portal URL, leaving every other
// preference untouched. Each setter reads-modifies-writes the one blob so a
// caller that only knows about its own field cannot wipe the rest. The
// read side is raw on purpose: writing back a normalized blob would stamp a
// discovery source nobody chose and falsely satisfy DiscoveryChosen.
func (s *Service) SetManagementURL(raw string) (Settings, error) {
	next := s.raw()
	next.ManagementURL = strings.TrimSpace(raw)
	if err := validateURL(next.ManagementURL); err != nil {
		return Settings{}, err
	}
	return s.save(next)
}

// SetDiscovery stores the discovery preferences, leaving every other
// preference untouched. The stored source is always concrete, so saving any
// choice — including the default the screen loaded with — records a decision.
func (s *Service) SetDiscovery(source string, englishOnly bool) (Settings, error) {
	if err := validateDiscoverySource(source); err != nil {
		return Settings{}, err
	}
	next := s.raw()
	next.DiscoverySource = normalizeDiscoverySource(source)
	next.DiscoveryEnglishOnly = englishOnly
	return s.save(next)
}

// save persists the blob verbatim and hands back the normalized view, so
// storage keeps "never set" while every caller still sees a usable source.
func (s *Service) save(in Settings) (Settings, error) {
	data, err := json.Marshal(in)
	if err != nil {
		return Settings{}, fmt.Errorf("encode server settings: %w", err)
	}
	if _, err := s.db.Exec("INSERT OR REPLACE INTO settings (key, value) VALUES (?, ?)", settingsKey, string(data)); err != nil {
		return Settings{}, fmt.Errorf("save server settings: %w", err)
	}
	out := in
	out.ManagementURL = strings.TrimSpace(out.ManagementURL)
	out.DiscoverySource = normalizeDiscoverySource(out.DiscoverySource)
	return out, nil
}

// normalizeDiscoverySource maps empty or unrecognized stored values onto the
// default, so a hand-edited or downgraded database still serves a working row.
func normalizeDiscoverySource(raw string) string {
	switch strings.TrimSpace(raw) {
	case DiscoverySourceTMDBTrending, DiscoverySourceTraktTrending, DiscoverySourceTMDBPopular:
		return strings.TrimSpace(raw)
	default:
		return DefaultDiscoverySource
	}
}

// validateDiscoverySource rejects a source the admin UI could not have offered,
// so a typo in an API call fails loudly instead of silently reverting.
func validateDiscoverySource(raw string) error {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	for _, valid := range DiscoverySources() {
		if trimmed == valid {
			return nil
		}
	}
	return fmt.Errorf("discovery_source must be one of %s", strings.Join(DiscoverySources(), ", "))
}

// validateURL accepts an empty string (clears the setting) or an absolute
// http(s) URL; anything else is rejected so the banner never links somewhere
// unusable.
func validateURL(raw string) error {
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("management_url must be an http(s) URL")
	}
	return nil
}
