package remediation

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"
)

// Remediation memory. A fix is only "already tried" against the exact arr
// download it acted on, so every executed queue-scoped action records that
// download id and every later reader keys on it.
//
// The motivating incident: a stalled torrent was removed + blocklisted, the arr
// re-grabbed the IDENTICAL release 48 seconds later (its blocklist matches on
// the release title, which differed by punctuation between the two indexer
// listings), and a standing rule then auto-approved the same fix again on the
// same release. Nothing in the system could see that the first fix had not
// held, because nothing recorded what a fix had acted on.
//
// Two consumers, deliberately separate:
//   - autoApprovalWouldRepeatFailedRemedy is the ENFORCEMENT boundary. A rule
//     may not silently replay a remedy against a release it already ran on;
//     that proposal waits for a human who can see the history.
//   - priorRemediationAttempts is the agent's CONTEXT. It carries only
//     server-authored identity and clock fields into the system prompt, never
//     arr free text (release names, error strings), which stays at user-role
//     trust exactly as buildSystemPrompt documents.

// errRemedyAlreadyApplied means a standing rule's proposal repeats a fix that
// already executed against the download the issue is still holding. It is an
// expected outcome, not a failure: the sweep skips quietly and the proposal
// stays visible for manual approval.
var errRemedyAlreadyApplied = errors.New("remedy already applied to this download")

// autoRulePausedRepeatIneffective is the fixed pause copy for a rule whose fix
// would have been replayed against a release it already ran on.
const autoRulePausedRepeatIneffective = "A fix this rule approved was already applied to this exact download and the problem came back. Review it before re-arming this rule."

// repeatPauseCause is the issue-thread clause for the same case. The generic
// "did not complete successfully" copy would be wrong here: the fix completed,
// it just did not hold.
const repeatPauseCause = "the same fix had already been applied to this exact download and the problem came back"

// actsOnOneDownload reports whether an action of this kind, with these canonical
// params, targets exactly ONE arr download. Only those actions can be attributed
// to a release, so only those are recorded and matched. Library-wide kinds
// (trigger_search, rescan) deliberately never gain a target.
func actsOnOneDownload(kind ActionKind, canonical json.RawMessage) bool {
	switch kind {
	case ActionRemediateQueue:
		var p RemediateQueueParams
		return json.Unmarshal(canonical, &p) == nil && p.QueueID > 0
	case ActionManualImport:
		var p ManualImportParams
		return json.Unmarshal(canonical, &p) == nil && p.QueueID > 0
	case ActionGrabRelease:
		// Only the replaced queue item is an existing download this action acts
		// on; the release it grabs afterwards is a new one the arr assigns.
		var p GrabReleaseParams
		return json.Unmarshal(canonical, &p) == nil && p.QueueIDToReplace > 0
	default:
		return false
	}
}

// issueDownloadIdentity reads the release an issue is currently pinned to. Call
// it IMMEDIATELY BEFORE dispatch, never after: the observation sweeper rewrites
// issues.download_id whenever the arr swaps the release, and an arr round-trip
// is long enough for that to land. Reading before means the recorded target is
// the same value the Executor's identity gate is about to validate against.
func (s *Service) issueDownloadIdentity(issueID int64) string {
	var downloadID sql.NullString
	if err := s.db.QueryRow("SELECT download_id FROM issues WHERE id = ?", issueID).Scan(&downloadID); err != nil {
		log.Printf("remediation: read issue %d download identity: %v", issueID, err)
		return ""
	}
	return strings.TrimSpace(downloadID.String)
}

// noteActionTargetDownload records the arr download a dispatched action acted on.
//
// Why the issue's download identity is the right source: the Executor's gate
// (validateDownloadIdentity) REFUSES to dispatch a queue-scoped action whose
// live queue row carries any other download id, so an action that reached the
// arr at all provably acted on that download. Copying it onto the action freezes
// the fact; issues.download_id itself keeps tracking whatever the arr holds now
// and can never answer "what did that past fix touch?".
//
// Best-effort by design: a lost stamp costs the repeat guard one lap, never the
// correctness of the dispatch that just happened.
func (s *Service) noteActionTargetDownload(actionID int64, kind ActionKind, canonical json.RawMessage, downloadID string) {
	if downloadID == "" || !actsOnOneDownload(kind, canonical) {
		return
	}
	if _, err := s.db.Exec(
		"UPDATE agent_actions SET target_download_id = ? WHERE id = ? AND target_download_id IS NULL",
		downloadID, actionID,
	); err != nil {
		log.Printf("remediation: record action %d target download: %v", actionID, err)
	}
}

// remediationAttempt is one already-dispatched fix on an issue, paired with the
// arr's own answer to whether it held.
type remediationAttempt struct {
	kind       ActionKind
	facet      string
	downloadID string
	executedAt time.Time
	// reAddedAt is set when the arr put this SAME download back after the fix
	// ran (issue_observation_downloads tracks the newest arr Added boundary per
	// download). That is the machine-checkable proof the fix did not hold.
	reAddedAt time.Time
}

