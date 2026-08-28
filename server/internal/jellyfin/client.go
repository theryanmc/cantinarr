// Package jellyfin is the Jellyfin implementation of mediaserver.Provider:
// an API-key client that reads the server's libraries and users and manages
// the accounts Cantinarr provisions on it.
package jellyfin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/windoze95/cantinarr-server/internal/mediaserver"
	"github.com/windoze95/cantinarr-server/internal/transporterr"
)

// Client talks to one Jellyfin server with an API key. API keys hold the
// Administrator role on Jellyfin, which every call here relies on.
type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

var _ mediaserver.Provider = (*Client)(nil)

// NewClient creates a client for the server at baseURL. Redirects are never
// followed: a redirect would otherwise hand the API key to whatever host the
// server named.
func NewClient(baseURL, apiKey string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout:       30 * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

// statusError is a non-2xx answer. The body is kept for classification and
// deliberately never rendered: Jellyfin error bodies can echo request data.
type statusError struct {
	status int
	body   []byte
}

func (e *statusError) Error() string {
	switch e.status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return "jellyfin rejected the API key"
	default:
		return fmt.Sprintf("jellyfin returned status %d", e.status)
	}
}

// do performs one request. op names the operation in errors instead of the
// URL, so nothing about the host ever appears in an error string.
func (c *Client) do(ctx context.Context, method, path, op string, body, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("jellyfin %s: encode request: %w", op, err)
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return fmt.Errorf("jellyfin %s: invalid url", op)
	}
	req.Header.Set("Authorization", `MediaBrowser Token="`+c.apiKey+`", Client="Cantinarr", Device="Cantinarr", DeviceId="cantinarr", Version="1"`)
	req.Header.Set("X-Emby-Token", c.apiKey)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("jellyfin %s: %s", op, transporterr.Summarize(err))
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return fmt.Errorf("jellyfin %s: read response: %w", op, err)
	}
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return fmt.Errorf("jellyfin %s: server returned redirect status %d (redirects are not followed; use the server's final URL)", op, resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &statusError{status: resp.StatusCode, body: data}
	}
	if out != nil && len(bytes.TrimSpace(data)) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("jellyfin %s: decode response: %w", op, err)
		}
	}
	return nil
}

func statusOf(err error) int {
	var se *statusError
	if errors.As(err, &se) {
		return se.status
	}
	return 0
}

type systemInfo struct {
	ServerName string `json:"ServerName"`
	Version    string `json:"Version"`
	ID         string `json:"Id"`
}

// virtualFolder deliberately declares no Locations: those are server-side
// filesystem paths and must never travel further than Jellyfin itself.
type virtualFolder struct {
	Name           string `json:"Name"`
	CollectionType string `json:"CollectionType"`
	ItemID         string `json:"ItemId"`
}

type userDTO struct {
	ID     string          `json:"Id"`
	Name   string          `json:"Name"`
	Policy json.RawMessage `json:"Policy"`
}

type policyFlags struct {
	IsAdministrator bool `json:"IsAdministrator"`
	IsDisabled      bool `json:"IsDisabled"`
}

func (d userDTO) remoteUser() mediaserver.RemoteUser {
	var flags policyFlags
	if len(d.Policy) > 0 {
		_ = json.Unmarshal(d.Policy, &flags)
	}
	return mediaserver.RemoteUser{
		ID:              d.ID,
		Name:            d.Name,
		IsAdministrator: flags.IsAdministrator,
		IsDisabled:      flags.IsDisabled,
	}
}

// SystemInfo reads the server identity; it doubles as the connection test.
func (c *Client) SystemInfo(ctx context.Context) (mediaserver.SystemInfo, error) {
	var info systemInfo
	if err := c.do(ctx, http.MethodGet, "/System/Info", "system info", nil, &info); err != nil {
		return mediaserver.SystemInfo{}, err
	}
	return mediaserver.SystemInfo{ServerName: info.ServerName, Version: info.Version, ID: info.ID}, nil
}

// Libraries lists the server's libraries. The returned ID is the ItemId the
// user policy's EnabledFolders expects.
func (c *Client) Libraries(ctx context.Context) ([]mediaserver.Library, error) {
	var folders []virtualFolder
	if err := c.do(ctx, http.MethodGet, "/Library/VirtualFolders", "list libraries", nil, &folders); err != nil {
		return nil, err
	}
	out := make([]mediaserver.Library, 0, len(folders))
	for _, f := range folders {
		if f.ItemID == "" {
			continue
		}
		out = append(out, mediaserver.Library{ID: f.ItemID, Name: f.Name, CollectionType: f.CollectionType})
	}
	return out, nil
}

// Users lists every account on the server.
func (c *Client) Users(ctx context.Context) ([]mediaserver.RemoteUser, error) {
	var dtos []userDTO
	if err := c.do(ctx, http.MethodGet, "/Users", "list users", nil, &dtos); err != nil {
		return nil, err
	}
	out := make([]mediaserver.RemoteUser, 0, len(dtos))
	for _, d := range dtos {
		out = append(out, d.remoteUser())
	}
	return out, nil
}

func userPath(remoteID string) string {
	return "/Users/" + url.PathEscape(remoteID)
}

