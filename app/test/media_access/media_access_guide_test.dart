import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/network/safe_http_log_interceptor.dart';
import 'package:cantinarr/core/theme/app_theme.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/media_access/ui/media_access_guide.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

/// One canned answer: status, body (a String is sent verbatim), content type.
class _Reply {
  const _Reply(this.status, this.body, {this.contentType = 'application/json'});
  final int status;
  final Object body;
  final String contentType;
}

/// Serves replies by "METHOD path"; a handler may consult the decoded request
/// body and the adapter's own request log (so a later GET can reflect an
/// earlier POST). Unknown paths answer 404.
class _JsonAdapter implements HttpClientAdapter {
  _JsonAdapter(this.handlers);

  final Map<String, _Reply Function(dynamic body, int callsSoFar)> handlers;
  final List<({String method, String path, dynamic body})> requests = [];

  int calls(String method, String path) =>
      requests.where((r) => r.method == method && r.path == path).length;

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    dynamic body;
    if (requestStream != null) {
      final bytes = await requestStream.expand((c) => c).toList();
      if (bytes.isNotEmpty) body = jsonDecode(utf8.decode(bytes));
    }
    final path = options.uri.path;
    final before = calls(options.method, path);
    requests.add((method: options.method, path: path, body: body));
    final handler = handlers['${options.method} $path'];
    if (handler == null) {
      return ResponseBody.fromString(
        jsonEncode({'error': 'not found'}),
        404,
        headers: {
          'content-type': ['application/json'],
        },
      );
    }
    final reply = handler(body, before);
    if (reply.status == 0) {
      throw DioException.connectionError(
        requestOptions: options,
        reason: 'connection refused',
      );
    }
    return ResponseBody.fromString(
      reply.body is String ? reply.body as String : jsonEncode(reply.body),
      reply.status,
      headers: {
        'content-type': [reply.contentType],
      },
    );
  }

  @override
  void close({bool force = false}) {}
}

class _FakeAuthNotifier extends AuthNotifier {
  _FakeAuthNotifier({required this.user, this.instances = const []});

  final UserProfile user;
  final List<ServiceInstance> instances;

  @override
  Future<AuthState> build() async => AuthState(
        connection: BackendConnection(
          serverUrl: 'http://localhost',
          accessToken: 'access',
          refreshToken: 'refresh',
          instances: instances,
        ),
        user: user,
      );
}

const _alice = UserProfile(id: 2, username: 'alice', role: 'user');
const _admin = UserProfile(id: 1, username: 'admin', role: 'admin');
const _jellyfin = ServiceInstance(
  id: 'jf-a',
  serviceType: 'jellyfin',
  name: 'Home Jellyfin',
);

Map<String, dynamic> _server({
  Map<String, dynamic>? account,
  String publicAddress = 'https://jf.example.com',
}) =>
    {
      'instance_id': 'jf-a',
      'service_type': 'jellyfin',
      'name': 'Home Jellyfin',
      'public_address': publicAddress,
      'account': account,
    };

Map<String, dynamic> _account({
  String username = 'alice',
  bool disabled = false,
  bool verified = true,
}) =>
    {'username': username, 'disabled': disabled, 'verified': verified};

Future<_JsonAdapter> _pumpGuide(
  WidgetTester tester, {
  required Map<String, _Reply Function(dynamic body, int callsSoFar)> handlers,
  UserProfile user = _alice,
  List<ServiceInstance> instances = const [_jellyfin],
  List<String>? logs,
}) async {
  tester.view.physicalSize = const Size(800, 1600);
  tester.view.devicePixelRatio = 1.0;
  addTearDown(tester.view.reset);

  final adapter = _JsonAdapter(handlers);
  final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
    ..httpClientAdapter = adapter;
  if (logs != null) {
    dio.interceptors.add(SafeHttpLogInterceptor(logPrint: logs.add));
  }
  await tester.pumpWidget(
    ProviderScope(
      overrides: [
        authProvider.overrideWith(
            () => _FakeAuthNotifier(user: user, instances: instances)),
        backendClientProvider.overrideWithValue(dio),
      ],
      child: const MaterialApp(theme: null, home: MediaAccessGuide()),
    ),
  );
  await tester.pumpAndSettle();
  return adapter;
}

