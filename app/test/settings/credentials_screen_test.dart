import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/theme/app_theme.dart';
import 'package:cantinarr/features/settings/settings_anchors.dart';
import 'package:cantinarr/features/settings/ui/credentials_screen.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  testWidgets('Codex provider is described as shared included access',
      (tester) async {
    final adapter = _CredentialsAdapter();
    final dio = Dio(BaseOptions(baseUrl: 'https://cantinarr.example'))
      ..httpClientAdapter = adapter;

    await tester.pumpWidget(
      ProviderScope(
        overrides: [backendClientProvider.overrideWithValue(dio)],
        child: MaterialApp(
          theme: AppTheme.dark,
          home: const CredentialsScreen(),
        ),
      ),
    );
    await tester.pumpAndSettle();

    expect(find.text('OpenAI (OAuth)'), findsOneWidget);
    expect(find.text('Shared account'), findsOneWidget);
    expect(
      find.text(
        'Connect one server OpenAI OAuth account for users with included access.',
      ),
      findsOneWidget,
    );
    expect(find.textContaining('one ChatGPT account and one Codex meter'),
        findsOneWidget);
    expect(find.text('Key missing'), findsNothing);
    expect(find.text('Daily shared-model test'), findsOneWidget);

    final healthToggle = find.byKey(const ValueKey('ai-health-check-toggle'));
    await tester.ensureVisible(healthToggle);
    await tester.tap(healthToggle);
    await tester.drag(
      find.byType(Scrollable).first,
      const Offset(0, -1200),
    );
    await tester.pumpAndSettle();
    final save = find.widgetWithText(ElevatedButton, 'Save');
    await tester.ensureVisible(save);
    await tester.tap(save);
    await tester.pumpAndSettle();

    expect(adapter.lastUpdate?['ai_health_check_enabled'], 'false');
  });

  testWidgets('saves an OpenAI key with its selected shared model',
      (tester) async {
    final adapter = _CredentialsAdapter();
    final dio = Dio(BaseOptions(baseUrl: 'https://cantinarr.example'))
      ..httpClientAdapter = adapter;

    await tester.pumpWidget(
      ProviderScope(
        overrides: [backendClientProvider.overrideWithValue(dio)],
        child: MaterialApp(
          theme: AppTheme.dark,
          home: const CredentialsScreen(),
        ),
      ),
    );
    await tester.pumpAndSettle();

    await tester.tap(find.byKey(const ValueKey('ai-provider-codex')));
    await tester.pumpAndSettle();
    await tester.tap(find.text('OpenAI').last);
    await tester.pumpAndSettle();

    await tester.drag(
      find.byType(Scrollable).first,
      const Offset(0, -900),
    );
    await tester.pumpAndSettle();
    final openAIKey = find.byWidgetPredicate(
      (widget) =>
          widget is TextField &&
          widget.decoration?.hintText == 'OpenAI API key',
    );
    await tester.ensureVisible(openAIKey);
    await tester.enterText(openAIKey, 'synthetic-shared-key');

    final save = find.widgetWithText(ElevatedButton, 'Save');
    await tester.ensureVisible(save);
    await tester.tap(save);
    await tester.pumpAndSettle();

    expect(adapter.lastUpdate, {
      'openai_key': 'synthetic-shared-key',
      'ai_provider': 'openai',
      'ai_model': 'gpt-4.1-mini',
    });
  });

  testWidgets('Grok OAuth provider shows the shared xAI account panel',
      (tester) async {
    final adapter = _CredentialsAdapter(provider: 'grok_oauth');
    final dio = Dio(BaseOptions(baseUrl: 'https://cantinarr.example'))
      ..httpClientAdapter = adapter;

    await tester.pumpWidget(
      ProviderScope(
        overrides: [backendClientProvider.overrideWithValue(dio)],
        child: MaterialApp(
          theme: AppTheme.dark,
          home: const CredentialsScreen(),
        ),
      ),
    );
    await tester.pumpAndSettle();

    expect(find.text('xAI Grok (OAuth)'), findsOneWidget);
    expect(find.text('Shared account'), findsOneWidget);
    expect(
      find.text(
        'Connect one server xAI Grok account for users with included access.',
      ),
      findsOneWidget,
    );
    expect(find.text('Shared xAI Grok allowance'), findsOneWidget);
    expect(find.text('Connect shared xAI Grok'), findsOneWidget);
  });

  testWidgets('saves an xAI API key under grok_key', (tester) async {
    final adapter = _CredentialsAdapter();
    final dio = Dio(BaseOptions(baseUrl: 'https://cantinarr.example'))
      ..httpClientAdapter = adapter;

    await tester.pumpWidget(
      ProviderScope(
        overrides: [backendClientProvider.overrideWithValue(dio)],
        child: MaterialApp(
          theme: AppTheme.dark,
          home: const CredentialsScreen(),
        ),
      ),
    );
    await tester.pumpAndSettle();

    await tester.drag(
      find.byType(Scrollable).first,
      const Offset(0, -1400),
    );
    await tester.pumpAndSettle();
    final grokKey = find.byWidgetPredicate(
      (widget) =>
          widget is TextField && widget.decoration?.hintText == 'xAI API key',
    );
    await tester.ensureVisible(grokKey);
    await tester.enterText(grokKey, 'synthetic-grok-key');

    final save = find.widgetWithText(ElevatedButton, 'Save');
    await tester.ensureVisible(save);
    await tester.tap(save);
    await tester.pumpAndSettle();

    expect(adapter.lastUpdate?['grok_key'], 'synthetic-grok-key');
  });

  testWidgets(
      'a highlight deep link scrolls to the Gemini section on load',
      (tester) async {
    final adapter = _CredentialsAdapter();
    final dio = Dio(BaseOptions(baseUrl: 'https://cantinarr.example'))
      ..httpClientAdapter = adapter;

    await tester.pumpWidget(
      ProviderScope(
        overrides: [backendClientProvider.overrideWithValue(dio)],
        child: MaterialApp(
          theme: AppTheme.dark,
          home: const CredentialsScreen(
            highlightId: SettingsAnchors.credentialsGemini,
          ),
        ),
      ),
    );
    // The body mounts only after the async status load; the anchor's trigger
    // fires then, so settling covers load, scroll, and the highlight fade.
    await tester.pumpAndSettle();

    expect(
      tester
          .state<ScrollableState>(find.byType(Scrollable).first)
          .position
          .pixels,
      greaterThan(0),
    );
    expect(find.text('Google Gemini (AI)'), findsOneWidget);
  });

  testWidgets('hides the base URL field when the server does not advertise it',
      (tester) async {
    final adapter = _CredentialsAdapter();
    await _pumpCredentials(tester, adapter);

    await tester.tap(find.byKey(const ValueKey('ai-provider-codex')));
    await tester.pumpAndSettle();
    await tester.tap(find.text('OpenAI').last);
    await tester.pumpAndSettle();

    expect(find.byKey(const ValueKey('openai-base-url')), findsNothing);
  });

  testWidgets('prefills the base URL from the credentials status',
      (tester) async {
    final adapter = _CredentialsAdapter(
      provider: 'openai',
      model: 'gpt-4.1-mini',
      openAiSupportsBaseUrl: true,
      openAiBaseUrl: 'http://llm-host:8080/v1',
    );
    await _pumpCredentials(tester, adapter);

    final field = find.byKey(const ValueKey('openai-base-url'));
    expect(field, findsOneWidget);
    expect(find.text('OpenAI base URL'), findsOneWidget);
    expect(
      tester.widget<TextField>(field).controller?.text,
      'http://llm-host:8080/v1',
    );
  });

  testWidgets('saves only the changed base URL, trimmed', (tester) async {
    final adapter = _CredentialsAdapter(
      provider: 'openai',
      model: 'gpt-4.1-mini',
      openAiSupportsBaseUrl: true,
      openAiBaseUrl: '',
    );
    await _pumpCredentials(tester, adapter);

    final field = find.byKey(const ValueKey('openai-base-url'));
    await tester.ensureVisible(field);
    await tester.enterText(field, ' http://llm-host:8080/v1 ');

    final save = find.widgetWithText(ElevatedButton, 'Save');
    await tester.scrollUntilVisible(save, 300,
        scrollable: find.byType(Scrollable).first);
    await tester.tap(save);
    await tester.pumpAndSettle();

    expect(adapter.lastUpdate, {'openai_base_url': 'http://llm-host:8080/v1'});
    expect(find.text('AI test passed. Settings saved.'), findsOneWidget);
  });

  testWidgets('clearing the base URL saves an empty string', (tester) async {
    final adapter = _CredentialsAdapter(
      provider: 'openai',
      model: 'gpt-4.1-mini',
      openAiSupportsBaseUrl: true,
      openAiBaseUrl: 'http://old-host:1234/v1',
    );
    await _pumpCredentials(tester, adapter);

    final field = find.byKey(const ValueKey('openai-base-url'));
    await tester.ensureVisible(field);
    await tester.enterText(field, '');

    final save = find.widgetWithText(ElevatedButton, 'Save');
    await tester.scrollUntilVisible(save, 300,
        scrollable: find.byType(Scrollable).first);
    await tester.tap(save);
    await tester.pumpAndSettle();

    expect(adapter.lastUpdate, {'openai_base_url': ''});
  });

  testWidgets('an untouched base URL is not a change', (tester) async {
    final adapter = _CredentialsAdapter(
      provider: 'openai',
      model: 'gpt-4.1-mini',
      openAiSupportsBaseUrl: true,
      openAiBaseUrl: 'http://llm-host:8080/v1',
    );
    await _pumpCredentials(tester, adapter);

    final save = find.widgetWithText(ElevatedButton, 'Save');
    await tester.scrollUntilVisible(save, 300,
        scrollable: find.byType(Scrollable).first);
    await tester.tap(save);
    await tester.pumpAndSettle();

    expect(adapter.lastUpdate, isNull);
    expect(find.text('No changes to save'), findsOneWidget);
  });

  testWidgets('shows the base URL field only for the OpenAI provider',
      (tester) async {
    final adapter = _CredentialsAdapter(openAiSupportsBaseUrl: true);
    await _pumpCredentials(tester, adapter);

    expect(find.byKey(const ValueKey('openai-base-url')), findsNothing);

    await tester.tap(find.byKey(const ValueKey('ai-provider-codex')));
    await tester.pumpAndSettle();
    await tester.tap(find.text('OpenAI').last);
    await tester.pumpAndSettle();

    expect(find.byKey(const ValueKey('openai-base-url')), findsOneWidget);
  });
}

