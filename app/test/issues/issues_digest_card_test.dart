import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/websocket_client.dart';
import 'package:cantinarr/core/providers/realtime_provider.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/issues/data/issue_models.dart';
import 'package:cantinarr/features/issues/data/issues_service.dart';
import 'package:cantinarr/features/issues/logic/issues_provider.dart';
import 'package:cantinarr/features/issues/ui/issues_list_screen.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

/// The scoreboard belongs at the head of the list it summarises, and its
/// numbers must survive a narrow card: a count may never be orphaned from the
/// word it counts, so the only place the line may wrap is before a "·".
void main() {
  testWidgets('digest card renders on the issues list with unbreakable stats',
      (tester) async {
    final service = _FakeIssuesService(
      issues: [_issue(1, 'needs_admin')],
      digest: const {
        'issues_resolved': 13,
        'zero_touch': 4,
        'rule_approved': 1,
        'self_cleared': 667,
        'needs_admin_open': 2,
        'paused_rules': 1,
      },
    );
    await _pump(tester, service);

    final line = tester
        .widgetList<Text>(find.byType(Text))
        .map((t) => t.data ?? '')
        .firstWhere((d) => d.contains('Last'), orElse: () => '');
    expect(line, isNotEmpty, reason: 'the digest card should render');

    // Every stat is glued: no plain space inside one, and "zero-touch" keeps a
    // non-breaking hyphen so it cannot split either.
    expect(line, contains('13\u00A0resolved'));
    expect(line, contains('667\u00A0cleared\u00A0on\u00A0their\u00A0own'));
    expect(line, contains('4\u00A0zero\u2011touch'));
    // The delimiter leads the next line: breakable space BEFORE the dot, glued
    // space after it.
    expect(line, contains(' ·\u00A0'));
    expect(line, isNot(contains('· ')));
  });

  testWidgets('closed tab says how much history it is not showing',
      (tester) async {
    final service = _FakeIssuesService(
      issues: [_issue(1, 'resolved'), _issue(2, 'resolved')],
      closedTotal: 667,
    );
    await _pump(tester, service);

    await tester.tap(find.text('Closed'));
    await tester.pumpAndSettle();

    expect(
      find.text('Showing the 2 most recent of 667 closed issues.'),
      findsOneWidget,
    );
  });

  testWidgets('no note when the closed list is complete', (tester) async {
    final service = _FakeIssuesService(
      issues: [_issue(1, 'resolved')],
      closedTotal: 1,
    );
    await _pump(tester, service);

    await tester.tap(find.text('Closed'));
    await tester.pumpAndSettle();

    expect(find.textContaining('most recent of'), findsNothing);
  });
}

Future<void> _pump(WidgetTester tester, _FakeIssuesService service) async {
  final container = ProviderContainer(
    overrides: [
      authProvider.overrideWith(_FakeAuthNotifier.new),
      issuesServiceProvider.overrideWithValue(service),
      realtimeEventsProvider.overrideWithValue(const Stream<WsEvent>.empty()),
    ],
  );
  addTearDown(container.dispose);
  await tester.pumpWidget(
    UncontrolledProviderScope(
      container: container,
      child: const MaterialApp(home: IssuesListScreen()),
    ),
  );
  await tester.pumpAndSettle();
}

Issue _issue(int id, String status) => Issue.fromJson({
      'id': id,
      'source': 'auto',
      'status': status,
      'media_type': 'tv',
      'title': 'Example Show',
      'detail': 'detail',
      'occurrences': 1,
      'read': true,
      'created_at': '2026-07-10T10:00:00Z',
      'updated_at': '2026-07-10T10:00:00Z',
      if (status == 'resolved') 'closed_at': '2026-07-10T11:00:00Z',
    });

const _adminState = AuthState(
  connection: BackendConnection(
    serverUrl: 'http://localhost',
    accessToken: 'access',
    refreshToken: 'refresh',
    services: AvailableServices(),
  ),
  user: UserProfile(id: 1, username: 'admin', role: 'admin'),
);

class _FakeAuthNotifier extends AuthNotifier {
  @override
  Future<AuthState> build() async => _adminState;
}

class _FakeIssuesService extends IssuesService {
  _FakeIssuesService({
    this.issues = const [],
    this.digest,
    this.closedTotal = 0,
  }) : super(backendDio: Dio());

  List<Issue> issues;
  Map<String, dynamic>? digest;
  int closedTotal;

  @override
  Future<IssuePage> listIssues({String? status}) async =>
      IssuePage(issues: issues, closedTotal: closedTotal);

  @override
  Future<Map<String, dynamic>> agentDigest({int days = 7}) async {
    final d = digest;
    if (d == null) throw StateError('no digest');
    return d;
  }
}
