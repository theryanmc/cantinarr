import 'package:cantinarr/core/theme/app_theme.dart';
import 'package:cantinarr/features/sonarr/data/sonarr_models.dart';
import 'package:cantinarr/features/sonarr/ui/widgets/season_progress.dart';
import 'package:flutter_test/flutter_test.dart';

/// The arr modules speak Sonarr: the season label is Sonarr's own
/// "{files} [+ {queued without a file}] / {episodeCount}".
SonarrStatistics _stats({int files = 0, int counted = 0, int total = 0}) =>
    SonarrStatistics(
      episodeFileCount: files,
      episodeCount: counted,
      totalEpisodeCount: total,
    );

/// One queue row per episode, the way Sonarr expands a grab.
List<SonarrQueueItem> _queue(int count, {bool hasFile = false}) => [
      for (var i = 0; i < count; i++)
        SonarrQueueItem(
          id: 900 + i,
          episodeId: 100 + i,
          title: 'Release $i',
          episodeHasFile: hasFile,
        ),
    ];

void main() {
  group('seasonProgressLabel', () {
    test('a season stuck in front of the import step shows what is coming', () {
      // Tremors: 13 episodes downloaded, none imported yet. Sonarr's label.
      final label = seasonProgressLabel(
        _stats(files: 0, counted: 13, total: 13),
        monitored: true,
        ended: true,
        queue: _queue(13),
      );

      expect(label.text, '0 + 13 / 13');
      // In flight wins over the red a bare 0 / 13 would earn.
      expect(label.color, AppTheme.downloading);
    });

    test('episodes on the way sit outside a denominator that cannot grow yet',
        () {
      // American Dad! S22: 11 aired and on disk, 2 unaired already grabbed.
      final label = seasonProgressLabel(
        _stats(files: 11, counted: 11, total: 13),
        monitored: true,
        ended: false,
        queue: _queue(2),
      );

      expect(label.text, '11 + 2 / 11');
      expect(label.color, AppTheme.downloading);
    });

    test('nothing in the queue leaves Sonarr\'s plain fraction', () {
      final label = seasonProgressLabel(
        _stats(files: 11, counted: 11, total: 13),
        monitored: true,
        ended: false,
      );

      expect(label.text, '11 / 11');
    });

    test('a complete season is green once the series has ended', () {
      final label = seasonProgressLabel(
        _stats(files: 13, counted: 13, total: 13),
        monitored: true,
        ended: true,
      );

      expect(label.text, '13 / 13');
      expect(label.color, AppTheme.available);
    });

    test('a caught-up running series is the info tone, not green', () {
      final label = seasonProgressLabel(
        _stats(files: 8, counted: 8, total: 10),
        monitored: true,
        ended: false,
      );

      expect(label.text, '8 / 8');
      expect(label.color, AppTheme.downloading);
    });

    test('a monitored gap is red and an unmonitored one is amber', () {
      final monitored = seasonProgressLabel(
        _stats(files: 9, counted: 13, total: 13),
        monitored: true,
        ended: true,
      );
      final unmonitored = seasonProgressLabel(
        _stats(files: 9, counted: 13, total: 13),
        monitored: false,
        ended: true,
      );

      expect(monitored.text, '9 / 13');
      expect(monitored.color, AppTheme.error);
      expect(unmonitored.color, AppTheme.requested);
    });

    test('a queued upgrade adds no episode', () {
      // The episode is already on disk, so Sonarr leaves it out of the "+ N".
      final label = seasonProgressLabel(
        _stats(files: 13, counted: 13, total: 13),
        monitored: true,
        ended: true,
        queue: _queue(1, hasFile: true),
      );

      expect(label.text, '13 / 13');
      expect(label.color, AppTheme.available);
    });

    test('a season pack listed once per episode counts each episode once', () {
      final label = seasonProgressLabel(
        _stats(files: 0, counted: 13, total: 13),
        monitored: true,
        ended: true,
        queue: [..._queue(2), ..._queue(2)],
      );

      expect(label.text, '0 + 2 / 13');
    });

    test('a season Sonarr counts nothing for stays quiet', () {
      // An unmonitored season with no files: Sonarr's own label is 0 / 0.
      final unmonitored = seasonProgressLabel(
        _stats(files: 0, counted: 0, total: 7),
        monitored: false,
        ended: true,
      );
      final missing =
          seasonProgressLabel(null, monitored: true, ended: false);

      expect(unmonitored.text, '0 / 0');
      expect(unmonitored.color, AppTheme.textSecondary);
      expect(missing.text, '0 / 0');
    });
  });

  group('SonarrQueueItem.episodeHasFile', () {
    test('comes off the embedded episode, defaulting to false', () {
      final upgrade = SonarrQueueItem.fromJson({
        'id': 5,
        'title': 'Release',
        'episode': {'id': 42, 'seasonNumber': 1, 'hasFile': true},
      });
      final fresh = SonarrQueueItem.fromJson({
        'id': 6,
        'title': 'Release',
        'episode': {'id': 43, 'seasonNumber': 1},
      });

      expect(upgrade.episodeHasFile, isTrue);
      expect(fresh.episodeHasFile, isFalse);
    });
  });
}
