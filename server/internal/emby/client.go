// Package emby is the Emby implementation of mediaserver.Provider: an API-key
// client that reads the server's libraries and users and manages the accounts
// Cantinarr provisions on it. Emby and Jellyfin share an ancestry and most
// routes, but differ where it matters here: an Emby account is created
// without a password and gets one in a second call, its libraries are
// addressed by the folder Guid, and its name rule is closed-source.
package emby

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

// Client talks to one Emby server with an API key. API keys act as an
// administrator on Emby, which every call here relies on.
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
// deliberately never rendered: Emby error bodies can echo request data.
type statusError struct {
	status int
	body   []byte
}

func (e *statusError) Error() string {
	switch e.status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return "emby rejected the API key"
	default:
		return fmt.Sprintf("emby returned status %d", e.status)
	}
}

// do performs one request. op names the operation in errors instead of the
// URL, so nothing about the host ever appears in an error string.
func (c *Client) do(ctx context.Context, method, path, op string, body, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("emby %s: encode request: %w", op, err)
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return fmt.Errorf("emby %s: invalid url", op)
	}
	// X-Emby-Token is what an API key travels in; X-Emby-Authorization is
	// where Emby's own clients name themselves, and it is read first.
	req.Header.Set("X-Emby-Token", c.apiKey)
	req.Header.Set("X-Emby-Authorization", `MediaBrowser Client="Cantinarr", Device="Cantinarr", DeviceId="cantinarr", Version="1", Token="`+c.apiKey+`"`)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("emby %s: %s", op, transporterr.Summarize(err))
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return fmt.Errorf("emby %s: read response: %w", op, err)
	}
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return fmt.Errorf("emby %s: server returned redirect status %d (redirects are not followed; use the server's final URL)", op, resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &statusError{status: resp.StatusCode, body: data}
	}
	if out != nil && len(bytes.TrimSpace(data)) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("emby %s: decode response: %w", op, err)
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

// flexString decodes a JSON string or number as a string. Emby's item ids
// became numeric internally in 4.7 and have travelled as either since.
type flexString string

func (s *flexString) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || bytes.Equal(data, []byte("null")) {
		*s = ""
		return nil
	}
	if data[0] == '"' {
		var v string
		if err := json.Unmarshal(data, &v); err != nil {
			return err
		}
		*s = flexString(v)
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(data, &n); err != nil {
		return err
	}
	*s = flexString(n.String())
	return nil
}

type systemInfo struct {
	ServerName string `json:"ServerName"`
	Version    string `json:"Version"`
	ID         string `json:"Id"`
}

// mediaFolder deliberately declares no Path: that is a server-side
// filesystem path and must never travel further than Emby itself.
type mediaFolder struct {
	Name           string     `json:"Name"`
	ID             flexString `json:"Id"`
	GUID           string     `json:"Guid"`
	CollectionType string     `json:"CollectionType"`
}

