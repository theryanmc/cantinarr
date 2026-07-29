import 'package:cantinarr/core/theme/app_theme.dart';
import 'package:cantinarr/features/sonarr/data/sonarr_models.dart';
import 'package:cantinarr/features/sonarr/ui/widgets/season_availability.dart';
import 'package:flutter_test/flutter_test.dart';

/// The season card counts the whole season, not Sonarr's episodeCount: that one
/// hides unaired and unmonitored episodes, so a season that is merely caught up
/// used to claim "100% • 11/11 Episodes Available" one tap away from a list of
/// 13 episodes.
SonarrStatistics _stats({
  int files = 0,
  int obtainable = 0,
  int total = 0,
  DateTime? nextAiring,
}) =>
    SonarrStatistics(
      episodeFileCount: files,
      episodeCount: obtainable,
      totalEpisodeCount: total,
      nextAiring: nextAiring,
    );

void main() {
  group('seasonAvailabilityLine', () {
    test('a caught-up airing season counts the episodes still to come', () {
      final line = seasonAvailabilityLine(
        _stats(files: 11, obtainable: 11, total: 13),
        moreToCome: true,
      );

      expect(line.text, '11/13 Episodes Available • 2 unaired');
      expect(line.text, isNot(contains('100%')));
      // Caught up, but not complete: green is reserved for a full season.
      expect(line.color, AppTheme.downloading);
    });

    test('a full season reads complete and green, with no suffix', () {
      final line = seasonAvailabilityLine(
        _stats(files: 13, obtainable: 13, total: 13),
        moreToCome: false,
      );

      expect(line.text, '13/13 Episodes Available');
      expect(line.color, AppTheme.available);
    });

    test('an aired gap is named separately from the unaired remainder', () {
      final line = seasonAvailabilityLine(
        _stats(files: 9, obtainable: 11, total: 13),
        moreToCome: true,
      );

      expect(line.text, '9/13 Episodes Available • 2 missing, 2 unaired');
      expect(line.color, AppTheme.error);
    });

    test('with nothing left to air the remainder is unmonitored, not unaired',
        () {
      // An unmonitored season: Sonarr counts only the episodes on disk, so the
      // rest are a choice the admin made rather than a gap to hunt.
      final line = seasonAvailabilityLine(
        _stats(files: 0, obtainable: 0, total: 13),
        moreToCome: false,
      );

      expect(line.text, '0/13 Episodes Available • 13 unmonitored');
      expect(line.color, AppTheme.requested);
    });

    test('a series whose unmonitored seasons dwarf the downloaded one', () {
      // Series-level statistics for the episode_totals_test scenario: one
      // downloaded season, three unmonitored ones. percentOfEpisodes says 100%.
      final line = seasonAvailabilityLine(
        _stats(files: 9, obtainable: 9, total: 34),
        moreToCome: false,
      );

      expect(line.text, '9/34 Episodes Available • 25 unmonitored');
      expect(line.color, AppTheme.requested);
    });

    test('falls back to the obtainable count when totalEpisodeCount is absent',
        () {
      final line = seasonAvailabilityLine(
        _stats(files: 8, obtainable: 8),
        moreToCome: false,
      );

      expect(line.text, '8/8 Episodes Available');
      expect(line.color, AppTheme.available);
    });

    test('an empty or statistics-less season stays neutral', () {
      final line = seasonAvailabilityLine(null, moreToCome: false);

      expect(line.text, '0/0 Episodes Available');
      expect(line.color, AppTheme.textSecondary);
    });
  });

  group('SonarrSeries.hasUpcomingEpisodes', () {
    SonarrSeason season(int number, {DateTime? nextAiring}) => SonarrSeason(
          seasonNumber: number,
          statistics: _stats(files: 11, obtainable: 11, total: 13,
              nextAiring: nextAiring),
        );

    test('true when any season is still waiting on an episode', () {
      final series = SonarrSeries(
        id: 1,
        title: 'American Dad!',
        seasons: [season(21), season(22, nextAiring: DateTime.utc(2026, 9, 13))],
      );

      expect(series.hasUpcomingEpisodes, isTrue);
    });

    test('false once nothing is left to air', () {
      final series = SonarrSeries(
        id: 2,
        title: 'Ended',
        seasons: [season(1), season(2)],
      );

      expect(series.hasUpcomingEpisodes, isFalse);
    });
  });

  group('SonarrStatistics.fromJson', () {
    test('reads nextAiring, and leaves it null when Sonarr omits it', () {
      final airing = SonarrStatistics.fromJson({
        'episodeFileCount': 11,
        'episodeCount': 11,
        'totalEpisodeCount': 13,
        'nextAiring': '2026-09-13T04:00:00Z',
      });
      final done = SonarrStatistics.fromJson({'episodeFileCount': 11});

      expect(airing.nextAiring, DateTime.utc(2026, 9, 13, 4));
      expect(airing.totalEpisodeCount, 13);
      expect(done.nextAiring, isNull);
    });
  });
}