func (c *Client) getUser(ctx context.Context, remoteID string) (userDTO, error) {
	var dto userDTO
	err := c.do(ctx, http.MethodGet, userPath(remoteID), "get user", nil, &dto)
	if statusOf(err) == http.StatusNotFound {
		return userDTO{}, mediaserver.ErrUserNotFound
	}
	if err != nil {
		return userDTO{}, err
	}
	return dto, nil
}

// GetUser reads one account; ErrUserNotFound when it no longer exists.
func (c *Client) GetUser(ctx context.Context, remoteID string) (mediaserver.RemoteUser, error) {
	dto, err := c.getUser(ctx, remoteID)
	if err != nil {
		return mediaserver.RemoteUser{}, err
	}
	return dto.remoteUser(), nil
}

// updatePolicy is the single path for every policy change. Jellyfin's
// POST /Users/{id}/Policy replaces the whole policy and validates required
// fields, so the policy is always fetched fresh, mutated as a map (unknown
// fields and numbers round-trip untouched), and posted back in full.
func (c *Client) updatePolicy(ctx context.Context, remoteID string, mutate func(policy map[string]any)) error {
	dto, err := c.getUser(ctx, remoteID)
	if err != nil {
		return err
	}
	policy := map[string]any{}
	if len(dto.Policy) > 0 {
		dec := json.NewDecoder(bytes.NewReader(dto.Policy))
		dec.UseNumber()
		if err := dec.Decode(&policy); err != nil {
			return fmt.Errorf("jellyfin get user: decode policy: %w", err)
		}
	}
	if len(policy) == 0 {
		return errors.New("jellyfin get user: policy missing from response")
	}
	mutate(policy)
	return c.do(ctx, http.MethodPost, userPath(remoteID)+"/Policy", "update user policy", policy, nil)
}

func setLibraries(policy map[string]any, libraryIDs []string) {
	if len(libraryIDs) == 0 {
		policy["EnableAllFolders"] = true
		policy["EnabledFolders"] = []string{}
		return
	}
	policy["EnableAllFolders"] = false
	policy["EnabledFolders"] = append([]string(nil), libraryIDs...)
}

// CreateUser implements mediaserver.Provider.
func (c *Client) CreateUser(ctx context.Context, name, password string, libraryIDs []string) (mediaserver.RemoteUser, error) {
	if !mediaserver.ValidUsername(name) {
		return mediaserver.RemoteUser{}, mediaserver.ErrInvalidName
	}
	// Jellyfin compares names case-insensitively; pre-check so a collision is
	// reported deterministically instead of depending on the 400 body.
	existing, err := c.Users(ctx)
	if err != nil {
		return mediaserver.RemoteUser{}, err
	}
	for _, u := range existing {
		if strings.EqualFold(u.Name, name) {
			return mediaserver.RemoteUser{}, mediaserver.ErrUserExists
		}
	}

	var created userDTO
	body := struct {
		Name     string `json:"Name"`
		Password string `json:"Password"`
	}{Name: name, Password: password}
	if err := c.do(ctx, http.MethodPost, "/Users/New", "create user", body, &created); err != nil {
		var se *statusError
		if errors.As(err, &se) && se.status == http.StatusBadRequest && bytes.Contains(bytes.ToLower(se.body), []byte("exist")) {
			return mediaserver.RemoteUser{}, mediaserver.ErrUserExists
		}
		return mediaserver.RemoteUser{}, err
	}
	if created.ID == "" {
		return mediaserver.RemoteUser{}, errors.New("jellyfin create user: response carried no user id")
	}

	if err := c.updatePolicy(ctx, created.ID, func(policy map[string]any) {
		policy["IsAdministrator"] = false
		setLibraries(policy, libraryIDs)
	}); err != nil {
		// Never leave an unrestricted account behind. Rollback uses a fresh
		// context so a cancelled create still cleans up.
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		if delErr := c.DeleteUser(cleanupCtx, created.ID); delErr != nil {
			return mediaserver.RemoteUser{}, errors.Join(fmt.Errorf("restrict new user: %w", err), fmt.Errorf("roll back new user: %w", delErr))
		}
		return mediaserver.RemoteUser{}, fmt.Errorf("restrict new user: %w", err)
	}
	return mediaserver.RemoteUser{ID: created.ID, Name: created.Name}, nil
}

// SetLibraries implements mediaserver.Provider.
func (c *Client) SetLibraries(ctx context.Context, remoteID string, libraryIDs []string) error {
	return c.updatePolicy(ctx, remoteID, func(policy map[string]any) { setLibraries(policy, libraryIDs) })
}

// SetDisabled implements mediaserver.Provider.
func (c *Client) SetDisabled(ctx context.Context, remoteID string, disabled bool) error {
	return c.updatePolicy(ctx, remoteID, func(policy map[string]any) { policy["IsDisabled"] = disabled })
}

// DeleteUser implements mediaserver.Provider. An account that is already
// gone counts as deleted.
func (c *Client) DeleteUser(ctx context.Context, remoteID string) error {
	err := c.do(ctx, http.MethodDelete, userPath(remoteID), "delete user", nil, nil)
	if statusOf(err) == http.StatusNotFound {
		return nil
	}
	return err
}
