import 'dart:async';

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../core/config/app_config.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/providers/instance_provider.dart';
import '../../chaptarr/data/chaptarr_api_service.dart';
import '../../chaptarr/data/chaptarr_models.dart';

/// The ways a Chaptarr book search can fail to return a usable result list.
/// Distinct from the success-but-empty case, which is expressed as
/// `error == null && searched == true && results.isEmpty` rather than a
/// fourth member here.
enum BookSearchError {
  /// No active Chaptarr instance is configured for this connection.
  noInstance,

  /// The active instance rejected the request (401/403).
  forbidden,

  /// Any other failure — network error, timeout, 5xx, malformed response.
  requestFailed,
}

/// Shell-level book-search state, scoped to the Books discovery tab.
class ShellBookSearchState {
  final String searchQuery;
  final List<ChaptarrBook> results;
  final bool isLoadingSearch;

  /// True once a lookup has completed successfully for [searchQuery] — the
  /// signal that distinguishes "no books found" from "hasn't searched yet".
  final bool searched;
  final BookSearchError? error;

  const ShellBookSearchState({
    this.searchQuery = '',
    this.results = const [],
    this.isLoadingSearch = false,
    this.searched = false,
    this.error,
  });

  /// [clearError] is explicit (not a plain nullable default) because a
  /// nullable-defaulted `copyWith` cannot set [error] back to null, and every
  /// successful result must clear a prior error.
  ShellBookSearchState copyWith({
    String? searchQuery,
    List<ChaptarrBook>? results,
    bool? isLoadingSearch,
    bool? searched,
    BookSearchError? error,
    bool clearError = false,
  }) =>
      ShellBookSearchState(
        searchQuery: searchQuery ?? this.searchQuery,
        results: results ?? this.results,
        isLoadingSearch: isLoadingSearch ?? this.isLoadingSearch,
        searched: searched ?? this.searched,
        error: clearError ? null : (error ?? this.error),
      );

  bool get isSearching => searchQuery.trim().isNotEmpty;
}

/// Manages Chaptarr book-lookup search from the shell search bar, scoped to
/// the Books discovery tab. Mirrors [ShellSearchNotifier]'s debounce and
/// generation-guard shape (`shell_search_provider.dart`), but with a typed
/// failure taxonomy instead of a free-text error, and no pagination —
/// `lookupBook` has no `page` parameter.
class ShellBookSearchNotifier extends StateNotifier<ShellBookSearchState> {
  final Ref _ref;
  Timer? _searchDebounce;
  int _searchGeneration = 0;

  ShellBookSearchNotifier(this._ref) : super(const ShellBookSearchState());

  void updateSearch(String query) {
    _searchDebounce?.cancel();
    final generation = ++_searchGeneration;

    if (query.trim().isEmpty) {
      state = const ShellBookSearchState();
      return;
    }

    state = state.copyWith(
      searchQuery: query,
      results: const [],
      isLoadingSearch: true,
      searched: false,
      clearError: true,
    );
    _searchDebounce = Timer(
      AppConfig.searchDebounce,
      () => _executeSearch(query: query, generation: generation),
    );
  }

  Future<void> _executeSearch({
    required String query,
    required int generation,
  }) async {
    // Tracer path: a single generic catch classifies any failure —
    // including no active instance — as BookSearchError.requestFailed. Task
    // 2 replaces this with the no-instance short-circuit and the
    // DioException 401/403-vs-other classification.
    try {
      final instance = _ref.read(instanceProvider).activeChaptarrInstance;
      final service = ChaptarrApiService(
        backendDio: _ref.read(backendClientProvider),
        instanceId: instance!.id,
      );
      final books = await service.lookupBook(query.trim());
      if (!mounted ||
          generation != _searchGeneration ||
          state.searchQuery != query) {
        return;
      }
      state = state.copyWith(
        results: books,
        isLoadingSearch: false,
        searched: true,
        clearError: true,
      );
    } catch (_) {
      if (!mounted ||
          generation != _searchGeneration ||
          state.searchQuery != query) {
        return;
      }
      state = state.copyWith(
        isLoadingSearch: false,
        searched: false,
        error: BookSearchError.requestFailed,
      );
    }
  }

  void reset() {
    _searchDebounce?.cancel();
    _searchGeneration++;
    state = const ShellBookSearchState();
  }

  @override
  void dispose() {
    _searchDebounce?.cancel();
    super.dispose();
  }
}

final shellBookSearchProvider =
    StateNotifierProvider<ShellBookSearchNotifier, ShellBookSearchState>(
  (ref) => ShellBookSearchNotifier(ref),
);