Future<void> _pumpCredentials(
  WidgetTester tester,
  _CredentialsAdapter adapter,
) async {
  final dio = Dio(BaseOptions(baseUrl: 'https://cantinarr.example'))
    ..httpClientAdapter = adapter;
  await tester.pumpWidget(
    ProviderScope(
      overrides: [backendClientProvider.overrideWithValue(dio)],
      child: MaterialApp(
        theme: AppTheme.dark,
        home: const CredentialsScreen(),
      ),
    ),
  );
  await tester.pumpAndSettle();
}

class _CredentialsAdapter implements HttpClientAdapter {
  _CredentialsAdapter({
    this.provider = 'codex',
    this.model,
    this.openAiSupportsBaseUrl = false,
    this.openAiBaseUrl,
  });

  final String provider;
  final String? model;
  final bool openAiSupportsBaseUrl;
  final String? openAiBaseUrl;
  Map<String, dynamic>? lastUpdate;

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    if (options.method == 'PUT' && requestStream != null) {
      final bytes = await requestStream.expand((chunk) => chunk).toList();
      lastUpdate = jsonDecode(utf8.decode(bytes)) as Map<String, dynamic>;
      return ResponseBody.fromString(
        jsonEncode({'status': 'ok'}),
        200,
        headers: {
          Headers.contentTypeHeader: [Headers.jsonContentType],
        },
      );
    }
    return ResponseBody.fromString(
      jsonEncode({
        'credentials': const <String, bool>{},
        'tmdb_using_builtin': false,
        'ai': {
          'config': {
            'provider': provider,
            'model': model ?? (provider == 'grok_oauth' ? 'grok-4.6' : 'gpt-5.4'),
          },
          if (openAiBaseUrl != null) 'openai_base_url': openAiBaseUrl,
          'providers': [
            {
              'id': 'codex',
              'label': 'OpenAI (OAuth)',
              'auth_type': 'user_oauth',
              'credential_key': '',
              'models': [
                {'id': 'gpt-5.4', 'label': 'GPT-5.4'},
              ],
            },
            {
              'id': 'openai',
              'label': 'OpenAI',
              'auth_type': 'api_key',
              'credential_key': 'openai_key',
              if (openAiSupportsBaseUrl) 'supports_base_url': true,
              'models': [
                {'id': 'gpt-4.1-mini', 'label': 'GPT-4.1 mini'},
              ],
            },
            {
              'id': 'grok',
              'label': 'xAI Grok',
              'auth_type': 'api_key',
              'credential_key': 'grok_key',
              'models': [
                {'id': 'grok-4.6', 'label': 'Grok 4.6'},
              ],
            },
            {
              'id': 'grok_oauth',
              'label': 'xAI Grok (OAuth)',
              'auth_type': 'user_oauth',
              'credential_key': '',
              'models': [
                {'id': 'grok-4.6', 'label': 'Grok 4.6'},
              ],
            },
          ],
          'health_check': {
            'enabled': true,
            'interval_hours': 24,
            'last_checked_at': null,
          },
        },
      }),
      200,
      headers: {
        Headers.contentTypeHeader: [Headers.jsonContentType],
      },
    );
  }

  @override
  void close({bool force = false}) {}
}