type mediaFoldersResponse struct {
	Items []mediaFolder `json:"Items"`
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

// Libraries lists the server's libraries. The returned ID is the folder Guid
// the user policy's EnabledFolders expects (Emby 4.7 made the plain Id a
// number that the policy no longer matches); the Id is the fallback for a
// server that reports no Guid.
func (c *Client) Libraries(ctx context.Context) ([]mediaserver.Library, error) {
	var resp mediaFoldersResponse
	if err := c.do(ctx, http.MethodGet, "/Library/MediaFolders", "list libraries", nil, &resp); err != nil {
		return nil, err
	}
	out := make([]mediaserver.Library, 0, len(resp.Items))
	for _, f := range resp.Items {
		id := f.GUID
		if id == "" {
			id = string(f.ID)
		}
		if id == "" {
			continue
		}
		out = append(out, mediaserver.Library{ID: id, Name: f.Name, CollectionType: f.CollectionType})
	}
	return out, nil
}

// Users lists every account on the server. Emby has answered this route
// both as a bare list and as a {Items, TotalRecordCount} query result, so
// both shapes are accepted.
func (c *Client) Users(ctx context.Context) ([]mediaserver.RemoteUser, error) {
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodGet, "/Users", "list users", nil, &raw); err != nil {
		return nil, err
	}
	var dtos []userDTO
	trimmed := bytes.TrimSpace(raw)
	switch {
	case len(trimmed) == 0:
	case trimmed[0] == '[':
		if err := json.Unmarshal(trimmed, &dtos); err != nil {
			return nil, fmt.Errorf("emby list users: decode response: %w", err)
		}
	default:
		var wrapped struct {
			Items []userDTO `json:"Items"`
		}
		if err := json.Unmarshal(trimmed, &wrapped); err != nil {
			return nil, fmt.Errorf("emby list users: decode response: %w", err)
		}
		dtos = wrapped.Items
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

// updatePolicy is the single path for every policy change. Emby's
// POST /Users/{id}/Policy replaces the whole policy, so it is always fetched
// fresh, mutated as a map (unknown fields and numbers round-trip untouched),
// and posted back in full — the same round trip Emby's own web UI makes.
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
			return fmt.Errorf("emby get user: decode policy: %w", err)
		}
	}
	if len(policy) == 0 {
		return errors.New("emby get user: policy missing from response")
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

// setPassword gives a freshly created account its password. CurrentPw is
// sent empty on purpose: Emby has verified the current password on this
// route, and a new account's current password is exactly that.
func (c *Client) setPassword(ctx context.Context, remoteID, password string) error {
	body := struct {
		ID            string `json:"Id"`
		CurrentPw     string `json:"CurrentPw"`
		NewPw         string `json:"NewPw"`
		ResetPassword bool   `json:"ResetPassword"`
	}{ID: remoteID, CurrentPw: "", NewPw: password, ResetPassword: false}
	return c.do(ctx, http.MethodPost, userPath(remoteID)+"/Password", "set password", body, nil)
}

// rollback deletes a half-made account and returns cause, joined with the
// delete's own error if that failed too. It uses a fresh context so a
// cancelled create still cleans up.
func (c *Client) rollback(ctx context.Context, remoteID string, cause error) error {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if delErr := c.DeleteUser(cleanupCtx, remoteID); delErr != nil {
		return errors.Join(cause, fmt.Errorf("roll back new user: %w", delErr))
	}
	return cause
}

// rollbackByName deletes an account that carries name, for the case where
// the create request may have reached the server but its answer never came
// back readable (a cut connection, a timeout, an unreadable body). The
// pre-check proved no account carried this name a moment ago, so one that
// does now is the one just made. Runs on a fresh context.
func (c *Client) rollbackByName(ctx context.Context, name string) error {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	users, err := c.Users(cleanupCtx)
	if err != nil {
		return fmt.Errorf("look up new user for roll back: %w", err)
	}
	for _, u := range users {
		if strings.EqualFold(u.Name, name) {
			if err := c.DeleteUser(cleanupCtx, u.ID); err != nil {
				return fmt.Errorf("roll back new user: %w", err)
			}
			return nil
		}
	}
	return nil
}

// CreateUser implements mediaserver.Provider. Emby creates the account
// without a password, so the password is a second call and the policy a
// third; a failure at either step deletes the account, because an Emby
// account with no password signs in with an empty one.
func (c *Client) CreateUser(ctx context.Context, name, password string, libraryIDs []string) (mediaserver.RemoteUser, error) {
	if !mediaserver.ValidUsername(name) {
		return mediaserver.RemoteUser{}, mediaserver.ErrInvalidName
	}
	// Emby compares names case-insensitively; pre-check so a collision is
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
		Name string `json:"Name"`
	}{Name: name}
	if err := c.do(ctx, http.MethodPost, "/Users/New", "create user", body, &created); err != nil {
		var se *statusError
		if errors.As(err, &se) && se.status == http.StatusBadRequest {
			if bytes.Contains(bytes.ToLower(se.body), []byte("exist")) {
				return mediaserver.RemoteUser{}, mediaserver.ErrUserExists
			}
			// Emby's name rule is closed-source; a 400 that is not a
			// duplicate is the name, and the admin can link an account.
			return mediaserver.RemoteUser{}, mediaserver.ErrInvalidName
		}
		if se == nil {
			// Not a refusal: the server may have made the account and the
			// answer got lost. Never leave a password-less account behind.
			if rbErr := c.rollbackByName(ctx, name); rbErr != nil {
				return mediaserver.RemoteUser{}, errors.Join(err, rbErr)
			}
		}
		return mediaserver.RemoteUser{}, err
	}
	if created.ID == "" {
		err := errors.New("emby create user: response carried no user id")
		if rbErr := c.rollbackByName(ctx, name); rbErr != nil {
			return mediaserver.RemoteUser{}, errors.Join(err, rbErr)
		}
		return mediaserver.RemoteUser{}, err
	}

	if err := c.setPassword(ctx, created.ID, password); err != nil {
		return mediaserver.RemoteUser{}, c.rollback(ctx, created.ID, fmt.Errorf("set password: %w", err))
	}
	if err := c.updatePolicy(ctx, created.ID, func(policy map[string]any) {
		policy["IsAdministrator"] = false
		setLibraries(policy, libraryIDs)
	}); err != nil {
		return mediaserver.RemoteUser{}, c.rollback(ctx, created.ID, fmt.Errorf("restrict new user: %w", err))
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
