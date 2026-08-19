import 'package:flutter/material.dart';

import '../../../core/theme/app_theme.dart';
import '../../request/data/book_ownership.dart';

/// The Recently Added row's per-card pill and subtitle.
///
/// A null [RecentBookStatus] means ownership could not be determined for a
/// card — render no pill at all (BOOK-01/D-02). It never means "available":
/// substituting a fallback label here would silently reintroduce the exact
/// hardcoded-"Available" bug BOOK-01 exists to fix, just narrowed to the
/// unmatched case.
class RecentBookStatus {
  final String label;
  final Color color;
  final String subtitle;

  const RecentBookStatus({
    required this.label,
    required this.color,
    required this.subtitle,
  });
}

/// A pill label + colour pair, without the always-present [subtitle] field
/// [RecentBookStatus] requires — the three verdicts below are constants, and
/// each one's subtitle is only known per-title, so the constants carry just
/// label+colour and a subtitle is attached when a [RecentBookStatus] is
/// built from one.
class _Verdict {
  final String label;
  final Color color;
  const _Verdict(this.label, this.color);
}

// Same three requester-vocabulary labels and the same two theme tokens as
// `search_library_status.dart`'s `_available`/`_partial`/`_requested` — no
// new theme token is introduced by this phase. `_partial` and `_requested`
// are added in Task 2 alongside the branches that use them; declaring them
// here unused would fail `flutter analyze`.
const _available = _Verdict('Available', AppTheme.available);

/// One format's D-04 subtitle part. Downloaded outranks monitored: a format
/// that is both is on disk already, and there is nothing outstanding left to
/// announce about it.
String? _formatPart(String label, FormatOwnership format) {
  if (format.downloaded) return label;
  if (format.monitored) return '$label requested';
  return null;
}

/// Builds the D-04 two-part subtitle from a title's ownership: eBook then
/// Audiobook, always in that order regardless of which format arrived first,
/// joined by `' + '`.
///
/// A format's part is omitted when it is neither downloaded nor monitored
/// (nothing to say about it), plain (`'eBook'`/`'Audiobook'`) when
/// downloaded, or suffixed with `' requested'` when monitored but not
/// downloaded. Examples: both downloaded -> `'eBook + Audiobook'`; eBook
/// downloaded, audiobook untouched -> `'eBook'`; eBook downloaded, audiobook
/// monitored -> `'eBook + Audiobook requested'`. Returns null when neither
/// format has anything to say.
///
/// This deliberately differs from `dashboard_books_tab.dart`'s `_ownershipChip`,
/// which joins with `' · '` and suffixes each downloaded format with the word
/// "available" — that wording is not copied here.
String? recentBookOwnershipSubtitle(BookOwnership ownership) {
  final parts = [
    _formatPart('eBook', ownership.ebook),
    _formatPart('Audiobook', ownership.audiobook),
  ].whereType<String>().toList();
  if (parts.isEmpty) return null;
  return parts.join(' + ');
}

/// The three-state BOOK-01 verdict for one Recently Added card, or null when
/// ownership could not be determined (D-02) — see the class doc on
/// [RecentBookStatus] for why null must never become a fallback "Available".
RecentBookStatus? buildRecentBookStatus(OwnedTitle? owned) {
  if (owned == null) return null;
  // An older Chaptarr server could not resolve format truth for this title;
  // D-02 says an unknown state renders no pill rather than a guessed one.
  if (!owned.statusKnown) return null;

  final e = owned.ownership.ebook;
  final a = owned.ownership.audiobook;
  if (!e.owned && !a.owned) {
    // The digest says the user has nothing for this title, which contradicts
    // the import event that put this record on the row in the first place —
    // the digest is stale or unreadable, so D-02 applies.
    return null;
  }

  // Non-null by construction: at least one format is owned once the guard
  // above has passed.
  final subtitle = recentBookOwnershipSubtitle(owned.ownership)!;

  if (e.downloaded && a.downloaded) {
    return RecentBookStatus(
      label: _available.label,
      color: _available.color,
      subtitle: subtitle,
    );
  }

  // The remainder of BOOK-01's three-state rule (Partially Available and
  // Requested) is completed in Task 2. Returning null here is D-02-safe: an
  // incomplete verdict shows no pill, never a wrong one.
  return null;
}
