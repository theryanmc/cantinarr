package remediation

import (
	"fmt"
	"time"
)

// AgentDigest is the rolling scoreboard: what the agent pipeline did over a
// window, computed live from the tables that already record everything. The
// system pages reliably when it needs a decision and says nothing when
// automation works — this is the deliberate pull surface that makes the quiet
// weeks legible, so a well-tuned agent stops reading as "the thing that only
// ever brings me problems".
type AgentDigest struct {
	Days int `json:"days"`

	IssuesOpened   int64 `json:"issues_opened"`
	IssuesResolved int64 `json:"issues_resolved"`
	// ZeroTouch counts resolved issues in the window where no human made any
	// action decision — the earned-autonomy lane doing its job end to end.
	ZeroTouch        int64 `json:"zero_touch"`
	ActionsExecuted  int64 `json:"actions_executed"`
	RuleApproved     int64 `json:"rule_approved"`
	ReporterClosed   int64 `json:"reporter_closed"`
	TokensIn         int64 `json:"tokens_in"`
	TokensOut        int64 `json:"tokens_out"`
	NeedsAdminOpen   int64 `json:"needs_admin_open"`
	PendingProposals int64 `json:"pending_proposals"`
	PausedRules      int64 `json:"paused_rules"`

	// RuleCounts names the rules that did the work: label -> executed count in
	// the window, newest-heavy rules first in the slice.
	RuleCounts []DigestRuleCount `json:"rule_counts"`

	GeneratedAt time.Time `json:"generated_at"`
}

// DigestRuleCount is one standing rule's contribution to the window.
type DigestRuleCount struct {
	Label string `json:"label"`
	Count int64  `json:"count"`
}

// Digest computes the agent scoreboard for the trailing N days (1..90).
func (s *Service) Digest(days int) (*AgentDigest, error) {
	if days <= 0 {
		days = 7
	}
	if days > 90 {
		days = 90
	}
	cutoff := fmt.Sprintf("-%d days", days)
	d := &AgentDigest{Days: days, GeneratedAt: time.Now().UTC()}

	row := s.db.QueryRow(`SELECT
		(SELECT COUNT(1) FROM issues WHERE created_at >= datetime('now', ?1)),
		(SELECT COUNT(1) FROM issues WHERE closed_at >= datetime('now', ?1) AND status = 'resolved'),
		(SELECT COUNT(1) FROM issues i WHERE i.closed_at >= datetime('now', ?1) AND i.status = 'resolved'
		   AND NOT EXISTS (SELECT 1 FROM agent_actions a WHERE a.issue_id = i.id AND a.decided_by IS NOT NULL)),
		(SELECT COUNT(1) FROM agent_actions WHERE executed_at >= datetime('now', ?1) AND status = 'executed'),
		(SELECT COUNT(1) FROM agent_actions WHERE executed_at >= datetime('now', ?1) AND status = 'executed' AND auto_rule_id IS NOT NULL AND decided_by IS NULL),
		(SELECT COUNT(1) FROM issues WHERE closed_at >= datetime('now', ?1) AND resolution_kind = ?2),
		(SELECT COALESCE(SUM(input_tokens), 0) FROM agent_runs WHERE started_at >= datetime('now', ?1)),
		(SELECT COALESCE(SUM(output_tokens), 0) FROM agent_runs WHERE started_at >= datetime('now', ?1)),
		(SELECT COUNT(1) FROM issues WHERE closed_at IS NULL AND status = 'needs_admin'),
		(SELECT COUNT(1) FROM agent_actions a JOIN issues i ON i.id = a.issue_id
		   WHERE a.status = 'proposed' AND i.closed_at IS NULL AND i.status = 'awaiting_approval'),
		(SELECT COUNT(1) FROM agent_approval_rules WHERE status = 'paused')`,
		cutoff, ResolutionReporterConfirmed,
	)
	if err := row.Scan(&d.IssuesOpened, &d.IssuesResolved, &d.ZeroTouch, &d.ActionsExecuted,
		&d.RuleApproved, &d.ReporterClosed, &d.TokensIn, &d.TokensOut,
		&d.NeedsAdminOpen, &d.PendingProposals, &d.PausedRules); err != nil {
		return nil, fmt.Errorf("compute agent digest: %w", err)
	}

	rows, err := s.db.Query(`
		SELECT r.problem_kind, r.action_kind, r.action_facet, COUNT(1)
		FROM agent_actions a
		JOIN agent_approval_rules r ON r.id = a.auto_rule_id
		WHERE a.executed_at >= datetime('now', ?) AND a.status = 'executed'
		GROUP BY r.id ORDER BY COUNT(1) DESC LIMIT 10`, cutoff)
	if err != nil {
		return nil, fmt.Errorf("compute digest rule counts: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var problem, kind, facet string
		var count int64
		if err := rows.Scan(&problem, &kind, &facet, &count); err != nil {
			return nil, err
		}
		d.RuleCounts = append(d.RuleCounts, DigestRuleCount{
			Label: approvalRuleLabel(problem, ActionKind(kind), facet),
			Count: count,
		})
	}
	return d, rows.Err()
}
