package remediation

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/windoze95/cantinarr-server/internal/ai"
	"github.com/windoze95/cantinarr-server/internal/mcp"
)

// The real-pipeline harness: detection through typed verification with no test
// seams in the path. The fake is an HTTP Sonarr; everything between it and the
// assertions is production code — fetchQueueSnapshot's parse and Import Doctor
// verdict, observation promotion windows, every recovery preflight, the REAL
// mcp.ToolServer (scope injection, typed verification), ProposeAction's
// validation, the approvals decision core, the REAL Executor's arr mutations,
// resume, and the server-side recovery proofs. The runner-level stubs
// (recoveryProbe, fakeToolHost) exist for focused unit tests; this file exists
// because those seams are exactly where the pre-air preflight defect hid.

// pipelineHarness bundles the service, the fake Sonarr, and a Runner factory
// that shares one scripted-turn sequence across Run and Resume — the script
// simply continues where the previous segment stopped, the way a real model
// transcript does.
type pipelineHarness struct {
	svc  *Service
	fake *preAirFake
}

func newPipelineHarness(t *testing.T) *pipelineHarness {
	t.Helper()
	svc, _, fake := setupPreAirService(t)
	if _, err := svc.SetSettings(Settings{
		Enabled: true, AutoDispatch: true, Mode: ModeSupervised,
		MaxSteps: 12, MaxTurnTokens: 1024, MaxWallClockSecs: 30, DailyRunCap: 50,
	}); err != nil {
		t.Fatalf("set settings: %v", err)
	}
	if _, err := svc.db.Exec("INSERT INTO users (id, username, password_hash, role) VALUES (1, 'admin', '', 'admin')"); err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	return &pipelineHarness{svc: svc, fake: fake}
}

// runner builds a Runner over the REAL tool server — reads and agent tools
// dispatch into production mcp code wired to the harness service and registry.
func (h *pipelineHarness) runner(script *scriptedTurn) *Runner {
	toolServer := mcp.NewToolServer(nil, nil, h.svc.registry, nil)
	toolServer.SetIssueStore(h.svc)
	return &Runner{
		db:         h.svc.db,
		svc:        h.svc,
		toolServer: toolServer,
		turns:      scriptedTurnResolver(script),
		procToken:  "test",
	}
}

// observe ingests the fake's CURRENT queue through the production fetch+parse
// path at an explicit clock, exactly as the poller/sweeper feed production.
func (h *pipelineHarness) observe(t *testing.T, at time.Time) {
	t.Helper()
	items, err := h.svc.fetchQueueSnapshot("sonarr", preAirSonarrID)
	if err != nil {
		t.Fatalf("fetch queue snapshot: %v", err)
	}
	if err := h.svc.observeQueueSnapshot("sonarr", preAirSonarrID, items, at); err != nil {
		t.Fatalf("observe queue snapshot: %v", err)
	}
}

func toolCall(id, name, input string) ai.TranscriptMessage {
	return ai.TranscriptMessage{Role: ai.RoleAssistant, Content: []ai.TranscriptBlock{{
		Type: ai.BlockToolUse, ID: id, Name: name, Input: json.RawMessage(input),
	}}}
}

// stalledUpgradeQueueRow is a Sonarr queue row for a stalled torrent that is an
// UPGRADE: the episode already holds a file, which is what makes the abandoned
// repair provable later (the unchanged library file IS the success).
func stalledUpgradeQueueRow(queueID int, downloadID string, added time.Time) map[string]any {
	return map[string]any{
		"id":        queueID,
		"seriesId":  28,
		"episodeId": 28203,
		"series":    map[string]any{"id": 28, "title": "Futurama", "tvdbId": 73871, "tmdbId": 615},
		"episode": map[string]any{
			"id": 28203, "seriesId": 28, "seasonNumber": 2, "episodeNumber": 3,
			"episodeFileId": 50203, "hasFile": true, "title": "S02E03",
		},
		"episodeHasFile":        true,
		"title":                 "Futurama.S02E03.1080p.WEB-DL",
		"status":                "queued",
		"trackedDownloadStatus": "warning",
		"trackedDownloadState":  "downloading",
		"errorMessage":          "stalled with no connections",
		"downloadId":            downloadID,
		"protocol":              "torrent",
		"size":                  1000000000.0,
		"sizeleft":              500000000.0,
		"added":                 added.Format(time.RFC3339),
	}
}

