import 'package:cantinarr/features/discover/data/discover_api_service.dart';
import 'package:cantinarr/features/shell/logic/shell_search_provider.dart';
import 'package:dio/dio.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  group('isAiPromptQuery', () {
    test('detects obvious AI prompts', () {
      expect(isAiPromptQuery('What should I watch tonight?'), true);
      expect(isAiPromptQuery('recommend sci-fi movies'), true);
      expect(isAiPromptQuery('find me shows like Severance'), true);
      expect(isAiPromptQuery('is The Matrix worth watching'), true);
    });

    test('keeps title-like searches in normal search', () {
      expect(isAiPromptQuery('Severance'), false);
      expect(isAiPromptQuery('Once Upon a Time in Hollywood'), false);
      expect(isAiPromptQuery('What We Do in the Shadows'), false);
      expect(isAiPromptQuery('How I Met Your Mother'), false);
      expect(isAiPromptQuery('Who Framed Roger Rabbit'), false);
    });
  });

  test('search mode re-evaluates when an AI prompt is edited into a title', () {
    final notifier = ShellSearchNotifier(
      DiscoverApiService(backendDio: Dio()),
      aiAvailable: true,
    );

    addTearDown(notifier.dispose);

    notifier.updateSearch('What should I watch tonight?');
    expect(notifier.state.searchMode, SearchMode.aiReady);
    expect(notifier.state.isLoadingSearch, true);

    notifier.updateSearch('Severance');
    expect(notifier.state.searchMode, SearchMode.search);
    expect(notifier.state.isLoadingSearch, true);
  });

  test('ai-ready mode sticks while the user appends more text', () {
    final notifier = ShellSearchNotifier(
      DiscoverApiService(backendDio: Dio()),
      aiAvailable: true,
    );

    addTearDown(notifier.dispose);

    notifier.updateSearch('Dune?');
    expect(notifier.state.searchMode, SearchMode.aiReady);

    notifier.updateSearch('Dune? part two');
    expect(notifier.state.searchMode, SearchMode.aiReady);
    expect(notifier.state.isLoadingSearch, true);
  });

  group('enterAiMode', () {
    ShellSearchNotifier makeNotifier({bool aiAvailable = true}) {
      final notifier = ShellSearchNotifier(
        DiscoverApiService(backendDio: Dio()),
        aiAvailable: aiAvailable,
      );
      addTearDown(notifier.dispose);
      return notifier;
    }

    test('is a no-op when AI is unavailable', () {
      final notifier = makeNotifier(aiAvailable: false);

      notifier.enterAiMode();
      expect(notifier.state.searchMode, SearchMode.search);
    });

    test('enters with an empty query and sticks through any edit', () {
      final notifier = makeNotifier();

      notifier.enterAiMode();
      expect(notifier.state.searchMode, SearchMode.aiReady);

      // Title-like text would flip the heuristic back to normal search; the
      // explicit choice must survive it — rewrites, deletions, and an
      // emptied field included.
      notifier.updateSearch('Severance');
      expect(notifier.state.searchMode, SearchMode.aiReady);

      notifier.updateSearch('Sev');
      expect(notifier.state.searchMode, SearchMode.aiReady);

      notifier.updateSearch('');
      expect(notifier.state.searchMode, SearchMode.aiReady);
    });

    test('keeps already-typed text and its pending fetch alive', () {
      final notifier = makeNotifier();

      notifier.updateSearch('dark comedies');
      expect(notifier.state.searchMode, SearchMode.search);

      notifier.enterAiMode();
      expect(notifier.state.searchMode, SearchMode.aiReady);
      expect(notifier.state.searchQuery, 'dark comedies');
      expect(notifier.state.isLoadingSearch, true);
    });

    test('exitAiMode clears the explicit choice', () {
      final notifier = makeNotifier();

      notifier.enterAiMode();
      notifier.exitAiMode();
      expect(notifier.state.searchMode, SearchMode.search);

      // Back on the heuristic: a title stays a title.
      notifier.updateSearch('Severance');
      expect(notifier.state.searchMode, SearchMode.search);
    });
  });
}
