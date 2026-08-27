import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../core/layout/adaptive.dart';
import '../../../core/providers/instance_provider.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/cached_image.dart';
import '../../chaptarr/data/chaptarr_image.dart';
import '../../chaptarr/data/chaptarr_models.dart';
import '../../dashboard/data/book_library_service.dart';
import '../../dashboard/logic/book_ownership_matcher.dart';
import '../../request/data/book_ownership.dart';
import '../../shell/logic/shell_book_search_provider.dart';

/// Book-search results overlay for the shell toolbar, rendered on
/// `/dashboard/books` in the same [Positioned.fill] slot [SearchResultsView]
/// occupies for every other module. Ports `_BookResultTile` / `_OwnershipChip`
/// / `_ResolvedBookResult` out of `dashboard_books_tab.dart` verbatim; the
/// ordering/ambiguity rules come from [resolveBookSearchIdentity], reused
/// unchanged.
class BookSearchResultsView extends ConsumerWidget {
  final List<ChaptarrBook> results;
  final String query;
  final bool isLoading;

  /// True once a lookup has completed successfully for [query] — the signal
  /// that distinguishes "no books found" from "hasn't searched yet".
  final bool searched;
  final BookSearchError? error;
  final VoidCallback? onResultTap;

  const BookSearchResultsView({
    super.key,
    required this.results,
    required this.query,
    required this.isLoading,
    required this.searched,
    required this.error,
    this.onResultTap,
  });

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    if (error != null) {
      // Copied character-for-character out of dashboard_books_tab.dart —
      // user-facing contract text (FAIL-01/02/03). Reword nothing,
      // interpolate nothing (threat T-03-04).
      final message = switch (error!) {
        BookSearchError.noInstance => 'No Chaptarr instance is available.',
        BookSearchError.forbidden =>
          'You do not have access to search this book library.',
        BookSearchError.requestFailed =>
          'Books could not be searched. Check the connection and try again.',
      };
      return Center(
        child: Padding(
          padding: const EdgeInsets.all(24),
          child: Text(
            message,
            textAlign: TextAlign.center,
            style: const TextStyle(color: AppTheme.error),
          ),
        ),
      );
    }

    if (isLoading) {
      return const Center(
        child: CircularProgressIndicator(color: AppTheme.accent),
      );
    }

    // What the user already owns, used to mark results, and to surface
    // owned/monitored books the metadata search missed.
    final digest =
        ref.watch(ownedBooksProvider).valueOrNull ?? const <OwnedTitle>[];
    final identity = resolveBookSearchIdentity(
      query: query,
      lookupResults: results,
      digest: digest,
    );
    // Concrete library records not already represented by a safe one-to-one
    // lookup mapping. Ambiguous candidates are shown separately here so the
    // requester can choose a real record rather than targeting a fuzzy guess.
    final injected = identity.libraryRows;
    // Mark each lookup result with its ownership and float owned titles to
    // the top, preserving Chaptarr's relevance order within each bucket
    // (don't collapse versions — the user wants to see ones they don't own).
    // Only owned results carry a cover: the owned record's cached
    // /MediaCover, which loads with the API key. Lookup (/MediaCoverProxy)
    // covers are broken server-side in this fork, so not-yet-owned rows
    // stay iconic.
    final owned = <_ResolvedBookResult>[];
    final rest = <_ResolvedBookResult>[];
    for (var lookupIndex = 0; lookupIndex < results.length; lookupIndex++) {
      final book = results[lookupIndex];
      final match = identity.matches[book];
      final identityAmbiguous = identity.contested.containsKey(book);
      final cover =
          (match != null && match.cover.isNotEmpty) ? match.cover : null;
      final libraryId = match?.foreignBookId.trim() ?? '';
      final lookupId = book.foreignBookId?.trim() ?? '';
      ((match?.ownership.anyOwned ?? false) ? owned : rest).add(
        _ResolvedBookResult(
          book: book,
          ownership: match?.ownership,
          identityAmbiguous: identityAmbiguous,
          sourceIdentity: 'lookup:$lookupIndex',
          cover: cover,
          canonicalForeignId: libraryId.isNotEmpty ? libraryId : lookupId,
        ),
      );
    }
    final ordered = <_ResolvedBookResult>[
      for (var libraryIndex = 0; libraryIndex < injected.length; libraryIndex++)
        _ResolvedBookResult(
          book: _ownedTitleAsBook(injected[libraryIndex]),
          ownership: injected[libraryIndex].ownership,
          identityAmbiguous: false,
          sourceIdentity: 'library:$libraryIndex',
          cover: injected[libraryIndex].cover.isNotEmpty
              ? injected[libraryIndex].cover
              : null,
          canonicalForeignId: injected[libraryIndex].foreignBookId,
        ),
      ...owned,
      ...rest,
    ];

