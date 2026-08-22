package credentials

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/secrets"
)

const (
	maxCredentialSettingsBody = 128 << 10
	maxAIModelLength          = 256
	maxAIKeyLength            = 32 << 10
	maxAIBaseURLLength        = 2048
)

// Handler provides admin-only REST endpoints for credential management.
type Handler struct {
	registry            *Registry
	sharedAIConfigured  func() bool
	validateSharedAI    func(context.Context, AIProfile) error
	sharedAIValidated   func(AIConfig)
	authorizePermission auth.PermissionAuthorizer
	updateMu            sync.Mutex
}

// SetSharedAIConfigured supplies the runtime-aware shared readiness check after
// the AI/Codex adapter has been constructed. It is wired once at startup.
func (h *Handler) SetSharedAIConfigured(check func() bool) {
	h.sharedAIConfigured = check
}

// SetSharedAIValidator makes a real response turn a mandatory precondition for
// shared API-key, provider, and model writes. validated runs only after commit.
func (h *Handler) SetSharedAIValidator(validate func(context.Context, AIProfile) error, validated func(AIConfig)) {
	h.validateSharedAI = validate
	h.sharedAIValidated = validated
}

// SetPermissionAuthorizer supplies the authoritative user/device permission
// check repeated after a provider probe and immediately before persistence.
func (h *Handler) SetPermissionAuthorizer(authorize auth.PermissionAuthorizer) {
	h.authorizePermission = authorize
}

// NewHandler creates a new credentials handler.
func NewHandler(registry *Registry) *Handler {
	return &Handler{registry: registry}
}

