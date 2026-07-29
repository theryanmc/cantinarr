import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/features/sonarr/data/sonarr_models.dart';
import 'package:cantinarr/features/sonarr/ui/sonarr_series_detail_screen.dart';
import 'package:cantinarr/core/theme/app_theme.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

/// American Dad!-shaped payload: season 22 has 13 episodes, 11 of them aired
/// and on disk, and Sonarr is still waiting on the last two. Sonarr's own
/// episodeCount stops at 11, which is how the card came to claim "100% •
/// 11/11 Episodes Available" for a season the episode list shows 13 rows for.
Map<String, dynamic> _seriesJson() => {
      'id': 7,
      'title': 'American Dad!',
      'monitored': true,
      'status': 'continuing',
      'statistics': {
        'seasonCount': 2,
        'episodeFileCount': 19,
        'episodeCount': 19,
        'totalEpisodeCount': 21,
        'sizeOnDisk': 12000000000,
        'percentOfEpisodes': 100.0,
      },
      'seasons': [
        {
          'seasonNumber': 21,
          'monitored': true,
          'statistics': {
            'episodeFileCount': 8,
            'episodeCount': 8,
            'totalEpisodeCount': 8,
            'sizeOnDisk': 5000000000,
            'percentOfEpisodes': 100.0,
          },
        },
        {
          'seasonNumber': 22,
          'monitored': true,
          'statistics': {
            'episodeFileCount': 11,
            'episodeCount': 11,
            'totalEpisodeCount': 13,
            'sizeOnDisk': 6657000000,
            'percentOfEpisodes': 100.0,
            'nextAiring': '2026-09-13T04:00:00Z',
          },
        },
      ],
    };

class _SeriesAdapter implements HttpClientAdapter {
  @override
  Future<ResponseBody> fetch(RequestOptions options, Stream<Uint8List>? _,
          Future<void>? __) async =>
      ResponseBody.fromString(
        jsonEncode(_seriesJson()),
        200,
        headers: {
          Headers.contentTypeHeader: [Headers.jsonContentType]
        },
      );

  @override
  void close({bool force = false}) {}
}

Color _colorOf(WidgetTester tester, String text) =>
    tester.widget<Text>(find.text(text)).style!.color!;

void main() {
  testWidgets('an airing season counts the episodes it is still waiting on',
      (tester) async {
    // Phone-sized, like the screen this shipped wrong on.
    await tester.binding.setSurfaceSize(const Size(390, 844));
    addTearDown(() => tester.binding.setSurfaceSize(null));

    final dio = Dio(BaseOptions(baseUrl: 'https://cantinarr.example'))
      ..httpClientAdapter = _SeriesAdapter();

    await tester.pumpWidget(ProviderScope(
      overrides: [backendClientProvider.overrideWithValue(dio)],
      child: MaterialApp(
        theme: AppTheme.dark,
        home: SonarrSeriesDetailScreen(
          instanceId: 'sonarr-main',
          series: SonarrSeries.fromJson(_seriesJson()),
        ),
      ),
    ));
    await tester.pumpAndSettle();

    // Season 22: caught up on everything that has aired, but not complete —
    // and never "100%", which is what made it look finished.
    expect(find.text('11/13 Episodes Available • 2 unaired'), findsOneWidget);
    expect(
      _colorOf(tester, '11/13 Episodes Available • 2 unaired'),
      AppTheme.downloading,
    );
    expect(find.textContaining('11/11'), findsNothing);
    expect(find.textContaining('100%'), findsNothing);

    // Season 21 is genuinely done, and All Seasons rolls both up.
    expect(find.text('8/8 Episodes Available'), findsOneWidget);
    expect(_colorOf(tester, '8/8 Episodes Available'), AppTheme.available);
    expect(find.text('19/21 Episodes Available • 2 unaired'), findsOneWidget);
  });
}
