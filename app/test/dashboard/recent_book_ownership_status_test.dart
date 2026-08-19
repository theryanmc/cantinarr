import 'package:cantinarr/features/dashboard/logic/recent_book_ownership_status.dart';
import 'package:cantinarr/features/request/data/book_ownership.dart';
import 'package:cantinarr/core/theme/app_theme.dart';
import 'package:flutter_test/flutter_test.dart';

OwnedTitle _title({
  required FormatOwnership ebook,
  required FormatOwnership audiobook,
  bool statusKnown = true,
}) =>
    OwnedTitle(
      title: 'Ahsoka',
      author: 'E. K. Johnston',
      foreignBookId: 'fb-1',
      ownership: BookOwnership(ebook: ebook, audiobook: audiobook),
      statusKnown: statusKnown,
    );

const _downloaded = FormatOwnership(downloaded: true);
const _downloadedAndMonitored = FormatOwnership(downloaded: true, monitored: true);
const _monitored = FormatOwnership(monitored: true);
const _neither = FormatOwnership();

void main() {
  group('recentBookOwnershipSubtitle', () {
    test('both downloaded joins plain names in eBook-then-Audiobook order',
        () {
      final subtitle = recentBookOwnershipSubtitle(
        const BookOwnership(ebook: _downloaded, audiobook: _downloaded),
      );
      expect(subtitle, 'eBook + Audiobook');
    });

    test('eBook downloaded, audiobook untouched -> plain eBook only', () {
      final subtitle = recentBookOwnershipSubtitle(
        const BookOwnership(ebook: _downloaded, audiobook: _neither),
      );
      expect(subtitle, 'eBook');
    });

    test('audiobook downloaded, eBook untouched -> plain Audiobook only', () {
      final subtitle = recentBookOwnershipSubtitle(
        const BookOwnership(ebook: _neither, audiobook: _downloaded),
      );
      expect(subtitle, 'Audiobook');
    });

    test('eBook downloaded, audiobook monitored -> requested suffix on audiobook',
        () {
      final subtitle = recentBookOwnershipSubtitle(
        const BookOwnership(ebook: _downloaded, audiobook: _monitored),
      );
      expect(subtitle, 'eBook + Audiobook requested');
    });

    test('audiobook downloaded, eBook monitored -> requested suffix on eBook',
        () {
      final subtitle = recentBookOwnershipSubtitle(
        const BookOwnership(ebook: _monitored, audiobook: _downloaded),
      );
      expect(subtitle, 'eBook requested + Audiobook');
    });

    test('both monitored, neither downloaded -> both requested', () {
      final subtitle = recentBookOwnershipSubtitle(
        const BookOwnership(ebook: _monitored, audiobook: _monitored),
      );
      expect(subtitle, 'eBook requested + Audiobook requested');
    });

    test('only eBook monitored -> eBook requested alone', () {
      final subtitle = recentBookOwnershipSubtitle(
        const BookOwnership(ebook: _monitored, audiobook: _neither),
      );
      expect(subtitle, 'eBook requested');
    });

    test('only audiobook monitored -> Audiobook requested alone', () {
      final subtitle = recentBookOwnershipSubtitle(
        const BookOwnership(ebook: _neither, audiobook: _monitored),
      );
      expect(subtitle, 'Audiobook requested');
    });

    test('neither downloaded nor monitored -> null, nothing to say', () {
      final subtitle = recentBookOwnershipSubtitle(
        const BookOwnership(ebook: _neither, audiobook: _neither),
      );
      expect(subtitle, isNull);
    });

    test('a format both downloaded and monitored uses its plain name', () {
      final subtitle = recentBookOwnershipSubtitle(
        const BookOwnership(
          ebook: _downloadedAndMonitored,
          audiobook: _neither,
        ),
      );
      expect(subtitle, 'eBook');
    });
  });

  group('buildRecentBookStatus', () {
    test('returns null for a null argument', () {
      expect(buildRecentBookStatus(null), isNull);
    });

    test('returns null when statusKnown is false, even with both downloaded',
        () {
      final status = buildRecentBookStatus(
        _title(
          ebook: _downloaded,
          audiobook: _downloaded,
          statusKnown: false,
        ),
      );
      expect(status, isNull);
    });

    test('returns null when no format is downloaded or monitored', () {
      final status = buildRecentBookStatus(
        _title(ebook: _neither, audiobook: _neither),
      );
      expect(status, isNull);
    });

    test('both formats downloaded returns Available with the D-04 subtitle',
        () {
      final status = buildRecentBookStatus(
        _title(ebook: _downloaded, audiobook: _downloaded),
      );
      expect(status, isNotNull);
      expect(status!.label, 'Available');
      expect(status.color, AppTheme.available);
      expect(status.subtitle, 'eBook + Audiobook');
    });
  });
}
