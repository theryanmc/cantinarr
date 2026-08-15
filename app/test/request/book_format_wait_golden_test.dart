import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/theme/app_theme.dart';
import 'package:cantinarr/features/request/data/request_service.dart'
    hide RequestOptions;
import 'package:cantinarr/features/request/ui/book_format_panel.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

/// A requester's view of a book Cantinarr has accepted but the library has not
/// taken yet. This state used to render as a plain "Requested" pill with no
/// further text anywhere, which is indistinguishable from a request the app
/// dropped on the floor. The golden pins the replacement: a distinct pill and a
/// standing explanation that does not depend on having caught a snackbar.
/// Regenerate with `flutter test --update-goldens`.
///
/// The copy carries no timestamp on purpose, so this golden is stable — the
/// requester is told what is happening, not asked to time it.
void main() {
  testWidgets('a waiting format on a phone', (tester) async {
    await _pumpPanel(tester, const Size(402, 874));

    await expectLater(
      find.byType(MaterialApp),
      matchesGoldenFile('goldens/book_format_wait_phone.png'),
    );
  });

  testWidgets('the wait survives a narrow screen at double text size',
      (tester) async {
    // 320 logical pixels with 200% text: the pill's own words are longer than
    // the ones it replaced, so the row has to stack rather than overflow.
    await _pumpPanel(tester, const Size(320, 900), textScale: 2.0);

    expect(tester.takeException(), isNull);
    expect(find.text('Waiting for library'), findsOneWidget);
    expect(
      find.textContaining('The library is still adding this author'),
      findsOneWidget,
    );
  });
}

Future<void> _pumpPanel(
  WidgetTester tester,
  Size size, {
  double textScale = 1.0,
}) async {
  tester.view.physicalSize = size;
  tester.view.devicePixelRatio = 1.0;
  addTearDown(tester.view.reset);

  final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
    ..httpClientAdapter = _WaitingStatusAdapter();

  await tester.pumpWidget(
    MaterialApp(
      theme: AppTheme.dark,
      home: Scaffold(
        backgroundColor: AppTheme.background,
        body: MediaQuery(
          data: MediaQueryData(textScaler: TextScaler.linear(textScale)),
          // The real screen hosts this panel inside a ListView, so a tall
          // accessibility layout scrolls there rather than overflowing. Mirror
          // that here: the failure worth catching is a row that cannot fit
          // horizontally, not a page that got taller.
          child: ListView(
            padding: const EdgeInsets.all(16),
            children: [
              BookFormatPanel(
                foreignId: 'gr:40738778',
                title: 'The Body Keeps the Score',
                service: RequestService(backendDio: dio),
              ),
            ],
          ),
        ),
      ),
    ),
  );
  await tester.pumpAndSettle();
}

class _WaitingStatusAdapter implements HttpClientAdapter {
  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async =>
      ResponseBody.fromString(
        jsonEncode({
          'status': 'requested',
          'book_formats': {'ebook': 'requested'},
          'book_format_waits': {
            'ebook': {
              'reason': 'author_import',
              'waiting_since': '2026-08-14T20:27:36Z',
              'last_attempt_at': '2026-08-15T02:35:49Z',
            },
          },
        }),
        200,
        headers: {
          'content-type': ['application/json'],
        },
      );

  @override
  void close({bool force = false}) {}
}
