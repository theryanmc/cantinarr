import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/app.dart';
import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/network/websocket_client.dart';
import 'package:cantinarr/core/providers/realtime_provider.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

/// The admin-only "update available" banner, exercised end to end through the
/// real provider + service chain against a canned /api/admin/update-status
/// payload: visibility gating (admin, availability, dismissal) and the
/// portal-vs-guide primary action.
void main() {
  testWidgets('shows for an admin, with the guide action when no portal is set',
      (tester) async {
    final adapter = _UpdateStatusAdapter();
    await _pumpApp(tester, role: 'admin', adapter: adapter);

    expect(find.text('Cantinarr 1.3.0 is available'), findsOneWidget);
    expect(find.text('Notes'), findsOneWidget);
    expect(find.text('How to update'), findsOneWidget);
    expect(find.text('Update'), findsNothing);
  });

  testWidgets('offers the management portal as the primary action when set',
      (tester) async {
    final adapter =
        _UpdateStatusAdapter(managementUrl: 'http://tower.local/docker');
    await _pumpApp(tester, role: 'admin', adapter: adapter);

    expect(find.text('Cantinarr 1.3.0 is available'), findsOneWidget);
    expect(find.text('Update'), findsOneWidget);
    expect(find.text('How to update'), findsNothing);
  });

  testWidgets('never renders — or even fetches — for a non-admin',
      (tester) async {
    final adapter = _UpdateStatusAdapter();
    await _pumpApp(tester, role: 'user', adapter: adapter);

    expect(find.text('Cantinarr 1.3.0 is available'), findsNothing);
    expect(adapter.updateStatusCalls, 0,
        reason: 'the admin-only endpoint must not be called for a requester');
  });

  testWidgets('stays hidden when the server reports no update available',
      (tester) async {
    final adapter = _UpdateStatusAdapter(available: false);
    await _pumpApp(tester, role: 'admin', adapter: adapter);

    expect(find.text('Cantinarr 1.3.0 is available'), findsNothing);
  });

  testWidgets('stays hidden for a release the admin already dismissed',
      (tester) async {
    final adapter = _UpdateStatusAdapter();
    await _pumpApp(
      tester,
      role: 'admin',
      adapter: adapter,
      prefs: {'dismissed_update_version': '1.3.0'},
    );

    expect(find.text('Cantinarr 1.3.0 is available'), findsNothing);
  });

  testWidgets('dismissing hides the banner and persists per release',
      (tester) async {
    final adapter = _UpdateStatusAdapter();
    await _pumpApp(tester, role: 'admin', adapter: adapter);
    expect(find.text('Cantinarr 1.3.0 is available'), findsOneWidget);

    await tester.tap(find.byIcon(Icons.close));
    await tester.pumpAndSettle();

    expect(find.text('Cantinarr 1.3.0 is available'), findsNothing);
    final prefs = await SharedPreferences.getInstance();
    expect(prefs.getString('dismissed_update_version'), '1.3.0',
        reason: 'the dismissal silences exactly the offered version');
  });
}

const _server = 'https://media.example.com';

AuthState _stateWithRole(String role) => AuthState(
      connection: const BackendConnection(
        serverUrl: _server,
        accessToken: 'access',
        refreshToken: 'refresh',
        services: AvailableServices(),
      ),
      user: UserProfile(id: 1, username: 'tester', role: role),
    );

/// Pumps [CantinarrApp] with the platform boundaries faked, routing HTTP into
/// [adapter] so the update-status chain runs for real.
Future<ProviderContainer> _pumpApp(
  WidgetTester tester, {
  required String role,
  required _UpdateStatusAdapter adapter,
  Map<String, Object> prefs = const {},
}) async {
  SharedPreferences.setMockInitialValues(prefs);
  final links = _FakeDeepLinks();
  final container = ProviderContainer(overrides: [
    authProvider.overrideWith(() => _FakeAuthNotifier(_stateWithRole(role))),
    deepLinkSourceProvider.overrideWithValue(links),
    realtimeEventsProvider.overrideWithValue(const Stream<WsEvent>.empty()),
    backendClientProvider.overrideWithValue(
      Dio(BaseOptions(baseUrl: _server))..httpClientAdapter = adapter,
    ),
  ]);
  addTearDown(container.dispose);
  addTearDown(links.controller.close);

  await tester.pumpWidget(
    UncontrolledProviderScope(
      container: container,
      child: const CantinarrApp(),
    ),
  );
  await tester.pumpAndSettle();
  return container;
}

class _FakeAuthNotifier extends AuthNotifier {
  _FakeAuthNotifier(this._initial);

  final AuthState _initial;

  @override
  Future<AuthState> build() async => _initial;
}

class _FakeDeepLinks implements DeepLinkSource {
  final StreamController<Uri> controller = StreamController<Uri>.broadcast();

  @override
  Future<Uri?> getInitialLink() async => null;

  @override
  Stream<Uri> get uriLinkStream => controller.stream;
}

/// Canned backend: /api/admin/update-status answers from the constructor
/// knobs (and counts its calls); everything else gets the empty paged payload
/// the landing screens expect.
class _UpdateStatusAdapter implements HttpClientAdapter {
  _UpdateStatusAdapter({
    this.available = true,
    this.managementUrl = '',
  });

  final bool available;
  final String managementUrl;
  int updateStatusCalls = 0;

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    final Object body;
    if (options.path == '/api/admin/update-status') {
      updateStatusCalls++;
      body = {
        'update': {
          'current': '1.2.3',
          'latest': '1.3.0',
          'available': available,
          'url': 'https://github.com/windoze95/cantinarr/releases/tag/v1.3.0',
        },
        'management_url': managementUrl,
      };
    } else if (options.path == '/api/trakt/anticipated') {
      body = <dynamic>[];
    } else {
      body = {
        'page': 1,
        'results': <dynamic>[],
        'total_pages': 0,
        'total_results': 0,
      };
    }
    return ResponseBody.fromString(
      jsonEncode(body),
      200,
      headers: {
        'content-type': ['application/json'],
      },
    );
  }

  @override
  void close({bool force = false}) {}
}
