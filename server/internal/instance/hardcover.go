package instance

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/windoze95/cantinarr-server/internal/httpx"
)

// Hardcover is the book community whose GraphQL API Chaptarr's metadata
// already points at (hc: identities). An admin connects a Chaptarr instance
// to Hardcover by pasting an account's API token here. The token is a
// per-instance secret with the same contract as the arr API key: held
// encrypted at rest, write-only through the API, never logged, and gone with
// the instance. Chaptarr holds its own copy of the same token and correctly
// refuses to hand it back, so this is the only copy Cantinarr can call with.

// hardcoverAPIURL is Hardcover's GraphQL endpoint. Handlers dial through
// h.hardcoverAPIURL so tests can stand in for it.
const hardcoverAPIURL = "https://api.hardcover.app/v1/graphql"

// hardcoverTokenMaxLen bounds a pasted token. Hardcover issues JWTs of a few
// hundred bytes; anything past this is not a token.
const hardcoverTokenMaxLen = 4096

// errHardcoverRejected means Hardcover answered and said the token is not
// valid, as opposed to Hardcover being unreachable.
var errHardcoverRejected = errors.New("Hardcover rejected the API token")

// SupportsHardcover reports whether a service type carries a Hardcover token.
func SupportsHardcover(serviceType string) bool { return serviceType == "chaptarr" }

// HasHardcoverToken reports whether an instance holds a Hardcover token
// without decrypting it: whether the slot is empty is stored metadata, so
// this read never needs the encryption key.
func (s *Store) HasHardcoverToken(id string) (bool, error) {
	var stored string
	err := s.db.QueryRow(
		"SELECT hardcover_token FROM service_instances WHERE id = ?", id,
	).Scan(&stored)
	if err == sql.ErrNoRows {
		return false, fmt.Errorf("instance not found: %s", id)
	}
	if err != nil {
		return false, fmt.Errorf("get hardcover status: %w", err)
	}
	return stored != "", nil
}

// HardcoverToken returns the decrypted Hardcover token for an instance, or
// "" when none is connected.
func (s *Store) HardcoverToken(id string) (string, error) {
	var stored string
	err := s.db.QueryRow(
		"SELECT hardcover_token FROM service_instances WHERE id = ?", id,
	).Scan(&stored)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("instance not found: %s", id)
	}
	if err != nil {
		return "", fmt.Errorf("get hardcover token: %w", err)
	}
	if stored == "" {
		return "", nil
	}
	token, err := s.cipher.Decrypt(stored)
	if err != nil {
		return "", fmt.Errorf("decrypt hardcover token for %s (wrong encryption key?): %w", id, err)
	}
	return token, nil
}

// SetHardcoverToken stores a verified token, encrypted at rest. Only that
// column moves, so a concurrent admin save of the rest of the instance is
// never overwritten.
func (s *Store) SetHardcoverToken(id, token string) error {
	if token == "" {
		return errors.New("hardcover token is required")
	}
	encrypted, err := s.cipher.Encrypt(token)
	if err != nil {
		return fmt.Errorf("encrypt hardcover token: %w", err)
	}
	res, err := s.db.Exec(
		"UPDATE service_instances SET hardcover_token = ? WHERE id = ?", encrypted, id,
	)
	if err != nil {
		return fmt.Errorf("store hardcover token: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("instance not found: %s", id)
	}
	return nil
}

// ClearHardcoverToken disconnects Hardcover from an instance.
func (s *Store) ClearHardcoverToken(id string) error {
	res, err := s.db.Exec(
		"UPDATE service_instances SET hardcover_token = '' WHERE id = ?", id,
	)
	if err != nil {
		return fmt.Errorf("clear hardcover token: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("instance not found: %s", id)
	}
	return nil
}

// HardcoverStatus answers GET /instances/{id}/hardcover: whether this
// instance type can hold a Hardcover token and whether one is connected. The
// token itself never appears.
func (h *Handler) HardcoverStatus(w http.ResponseWriter, r *http.Request) {
	inst, ok := h.hardcoverInstance(w, r)
	if !ok {
		return
	}
	if !SupportsHardcover(inst.ServiceType) {
		writeHardcoverStatus(w, false, false)
		return
	}
	configured, err := h.store.HasHardcoverToken(inst.ID)
	if err != nil {
		http.Error(w, `{"error":"failed to read hardcover status"}`, http.StatusInternalServerError)
		return
	}
	writeHardcoverStatus(w, true, configured)
}

// SaveHardcoverToken answers PUT /instances/{id}/hardcover with {token}. The
// token is verified against Hardcover before anything is stored -- a token
// Hardcover rejects is a 400 and leaves the previous connection in place; a
// Hardcover that cannot be reached is a 502, so blindness never reads as a
// bad token.
func (h *Handler) SaveHardcoverToken(w http.ResponseWriter, r *http.Request) {
	inst, ok := h.hardcoverInstance(w, r)
	if !ok {
		return
	}
	if !SupportsHardcover(inst.ServiceType) {
		http.Error(w, `{"error":"Hardcover connects to Chaptarr instances only"}`, http.StatusBadRequest)
		return
	}
	var body struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	token, err := normalizeHardcoverToken(body.Token)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusBadRequest)
		return
	}
	if err := verifyHardcoverToken(r.Context(), h.hardcoverAPIURL, token); err != nil {
		if errors.Is(err, errHardcoverRejected) {
			http.Error(w, `{"error":"Hardcover rejected the API token. Copy it again from Hardcover's settings and try once more."}`, http.StatusBadRequest)
			return
		}
		// The token is never part of err; log the reachability failure and
		// tell the admin which side did not answer.
		log.Printf("instance: hardcover token verification for %s failed: %v", inst.ID, err)
		http.Error(w, `{"error":"could not reach Hardcover to verify the token"}`, http.StatusBadGateway)
		return
	}
	if err := h.store.SetHardcoverToken(inst.ID, token); err != nil {
		http.Error(w, `{"error":"failed to store hardcover token"}`, http.StatusInternalServerError)
		return
	}
	h.notifyHardcoverChanged(inst.ID)
	writeHardcoverStatus(w, true, true)
}

