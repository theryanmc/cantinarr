# Media server accounts

Real Jellyfin truth for account creation, library scope, switching access off and on, and linking. Request/response contracts, RBAC, rollback ordering, and log redaction are proven by the hermetic suites; every case here reads the resulting state on the media server itself (`GET /Users/{id}`, a sign-in as the created user, and `GET /Users/{id}/Views` for library scope).

Use the [run template](../run-template.md) to record executions of these cases.

## Account creation, scope, and linking

- [ ] `MSRV-001` · P0 · UI/LIVE — Add a Jellyfin instance with a real API key; verify Test Connection passes, the Shared libraries section lists exactly the server's libraries under its reported name, and the chosen library ids plus the sign-in address survive a save and reopen.
- [ ] `MSRV-002` · P0 · UI/LIVE — As a granted user, create an account from the guide with a chosen password; verify `GET /Users` on Jellyfin shows the user with `IsAdministrator=false`, a sign-in with that password succeeds, and the Cantinarr database holds no trace of the password.
- [ ] `MSRV-003` · P0 · LIVE — Create an account whose username already exists on the server; verify the app reports the name as taken, no Jellyfin user is created or altered, and linking that existing account from Users records the row without changing the Jellyfin user.
- [ ] `MSRV-009` · P1 · UI/LIVE — Create with two of three libraries chosen; verify `GET /Users/{id}/Views` as the new user lists only those two, and a fourth library added on Jellyfin afterwards stays hidden.
- [ ] `MSRV-010` · P1 · UI/LIVE — Create with no libraries chosen, then add a library on Jellyfin; verify the user sees it with no Cantinarr action.

## Access off and on

- [ ] `MSRV-004` · P0 · UI/LIVE — Remove the user's grant in the instance editor; verify the Jellyfin user reads `IsDisabled=true`, the guide says access is turned off, and granting again flips it back with watch history intact.
- [ ] `MSRV-005` · P0 · LIVE — Delete a Cantinarr user with a linked account; verify the Jellyfin user is disabled, not deleted, and the account row is gone.
- [ ] `MSRV-006` · P1 · UI/LIVE — Set, change, and clear the sign-in address; verify the guide shows the exact address, Copy and Open work on iOS, Android, and web, and clearing it shows the ask-your-admin line.

## Blindness, exposure, and secrets

- [ ] `MSRV-007` · P1 · CHAOS/UI — Stop the Jellyfin container and open the guide; verify an existing account renders from Cantinarr's record with the could-not-confirm line rather than as no account, and that a create attempt fails with the generic message and leaves no half-created user once Jellyfin returns.
- [ ] `MSRV-008` · P0 · SEC — As an ungranted requester, call `POST /api/media-servers/{id}/account` with a real and a made-up instance id; verify both answer the identical 403 body, `GET /api/media-servers` and `/api/config` carry no instance URL, and every admin media-server route answers 403 for the requester role.
- [ ] `MSRV-011` · P1 · SEC — Capture server logs and the app's HTTP debug log while creating an account; verify neither contains the API key or the chosen password and that `/api/media-servers/…` paths are cut after the scope segment.
