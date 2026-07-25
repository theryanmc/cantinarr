package push

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strconv"
	"time"
)

// Notifier turns request and content events into push notifications via the
// gateway. It implements request.Notifier. Sends are fire-and-forget so a slow
// or failing gateway never blocks an approval/denial. Every notification is
// filtered by the recipient's per-category preferences before dispatch.
//
// The Notifier holds the push Manager rather than a static client, so it picks
// up a gateway that enrolled after boot and no-ops cleanly while push is
// unconfigured or the gateway is unreachable (Manager.Client() == nil). After
// each send it inspects the gateway's per-device results and drops any local
// token the gateway pruned (token rejected by APNs), keeping push_tokens from
// accumulating dead rows.
type Notifier struct {
	mgr    *Manager
	db     *sql.DB
	prefs  *PrefsStore
	logger *slog.Logger
}

// NewNotifier builds a push notifier over the push Manager. A nil manager (or
// one whose client has not enrolled) makes every method a no-op, so callers can
// wire it unconditionally and let the manager gate on configuration.
func NewNotifier(db *sql.DB, mgr *Manager, logger *slog.Logger) *Notifier {
	if logger == nil {
		logger = slog.Default()
	}
	return &Notifier{mgr: mgr, db: db, prefs: NewPrefsStore(db), logger: logger}
}

// client returns the currently-enrolled gateway client, or nil when push is
// unconfigured or the gateway has not yet come up. nil-receiver safe.
func (n *Notifier) client() *Client {
	if n == nil || n.mgr == nil {
		return nil
	}
	return n.mgr.Client()
}

// ContentReady reports whether new-content alerts can currently be delivered.
// The WebSocket poller calls it to hold back the single queue-departure diff it
// resumes after a restart, so completions that survived the restart are not
// dropped into a gateway that has not enrolled yet. Steady-state alerts are not
// gated on it.
func (n *Notifier) ContentReady() bool { return n.client() != nil }

// NotifyUser pushes a per-user event to its recipient, gated on their opt-in
// for the matching category. Two events produce a notification today:
// "request_decision" (approval/denial outcome, off by default) and
// "plex_invite_sent" ("check your email", fixed template, on by default).
// Any other event type is a no-op here (WS-only).
func (n *Notifier) NotifyUser(userID int64, eventType string, data map[string]interface{}) {
	client := n.client()
	if client == nil {
		return
	}
	switch eventType {
	case CategoryRequestDecision:
		if !n.prefs.optedIn(userID, CategoryRequestDecision) {
			return
		}
		title, body := decisionMessage(data)
		if title == "" {
			return
		}
		n.send(client, []int64{userID}, title, body, passthrough(CategoryRequestDecision, data))
	case CategoryPlexInviteSent:
		if !n.prefs.optedIn(userID, CategoryPlexInviteSent) {
			return
		}
		n.send(client, []int64{userID}, "Plex invite sent",
			"Your Plex invite is on its way — check your email",
			map[string]any{"type": CategoryPlexInviteSent})
	}
}

// NotifyAdmins pushes an admin-scoped event to every admin opted into the
// matching category. Two events produce a notification today: "request_pending"
// (a new media request) and "issue_created" (a new AI-remediation issue, on by
// default). Any other event type is a no-op here (WS-only).
//
// Untrusted-text invariant (M5): the alert body is a FIXED server-authored
// template, never an interpolated title/reason; the media title travels only as
// a structured passthrough field (and as the request alert's body, which is
// arr/user-sourced and unchanged from existing behavior).
func (n *Notifier) NotifyAdmins(eventType string, data map[string]interface{}) {
	client := n.client()
	if client == nil {
		return
	}
	switch eventType {
	case CategoryRequestPending:
		n.notifyRequestPending(client, data)
	case CategoryIssueCreated:
		n.notifyIssueCreated(client, data)
	case CategoryAgentActionPending:
		n.notifyAgentActionPending(client, data)
	case CategoryPlexAccessRequest:
		n.notifyPlexAccessRequested(client, data)
	}
}