// ClearHardcoverToken answers DELETE /instances/{id}/hardcover.
func (h *Handler) ClearHardcoverToken(w http.ResponseWriter, r *http.Request) {
	inst, ok := h.hardcoverInstance(w, r)
	if !ok {
		return
	}
	if !SupportsHardcover(inst.ServiceType) {
		writeHardcoverStatus(w, false, false)
		return
	}
	if err := h.store.ClearHardcoverToken(inst.ID); err != nil {
		http.Error(w, `{"error":"failed to clear hardcover token"}`, http.StatusInternalServerError)
		return
	}
	h.notifyHardcoverChanged(inst.ID)
	writeHardcoverStatus(w, true, false)
}

func (h *Handler) hardcoverInstance(w http.ResponseWriter, r *http.Request) (*Instance, bool) {
	inst, err := h.store.Get(chi.URLParam(r, "instanceID"))
	if err != nil {
		http.Error(w, `{"error":"failed to get instance"}`, http.StatusInternalServerError)
		return nil, false
	}
	if inst == nil {
		http.Error(w, `{"error":"instance not found"}`, http.StatusNotFound)
		return nil, false
	}
	return inst, true
}

func writeHardcoverStatus(w http.ResponseWriter, supported, configured bool) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"supported":  supported,
		"configured": configured,
	})
}

// normalizeHardcoverToken accepts what an admin actually pastes -- Hardcover's
// settings page shows the token with a "Bearer " prefix -- and refuses
// anything that could not be a single header value.
func normalizeHardcoverToken(raw string) (string, error) {
	token := strings.TrimSpace(raw)
	if len(token) > 7 && strings.EqualFold(token[:7], "bearer ") {
		token = strings.TrimSpace(token[7:])
	}
	if token == "" {
		return "", errors.New("token is required")
	}
	if len(token) > hardcoverTokenMaxLen || strings.ContainsAny(token, " \t\r\n") {
		return "", errors.New("that does not look like a Hardcover API token")
	}
	return token, nil
}

// verifyHardcoverToken asks Hardcover whether the token authenticates. It
// returns errHardcoverRejected when Hardcover answered and refused the token
// (an authentication status, or a GraphQL error in a 200 body -- Hardcover
// reports an expired token that way) and a plain error when Hardcover could
// not be reached or answered something unexpected.
func verifyHardcoverToken(ctx context.Context, apiURL, token string) error {
	payload, err := json.Marshal(map[string]string{"query": "query CantinarrVerify { me { id } }"})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	client := &http.Client{Transport: httpx.External(), Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("hardcover: %w", err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return errHardcoverRejected
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("hardcover: unexpected status %d", resp.StatusCode)
	}
	var reply struct {
		Data struct {
			Me json.RawMessage `json:"me"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&reply); err != nil {
		return fmt.Errorf("hardcover: invalid response: %w", err)
	}
	if len(reply.Errors) > 0 {
		// Hardcover answered the request and would not run it for this
		// token; the message text is Hardcover's and is not echoed.
		return errHardcoverRejected
	}
	if !hardcoverNamedAccount(reply.Data.Me) {
		return errors.New("hardcover: response named no account")
	}
	return nil
}

// hardcoverNamedAccount reads `me` in either shape Hardcover has used -- a
// list with the caller's account as its one element, or the account object
// itself -- and reports whether an account was actually there.
func hardcoverNamedAccount(me json.RawMessage) bool {
	trimmed := bytes.TrimSpace(me)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return false
	}
	if trimmed[0] == '[' {
		var list []map[string]any
		return json.Unmarshal(trimmed, &list) == nil && len(list) > 0
	}
	var one map[string]any
	return json.Unmarshal(trimmed, &one) == nil && len(one) > 0
}