/// Pumping a second guide into the same tree would update the existing
/// elements (same widget types, same positions) and keep the first pump's
/// state, overrides, and any open sheet; tear the tree down between pumps.
Future<void> _unmount(WidgetTester tester) async {
  await tester.pumpWidget(const SizedBox());
  await tester.pumpAndSettle();
}

/// Opens the password sheet from the account card.
Future<void> _openSheet(WidgetTester tester) async {
  await tester.tap(find.widgetWithText(ElevatedButton, 'Create my account'));
  await tester.pumpAndSettle();
  expect(find.text('Create your Jellyfin account'), findsOneWidget);
}

Future<void> _submitPassword(
  WidgetTester tester, {
  required String password,
  String? confirm,
}) async {
  await tester.enterText(
      find.widgetWithText(TextField, 'Password'), password);
  await tester.enterText(
      find.widgetWithText(TextField, 'Confirm password'), confirm ?? password);
  await tester.tap(find.widgetWithText(ElevatedButton, 'Create account'));
  await tester.pumpAndSettle();
}

/// A create handler whose GET flips to an active account once the POST
/// has been made, the way the real server would answer.
Map<String, _Reply Function(dynamic, int)> _createFlow(_Reply postReply) {
  var created = false;
  return {
    'GET /api/media-servers': (_, __) => _Reply(200, [
          _server(account: created ? _account() : null),
        ]),
    'POST /api/media-servers/jf-a/account': (_, __) {
      if (postReply.status == 201) created = true;
      return postReply;
    },
  };
}

