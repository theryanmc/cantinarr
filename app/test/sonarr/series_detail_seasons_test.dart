import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/theme/app_theme.dart';
import 'package:cantinarr/features/sonarr/data/sonarr_models.dart';
import 'package:cantinarr/features/sonarr/ui/sonarr_series_detail_screen.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

/// Tremors-shaped payload: one 13-episode season, all aired and monitored,
/// nothing imported yet — plus a Specials season nobody monitors.
Map<String, dynamic> _seriesJson() => {
      'id': 7,
      'title': 'Tremors',
      'monitored': true,
      'status': 'ended',
      'statistics': {
        'seasonCount': 2,
        'episodeFileCount': 0,
        'episodeCount': 13,
        'totalEpisodeCount': 20,
        'sizeOnDisk': 0,
        'percentOfEpisodes': 0.0,
      },
      'seasons': [
        {
          'seasonNumber': 0,
          'monitored': false,
          'statistics': {
            'episodeFileCount': 0,
            'episodeCount': 0,
            'totalEpisodeCount': 7,
            'sizeOnDisk': 0,
            'percentOfEpisodes': 0.0,
          },
        },
        {
          'seasonNumber': 1,
          'monitored': true,
          'statistics': {
            'episodeFileCount': 0,
            'episodeCount': 13,
            'totalEpisodeCount': 13,
            'sizeOnDisk': 0,
            'percentOfEpisodes': 0.0,
          },
        },
      ],
    };

/// One `queue/details` row per queued episode, as Sonarr returns it.
Map<String, dynamic> _queueRow(int episode) => {
      'id': 900 + episode,
      'seriesId': 7,
      'title': 'Tremors.S01E$episode.WEBRip',
      'status': 'completed',
      'trackedDownloadState': 'importPending',
      'episode': {
        'id': 100 + episode,
        'seasonNumber': 1,
        'episodeNumber': episode,
        'hasFile': false,
      },
    };

class _SeriesAdapter implements HttpClientAdapter {
  _SeriesAdapter({this.queue = const [], this.queueFails = false});

  final List<Map<String, dynamic>> queue;
  final bool queueFails;

  @override
  Future<ResponseBody> fetch(RequestOptions options, Stream<Uint8List>? _,
      Future<void>? __) async {
    const json = Headers.jsonContentType;
    if (options.path.endsWith('/queue/details')) {
      if (queueFails) {
        return ResponseBody.fromString('{"message":"boom"}', 500, headers: {
          Headers.contentTypeHeader: [json]
        });
      }
      return ResponseBody.fromString(jsonEncode(queue), 200, headers: {
        Headers.contentTypeHeader: [json]
      });
    }
    return ResponseBody.fromString(jsonEncode(_seriesJson()), 200, headers: {
      Headers.contentTypeHeader: [json]
    });
  }

  @override
  void close({bool force = false}) {}
}

Future<void> _pump(WidgetTester tester, _SeriesAdapter adapter) async {
  // Phone-sized, like the screen this is read on.
  await tester.binding.setSurfaceSize(const Size(390, 844));
  addTearDown(() => tester.binding.setSurfaceSize(null));

  final dio = Dio(BaseOptions(baseUrl: 'https://cantinarr.example'))
    ..httpClientAdapter = adapter;

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
}

/// The All Seasons roll-up and the season card can carry the same label, so
/// take the first — they are styled by the same widget either way.
Color _colorOf(WidgetTester tester, String text) =>
    tester.widgetList<Text>(find.text(text)).first.style!.color!;

void main() {
  testWidgets('a season waiting on its imports reads Sonarr\'s "0 + 13 / 13"',
      (tester) async {
    await _pump(
      tester,
      _SeriesAdapter(queue: [for (var e = 1; e <= 13; e++) _queueRow(e)]),
    );

    // Season 1 and the All Seasons roll-up both carry the "+ 13".
    expect(find.text('0 + 13 / 13'), findsNWidgets(2));
    expect(_colorOf(tester, '0 + 13 / 13'), AppTheme.downloading);
    // Specials: nothing monitored, so Sonarr counts nothing.
    expect(find.text('0 / 0'), findsOneWidget);
    expect(find.textContaining('%'), findsNothing);
  });

  testWidgets('with an empty queue the label is the plain fraction',
      (tester) async {
    await _pump(tester, _SeriesAdapter());

    expect(find.text('0 / 13'), findsNWidgets(2));
    expect(_colorOf(tester, '0 / 13'), AppTheme.error);
  });

  testWidgets('a queue that fails to load leaves the labels standing',
      (tester) async {
    await _pump(tester, _SeriesAdapter(queueFails: true));

    expect(find.text('0 / 13'), findsNWidgets(2));
    expect(find.textContaining('Failed to load'), findsNothing);
  });
}
