package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/credentials"
	"github.com/windoze95/cantinarr-server/internal/db"
	"github.com/windoze95/cantinarr-server/internal/secrets"
	"github.com/windoze95/cantinarr-server/internal/serversettings"
)

// newDiscoverySettingsEnv builds the handlers over a real settings service and
// credentials registry. Authorization is covered by the RBAC route sweep, which
// walks every /api/admin/ pattern; these tests cover the payload contract.
func newDiscoverySettingsEnv(t *testing.T, traktConfigured bool) (*serversettings.Service, *credentials.Registry) {
	t.Helper()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	cipher, err := secrets.NewCipher(bytes.Repeat([]byte{0x42}, 32))
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	creds := credentials.NewRegistry(database, cipher)
	if traktConfigured {
		if err := creds.SetCredential(credentials.KeyTraktClientID, "TRAKT_CLIENT_ID"); err != nil {
			t.Fatalf("set Trakt credential: %v", err)
		}
	}
	return serversettings.NewService(database), creds
}

func decodeDiscoveryResponse(t *testing.T, body string) discoverySettingsResponse {
	t.Helper()
	var out discoverySettingsResponse
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("decode discovery settings: %v (body = %s)", err, body)
	}
	return out
}

// TestDiscoverySettingsReportsChoicesAndTraktAvailability gives the admin UI
// what it needs to render an honest picker: every source, plus whether Trakt
// can actually be selected.
func TestDiscoverySettingsReportsChoicesAndTraktAvailability(t *testing.T) {
	for _, traktConfigured := range []bool{true, false} {
		settings, creds := newDiscoverySettingsEnv(t, traktConfigured)
		rec := httptest.NewRecorder()
		discoverySettingsHandler(settings, creds)(rec, httptest.NewRequest(http.MethodGet, "/", nil))

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
		}
		got := decodeDiscoveryResponse(t, rec.Body.String())
		if got.Source != serversettings.DefaultDiscoverySource {
			t.Errorf("source = %q, want the default %q", got.Source, serversettings.DefaultDiscoverySource)
		}
		if len(got.Sources) != len(serversettings.DiscoverySources()) {
			t.Errorf("sources = %v, want all %d choices", got.Sources, len(serversettings.DiscoverySources()))
		}
		if got.TraktConfigured != traktConfigured {
			t.Errorf("trakt_configured = %t, want %t", got.TraktConfigured, traktConfigured)
		}
	}
}

// TestUpdateDiscoverySettingsRoundTrips proves a save is readable back, which
// is what the screen relies on to show the state it just wrote.
func TestUpdateDiscoverySettingsRoundTrips(t *testing.T) {
	settings, creds := newDiscoverySettingsEnv(t, true)

	body := `{"source":"trakt_trending","english_only":true}`
	rec := httptest.NewRecorder()
	updateDiscoverySettingsHandler(settings, creds)(
		rec, httptest.NewRequest(http.MethodPut, "/", strings.NewReader(body)))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	saved := decodeDiscoveryResponse(t, rec.Body.String())
	if saved.Source != serversettings.DiscoverySourceTraktTrending || !saved.EnglishOnly {
		t.Errorf("saved = (%q, %t), want (trakt_trending, true)", saved.Source, saved.EnglishOnly)
	}

	rec = httptest.NewRecorder()
	discoverySettingsHandler(settings, creds)(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	reread := decodeDiscoveryResponse(t, rec.Body.String())
	if reread.Source != saved.Source || reread.EnglishOnly != saved.EnglishOnly {
		t.Errorf("re-read = (%q, %t), want the saved values", reread.Source, reread.EnglishOnly)
	}
}

// TestUpdateDiscoverySettingsRejectsBadInput keeps a malformed call from
// landing in the settings blob.
func TestUpdateDiscoverySettingsRejectsBadInput(t *testing.T) {
	cases := map[string]string{
		"unknown source": `{"source":"netflix_top_10"}`,
		"malformed body": `{"source":`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			settings, creds := newDiscoverySettingsEnv(t, true)
			rec := httptest.NewRecorder()
			updateDiscoverySettingsHandler(settings, creds)(
				rec, httptest.NewRequest(http.MethodPut, "/", strings.NewReader(body)))

			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", rec.Code)
			}
			if got := settings.Get().DiscoverySource; got != serversettings.DefaultDiscoverySource {
				t.Errorf("stored source = %q, want the rejected write to have changed nothing", got)
			}
		})
	}
}
