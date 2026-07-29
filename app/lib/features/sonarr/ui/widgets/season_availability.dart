import 'dart:math' as math;

import 'package:flutter/material.dart';
import '../../../../core/theme/app_theme.dart';
import '../../data/sonarr_models.dart';

/// (text, colour) pair for the availability line on a season card.
typedef SeasonAvailability = ({String text, Color color});

/// Builds "11/13 Episodes Available • 2 unaired" for one season — or, from the
/// series-level statistics, for the whole series.
///
/// The denominator is every episode Sonarr knows about, not its `episodeCount`:
/// that one drops unaired and unmonitored episodes, so a 13-episode season with
/// two still to come reads "100% • 11/11 Episodes Available" and looks finished
/// next to an episode list that has 13 rows. Sonarr can get away with the same
/// denominator because it renders the queue in the label too ("11 + 2 / 11");
/// this card has no queue, so it spends the denominator on the whole season and
/// names what is left over instead.
///
/// The suffix splits that remainder using the episode list's own vocabulary:
/// episodes that aired and are wanted but absent are "missing", and the rest are
/// "unaired" while the season still has an air date pending ([moreToCome]) or
/// "unmonitored" when it does not.
///
/// Colour follows the library tile's grammar: green only once every episode is
/// on disk, red for a gap Sonarr is hunting, amber for a gap nobody monitors,
/// and the info tone for a season that is caught up with more still to air.
SeasonAvailability seasonAvailabilityLine(
  SonarrStatistics? stats, {
  required bool moreToCome,
}) {
  final files = stats?.episodeFileCount ?? 0;
  final obtainable = stats?.episodeCount ?? 0;
  // Older payloads (and Sonarr's series-level stats before v3) can omit
  // totalEpisodeCount; fall back to the obtainable count rather than dividing
  // by a zero the season plainly is not.
  final total = math.max(stats?.totalEpisodeCount ?? 0, obtainable);
  if (total == 0) {
    return (
      text: '0/0 Episodes Available',
      color: AppTheme.textSecondary,
    );
  }

  final missing = math.max(0, obtainable - files);
  final held = total - obtainable;
  final parts = [
    if (missing > 0) '$missing missing',
    if (held > 0) moreToCome ? '$held unaired' : '$held unmonitored',
  ];

  return (
    text: '$files/$total Episodes Available'
        '${parts.isEmpty ? '' : ' • ${parts.join(', ')}'}',
    color: files >= total
        ? AppTheme.available
        : missing > 0
            ? AppTheme.error
            : moreToCome
                ? AppTheme.downloading
                : AppTheme.requested,
  );
}