// heldQuestionable reports whether the arr re-added this exact download after
// the fix dispatched.
func (a remediationAttempt) recurred() bool {
	return !a.reAddedAt.IsZero() && a.reAddedAt.After(a.executedAt)
}

// priorRemediationAttempts returns every dispatched, download-attributed fix on
// an issue, oldest first, joined to the arr's re-add boundary for that same
// download. Failed actions are excluded: a fix that never reached the arr is
// not something the agent already tried.
func (s *Service) priorRemediationAttempts(issueID int64) ([]remediationAttempt, error) {
	rows, err := s.db.Query(
		`SELECT a.kind, COALESCE(NULLIF(a.approved_params, ''), a.params),
		        a.target_download_id, a.executed_at, d.arr_added_at
		 FROM agent_actions a
		 LEFT JOIN issue_observation_downloads d
		   ON d.issue_id = a.issue_id AND lower(d.download_id) = lower(a.target_download_id)
		 WHERE a.issue_id = ? AND a.status IN (?, ?)
		   AND a.executed_at IS NOT NULL
		   AND a.target_download_id IS NOT NULL AND a.target_download_id != ''
		 ORDER BY a.executed_at, a.id`,
		issueID, ActionExecuted, ActionOutcomeUnknown,
	)
	if err != nil {
		return nil, fmt.Errorf("query prior remediation attempts: %w", err)
	}
	defer rows.Close()
	var out []remediationAttempt
	for rows.Next() {
		var kind, params, downloadID string
		var executedAt sql.NullTime
		var reAddedAt sql.NullTime
		if err := rows.Scan(&kind, &params, &downloadID, &executedAt, &reAddedAt); err != nil {
			return nil, fmt.Errorf("scan prior remediation attempt: %w", err)
		}
		attempt := remediationAttempt{
			kind:       ActionKind(kind),
			downloadID: downloadID,
			executedAt: executedAt.Time,
			reAddedAt:  reAddedAt.Time,
		}
		if facet, ok := actionAutoFacet(attempt.kind, json.RawMessage(params)); ok {
			attempt.facet = facet
		}
		out = append(out, attempt)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read prior remediation attempts: %w", err)
	}
	return out, nil
}

// autoApprovalWouldRepeatFailedRemedy reports whether a proposed action replays
// a (kind, facet) that already dispatched against the download the issue is
// STILL holding. That pairing is the loop: the fix ran, the same release is
// back, and repeating it can only produce the same outcome.
//
// It compares against issues.download_id — the release the arr holds now — so a
// genuinely new release that inherited the same problem is never mistaken for a
// repeat. An issue with no download identity can't be judged and never matches.
func (s *Service) autoApprovalWouldRepeatFailedRemedy(actionID int64) (bool, error) {
	var issueID int64
	var kind, params string
	err := s.db.QueryRow(
		`SELECT issue_id, kind, COALESCE(NULLIF(approved_params, ''), params)
		 FROM agent_actions WHERE id = ?`, actionID,
	).Scan(&issueID, &kind, &params)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("load action %d for repeat check: %w", actionID, err)
	}
	facet, ok := actionAutoFacet(ActionKind(kind), json.RawMessage(params))
	if !ok {
		return false, nil
	}
	rows, err := s.db.Query(
		`SELECT COALESCE(NULLIF(a.approved_params, ''), a.params)
		 FROM agent_actions a JOIN issues i ON i.id = a.issue_id
		 WHERE a.issue_id = ? AND a.id != ? AND a.kind = ? AND a.status IN (?, ?)
		   AND a.target_download_id IS NOT NULL AND a.target_download_id != ''
		   AND i.download_id IS NOT NULL AND i.download_id != ''
		   AND lower(a.target_download_id) = lower(i.download_id)`,
		issueID, actionID, kind, ActionExecuted, ActionOutcomeUnknown,
	)
	if err != nil {
		return false, fmt.Errorf("query repeated remedies: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var priorParams string
		if err := rows.Scan(&priorParams); err != nil {
			return false, fmt.Errorf("scan repeated remedy: %w", err)
		}
		// The facet must come from the typed params, exactly as rule matching
		// derives it, so "already tried" can never be broader than the opt-in
		// an admin actually armed.
		if prior, ok := actionAutoFacet(ActionKind(kind), json.RawMessage(priorParams)); ok && prior == facet {
			return true, nil
		}
	}
	return false, rows.Err()
}

// pauseRuleForRepeatedRemedy disarms a rule that was about to replay an
// ineffective fix, recording the thread evidence in the same transaction.
func (s *Service) pauseRuleForRepeatedRemedy(ruleID, issueID int64) (bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	paused, err := pauseApprovalRuleTx(tx, ruleID, autoRulePausedRepeatIneffective)
	if err != nil || !paused {
		return false, err
	}
	label, err := approvalRuleLabelTx(tx, ruleID)
	if err != nil {
		return false, err
	}
	if err := insertRulePausedMessageTx(tx, issueID, label, repeatPauseCause); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}