// TestPipelineStalledUpgradeFullLoop drives the complete production loop for a
// stalled upgrade: HTTP intake + Doctor verdict → silent observation →
// promotion → real preflight → real tool reads → real proposal → real approval
// core → real Executor mutation against the fake arr → resume → typed
// queue-target verification → upgradeAbandonProven → resolved. One incident,
// one approval, zero test seams.
func TestPipelineStalledUpgradeFullLoop(t *testing.T) {
	h := newPipelineHarness(t)

	// Season 2 is ordinary: E3 aired three weeks ago and holds file 50203
	// (imported after air). The queue holds a stalled upgrade for that episode.
	episodes, files := buildPreAirSeason(28, 2, []preAirEpisode{
		{number: 3, airsIn: -21 * 24 * time.Hour, hasFile: true},
	})
	h.fake.setLibrary(episodes, files)
	base := time.Now().UTC().Add(-30 * time.Minute)
	h.fake.setQueue([]map[string]any{stalledUpgradeQueueRow(41, "TORRENTABC123", base)})

	// Intake twice through the production path: first sighting starts the quiet
	// observation; the second, past the min/quiet windows, promotes it.
	h.observe(t, base)
	h.observe(t, base.Add(11*time.Minute))

	issue := soleIssue(t, h.svc)
	if issue.Status != IssueOpen {
		t.Fatalf("issue after promotion = %q, want %q", issue.Status, IssueOpen)
	}
	problemKind := issueProblemKind(t, h.svc, issue.ID)
	if problemKind == "" {
		t.Fatalf("promoted issue has no problem_kind; the Doctor verdict was lost")
	}

	script := &scriptedTurn{turns: []ai.TranscriptMessage{
		toolCall("r1", "get_queue", `{}`),
		toolCall("p1", mcp.ToolProposeAction, `{"issue_id":0,"kind":"remediate_queue","params":{"media_type":"tv","queue_id":41,"action":"blocklist_only"},"rationale":"Stalled with no seeders; the library already holds a copy nobody asked to replace."}`),
		toolCall("r2", "get_queue", `{}`),
		toolCall("c1", mcp.ToolConcludeIssue, `{"issue_id":0,"status":"resolved","resolution":"The stalled upgrade was removed and blocklisted; the existing copy is intact."}`),
	}}
	r := h.runner(script)

	if err := r.Run(context.Background(), issue.ID); err != nil {
		t.Fatalf("Run: %v", err)
	}
	after := soleIssue(t, h.svc)
	if after.Status != IssueAwaitingApproval {
		t.Fatalf("issue after run = %q, want %q", after.Status, IssueAwaitingApproval)
	}

	var actionID int64
	if err := h.svc.db.QueryRow(
		"SELECT id FROM agent_actions WHERE issue_id = ? AND status = ?", issue.ID, ActionProposed,
	).Scan(&actionID); err != nil {
		t.Fatalf("find proposed action: %v", err)
	}

	act, err := h.svc.ApproveAction(1, actionID, nil)
	if err != nil {
		t.Fatalf("ApproveAction: %v", err)
	}
	if act.Status != ActionExecuted {
		t.Fatalf("approved action status = %q (result %v), want %q", act.Status, act.ResultText, ActionExecuted)
	}
	deletes := h.fake.queueDeletesSeen()
	if len(deletes) != 1 || !strings.HasPrefix(deletes[0], "/api/v3/queue/41?") ||
		!strings.Contains(deletes[0], "blocklist=true") || !strings.Contains(deletes[0], "skipRedownload=true") {
		t.Fatalf("arr queue mutations = %v, want one blocklist_only delete of row 41", deletes)
	}
	var targetDownload string
	if err := h.svc.db.QueryRow(
		"SELECT COALESCE(target_download_id,'') FROM agent_actions WHERE id = ?", actionID,
	).Scan(&targetDownload); err != nil {
		t.Fatalf("read target_download_id: %v", err)
	}
	if targetDownload != "TORRENTABC123" {
		t.Fatalf("target_download_id = %q, want the dispatched download identity", targetDownload)
	}

	// The executor emptied the queue, and production deliberately waits out the
	// absence settle window before any conclusion — the first complete no-match
	// snapshot is never permission to close. Drive that real timeline: the first
	// absent snapshot starts settling (suspending the issue back to recovering),
	// the timer is backdated past the window, and the next snapshot re-promotes
	// the issue for the staged resume.
	h.observe(t, time.Now().UTC())
	if _, err := h.svc.db.Exec(
		"UPDATE issue_observations SET settling_since = ? WHERE issue_id = ?",
		time.Now().UTC().Add(-3*time.Minute), issue.ID,
	); err != nil {
		t.Fatalf("backdate settle window: %v", err)
	}
	h.observe(t, time.Now().UTC())
	if mid := soleIssue(t, h.svc); mid.Status != IssueOpen {
		t.Fatalf("issue after settled absence = %q, want re-promoted %q", mid.Status, IssueOpen)
	}

	// Current production truth, surfaced by this harness: the staged resume is
	// ORPHANED here. Its claim requires the issue at `investigating`, but the
	// settle dance above re-promoted it to `open`, so the close arrives via a
	// FRESH run that recoverWork enqueues for the open issue — reading the same
	// script continuation (scoped read, then conclude). upgradeAbandonProven
	// supplies the server-side proof: the library file is unchanged and the
	// server itself dispatched blocklist_only. (A later wave may teach the
	// resume claim to accept a re-promoted issue; this assertion is the pin
	// that documents today's two-run shape until then.)
	if err := r.Resume(context.Background(), issue.ID); err != nil {
		t.Fatalf("orphaned Resume attempt errored (want quiet no-op): %v", err)
	}
	if err := r.Run(context.Background(), issue.ID); err != nil {
		t.Fatalf("fresh Run after settle: %v", err)
	}

	final := soleIssue(t, h.svc)
	if final.Status != IssueResolved || final.ResolutionKind != ResolutionArrStateCleared {
		t.Fatalf("final issue = status %q / kind %q, want %q / %q",
			final.Status, final.ResolutionKind, IssueResolved, ResolutionArrStateCleared)
	}
	count, runStatus := agentRunRows(t, h.svc, issue.ID)
	if count != 2 || runStatus != "succeeded" {
		t.Fatalf("agent_runs = %d rows (last %q), want the orphaned-resume + succeeded fresh-run pair", count, runStatus)
	}
}

