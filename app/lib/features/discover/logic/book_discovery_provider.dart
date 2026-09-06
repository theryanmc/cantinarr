import 'dart:async';

import 'package:flutter/foundation.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../core/network/backend_client.dart';
import '../../../core/providers/library_refresh_provider.dart';
import '../../../core/providers/realtime_provider.dart';
import '../../request/data/request_service.dart';
import '../data/book_discovery_service.dart';
import 'discovery_access.dart';

/// Changes of account, server, permissions or grants rebuild every discovery
/// provider. Refresh errors may retain metadata only inside this same scope.
final bookDiscoveryScopeProvider = catalogDiscoveryScopeProvider;

bool bookDiscoveryAllowed(Ref ref, String? instanceId) =>
    ref.read(discoveryAccessProvider).canBrowse('chaptarr', instanceId);

void _authorize(Ref ref, String? instanceId) {
  ref.watch(bookDiscoveryScopeProvider);
  if (!bookDiscoveryAllowed(ref, instanceId)) {
    throw const BookDiscoveryException(
        'Books are not available for this account.',
        accessDenied: true);
  }
}

final bookDiscoveryServiceProvider =
    Provider((ref) => BookDiscoveryService(ref.watch(backendClientProvider)));

typedef BookDiscoveryKey = ({String foreignId, String? instanceId});

/// One-shot handoff into the existing shell book search.
final bookDiscoverySearchSeedProvider =
    StateProvider<({String query, String instanceId})?>((_) => null);

final bookDiscoveryDetailProvider = FutureProvider.autoDispose
    .family<DiscoveryBook, BookDiscoveryKey>((ref, key) {
  _authorize(ref, key.instanceId);
  return ref
      .read(bookDiscoveryServiceProvider)
      .book(key.foreignId, key.instanceId);
});

final bookGenresProvider =
    FutureProvider.autoDispose.family<List<BookGenre>, String?>((ref, id) {
  _authorize(ref, id);
  return ref.read(bookDiscoveryServiceProvider).genres(id);
});

final bookRequestTargetsProvider = FutureProvider.autoDispose
    .family<List<BookRequestTarget>, ({String foreignId, String instanceId})>(
        (ref, key) {
  _authorize(ref, key.instanceId);
  ref.watch(libraryRefreshTickProvider);
  ref.watch(libraryChangedEventsProvider);
  // While a card is visible its mapping is refreshed at least every minute.
  final timer = Timer(const Duration(seconds: 60), ref.invalidateSelf);
  ref.onDispose(timer.cancel);
  return ref
      .read(bookDiscoveryServiceProvider)
      .targets(key.foreignId, key.instanceId);
});

final discoveryBookStatusProvider = FutureProvider.autoDispose
    .family<BookRequestStatusDetail?, ({String foreignId, String instanceId})>(
        (ref, key) async {
  _authorize(ref, key.instanceId);
  final candidates = await ref.watch(bookRequestTargetsProvider(key).future);
  if (candidates.length != 1) return null;
  return RequestService(backendDio: ref.read(backendClientProvider))
      .checkBookStatusDetail(candidates.single.foreignId,
          instanceId: key.instanceId);
});

@immutable
class BookBrowseQuery {
  final String feed;
  final String? instanceId;
  final String? genre;
  const BookBrowseQuery({this.feed = 'popular', this.instanceId, this.genre});
  String get location => Uri(path: '/browse/books/$feed', queryParameters: {
        if (instanceId != null) 'instance_id': instanceId,
        if (genre != null) 'genre': genre!,
      }).toString();
  static BookBrowseQuery? tryParse(Uri uri) {
    final p = uri.pathSegments;
    final id = uri.queryParameters['instance_id'];
    final genre = uri.queryParameters['genre'];
    if (p.length != 3 ||
        p[0] != 'browse' ||
        p[1] != 'books' ||
        !{'popular', 'genre'}.contains(p[2]) ||
        (id != null && id.trim().isEmpty) ||
        (p[2] == 'genre' ? genre == null || genre.isEmpty : genre != null)) {
      return null;
    }
    return BookBrowseQuery(feed: p[2], instanceId: id, genre: genre);
  }

  @override
  bool operator ==(Object other) =>
      other is BookBrowseQuery &&
      feed == other.feed &&
      instanceId == other.instanceId &&
      genre == other.genre;
  @override
  int get hashCode => Object.hash(feed, instanceId, genre);
}

class BookFeedNotifier extends ChangeNotifier {
  final BookDiscoveryService service;
  final BookBrowseQuery query;
  List<DiscoveryBook> items = const [];
  bool loading = false;
  Object? error;
  int? nextPage = 1;
  String emptyMessage = '';
  bool _disposed = false;
  bool _lastLoadWasRefresh = false;
  BookFeedNotifier(this.service, this.query);
  Future<void> retry() => load(refresh: _lastLoadWasRefresh);
  Future<void> load({bool refresh = false}) async {
    if (loading || (!refresh && nextPage == null)) return;
    _lastLoadWasRefresh = refresh;
    loading = true;
    error = null;
    notifyListeners();
    try {
      final page = await service.feed(query.feed, query.instanceId,
          genre: query.genre, page: refresh ? 1 : nextPage!);
      if (_disposed) return;
      final seen = <String>{};
      items = [...(refresh ? <DiscoveryBook>[] : items), ...page.results]
          .where((b) => seen.add(b.foreignId))
          .toList();
      nextPage = page.nextPage;
      emptyMessage = page.emptyMessage;
    } catch (e) {
      if (_disposed) return;
      error = e;
      if (e is BookDiscoveryException && e.accessDenied) items = const [];
    }
    if (_disposed) return;
    loading = false;
    notifyListeners();
  }

  @override
  void dispose() {
    _disposed = true;
    super.dispose();
  }
}

final bookFeedProvider = ChangeNotifierProvider.autoDispose
    .family<BookFeedNotifier, BookBrowseQuery>((ref, query) {
  ref.watch(bookDiscoveryScopeProvider);
  final notifier =
      BookFeedNotifier(ref.read(bookDiscoveryServiceProvider), query);
  if (bookDiscoveryAllowed(ref, query.instanceId)) {
    unawaited(notifier.load());
  } else {
    notifier.error = const BookDiscoveryException(
        'Books are not available for this account.',
        accessDenied: true);
  }
  return notifier;
});
