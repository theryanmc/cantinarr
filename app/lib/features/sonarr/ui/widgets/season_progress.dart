import 'package:flutter/material.dart';
import '../../../../core/theme/app_theme.dart';
import '../../data/sonarr_models.dart';

/// (text, colour) pair for a season card's progress label.
typedef SeasonProgress = ({String text, Color color});

/// Sonarr's own progress label for one season — or, from the series-level
/// statistics, for the whole series: "11 / 11", or "0 + 13 / 13" when episodes
/// are on the way.
///
/// The parts are Sonarr's, verbatim (`SeriesIndexProgressBar`): episode files
/// on disk, plus the queued episodes that have no file yet, over `episodeCount`
/// — the episodes that have aired and are monitored, plus anything already on
/// disk. The "+ N" is what keeps that fraction honest while the denominator
/// cannot grow yet: two unaired episodes already grabbed read "11 + 2 / 11",
/// and a season downloaded but parked in front of the import step reads
/// "0 + 13 / 13" rather than a bare "0 / 13".
///
/// Colour mirrors Sonarr's `getProgressBarKind`: anything in flight wins, then
/// a full count is green for an ended series and the info tone while it is
/// still running, then a short count is red when monitored and amber when not.
SeasonProgress seasonProgressLabel(
  SonarrStatistics? stats, {
  required bool monitored,
  required bool ended,
  List<SonarrQueueItem> queue = const [],
}) {
  final files = stats?.episodeFileCount ?? 0;
  final counted = stats?.episodeCount ?? 0;
  // Sonarr lists a season pack once per episode and an episode can be
  // re-grabbed, so count episodes rather than rows — and drop the ones that
  // already have a file, since an upgrade brings no new episode.
  final incoming = <int>{
    for (final item in queue)
      if (!item.episodeHasFile) item.episodeId ?? -item.id,
  }.length;

  final text =
      incoming > 0 ? '$files + $incoming / $counted' : '$files / $counted';
  // Nothing counted yet (an unmonitored season, or stats Sonarr never sent):
  // say so quietly instead of colouring an empty fraction complete.
  if (counted == 0) return (text: text, color: AppTheme.textSecondary);

  return (
    text: text,
    color: incoming > 0
        ? AppTheme.downloading
        : files >= counted
            ? (ended ? AppTheme.available : AppTheme.downloading)
            : monitored
                ? AppTheme.error
                : AppTheme.requested,
  );
}