// notifyRequestPending pushes a new pending request to opted-in admins, badging
// the home-screen icon with the live queue depth.
func (n *Notifier) notifyRequestPending(client *Client, data map[string]interface{}) {
	title := str(data["title"])
	if title == "" {
		title = "a title"
	}
	recipients, err := n.prefs.usersOptedInto(CategoryRequestPending)
	if err != nil {
		n.logger.Error("push: resolve request_pending recipients", "err", err)
		return
	}
	if len(recipients) == 0 {
		return
	}
	// Set the home-screen icon badge to the live queue depth so admins see the
	// count even while the app is closed (the gateway maps this to aps.badge).
	var opts SendOptions
	if count, ok := intval(data["pending_count"]); ok {
		opts.Badge = &count
	}
	n.sendWithOptions(client, recipients, "New request", title, passthrough(CategoryRequestPending, data), opts)
}

// notifyIssueCreated pushes a new AI-remediation issue to opted-in admins. The
// body is a fixed template (the untrusted issue title is NOT placed on the
// lock screen); issue_id rides along for tap deep-linking. Issue state does not
// write the global app-icon badge: recovery can silently remove issue attention,
// while the app icon has one independent owner (pending request approvals).
func (n *Notifier) notifyIssueCreated(client *Client, data map[string]interface{}) {
	recipients, err := n.prefs.usersOptedInto(CategoryIssueCreated)
	if err != nil {
		n.logger.Error("push: resolve issue_created recipients", "err", err)
		return
	}
	if len(recipients) == 0 {
		return
	}
	out := map[string]any{"type": CategoryIssueCreated}
	if v, ok := data["issue_id"]; ok {
		out["issue_id"] = v
	}
	title := "New problem reported"
	body := "Someone reported a problem with their media"
	if str(data["source"]) == "auto" {
		title = "Problem needs attention"
		body = "Cantinarr found a media problem that did not recover automatically"
	} else if str(data["source"]) == "system" {
		title = "Shared AI needs attention"
		body = "Cantinarr's shared AI model failed its daily response test"
	}
	n.sendWithOptions(client, recipients, title, body, out, SendOptions{})
}

// notifyAgentActionPending pushes "the AI proposed a fix, approve it" to opted-in
// admins. The body is a FIXED template — the agent's rationale and any release
// name are UNTRUSTED and never placed on the lock screen; issue_id rides along
// for tap deep-linking. Like issue-created pushes, this queue does not overwrite
// the global app-icon badge owned by pending request approvals.
func (n *Notifier) notifyAgentActionPending(client *Client, data map[string]interface{}) {
	recipients, err := n.prefs.usersOptedInto(CategoryAgentActionPending)
	if err != nil {
		n.logger.Error("push: resolve agent_action_pending recipients", "err", err)
		return
	}
	if len(recipients) == 0 {
		return
	}
	out := map[string]any{"type": CategoryAgentActionPending}
	if v, ok := data["issue_id"]; ok {
		out["issue_id"] = v
	}
	n.sendWithOptions(client, recipients, "A fix needs your approval", "The assistant proposed a fix for a problem and needs you to approve it", out, SendOptions{})
}

// notifyPlexAccessRequested pushes "a user shared their Plex email" to
// opted-in admins. The body is one of three FIXED templates picked by the
// invite_state enum ("" needs a manual invite, "sent" auto-invite went out,
// "failed" auto-invite needs a retry) — the username and email are
// user-controlled and never placed on the lock screen; user_id rides along
// for tap deep-linking to the Users screen. A collapse id per user keeps a
// user editing their email from stacking alerts.
func (n *Notifier) notifyPlexAccessRequested(client *Client, data map[string]interface{}) {
	recipients, err := n.prefs.usersOptedInto(CategoryPlexAccessRequest)
	if err != nil {
		n.logger.Error("push: resolve plex_access_request recipients", "err", err)
		return
	}
	if len(recipients) == 0 {
		return
	}
	body := "Someone shared their Plex email and is waiting for an invite"
	switch str(data["invite_state"]) {
	case "sent":
		body = "Someone shared their Plex email — their invite was sent automatically"
	case "failed":
		body = "Someone shared their Plex email — the automatic invite failed, send it from the Users screen"
	}
	out := map[string]any{"type": CategoryPlexAccessRequest}
	if v, ok := data["user_id"]; ok {
		out["user_id"] = v
	}
	var opts SendOptions
	if id, ok := intval(data["user_id"]); ok {
		opts.CollapseID = fmt.Sprintf("%s:%d", CategoryPlexAccessRequest, id)
	}
	n.sendWithOptions(client, recipients, "Plex access request", body, out, opts)
}

