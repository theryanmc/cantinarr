# Book automation setup

Books differ from movies and TV in two ways worth knowing before you start:

- **Chaptarr has no global default instance.** Requesters need a per-user pin or explicit instance grant to see Books. Admins can browse Open Library before connecting Chaptarr; requests and library search require the connection.
- **An ebook can finish downloading between two polls.** Instant updates aren't a nicety here; they're what makes the "ready to read" notification reliable.

This page is the whole path, in order.

## 1. Have a working Chaptarr

Cantinarr manages an existing Chaptarr instance — it doesn't deploy one. What it needs from you:

- A URL the **Cantinarr server** can reach. Clients never dial instance URLs, so cluster-internal names are fine and preferred; see the [instance URL guidance](../README.md#configuration).
- A Chaptarr API key.
- Chaptarr itself already working: a root folder, an indexer, a download client, and grabs that actually complete.

That last point is the one that eats a weekend. If you're building the Chaptarr side from scratch — particularly routing Chaptarr and its torrent client through a VPN gateway, where the container topology is easy to get subtly wrong — [`mam-chaptarr-protonvpn-skill`](https://github.com/windoze95/mam-chaptarr-protonvpn-skill) is an agent skill that walks the whole build: one Gluetun/ProtonVPN namespace per environment, qBittorrent and Chaptarr attached to it, forwarded-port sync, indexer and tracker-host sessions kept separate, and verification that refuses to accept "the WebUI loads" as proof it works. It's an independent project, not part of Cantinarr, and it covers the layer *below* this page.

One thing that carries straight over: if Chaptarr shares a gateway's network stack (`network_mode: container:<gateway>`), it has no address of its own, so the instance URL you give Cantinarr must name the gateway that publishes the port.

## 2. Add the instance

**Settings → Add Instance**, service type `chaptarr` (the Setup Checklist's **Books** row opens this same form with Chaptarr already selected), then the URL and API key. Save runs a live connection check from the server — the same host that will dial it in production — so a green result means what it says.

Chaptarr speaks the Readarr `/api/v1` API. Enter just the base URL; Cantinarr appends the API path.

Use Chaptarr's **root** URL, never one of its media-scoped prefixes (`/ebook`, `/audiobook`, or a `/readarr/...` compatibility path). Those prefixes exist for Readarr-only clients and change how Chaptarr answers identity lookups; Cantinarr talks to the native API and handles both formats itself.

## 3. Grant access per user

This is the step people miss. Unlike Radarr and Sonarr, Chaptarr has no global default — pinning a user to a Chaptarr instance is how you grant that user access to books.

Pin from either side: the instance editor, or **Settings → Users** for one person. Remove both the pin and any explicit grants to revoke access. Admins see Books before setup unless it was conditionally hidden for the server, and see configured Chaptarr instances without a pin; everyone else needs one, and until they have it `services.chaptarr` stays `false` and the Books tab stays hidden.

Running more than one Chaptarr instance is fine — pin different households or different libraries to different instances.

## 4. Check instant updates

Adding the instance already turned these on: the server rotates a per-instance credential and installs its own authenticated webhook in Chaptarr the moment the instance is created; the secret moves server-to-server and never reaches a device. The create confirmation says whether it worked.

If it couldn't — most commonly because the callback wasn't reachable — open the instance: the **Instant updates** section shows the live state, read from Chaptarr itself, and **Configure instant updates** re-runs the install. Set `CANTINARR_ARR_CALLBACK_URL` first if Cantinarr sits behind a reverse proxy. The callback has to be resolvable **from inside the Chaptarr container**, so in Docker or Kubernetes a cluster-internal origin like `http://cantinarr:8585` is usually the right value.

Without this, Cantinarr falls back to polling, and a fast ebook grab can land and be announced late — or, if it imports and finishes between two polls, look like nothing happened.

The webhook also speeds up "Waiting for library" requests: Chaptarr announces the moment a queued author import lands, and Cantinarr completes the waiting request right then instead of on its next five-minute check.

## 5. Optional — let people download the files

Off by default, and deliberately two-layered. Chaptarr reports file paths but doesn't serve the bytes, so the deployment has to hand Cantinarr the files itself:

1. Mount each library read-only into the container and list the visible boundary in `CANTINARR_MEDIA_ROOTS` (for example `- /mnt/nas/books:/media/books:ro` with `CANTINARR_MEDIA_ROOTS=/media`).
2. In the instance editor, map each path Chaptarr reports to a folder inside that boundary.

A Chaptarr instance often needs several mappings — `/ebooks`, `/audiobooks`, and any per-library variants. The two sides don't have to match, and folder names never determine the book format; the format comes from the arr's own file record.

An instance offers downloads only once explicit mappings are saved for it.

## 6. Verify

- The Books tab appears for a pinned non-admin user, opening on **Popular Books**, then **Browse by genre**, Recently Added, Authors and Series.
- Searching a title returns results, and requesting an eBook or Audiobook row reads **Requested** until it downloads.
- A grab that completes in Chaptarr flips the row to available within seconds, not on the next poll — that's the webhook working.
- If downloads are on, a completed book offers a working download from a device.

## Discover books

Admins can browse Popular Books, genres, covers, and cold work links before Chaptarr is connected. **Connect Chaptarr to request books** opens the existing instance form with Chaptarr selected; saving returns to the title and loads its verified request target and per-format status. The Books tab has a fixed **Set up Chaptarr** / **Hide this tab** footer until a Chaptarr instance is configured. Hiding applies to everyone and can be changed under **Settings > Modules > Discover > Discover tabs**. Configuring Chaptarr restores the tab automatically, even if it later goes offline; removing it makes the saved hide preference apply again. The footer replaces the previous toolbar setup shortcut, and setup returns to the catalog after saving or cancelling. Library rows and ownership badges require a connected instance. Requesters and kids still need an explicit grant. On an older server, admins see an update notice for unsupported browsing before setup; Hide is disabled with an update explanation when tab visibility settings are unsupported.

Popular Books and twelve genres use [Open Library search](https://openlibrary.org/dev/docs/api/search), with no extra account, API key or setting. Popularity is the provider's overall reading-list ranking, labelled **Popular on Open Library**. Genre pages keep provider relevance order; Biography & Memoir searches either subject. There is no time-period selector or separate audiobook chart.

Books prefer English edition titles and covers, falling back to the work when none exists. This does not change the work's identity or hide books in other languages. Movie/TV discovery source and English-only settings do not affect books. Covers load on the device from fixed `covers.openlibrary.org` URLs, so devices need internet access to that host.

See all and genre pages load 20 books at a time and retain the selected instance and genre when you return from details. Popular Books starts loading when you enter Discovery, before opening Books. Open rows and grids fetch one page ahead as you scroll; nearby covers are prefetched too. This warms catalog metadata, while availability still comes from live Chaptarr reads. An eBook's availability says nothing about its audiobook. Opening a book checks its Open Library work ID, edition IDs and checksum-validated ISBNs against the selected Chaptarr catalog and live library. One verified match can be used automatically; multiple matches or title/author suggestions require an explicit choice on the same page. A catalog outage preserves the book details and lets you save the requested formats immediately. Search runs Open Library and the library catalog independently, shown beside each other on wide screens and in separate tabs on phones.

Discovery requires discovery permission and, for requesters including kids accounts, a Chaptarr grant. Admins may browse external metadata without an instance. External metadata is shared, but access is checked before and after cache/provider work. Targets and availability belong to the selected instance. Visible targets refresh after request/library events and through Refresh matches; confirmation rechecks the current choices before saving a mapping. If an older Cantinarr server lacks discovery endpoints, the app shows an update notice and keeps library browsing and search.

## Saved requests and automatic recovery

Book requests are saved before delivery, with separate progress for eBook and Audiobook. Approval policy still applies: **Waiting for approval** cannot be bypassed by retrying or confirming a match. Approved requests that encounter a temporary lookup, connection, rate-limit or service failure show **Waiting for catalog** and retry after one minute, doubling up to six hours and honoring a longer upstream `Retry-After`. The schedule survives restarts. After 50 failed delivery attempts, or a configuration/identity failure that needs a person, the request remains saved with **Needs attention**.

The book page shows progress and offers **Try again**, **Cancel request**, and matching choices when needed. Admins can manage the same saved work under **Settings > Pending requests > Saved requests**; delivery waits do not increase the approval badge or send an approval-needed push. A shared book request retains each person's requested formats. Cancelling your subscription preserves other subscribers; an admin can cancel the shared request. Already delivered formats are never undone by cancelling remaining work.

A confirmed match records the selected library identity, then re-reads Chaptarr before adding or monitoring it. Distinct catalog records are never merged by title. Delivery stays pinned to the originally authorized instance, checks the requester's current grant before each attempt, and never falls back to another library. Current files, downloads and monitoring state come from Chaptarr and outrank a saved wait. A failed library read is marked unknown; a completed delivery is not proof that a file still exists.

**Waiting for library** means Chaptarr accepted the author import and owns its retry loop. Cantinarr observes its pending-import API every few minutes and on the managed webhook. An active native import has no Cantinarr time limit and is not repeatedly added. An import that lands resumes the remaining formats; a failed, cancelled or ambiguous import needs attention. Older Chaptarr versions without that API retain their supported add-probe fallback. Requests saved by older Cantinarr versions retain their existing approval/import gates.
