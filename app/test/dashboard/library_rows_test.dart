import 'package:cantinarr/features/dashboard/logic/library_rows.dart';
import 'package:cantinarr/features/radarr/data/radarr_models.dart';
import 'package:flutter_test/flutter_test.dart';

RadarrMovie _movie({
  required int id,
  required bool hasFile,
  DateTime? added,
  DateTime? fileDateAdded,
  bool withFile = true,
}) =>
    RadarrMovie(
      id: id,
      title: 'Movie $id',
      year: 2020,
      tmdbId: id,
      hasFile: hasFile,
      monitored: true,
      added: added,
      movieFile: hasFile && withFile
          ? RadarrMovieFile(id: id, dateAdded: fileDateAdded)
          : null,
    );

void main() {
  final now = DateTime(2026, 7, 25);

  test('orders by when the file landed, not when the movie was added', () {
    // The classic case: requested long ago, downloaded today. Ordering by the
    // movie record's `added` buries it under the back catalogue on exactly the
    // day the user wants to see it.
    final requestedLongAgo = _movie(
      id: 1,
      hasFile: true,
      added: DateTime(2020, 1, 1),
      fileDateAdded: now,
    );
    final addedRecently = _movie(
      id: 2,
      hasFile: true,
      added: now.subtract(const Duration(days: 7)),
      fileDateAdded: now.subtract(const Duration(days: 7)),
    );

    final row = recentlyDownloadedMovies([addedRecently, requestedLongAgo]);

    expect(row.map((m) => m.id), [1, 2]);
  });

  test('excludes movies with no file', () {
    final row = recentlyDownloadedMovies([
      _movie(id: 1, hasFile: false, added: now),
      _movie(id: 2, hasFile: true, fileDateAdded: now),
    ]);

    expect(row.map((m) => m.id), [2]);
  });

  test('keeps a downloaded movie whose file record was omitted', () {
    // The movie list endpoint sometimes omits movieFile even when hasFile is
    // true; keying off the file alone would delete these from the row.
    final noFileRecord = _movie(
      id: 1,
      hasFile: true,
      added: now,
      withFile: false,
    );
    final older = _movie(
      id: 2,
      hasFile: true,
      fileDateAdded: now.subtract(const Duration(days: 30)),
    );

    final row = recentlyDownloadedMovies([older, noFileRecord]);

    expect(row.map((m) => m.id), [1, 2]);
  });

  test('caps the row length', () {
    final movies = List.generate(
      25,
      (i) => _movie(
        id: i,
        hasFile: true,
        fileDateAdded: now.subtract(Duration(days: i)),
      ),
    );

    expect(recentlyDownloadedMovies(movies).length, 10);
    expect(recentlyDownloadedMovies(movies, limit: 3).map((m) => m.id),
        [0, 1, 2]);
  });

  test('decodes the file import timestamp from Radarr', () {
    final file = RadarrMovieFile.fromJson({
      'id': 5,
      'dateAdded': '2026-07-20T10:30:00Z',
    });

    expect(file.dateAdded, DateTime.parse('2026-07-20T10:30:00Z'));
  });
}