// NotifyNewMovie pushes a "movie became available" alert to every user opted
// into the new_movie category (on by default). A collapse id keeps repeat
// availability pings for the same movie from stacking on-device.
func (n *Notifier) NotifyNewMovie(title string, tmdbID int) {
	n.notifyNewContent(CategoryNewMovie, "movie", "New movie available", title+" is ready to watch", title, tmdbID)
}

// NotifyNewEpisode pushes a "new episode available" alert to every user opted
// into the new_episode category (on by default).
func (n *Notifier) NotifyNewEpisode(seriesTitle string, tmdbID int) {
	n.notifyNewContent(CategoryNewEpisode, "tv", "New episode available", "New on "+seriesTitle, seriesTitle, tmdbID)
}

// NotifyNewBook pushes a "book became available" alert for a completed
// Chaptarr import. The audience is users opted into the new_book category (on
// by default) whose book library is the instance the import landed on — a
// chaptarr assignment is the books access model, and it scopes admins too;
// only an admin with no books assignment hears every instance. format is the
// Chaptarr book record's media type ("ebook"/"audiobook"): a title's two
// formats are separate records sharing a foreignBookId, and each import
// alerts on its own.
func (n *Notifier) NotifyNewBook(title, foreignID, instanceID, format string) {
	client := n.client()
	if client == nil || title == "" {
		return
	}
	if !n.claimContentAlert(CategoryNewBook, "book", foreignID+"|"+format, title) {
		return
	}
	recipients, err := n.prefs.usersOptedIntoNewBook(instanceID)
	if err != nil {
		n.logger.Error("push: resolve new-content recipients", "err", err, "category", CategoryNewBook)
		return
	}
	if len(recipients) == 0 {
		return
	}
	// Books have no TMDB id: the payload carries the same deep-link identity a
	// book request decision does (foreign_id, pinned instance, title hint), so
	// the app's existing book tap routing applies unchanged.
	data := map[string]any{
		"type":        CategoryNewBook,
		"media_type":  "book",
		"foreign_id":  foreignID,
		"instance_id": instanceID,
		"title":       title,
	}
	if format != "" {
		data["book_format"] = format
	}
	n.sendWithOptions(client, recipients, "New book available", bookReadyBody(title, format), data, SendOptions{
		CollapseID: fmt.Sprintf("%s:%s:%s", CategoryNewBook, foreignID, format),
	})
}

// bookReadyBody derives the availability copy from the imported record's
// format, using the same eBook/Audiobook nomenclature as the decision copy.
func bookReadyBody(title, format string) string {
	switch format {
	case "ebook":
		return title + " eBook is ready to read"
	case "audiobook":
		return title + " Audiobook is ready to play"
	default:
		return title + " is ready"
	}
}

// notifyNewContent is the shared body for the new-content notifications: it
// resolves the opted-in audience for the category and dispatches one collapsed
// push carrying the media identity for tap routing.
func (n *Notifier) notifyNewContent(category, mediaType, title, body, mediaTitle string, tmdbID int) {
	client := n.client()
	if client == nil || mediaTitle == "" {
		return
	}
	if !n.claimContentAlert(category, mediaType, strconv.Itoa(tmdbID), mediaTitle) {
		return
	}
	recipients, err := n.prefs.usersOptedInto(category)
	if err != nil {
		n.logger.Error("push: resolve new-content recipients", "err", err, "category", category)
		return
	}
	if len(recipients) == 0 {
		return
	}
	data := map[string]any{
		"type":       category,
		"tmdb_id":    tmdbID,
		"media_type": mediaType,
	}
	n.sendWithOptions(client, recipients, title, body, data, SendOptions{
		CollapseID: fmt.Sprintf("%s:%d", category, tmdbID),
	})
}

// contentAlertWindow is how long a new-content alert suppresses repeats of the
// same content: long enough to absorb a season pack's per-file webhooks plus
// the queue poll re-witnessing the same import, short enough that tonight's
// episode still alerts after last week's.
const contentAlertWindow = 10 * time.Minute

// contentAlertRetention bounds content_alert_claims without a sweeper goroutine:
// old rows are dropped opportunistically whenever a claim is granted. Well past
// contentAlertWindow, so it can never shorten the suppression it stores.
const contentAlertRetention = 24 * time.Hour

