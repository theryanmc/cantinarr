import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/theme/app_theme.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/setup_wizard/ui/setup_wizard_screen.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

/// The counted headers use non-breaking spaces so a wrapped header never
/// leaves a bare "1" on its own line. Spelling them out here means a stray
/// plain space in the widget fails the test rather than passing invisibly.
const _nb = '\u00A0';

void main() {
  setUp(() => SharedPreferences.setMockInitialValues({}));

  testWidgets('each section header counts what is still unconfigured',
      (tester) async {
    await _pumpWizard(tester, [
      ('radarr', true, false),
      ('sonarr', false, false),
      ('tmdb', true, false),
      ('trakt', false, true),
      ('books', false, true),
    ]);

    expect(find.text('ESSENTIALS$_nb· 1${_nb}LEFT'), findsOneWidget);
    expect(find.text('NICE TO HAVE$_nb· 2${_nb}LEFT'), findsOneWidget);
  });

  testWidgets('a finished section says so instead of looking like an empty one',
      (tester) async {
    await _pumpWizard(tester, [
      ('radarr', true, false),
      ('sonarr', true, false),
      ('tmdb', true, false),
      ('trakt', false, true),
    ]);

    expect(find.text('ESSENTIALS$_nb· DONE'), findsOneWidget);
    expect(find.text('NICE TO HAVE$_nb· 1${_nb}LEFT'), findsOneWidget);

    // The reward has to be visible, not just worded: a done section reads in
    // the same green as the row checkmarks.
    final done = tester.widget<Text>(find.text('ESSENTIALS$_nb· DONE'));
    final suffix = (done.textSpan! as TextSpan).children!.last as TextSpan;
    expect(suffix.style?.color, AppTheme.available);
  });
}

Future<void> _pumpWizard(
  WidgetTester tester,
  List<(String, bool, bool)> items,
) async {
  final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
    ..httpClientAdapter = _WizardAdapter(items);

  await tester.pumpWidget(
    ProviderScope(
      overrides: [
        authProvider.overrideWith(_AdminAuthNotifier.new),
        backendClientProvider.overrideWithValue(dio),
      ],
      child: MaterialApp(
        theme: AppTheme.dark,
        home: const SetupWizardScreen(),
      ),
    ),
  );
  await tester.pumpAndSettle();
}

class _AdminAuthNotifier extends AuthNotifier {
  @override
  Future<AuthState> build() async => const AuthState(
        connection: BackendConnection(
          serverUrl: 'http://localhost',
          accessToken: 'access',
          refreshToken: 'refresh',
        ),
        user: UserProfile(
          id: 1,
          username: 'admin',
          role: 'admin',
          permissions: [],
        ),
      );

  @override
  Future<void> refreshUser() async {}
}

class _WizardAdapter implements HttpClientAdapter {
  _WizardAdapter(this.items);

  /// (key, configured, optional) per checklist row.
  final List<(String, bool, bool)> items;

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    return ResponseBody.fromString(
      jsonEncode({
        'items': [
          for (final (key, configured, optional) in items)
            {
              'key': key,
              'title': key,
              'description': 'about $key',
              'configured': configured,
              'optional': optional,
            },
        ],
        'configured': items.where((i) => i.$2).length,
        'total': items.length,
      }),
      200,
      headers: {
        'content-type': ['application/json'],
      },
    );
  }

  @override
  void close({bool force = false}) {}
}