void main() {
  testWidgets(
      'create flow validates locally, posts the password once, and shows the '
      'account', (tester) async {
    final adapter = await _pumpGuide(
      tester,
      handlers: _createFlow(const _Reply(201, {
        'username': 'alice',
        'public_address': 'https://jf.example.com',
      })),
    );

    expect(find.text('Watch on Jellyfin'), findsOneWidget);
    expect(
      find.text('You have access to Home Jellyfin. Create your account to '
          'start watching.'),
      findsOneWidget,
    );
    expect(find.text('Your account'), findsOneWidget);
    expect(find.text('Install the Jellyfin app'), findsOneWidget);
    expect(find.text('Request here, watch there'), findsOneWidget);

    await _openSheet(tester);
    expect(
      find.textContaining("You'll sign in to Home Jellyfin as alice"),
      findsOneWidget,
    );

    await _submitPassword(tester, password: 'short');
    expect(find.text('Password must be at least 8 characters.'),
        findsOneWidget);
    await _submitPassword(tester,
        password: 'correct-horse', confirm: 'different-one');
    expect(find.text('Passwords do not match.'), findsOneWidget);
    expect(adapter.calls('POST', '/api/media-servers/jf-a/account'), 0);

    await _submitPassword(tester, password: 'correct-horse');
    final post = adapter.requests
        .singleWhere((r) => r.method == 'POST');
    expect(post.body, {'password': 'correct-horse'});
    expect(find.text('Create your Jellyfin account'), findsNothing);
    expect(find.text('Account created. Sign in with your new password.'),
        findsOneWidget);

    // The card re-read the server: the account is shown with where to sign
    // in, and the create button is gone.
    expect(find.text('Username'), findsOneWidget);
    expect(find.text('alice'), findsOneWidget);
    expect(find.text('Sign in at https://jf.example.com'), findsOneWidget);
    expect(find.widgetWithText(TextButton, 'Copy address'), findsOneWidget);
    expect(find.widgetWithText(TextButton, 'Open'), findsOneWidget);
    expect(find.widgetWithText(ElevatedButton, 'Create my account'),
        findsNothing);
  });

  testWidgets('a taken name says to ask the admin to link it', (tester) async {
    await _pumpGuide(
      tester,
      handlers: _createFlow(const _Reply(409, {
        'error': 'that name is already taken on this server; ask your admin '
            'to link it to you',
        'code': 'name_taken',
      })),
    );

    await _openSheet(tester);
    await _submitPassword(tester, password: 'correct-horse');

    expect(
      find.text('The name alice is already taken on Home Jellyfin. Ask your '
          'admin to link that account to you.'),
      findsOneWidget,
    );
    // The sheet stays open for another try.
    expect(find.text('Create your Jellyfin account'), findsOneWidget);
  });

  testWidgets('an existing account closes the sheet and refreshes',
      (tester) async {
    var polls = 0;
    await _pumpGuide(
      tester,
      handlers: {
        'GET /api/media-servers': (_, __) => _Reply(200, [
              // Absent on the first read, present once re-read after the
              // refusal, as if another device had just created it.
              _server(account: polls++ == 0 ? null : _account()),
            ]),
        'POST /api/media-servers/jf-a/account': (_, __) => const _Reply(409, {
              'error': 'you already have an account on this server',
              'code': 'account_exists',
            }),
      },
    );

    await _openSheet(tester);
    await _submitPassword(tester, password: 'correct-horse');

    expect(find.text('Create your Jellyfin account'), findsNothing);
    expect(find.text('You already have an account here.'), findsOneWidget);
    expect(find.text('Username'), findsOneWidget);
    expect(find.text('alice'), findsOneWidget);
  });

  testWidgets('other refusals are said in requester words', (tester) async {
    // A text/plain JSON body (Go's http.Error) must decode like any other.
    final invalidName = _Reply(
      400,
      '${jsonEncode({
            'error': "your Cantinarr username can't be used as a name on "
                'this server; ask your admin to link an account for you',
            'code': 'invalid_name',
          })}\n',
      contentType: 'text/plain; charset=utf-8',
    );
    const notAvailable =
        _Reply(403, {'error': 'that server is not available to you'});
    const upstream = _Reply(502, {
      'error': "couldn't create the account right now; try again later"
    });
    const offline = _Reply(0, '');
    final expectations = <_Reply, String>{
      invalidName: "Home Jellyfin doesn't accept your username as an account "
          'name. Ask your admin to link an account for you.',
      notAvailable: 'That server is not available to you.',
      upstream: "Couldn't create the account. Try again in a moment, or ask "
          'your admin.',
      offline: "Couldn't reach the server. Check your connection and try "
          'again.',
    };

    for (final entry in expectations.entries) {
      await _pumpGuide(tester, handlers: _createFlow(entry.key));
      await _openSheet(tester);
      await _submitPassword(tester, password: 'correct-horse');
      expect(find.text(entry.value), findsOneWidget,
          reason: 'status ${entry.key.status}');
      expect(find.text('Create your Jellyfin account'), findsOneWidget);
      await _unmount(tester);
    }
  });

  testWidgets('a turned-off account says so and offers nothing to create',
      (tester) async {
    await _pumpGuide(
      tester,
      handlers: {
        'GET /api/media-servers': (_, __) => _Reply(200, [
              _server(account: _account(disabled: true)),
            ]),
      },
    );

    expect(
      find.text('Your access to Home Jellyfin is turned off. Ask your admin '
          "if you think that's a mistake."),
      findsOneWidget,
    );
    expect(find.widgetWithText(ElevatedButton, 'Create my account'),
        findsNothing);
    expect(find.text('Username'), findsNothing);
  });

  testWidgets('an unconfirmed account is said to be unconfirmed',
      (tester) async {
    await _pumpGuide(
      tester,
      handlers: {
        'GET /api/media-servers': (_, __) => _Reply(200, [
              _server(account: _account(verified: false)),
            ]),
      },
    );

    expect(find.text('alice'), findsOneWidget);
    expect(
      find.text("We couldn't confirm this account with the server just now. "
          'Signing in should still work.'),
      findsOneWidget,
    );
  });

  testWidgets('a missing sign-in address says to ask the admin',
      (tester) async {
    await _pumpGuide(
      tester,
      handlers: {
        'GET /api/media-servers': (_, __) => _Reply(200, [
              _server(account: _account(), publicAddress: ''),
            ]),
      },
    );

    expect(
      find.text("Your admin hasn't shared the sign-in address yet. Ask them "
          'where to sign in.'),
      findsOneWidget,
    );
    expect(find.widgetWithText(TextButton, 'Open'), findsNothing);
    expect(find.textContaining('Sign in at'), findsNothing);
  });

  testWidgets('nothing shared reads differently for requesters and admins',
      (tester) async {
    final empty = {
      'GET /api/media-servers': (_, __) => const _Reply(200, <Object>[]),
    };

    await _pumpGuide(tester, handlers: empty, instances: const []);
    expect(
      find.text('No media server is shared with you yet. Ask your admin for '
          'access.'),
      findsOneWidget,
    );
    expect(find.text('Watch on your media server'), findsOneWidget);

    await _unmount(tester);
    await _pumpGuide(tester,
        handlers: empty, user: _admin, instances: const []);
    expect(
      find.text('No media server is shared with your account yet. Open the '
          'instance under Settings and add yourself under User Access.'),
      findsOneWidget,
    );
  });

  testWidgets('a failed load offers Retry', (tester) async {
    final adapter = await _pumpGuide(
      tester,
      handlers: {
        'GET /api/media-servers': (_, calls) => calls == 0
            ? const _Reply(503, {'error': 'temporarily unavailable'})
            : _Reply(200, [_server()]),
      },
    );

    expect(find.text("Couldn't load your media servers."), findsOneWidget);
    // The granted set from the config still names the title meanwhile.
    expect(find.text('Watch on Jellyfin'), findsOneWidget);

    await tester.tap(find.widgetWithText(ElevatedButton, 'Retry'));
    await tester.pumpAndSettle();

    expect(adapter.calls('GET', '/api/media-servers'), 2);
    expect(find.text("Couldn't load your media servers."), findsNothing);
    expect(find.widgetWithText(ElevatedButton, 'Create my account'),
        findsOneWidget);
  });

  testWidgets('the password never reaches the safe HTTP log', (tester) async {
    final logs = <String>[];
    await _pumpGuide(
      tester,
      logs: logs,
      handlers: _createFlow(const _Reply(201, {
        'username': 'alice',
        'public_address': 'https://jf.example.com',
      })),
    );

    await _openSheet(tester);
    await _submitPassword(tester, password: 'correct-horse-battery');

    final output = logs.join('\n');
    expect(output, contains('POST /api/media-servers/…'));
    expect(output, isNot(contains('correct-horse-battery')));
    expect(output, isNot(contains('jf-a')));
    expect(output, isNot(contains('password')));
  });

  testWidgets('the app theme renders the guide without overflow',
      (tester) async {
    tester.view.physicalSize = const Size(360, 800);
    tester.view.devicePixelRatio = 1.0;
    addTearDown(tester.view.reset);

    final adapter = _JsonAdapter({
      'GET /api/media-servers': (_, __) => _Reply(200, [
            _server(account: _account(verified: false)),
          ]),
    });
    final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
      ..httpClientAdapter = adapter;
    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          authProvider.overrideWith(() => _FakeAuthNotifier(user: _alice)),
          backendClientProvider.overrideWithValue(dio),
        ],
        child: MaterialApp(theme: AppTheme.dark, home: const MediaAccessGuide()),
      ),
    );
    await tester.pumpAndSettle();

    expect(tester.takeException(), isNull);
    expect(find.text('Sign in at https://jf.example.com'), findsOneWidget);
  });
}
