# Instances, realtime behavior, and push

Connecting real services, managed-webhook truth against real arrs, end-to-end realtime convergence, and real APNs delivery on physical devices. Instance CRUD contracts, event mapping, WebSocket authorization, and preference logic are proven by the hermetic suites.

Use the [run template](../run-template.md) to record executions of these cases.

## Real service connections and managed webhooks

- [ ] `INST-001` · P0 · UI/LIVE — Create and test one valid instance of each supported type: Radarr, Sonarr, Chaptarr, SABnzbd, qBittorrent, NZBGet, Transmission, and Tautulli; verify type-specific credentials and capabilities work.
- [ ] `INST-013` · P0 · LIVE — Configure instant updates on Radarr and Sonarr; verify Cantinarr rotates a server-only credential, upserts one managed Connect webhook, and the app never receives the secret.

## Realtime convergence

- [ ] `RT-007` · P0 · LIVE — Import a movie and several episodes; verify availability/events and new-content notification once despite webhook + poll overlap.

## Push delivery and notification taps

- [ ] `PUSH-004` · P0 · LIVE — Register, rotate, and delete APNs tokens for one/multiple devices; verify tokens bind to authenticated device/user and another user cannot alter them.
- [ ] `PUSH-006` · P0 · LIVE/UI — On iOS, cover not-determined, allowed, denied, settings redirect, and return-to-app; verify permission controls and token registration reflect actual OS state.
- [ ] `PUSH-013` · P0 · LIVE — Import movie, multiple episodes, and a Chaptarr book format; verify opted-in audiences (a book alert reaches only admins and that instance's grantees), correct movie/series/book-format copy, collapse keys, no duplicate from poll/webhook overlap, and silence for a failed/removed book download leaving the queue.
- [ ] `PUSH-025` · P0 · LIVE — Restart the container with a Chaptarr book download in flight and let it import while the server is down; verify the alert arrives on the first poll after boot (books have no webhook, so this is their only witness), arrives once, and carries the right format copy.
- [ ] `PUSH-026` · P1 · LIVE — Restart with a movie/episode download in flight; verify exactly one alert across the webhook and the resumed poll diff, and that a restart more than 6 hours after the import stays silent.
- [ ] `PUSH-027` · P1 · LIVE — Restart with the push gateway unreachable, then restore it; verify the resumed alert waits for enrollment rather than being lost, and that a gateway left down past the 6-hour cutoff drops the batch without wedging the poller.
- [ ] `PUSH-017` · P0 · LIVE/UI — Tap every notification type from foreground, background, and terminated app; verify exact detail/approval/issue/action/users/Plex/settings destination (book request decisions and new-book alerts open the book detail via the payload's foreign id; older payloads without one open the Books tab) and no duplicate navigation.
- [ ] `PUSH-023` · P1 · LIVE — Use self-test from preferences and admin per-user diagnostics; verify delivered/no-token/not-configured/partial/failure results and no test notification changes product badges.