// contentAlertStormCap bounds how many distinct content alerts may fan out
// within one contentAlertWindow before the rest of the burst is suppressed. It
// is the live-path analogue of the poller's resumed-batch cap: a mass job — a
// bulk manual import firing one webhook per title, several instances resuming
// at once — would otherwise page every opted-in household member once per
// title, and the only remedy a user has is a permanent category opt-out. Past
// this rate the app's live view is the better answer. Sized above the poller's
// per-instance resumed cap so one full resumed batch never starves a
// concurrent organic alert; suppressed alerts are logged, never silent.
const contentAlertStormCap = 12

// claimContentAlert reports whether this content alert should send, recording it
// so duplicates within contentAlertWindow are dropped. id is the content's
// stable identity — the tmdb id for movies/TV, foreignBookId plus format for
// books. The key includes the title so unresolved ids (tmdb 0) never collapse
// different content.
//
// The claim is durable so the window survives a restart: the queue poller
// resumes its departure diff across a restart, and without a persisted claim a
// completion the webhook already announced would be announced a second time.
// Durable is not permanent — the window is still a window, so a claim whose send
// failed is re-claimable once it lapses.
func (n *Notifier) claimContentAlert(category, mediaType, id, title string) bool {
	key := fmt.Sprintf("%s|%s|%s|%s", category, mediaType, id, title)
	if n.db == nil {
		// No ledger must never silence an alert.
		return true
	}
	// One statement: the conditional upsert grants the claim only when there is
	// no row or the existing one has lapsed, and the affected-row count is the
	// verdict. A SELECT-then-INSERT would race on the shared single-connection
	// pool without adding anything.
	res, err := n.db.Exec(`
        INSERT INTO content_alert_claims (alert_key) VALUES (?)
        ON CONFLICT(alert_key) DO UPDATE SET claimed_at = CURRENT_TIMESTAMP
        WHERE content_alert_claims.claimed_at <= datetime('now', ?)`,
		key, sqliteOffset(-contentAlertWindow))
	if err != nil {
		// Fail open: a duplicate alert beats silencing the whole content surface
		// on a transient database error.
		n.logger.Error("push: claim content alert", "err", err, "category", category)
		return true
	}
	rows, err := res.RowsAffected()
	if err != nil {
		n.logger.Error("push: claim content alert rows", "err", err, "category", category)
		return true
	}
	if rows != 1 {
		return false
	}
	if _, err := n.db.Exec(`DELETE FROM content_alert_claims WHERE claimed_at <= datetime('now', ?)`,
		sqliteOffset(-contentAlertRetention)); err != nil {
		n.logger.Error("push: sweep content alert claims", "err", err)
	}
	// Storm breaker: the claim above stays recorded (so the other witness of
	// the same import cannot re-try it), but past the burst cap the alert
	// itself is suppressed. Counting granted claims counts distinct content,
	// not devices, and the count includes this claim — the first cap-many in a
	// window deliver, the rest of the burst is dropped and logged.
	var inWindow int
	if err := n.db.QueryRow(`SELECT COUNT(*) FROM content_alert_claims WHERE claimed_at > datetime('now', ?)`,
		sqliteOffset(-contentAlertWindow)).Scan(&inWindow); err != nil {
		// Fail open, same as the claim itself: a burst slipping through beats
		// silencing the surface on a transient database error.
		n.logger.Error("push: count content alert window", "err", err, "category", category)
		return true
	}
	if inWindow > contentAlertStormCap {
		n.logger.Warn("push: suppressing content alert burst",
			"category", category, "title", title, "in_window", inWindow, "cap", contentAlertStormCap)
		return false
	}
	return true
}

// sqliteOffset renders a duration as a SQLite datetime() modifier, so the Go
// constants above stay the single source of truth for both windows.
func sqliteOffset(d time.Duration) string {
	return fmt.Sprintf("%+d seconds", int64(d/time.Second))
}

// send dispatches a notification in the background with panic recovery, so a
// failed delivery (or a marshalling bug) can never take down the caller.
func (n *Notifier) send(client *Client, userIDs []int64, title, body string, data map[string]any) {
	n.sendWithOptions(client, userIDs, title, body, data, SendOptions{})
}