    if (ordered.isEmpty) {
      // A search that ran and matched nothing says so; a search that hasn't
      // run yet (empty query, still in the idle state some caller passed
      // through) renders nothing here — the overlay isn't shown for an idle
      // query in the first place, but stay defensive about the two states.
      if (!searched) return const SizedBox.shrink();
      return const Center(
        child: Padding(
          padding: EdgeInsets.all(32),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              Icon(Icons.menu_book, size: 48, color: AppTheme.textSecondary),
              SizedBox(height: 12),
              Text(
                'No books found. Try a different search.',
                textAlign: TextAlign.center,
                style: TextStyle(color: AppTheme.textSecondary),
              ),
            ],
          ),
        ),
      );
    }

    final instanceId = ref.watch(instanceProvider).activeChaptarrInstance?.id;
    // Full-width scroll surface; the result column is capped and centered so
    // rows stay readable on desktop widths.
    return LayoutBuilder(builder: (context, constraints) {
      final hPad = AppBreakpoints.centeredContentPadding(
        constraints.maxWidth,
        minPadding: 0,
      );
      return ListView.separated(
        padding: EdgeInsets.fromLTRB(hPad, 8, hPad, 8),
        itemCount: ordered.length,
        separatorBuilder: (_, __) =>
            const Divider(height: 1, color: AppTheme.border),
        itemBuilder: (_, i) => _BookResultTile(
          book: ordered[i].book,
          canonicalForeignId: ordered[i].canonicalForeignId,
          ownership: ordered[i].ownership,
          identityAmbiguous: ordered[i].identityAmbiguous,
          sourceIdentity: ordered[i].sourceIdentity,
          cover: instanceId == null
              ? null
              : chaptarrImageSource(ref, ordered[i].cover, instanceId),
          instanceId: instanceId,
          searchedTerm: query,
          onTap: onResultTap,
        ),
      );
    });
  }
}

class _ResolvedBookResult {
  final ChaptarrBook book;
  final BookOwnership? ownership;
  final bool identityAmbiguous;
  final String sourceIdentity;
  final String? cover;
  final String canonicalForeignId;

  const _ResolvedBookResult({
    required this.book,
    required this.ownership,
    required this.identityAmbiguous,
    required this.sourceIdentity,
    required this.cover,
    required this.canonicalForeignId,
  });
}

class _BookResultTile extends StatelessWidget {
  final ChaptarrBook book;
  final String canonicalForeignId;
  final BookOwnership? ownership;
  final bool identityAmbiguous;
  final String sourceIdentity;
  final ChaptarrImageSource? cover;
  final String? instanceId;

  /// The term these results belong to. It travels to the detail page so a
  /// request can hand the server the search that already found this record.
  final String searchedTerm;

  /// Called right before navigating away, mirroring [SearchResultsView]'s
  /// `_SearchResultTile` — the shell dismisses the keyboard on tap.
  final VoidCallback? onTap;

  const _BookResultTile({
    required this.book,
    required this.canonicalForeignId,
    this.ownership,
    this.identityAmbiguous = false,
    required this.sourceIdentity,
    this.cover,
    required this.instanceId,
    this.searchedTerm = '',
    this.onTap,
  });