// Get returns which credentials are configured (booleans, never values).
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	status := make(map[string]any, len(AllKeys)+1)
	credentials := make(map[string]bool, len(AllKeys))
	for _, key := range AllKeys {
		configured := h.registry.IsConfigured(key)
		status[key] = configured
		credentials[key] = configured
	}
	status["credentials"] = credentials
	// Distinct from the per-key booleans: whether TMDB is currently running on
	// the built-in public token (no admin token stored).
	status["tmdb_using_builtin"] = h.registry.TMDBUsingBuiltIn()
	status["trakt_using_builtin"] = h.registry.TraktUsingBuiltIn()
	configured := h.registry.IsAIConfigured()
	if h.sharedAIConfigured != nil {
		configured = h.sharedAIConfigured()
	}
	config := h.registry.GetAIConfig()
	status["ai"] = map[string]any{
		"config": config,
		// Flat sibling of config on purpose: AIConfig also serializes into
		// non-admin payloads, and a LAN endpoint belongs only in this
		// admin-gated response.
		"openai_base_url": strings.TrimSpace(h.registry.GetSetting(KeyOpenAIBaseURL)),
		// Flat sibling for the same reason as the base URL above.
		"openai_reasoning_effort": strings.TrimSpace(h.registry.GetSetting(KeyOpenAIReasoningEffort)),
		"providers":       AIProviders,
		"health_check": map[string]any{
			"enabled":        h.registry.AIHealthCheckEnabled(),
			"interval_hours": int(AIHealthCheckInterval / time.Hour),
			"last_checked_at": func() any {
				checked := h.registry.AIHealthLastCheck()
				if checked.IsZero() {
					return nil
				}
				return checked.Format(time.RFC3339)
			}(),
		},
		"shared": map[string]any{
			"config":     config,
			"configured": configured,
		},
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

// Update sets one or more credentials and non-secret AI settings. Only
// non-empty fields are written.
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var body map[string]string
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxCredentialSettingsBody)).Decode(&body); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	// Keep the validated snapshot and its transaction indivisible from another
	// admin settings request in this process. Provider turns are intentionally
	// inside this lock because committing a different concurrent key/model pair
	// would invalidate the exact-candidate guarantee.
	h.updateMu.Lock()
	defer h.updateMu.Unlock()

	valid := make(map[string]bool, len(AllKeys))
	for _, k := range AllKeys {
		valid[k] = true
	}
	valid[KeyAIProvider] = true
	valid[KeyAIModel] = true
	valid[KeyAIHealthCheckEnabled] = true
	valid[KeyOpenAIBaseURL] = true
	valid[KeyOpenAIReasoningEffort] = true

	for key := range body {
		if !valid[key] {
			http.Error(w, `{"error":"unknown credential key: `+key+`"}`, http.StatusBadRequest)
			return
		}
		if key == KeyAIProvider || key == KeyAIModel || key == KeyAIHealthCheckEnabled {
			continue
		}
	}

	current := h.registry.GetAIConfig()
	provider, providerSet := body[KeyAIProvider]
	model, modelSet := body[KeyAIModel]
	candidate := current
	if providerSet || modelSet {
		provider = strings.TrimSpace(provider)
		model = strings.TrimSpace(model)
		if !providerSet || provider == "" {
			provider = current.Provider
		}
		if !IsValidAIProvider(provider) {
			http.Error(w, `{"error":"unknown AI provider"}`, http.StatusBadRequest)
			return
		}
		if !modelSet || model == "" {
			if provider != current.Provider {
				model = DefaultAIModel(provider)
			} else {
				model = current.Model
			}
		}
		if len(model) > maxAIModelLength {
			http.Error(w, `{"error":"AI model is too long"}`, http.StatusBadRequest)
			return
		}
		candidate = AIConfig{Provider: provider, Model: model}
	}

	healthEnabled := h.registry.AIHealthCheckEnabled()
	healthValue, healthSet := body[KeyAIHealthCheckEnabled]
	if healthSet {
		parsed, err := strconv.ParseBool(strings.TrimSpace(healthValue))
		if err != nil {
			http.Error(w, `{"error":"ai_health_check_enabled must be true or false"}`, http.StatusBadRequest)
			return
		}
		healthEnabled = parsed
	}

	// The effective openai base URL for this save: the body's value when the
	// key is present (empty string is a deliberate clear), else the stored
	// override. Candidate profiles must always carry the effective value so a
	// key rotation with no base-URL field still validates against the
	// configured endpoint, not api.openai.com.
	baseURL := strings.TrimSpace(h.registry.GetSetting(KeyOpenAIBaseURL))
	baseURLValue, baseURLSet := body[KeyOpenAIBaseURL]
	if baseURLSet {
		baseURL = strings.TrimSpace(baseURLValue)
		if len(baseURL) > maxAIBaseURLLength {
			http.Error(w, `{"error":"openai_base_url is too long"}`, http.StatusBadRequest)
			return
		}
		if baseURL != "" {
			parsed, err := url.Parse(baseURL)
			if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
				http.Error(w, `{"error":"openai_base_url must be an absolute http or https URL"}`, http.StatusBadRequest)
				return
			}
		}
	}

	// Same effective-value contract as the base URL: candidate profiles carry
	// the pinned effort whether or not this save touched it.
	reasoningEffort := strings.TrimSpace(h.registry.GetSetting(KeyOpenAIReasoningEffort))
	reasoningEffortValue, reasoningEffortSet := body[KeyOpenAIReasoningEffort]
	if reasoningEffortSet {
		reasoningEffort = strings.ToLower(strings.TrimSpace(reasoningEffortValue))
		if !IsValidAIReasoningEffort(reasoningEffort) {
			http.Error(w, `{"error":"openai_reasoning_effort must be one of none, minimal, low, medium, high, or empty for auto"}`, http.StatusBadRequest)
			return
		}
	}

	profiles := make(map[string]AIProfile)
	for _, option := range AIProviders {
		if option.CredentialKey == "" {
			continue
		}
		if value := strings.TrimSpace(body[option.CredentialKey]); value != "" {
			if len(value) > maxAIKeyLength {
				http.Error(w, `{"error":"AI credential is too long"}`, http.StatusBadRequest)
				return
			}
			config := AIConfig{Provider: option.ID, Model: DefaultAIModel(option.ID)}
			if option.ID == candidate.Provider {
				config.Model = candidate.Model
			}
			profile := AIProfile{Config: config, APIKey: value, CredentialPresent: true}
			if option.ID == AIProviderOpenAI {
				profile.BaseURL = baseURL
				profile.ReasoningEffort = reasoningEffort
			}
			profiles[option.ID] = profile
			body[option.CredentialKey] = value
		}
	}
	mustTestSelected := providerSet || modelSet || (healthSet && healthEnabled && !h.registry.AIHealthCheckEnabled())
	if key := AIKeyCredentialKey(candidate.Provider); key != "" && strings.TrimSpace(body[key]) != "" {
		mustTestSelected = true
	}
	// A base-URL or pinned-effort change (or clear) alone must re-prove the
	// selected openai profile against the new configuration. With another
	// provider selected the value persists untested; selecting openai later
	// forces the probe via providerSet.
	if (baseURLSet || reasoningEffortSet) && candidate.Provider == AIProviderOpenAI {
		mustTestSelected = true
	}
	if mustTestSelected {
		profile, ok := profiles[candidate.Provider]
		if !ok {
			profile = AIProfile{Config: candidate}
			if key := AIKeyCredentialKey(candidate.Provider); key != "" {
				profile.APIKey = h.registry.GetCredential(key)
				profile.CredentialPresent = strings.TrimSpace(profile.APIKey) != ""
			} else {
				profile.CredentialPresent = IsOAuthAIProvider(candidate.Provider)
			}
			if candidate.Provider == AIProviderOpenAI {
				profile.BaseURL = baseURL
				profile.ReasoningEffort = reasoningEffort
			}
			profiles[candidate.Provider] = profile
		}
	}
	if len(profiles) > 0 && h.validateSharedAI == nil {
		http.Error(w, `{"error":"AI settings validation is unavailable"}`, http.StatusServiceUnavailable)
		return
	}
	for _, profile := range profiles {
		if err := h.validateSharedAI(r.Context(), profile); err != nil {
			log.Printf("credentials: shared AI validation failed provider=%q: %s", profile.Config.Provider, credentialValidationDiagnostic(err))
			writeCredentialValidationError(w, err)
			return
		}
	}
	if len(profiles) > 0 && !h.reauthorizeSharedAIWrite(w, r) {
		return
	}

	if err := h.applyUpdate(body, candidate, providerSet || modelSet, healthEnabled, healthSet, baseURL, baseURLSet, reasoningEffort, reasoningEffortSet); err != nil {
		http.Error(w, `{"error":"failed to save settings"}`, http.StatusInternalServerError)
		return
	}

	h.registry.Invalidate()
	if mustTestSelected && h.sharedAIValidated != nil {
		h.sharedAIValidated(candidate)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (h *Handler) reauthorizeSharedAIWrite(w http.ResponseWriter, r *http.Request) bool {
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return false
	}
	if h.authorizePermission == nil {
		http.Error(w, `{"error":"credential authorization is temporarily unavailable"}`, http.StatusServiceUnavailable)
		return false
	}
	if err := h.authorizePermission(r.Context(), claims.UserID, claims.DeviceID, auth.PermissionCredentialsManage); err != nil {
		if errors.Is(err, auth.ErrAuthUnavailable) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			http.Error(w, `{"error":"credential authorization is temporarily unavailable"}`, http.StatusServiceUnavailable)
		} else {
			http.Error(w, `{"error":"permission denied"}`, http.StatusForbidden)
		}
		return false
	}
	return true
}

