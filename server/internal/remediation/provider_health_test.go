package remediation

import (
	"context"
	"fmt"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/ai"
)

// An unconfigured shared provider is a standing condition, not an agent
// failure. Before this guard, every provider-less run gave up the issue at
// needs_admin (a dead end recoverWork never retries) and fed the auto-dispatch
// circuit breaker — five detected problems on a fresh, provider-less install
// silently reverted the admin's own auto_dispatch setting.

func unavailableResolver() autonomousTurnResolver {
	return autonomousTurnResolverFunc(func(context.Context, ai.AutonomousModelOverride) (ai.AutonomousTurn, error) {
		return ai.AutonomousTurn{}, fmt.Errorf("%w: nothing configured", ai.ErrSharedAIUnavailable)
	})
}

func openProviderIssues(t *testing.T, svc *Service) []Issue {
	t.Helper()
	var out []Issue
	issues, err := svc.ListIssues("")
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	for _, iss := range issues {
		if iss.Source == SourceSystem && iss.Title == remediationProviderTitle && iss.ClosedAt == nil {
			out = append(out, iss)
		}
	}
	return out
}

// TestRunWithoutProviderLeavesIssueWaiting: the issue must stay open (retryable
// by recoverWork), spend no run, and raise exactly ONE deduped system issue no
// matter how many issues retry.
func TestRunWithoutProviderLeavesIssueWaiting(t *testing.T) {
	r, svc, issueID := newTestRunner(t, &fakeToolHost{}, &scriptedTurn{})
	r.turns = unavailableResolver()

	for i := 0; i < 3; i++ {
		if err := r.Run(context.Background(), issueID); err != nil {
			t.Fatalf("Run %d without provider: %v", i, err)
		}
	}

	var status string
	if err := svc.db.QueryRow("SELECT status FROM issues WHERE id = ?", issueID).Scan(&status); err != nil {
		t.Fatalf("read issue status: %v", err)
	}
	if status != IssueOpen {
		t.Fatalf("issue status = %q, want still %q (retryable, never a needs_admin dead end)", status, IssueOpen)
	}
	if count, _ := agentRunRows(t, svc, issueID); count != 0 {
		t.Fatalf("agent_runs = %d rows, want none for a provider-less attempt", count)
	}
	provider := openProviderIssues(t, svc)
	if len(provider) != 1 {
		t.Fatalf("provider system issues = %d, want exactly one deduped", len(provider))
	}
	if provider[0].Status != IssueNeedsAdmin {
		t.Fatalf("provider issue status = %q, want %q", provider[0].Status, IssueNeedsAdmin)
	}
}

// TestProviderRestoredResolvesSystemIssue: the first successful resolve closes
// the standing system issue with its own resolution kind.
func TestProviderRestoredResolvesSystemIssue(t *testing.T) {
	r, svc, issueID := newTestRunner(t, &fakeToolHost{}, &scriptedTurn{})
	r.turns = unavailableResolver()
	if err := r.Run(context.Background(), issueID); err != nil {
		t.Fatalf("Run without provider: %v", err)
	}
	if got := openProviderIssues(t, svc); len(got) != 1 {
		t.Fatalf("provider system issues = %d, want one before recovery", len(got))
	}

	r.turns = scriptedTurnResolver(&scriptedTurn{})
	if err := r.Run(context.Background(), issueID); err != nil {
		t.Fatalf("Run with provider restored: %v", err)
	}

	if got := openProviderIssues(t, svc); len(got) != 0 {
		t.Fatalf("provider system issue still open after recovery: %#v", got)
	}
	var kind string
	if err := svc.db.QueryRow(
		"SELECT resolution_kind FROM issues WHERE dedupe_key = ? ORDER BY id DESC LIMIT 1",
		remediationProviderDedupeKey,
	).Scan(&kind); err != nil {
		t.Fatalf("read provider issue resolution kind: %v", err)
	}
	if kind != ResolutionRemediationProviderConfigured {
		t.Fatalf("resolution_kind = %q, want %q", kind, ResolutionRemediationProviderConfigured)
	}
}
