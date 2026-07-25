import '../../radarr/data/radarr_models.dart';

/// The dashboard's "Recently Downloaded" movies, newest file first.
///
/// Recency means when the FILE landed, not when the movie record was created.
/// Radarr's movie-level `added` is set when the movie is added to the library
/// (a request, an import list, a manual add) and is never touched on import or
/// upgrade, so ordering by it buries a title that was requested months ago and
/// only downloaded today underneath the back catalogue — on exactly the day the
/// user wants to see it.
///
/// The fallback to `added` is deliberate: the movie list endpoint sometimes
/// omits movieFile even when hasFile is true, and keying off the file alone
/// would drop those movies out of the row entirely.
List<RadarrMovie> recentlyDownloadedMovies(
  List<RadarrMovie> movies, {
  int limit = 10,
}) {
  DateTime landedAt(RadarrMovie m) =>
      m.movieFile?.dateAdded ?? m.added ?? DateTime(0);

  final downloaded = movies.where((m) => m.hasFile).toList()
    ..sort((a, b) => landedAt(b).compareTo(landedAt(a)));
  return downloaded.take(limit).toList();
}