// sendWithOptions is send with explicit gateway options (e.g. a collapse id).
// After the gateway replies it prunes any local token the gateway reported
// dead, so push_tokens self-cleans without a separate sweep.
func (n *Notifier) sendWithOptions(client *Client, userIDs []int64, title, body string, data map[string]any, opts SendOptions) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				n.logger.Error("push: send panicked", "err", fmt.Sprint(r))
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		resp, err := client.SendWithOptions(ctx, userIDs, title, body, data, opts)
		if err != nil {
			n.logger.Error("push: send notification", "err", err, "title", title)
			return
		}
		pruneDeadTokens(n.db, n.logger, resp)
	}()
}

// pruneDeadTokens deletes the local push_tokens row for every result the
// gateway flagged as pruned (token rejected by APNs as unregistered). Matching
// on token keeps it precise even if a device re-registered with a new token in
// the meantime. Best-effort: delete failures are logged, not surfaced. Shared
// by the Notifier (after each send) and the test-push handler.
func pruneDeadTokens(db *sql.DB, logger *slog.Logger, resp *SendResponse) {
	if resp == nil {
		return
	}
	pruned := 0
	for _, r := range resp.Results {
		if !r.Pruned || r.Token == "" {
			continue
		}
		if _, err := db.Exec("DELETE FROM push_tokens WHERE token = ?", r.Token); err != nil {
			logger.Error("push: prune dead token", "err", err, "device_id", r.DeviceID)
			continue
		}
		pruned++
	}
	if pruned > 0 {
		logger.Info("push: pruned dead device tokens", "count", pruned)
	}
}

// decisionMessage derives the alert title/body from a request_decision payload.
// An unrecognized decision yields an empty title so no notification is sent.
func decisionMessage(data map[string]interface{}) (title, body string) {
	mediaTitle := str(data["title"])
	if mediaTitle == "" {
		mediaTitle = "Your request"
	}
	subject, plural := decisionSubject(data, mediaTitle)
	switch str(data["decision"]) {
	case "approved":
		verb := " is"
		if plural {
			verb = " are"
		}
		return "Request approved", subject + verb + " on the way"
	case "denied":
		verb := " was"
		if plural {
			verb = " were"
		}
		body = subject + verb + " denied"
		if reason := str(data["reason"]); reason != "" {
			body += ": " + reason
		}
		return "Request denied", body
	default:
		return "", ""
	}
}

// decisionSubject preserves the existing whole-title copy for movies/TV and
// scopes book decisions to the concrete format coverage in the event. Partial
// approval events deliberately include only the formats that succeeded.
func decisionSubject(data map[string]interface{}, mediaTitle string) (string, bool) {
	if str(data["media_type"]) != "book" {
		return mediaTitle, false
	}
	switch str(data["book_format"]) {
	case "ebook":
		return mediaTitle + " eBook", false
	case "audiobook":
		return mediaTitle + " Audiobook", false
	case "both":
		return mediaTitle + " eBook and Audiobook", true
	default:
		return mediaTitle, false
	}
}

// passthrough copies the event fields the client uses to route a notification
// tap (type + media identity) into the APNs custom-data payload.
func passthrough(eventType string, data map[string]interface{}) map[string]any {
	out := map[string]any{"type": eventType}
	if v, ok := data["tmdb_id"]; ok {
		out["tmdb_id"] = v
	}
	if v := str(data["media_type"]); v != "" {
		out["media_type"] = v
	}
	// Books have no TMDB id. Keep the canonical identity, selected library, title
	// hint, and decision scope together so a tap cannot drift to whichever
	// Chaptarr instance happens to be active later. Movie/TV payloads remain
	// unchanged.
	if v := str(data["foreign_id"]); v != "" {
		out["foreign_id"] = v
	}
	if str(data["media_type"]) == "book" {
		if v := str(data["instance_id"]); v != "" {
			out["instance_id"] = v
		}
		if v := str(data["title"]); v != "" {
			out["title"] = v
		}
		if v := str(data["book_format"]); v != "" {
			out["book_format"] = v
		}
	}
	return out
}

// str reads a string value from a map[string]interface{}, returning "" when the
// key is absent or not a string.
func str(v interface{}) string {
	s, _ := v.(string)
	return s
}

// intval reads an int from a map value, tolerating the int/int64/float64 forms a
// value may take whether it arrived in-process or via a JSON round-trip.
func intval(v interface{}) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	default:
		return 0, false
	}
}