func writeCredentialValidationError(w http.ResponseWriter, err error) {
	message := "The selected AI provider and model could not complete a test message. Nothing was saved."
	var safe interface{ SafeUserMessage() string }
	if errors.As(err, &safe) && strings.TrimSpace(safe.SafeUserMessage()) != "" {
		message = safe.SafeUserMessage()
	}
	payload, marshalErr := json.Marshal(map[string]string{"error": message})
	if marshalErr != nil {
		http.Error(w, `{"error":"AI settings validation failed. Nothing was saved."}`, http.StatusUnprocessableEntity)
		return
	}
	http.Error(w, string(payload), http.StatusUnprocessableEntity)
}

func credentialValidationDiagnostic(err error) string {
	if err == nil {
		return ""
	}
	var safe interface{ SafeDiagnostic() string }
	if errors.As(err, &safe) && strings.TrimSpace(safe.SafeDiagnostic()) != "" {
		return safe.SafeDiagnostic()
	}
	return secrets.RedactError(err).Error()
}

func (h *Handler) applyUpdate(body map[string]string, config AIConfig, configSet bool, healthEnabled, healthSet bool, baseURL string, baseURLSet bool, reasoningEffort string, reasoningEffortSet bool) error {
	tx, err := h.registry.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, key := range AllKeys {
		value := strings.TrimSpace(body[key])
		if value == "" {
			continue
		}
		if isSecretKey(key) {
			value, err = h.registry.cipher.Encrypt(value)
			if err != nil {
				return fmt.Errorf("encrypt %s: %w", key, err)
			}
		}
		if _, err := tx.Exec("INSERT OR REPLACE INTO settings (key, value) VALUES (?, ?)", key, value); err != nil {
			return err
		}
	}
	if configSet {
		for key, value := range map[string]string{KeyAIProvider: config.Provider, KeyAIModel: config.Model} {
			if _, err := tx.Exec("INSERT OR REPLACE INTO settings (key, value) VALUES (?, ?)", key, value); err != nil {
				return err
			}
		}
	}
	if healthSet {
		if _, err := tx.Exec("INSERT OR REPLACE INTO settings (key, value) VALUES (?, ?)", KeyAIHealthCheckEnabled, strconv.FormatBool(healthEnabled)); err != nil {
			return err
		}
	}
	if baseURLSet {
		// Stored plaintext on purpose: an endpoint URL is configuration, not
		// a secret, and stays out of the AllKeys encryption loop above.
		if baseURL == "" {
			if _, err := tx.Exec("DELETE FROM settings WHERE key = ?", KeyOpenAIBaseURL); err != nil {
				return err
			}
		} else if _, err := tx.Exec("INSERT OR REPLACE INTO settings (key, value) VALUES (?, ?)", KeyOpenAIBaseURL, baseURL); err != nil {
			return err
		}
	}
	if reasoningEffortSet {
		// Same plaintext contract as the base URL; empty clears back to auto.
		if reasoningEffort == "" {
			if _, err := tx.Exec("DELETE FROM settings WHERE key = ?", KeyOpenAIReasoningEffort); err != nil {
				return err
			}
		} else if _, err := tx.Exec("INSERT OR REPLACE INTO settings (key, value) VALUES (?, ?)", KeyOpenAIReasoningEffort, reasoningEffort); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Delete removes a single credential.
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	h.updateMu.Lock()
	defer h.updateMu.Unlock()
	key := chi.URLParam(r, "key")

	valid := false
	for _, k := range AllKeys {
		if k == key {
			valid = true
			break
		}
	}
	if !valid {
		http.Error(w, `{"error":"unknown credential key"}`, http.StatusBadRequest)
		return
	}

	if err := h.registry.DeleteCredential(key); err != nil {
		http.Error(w, `{"error":"failed to delete credential"}`, http.StatusInternalServerError)
		return
	}

	h.registry.Invalidate()
	w.WriteHeader(http.StatusNoContent)
}
