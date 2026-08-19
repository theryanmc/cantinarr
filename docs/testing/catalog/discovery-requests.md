# Discovery and requests

Release-day journeys against real TMDB/Trakt and real arr instances. Search behavior, availability computation, request policy, and approval contracts are proven by the hermetic suites.

Use the [run template](../run-template.md) to record executions of these cases.

## Discovery against live providers

- [ ] `DISC-001` · P0 · UI/LIVE — Load actual dashboards: movie spotlight, Popular Movies, Top Rated, Coming Soon, Most Anticipated, Downloading Soon, Recently Downloaded; series spotlight, Popular TV Shows, Most Anticipated, Recently Downloaded, Airing Next. Verify identity/image/date/status/detail target.
- [ ] `DISC-007` · P0 · UI/LIVE — Move a title through unavailable, requested, downloading, partial, and available outside Cantinarr; verify search/detail chips converge over webhook/WebSocket/refetch with requester vocabulary only.
- [ ] `DISC-009` · P1 · LIVE — Against a real Chaptarr, download one book and side-load another that only a rescan discovers; verify both reach Recently Added within one refresh, covers render, and an eBook and Audiobook of the same title appear as two cards ordered by when each file landed. Against a real title with one format on disk and the other genuinely monitored: verify the card reads Requested while the second format is still fetching, flips to Available once it lands, and reads Partial for a title whose second format was never requested — and that the subtitle names both formats' state in eBook-then-Audiobook order.
- [ ] `DISC-017` · P1 · LIVE — With Trakt configured, load trending, popular, public lists/items, calendar, anticipated, and recommendations; verify IDs/media types bridge to usable details.

## Request and approval journeys

- [ ] `REQ-001` · P0 · UI/LIVE — Request a new movie with no approval required; verify the correct user's Radarr instance receives the exact TMDB ID, configured root/profile, monitored flag, and search action once.
- [ ] `REQ-006` · P0 · UI/LIVE — Select a noncontiguous set of real seasons; verify Specials are absent from requester UI and only selected real seasons are stored, monitored, and searched in sorted/deduplicated order.
- [ ] `REQ-021` · P0 · LIVE — Approve each pending media type without override; verify it executes the stored request once, records approver/time, leaves the queue, updates requester history/state, and sends configured decision push.