// TestPipelinePreAirSeasonRepairFullLoop drives the flagship season repair the
// way production runs it: the webhook detector opens ONE season issue, the
// agent investigates and proposes delete_media_files, ONE approval deletes all
// nine impossible files and marks their grabs failed at the real arr boundary,
// and — with the failed-download policy ON — the service owns the replacement
// search, so Cantinarr posts no command of its own (the fake treats any
// /command call as an unexpected request). The issue then closes through the
// class's own recovery proof at the resume preflight: nothing unaired holds a
// file. This is ISS-044's hermetic twin.
func TestPipelinePreAirSeasonRepairFullLoop(t *testing.T) {
	h := newPipelineHarness(t)

	// The default library IS the incident: nine files imported thirteen days
	// before their episodes air. History carries the grab+import pair per file,
	// which is the join the blocklist walk stands releases down by.
	var hist []map[string]any
	for n := 1; n <= 9; n++ {
		downloadID := fmt.Sprintf("NZB-%d", n)
		epID := 28*1000 + 11*100 + n
		hist = append(hist,
			map[string]any{"id": 9000 + n, "eventType": "downloadFolderImported", "episodeId": epID, "downloadId": downloadID},
			map[string]any{"id": 8000 + n, "eventType": "grabbed", "episodeId": epID, "downloadId": downloadID},
		)
	}
	h.fake.setSeriesHistory(hist)

	if err := h.svc.recordPreAirSeason(preAirSonarrID, 73871, 615, 11, "Futurama"); err != nil {
		t.Fatalf("record pre-air season: %v", err)
	}
	issue := soleIssue(t, h.svc)
	if issue.Status != IssueOpen || issueProblemKind(t, h.svc, issue.ID) == "" {
		t.Fatalf("pre-air issue = status %q kind %q, want open with a problem kind", issue.Status, issueProblemKind(t, h.svc, issue.ID))
	}

	script := &scriptedTurn{turns: []ai.TranscriptMessage{
		toolCall("r1", "get_episode_timeline", `{}`),
		toolCall("p1", mcp.ToolProposeAction, `{"issue_id":0,"kind":"delete_media_files","params":{"media_type":"tv","tmdb_id":615,"season":11,"episodes":[1,2,3,4,5,6,7,8,9],"blocklist":true},"rationale":"Nine files imported thirteen days before their episodes air cannot be those episodes."}`),
	}}
	r := h.runner(script)

	if err := r.Run(context.Background(), issue.ID); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if after := soleIssue(t, h.svc); after.Status != IssueAwaitingApproval {
		t.Fatalf("issue after run = %q, want %q", after.Status, IssueAwaitingApproval)
	}

	var actionID int64
	if err := h.svc.db.QueryRow(
		"SELECT id FROM agent_actions WHERE issue_id = ? AND status = ?", issue.ID, ActionProposed,
	).Scan(&actionID); err != nil {
		t.Fatalf("find proposed action: %v", err)
	}
	act, err := h.svc.ApproveAction(1, actionID, nil)
	if err != nil {
		t.Fatalf("ApproveAction: %v", err)
	}
	if act.Status != ActionExecuted {
		t.Fatalf("approved action = %q (result %v), want %q", act.Status, act.ResultText, ActionExecuted)
	}

	fileDeletes, failedGrabs := h.fake.mutationsSeen()
	if len(fileDeletes) != 9 || len(failedGrabs) != 9 {
		t.Fatalf("arr mutations = %d file deletes / %d failed grabs, want 9 / 9", len(fileDeletes), len(failedGrabs))
	}

	// The resume preflight closes the issue through preAirRepairProven: the
	// live season no longer holds any unaired file. The staged resume itself is
	// never consumed — this class recovers by its own proof, not by the model
	// narrating one.
	if err := r.Resume(context.Background(), issue.ID); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	final := soleIssue(t, h.svc)
	if final.Status != IssueResolved || final.ResolutionKind != ResolutionArrStateCleared {
		t.Fatalf("final issue = status %q / kind %q, want %q / %q",
			final.Status, final.ResolutionKind, IssueResolved, ResolutionArrStateCleared)
	}
	// The aggregate close aborts the staged resume the proof outran — a truthful
	// terminal state, not a dangling handoff.
	if count, last := agentRunRows(t, h.svc, issue.ID); count != 1 || last != "aborted" {
		t.Fatalf("agent_runs = %d rows (last %q), want one investigation aborted by its own proof", count, last)
	}
}