  @override
  Widget build(BuildContext context) {
    final year = book.releaseDate?.year;
    final subtitle = <String>[
      if (book.author?.authorName.isNotEmpty ?? false) book.author!.authorName,
      if (year != null) '$year',
    ].join(' · ');
    // Lookup metadata can use a provider-specific foreign id that differs
    // from the actual library record. Status, navigation, and mutation all
    // stay on the matched canonical library id while [book] preserves lookup
    // metadata.
    final fid = canonicalForeignId.trim();
    final lookupId = book.foreignBookId?.trim() ?? '';
    final o = ownership;
    final chip = _ownershipChip(o);
    // Ambiguity is about which library record this row is, not about
    // whether the requester may read it: the row still addresses a real
    // metadata record, and closing the tap left a just-requested book with
    // no way to be opened at all. The row states which record to act on
    // instead; the library rows it points at are listed above it.
    final canOpen = fid.isNotEmpty;
    final identityGuidance = identityAmbiguous
        ? 'May be the same as a book listed above'
        : fid.isEmpty
            ? 'Ask an admin to check this book’s library record'
            : null;
    final resultKey = ValueKey('book-result:$lookupId:$fid:$sourceIdentity');
    // The shell overlay wraps this view in an opaque ColoredBox (see
    // AppShell's Positioned.fill slot), which sits between a bare ListTile
    // and its ink-splash Material ancestor and makes the splash invisible.
    // dashboard_books_tab.dart never had this problem — its ListTile lived
    // directly under the Scaffold's own Material with no opaque widget in
    // between. Give the tile its own transparent Material so ink splashes
    // paint correctly in the overlay context without changing the row's
    // appearance.
    return Material(
      type: MaterialType.transparency,
      child: ListTile(
        key: resultKey,
        contentPadding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
        leading: ClipRRect(
          borderRadius: BorderRadius.circular(4),
          child: CachedImage(
            url: cover?.url,
            headers: cover?.headers,
            width: 44,
            height: 66,
            icon: Icons.menu_book,
          ),
        ),
        title: Text(
          book.title,
          maxLines: 2,
          overflow: TextOverflow.ellipsis,
          style: const TextStyle(
              color: AppTheme.textPrimary, fontWeight: FontWeight.w600),
        ),
        subtitle: (subtitle.isEmpty && chip == null && identityGuidance == null)
            ? null
            : Padding(
                padding: const EdgeInsets.only(top: 3),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    if (subtitle.isNotEmpty)
                      Text(subtitle,
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                          style:
                              const TextStyle(color: AppTheme.textSecondary)),
                    if (identityGuidance != null) ...[
                      if (subtitle.isNotEmpty) const SizedBox(height: 4),
                      Text(
                        identityGuidance,
                        style: const TextStyle(
                          color: AppTheme.requested,
                          fontSize: 12,
                          fontWeight: FontWeight.w600,
                        ),
                      ),
                    ],
                    if (chip != null) ...[
                      if (subtitle.isNotEmpty || identityGuidance != null)
                        const SizedBox(height: 4),
                      chip,
                    ],
                  ],
                ),
              ),
        // Requests belong on the detail page. The search row has one clear
        // action: open that book, where the requester can review metadata and
        // formats.
        trailing: canOpen
            ? const Icon(Icons.chevron_right, color: AppTheme.textSecondary)
            : null,
        onTap: canOpen
            ? () {
                onTap?.call();
                context.push(
                  '/detail/book/${Uri.encodeComponent(fid)}'
                  '?title=${Uri.encodeQueryComponent(book.title)}'
                  // The term that surfaced this row travels with it:
                  // requesting the book makes the server find this exact
                  // record again, and this is the search already known to
                  // return it.
                  '${searchedTerm.isEmpty ? '' : '&q=${Uri.encodeQueryComponent(searchedTerm)}'}'
                  '${instanceId == null ? '' : '&instance_id=${Uri.encodeQueryComponent(instanceId!)}'}',
                  extra: book,
                );
              }
            : null,
      ),
    );
  }
}

Widget? _ownershipChip(BookOwnership? o) {
  if (o == null || !o.anyOwned) return null;
  final states = <String>[
    if (o.ebook.downloaded)
      'eBook available'
    else if (o.ebook.monitored)
      'eBook requested',
    if (o.audiobook.downloaded)
      'Audiobook available'
    else if (o.audiobook.monitored)
      'Audiobook requested',
  ];
  // The grouped chip describes every represented format. A downloaded eBook
  // must not make the whole group look available while its audiobook is
  // still only monitored.
  final available = (!o.ebook.owned || o.ebook.downloaded) &&
      (!o.audiobook.owned || o.audiobook.downloaded);
  return _OwnershipChip(
    label: states.join(' · '),
    color: available ? AppTheme.available : AppTheme.requested,
  );
}

/// A synthetic result for an owned library title the metadata search didn't
/// return. It carries the owned record's foreignBookId, so a partly-owned
/// title (e.g. ebook present, audiobook missing) still gets a "Request more"
/// button to complete the missing format.
ChaptarrBook _ownedTitleAsBook(OwnedTitle t) => ChaptarrBook(
      id: 0,
      title: t.title,
      foreignBookId: t.foreignBookId.isNotEmpty ? t.foreignBookId : null,
      author: ChaptarrAuthorContext(id: 0, authorName: t.author),
      releaseDate: t.year > 0 ? DateTime(t.year) : null,
    );

/// A small colored pill marking that a search result is already in the
/// library.
class _OwnershipChip extends StatelessWidget {
  final String label;
  final Color color;

  const _OwnershipChip({required this.label, required this.color});

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.15),
        borderRadius: BorderRadius.circular(4),
      ),
      child: Text(label,
          style: TextStyle(
              color: color, fontSize: 10.5, fontWeight: FontWeight.w600)),
    );
  }
}