// TestPipelineUserComplaintReporterCloseFullLoop drives the reported path: a
// household member reports wrong content on an episode whose download finished
// weeks ago (the queue is EMPTY — the expected reading for a content
// complaint, never a dead end), the agent diagnoses from the library, ONE
// approval deletes the file and stands its grab down, and the REPORTER — never
// an admin adjudicating content they haven't watched — closes their own
// report. A user issue can never machine-close: the typed proofs are refused
// for subjective reports, so reporter_confirmed is the only terminal this
// test's happy path may reach.
func TestPipelineUserComplaintReporterCloseFullLoop(t *testing.T) {
	h := newPipelineHarness(t)
	if _, err := h.svc.db.Exec("INSERT INTO users (id, username, password_hash, role) VALUES (2, 'viewer', '', 'user')"); err != nil {
		t.Fatalf("seed reporter: %v", err)
	}

	// S02E03 aired three weeks ago and holds file 50203, imported post-air —
	// a perfectly healthy-looking library entry that happens to be the wrong
	// content. Only the person who watched it can know that.
	episodes, files := buildPreAirSeason(28, 2, []preAirEpisode{
		{number: 3, airsIn: -21 * 24 * time.Hour, hasFile: true},
	})
	h.fake.setLibrary(episodes, files)
	h.fake.setSeriesHistory([]map[string]any{
		{"id": 9203, "eventType": "downloadFolderImported", "episodeId": 28203, "downloadId": "NZB-WRONG"},
		{"id": 8203, "eventType": "grabbed", "episodeId": 28203, "downloadId": "NZB-WRONG"},
	})

	resp, err := h.svc.CreateUserIssue(2, &CreateIssueRequest{
		InstanceID: preAirSonarrID, MediaType: "tv", TmdbID: 615, TvdbID: 73871,
		SeasonNumber: 2, EpisodeNumber: 3, Category: CategoryWrongContent,
		Reason: "This is a different episode entirely.", Title: "Futurama",
	})
	if err != nil {
		t.Fatalf("CreateUserIssue: %v", err)
	}
	issueID := resp.IssueID

	// A content complaint starts in the same quiet observation every report
	// does. Its scope was never in the queue, so promotion runs the absence
	// path: backdate the report's clock past the min window, let one empty
	// snapshot start settling, backdate the settle, and the next promotes.
	if _, err := h.svc.db.Exec(
		"UPDATE issue_observations SET first_seen_at = ?, updated_at = ? WHERE issue_id = ?",
		time.Now().UTC().Add(-30*time.Minute), time.Now().UTC(), issueID,
	); err != nil {
		t.Fatalf("backdate observation: %v", err)
	}
	h.observe(t, time.Now().UTC())
	if _, err := h.svc.db.Exec(
		"UPDATE issue_observations SET settling_since = ? WHERE issue_id = ?",
		time.Now().UTC().Add(-3*time.Minute), issueID,
	); err != nil {
		t.Fatalf("backdate settle: %v", err)
	}
	h.observe(t, time.Now().UTC())
	promoted, err := h.svc.GetIssue(issueID)
	if err != nil {
		t.Fatalf("load promoted issue: %v", err)
	}
	if promoted.Status != IssueOpen {
		t.Fatalf("user issue after absence settle = %q, want promoted %q", promoted.Status, IssueOpen)
	}

	script := &scriptedTurn{turns: []ai.TranscriptMessage{
		toolCall("r1", "get_history", `{}`),
		toolCall("p1", mcp.ToolProposeAction, `{"issue_id":0,"kind":"delete_media_files","params":{"media_type":"tv","tmdb_id":615,"season":2,"episodes":[3],"blocklist":true},"rationale":"The reporter watched it; the file is the wrong episode. Delete it and stand the release down."}`),
	}}
	r := h.runner(script)
	if err := r.Run(context.Background(), issueID); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var actionID int64
	if err := h.svc.db.QueryRow(
		"SELECT id FROM agent_actions WHERE issue_id = ? AND status = ?", issueID, ActionProposed,
	).Scan(&actionID); err != nil {
		t.Fatalf("find proposed action: %v", err)
	}
	act, err := h.svc.ApproveAction(1, actionID, nil)
	if err != nil {
		t.Fatalf("ApproveAction: %v", err)
	}
	if act.Status != ActionExecuted {
		t.Fatalf("approved action = %q (result %v), want %q", act.Status, act.ResultText, ActionExecuted)
	}
	fileDeletes, failedGrabs := h.fake.mutationsSeen()
	if len(fileDeletes) != 1 || len(failedGrabs) != 1 {
		t.Fatalf("arr mutations = %d deletes / %d failed grabs, want 1 / 1", len(fileDeletes), len(failedGrabs))
	}

	// The reporter's verdict is the only closure a subjective report accepts.
	loaded, err := h.svc.GetIssue(issueID)
	if err != nil {
		t.Fatalf("reload issue: %v", err)
	}
	canConfirm, err := h.svc.CanReporterConfirmFix(loaded)
	if err != nil || !canConfirm {
		t.Fatalf("CanReporterConfirmFix = (%v, %v), want (true, nil) after an executed fix", canConfirm, err)
	}
	if err := h.svc.ReporterConfirmFix(context.Background(), issueID, 2); err != nil {
		t.Fatalf("ReporterConfirmFix: %v", err)
	}
	final, err := h.svc.GetIssue(issueID)
	if err != nil {
		t.Fatalf("load final issue: %v", err)
	}
	if final.Status != IssueResolved || final.ResolutionKind != ResolutionReporterConfirmed {
		t.Fatalf("final issue = status %q / kind %q, want %q / %q",
			final.Status, final.ResolutionKind, IssueResolved, ResolutionReporterConfirmed)
	}
}
