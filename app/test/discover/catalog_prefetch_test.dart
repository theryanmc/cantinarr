import 'dart:async';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/widgets/cached_image.dart';
import 'package:cantinarr/core/widgets/horizontal_item_row.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/discover/data/book_discovery_service.dart';
import 'package:cantinarr/features/discover/data/music_discovery_service.dart';
import 'package:cantinarr/features/discover/data/music_models.dart';
import 'package:cantinarr/features/discover/logic/book_discovery_provider.dart';
import 'package:cantinarr/features/discover/logic/music_browse_query.dart';
import 'package:cantinarr/features/discover/logic/music_feed_provider.dart';
import 'package:cantinarr/features/discover/ui/book_discovery_row.dart';
import 'package:cantinarr/features/discover/ui/catalog_prefetch.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

class Books extends BookDiscoveryService {
  Books() : super(Dio());
  final calls = <int>[];
  Future<BookDiscoveryPage> Function(int)? fetch;
  int targetsRead = 0;
  @override
  Future<BookDiscoveryPage> feed(String feed, String? instanceId,
      {String? genre, int page = 1}) async {
    calls.add(page);
    return fetch == null ? bookPage(page) : fetch!(page);
  }

  @override
  Future<List<BookGenre>> genres(String? id) async =>
      const [BookGenre('fantasy', 'Fantasy')];
  @override
  Future<List<BookRequestTarget>> targets(String id, String instance) async {
    targetsRead++;
    return [];
  }
}

BookDiscoveryPage bookPage(int page, {int? nextPage}) => BookDiscoveryPage(
    List.generate(
        20,
        (i) => DiscoveryBook(
            foreignId: 'ol:OL${(page - 1) * 20 + i + 1}W',
            title: 'Book ${(page - 1) * 20 + i + 1}',
            coverId: i + 1)),
    nextPage ?? (page < 5 ? page + 1 : null),
    '');

class Music extends MusicDiscoveryService {
  Music() : super(Dio());
  final calls = <(String, int)>[];
  Future<MusicPage> Function(MusicBrowseQuery, int)? fetch;
  @override
  Future<MusicPage> feed(MusicBrowseQuery query, int page) async {
    calls.add((query.feed, page));
    return fetch == null ? musicPage(page) : fetch!(query, page);
  }

  @override
  Future<List<MusicGenre>> genres(String? id) async => const [];
}

MusicPage musicPage(int page) =>
    MusicPage(page: page, nextPage: page < 5 ? page + 1 : null, results: [
      MusicAlbum(
          foreignId: 'album-$page',
          title: 'Album $page',
          artist: 'An Artist',
          artwork: '/artwork/album-$page')
    ]);

AuthState auth(
        {String role = 'admin',
        bool child = false,
        bool capability = true,
        bool grant = false}) =>
    AuthState(
      connection: BackendConnection(
          serverUrl: 'http://localhost',
          accessToken: 'test-token',
          refreshToken: 'test-refresh',
          adminCatalogBrowsing: capability,
          instances: grant
              ? const [
                  ServiceInstance(
                      id: 'books',
                      name: 'Books',
                      serviceType: 'chaptarr',
                      isDefault: true),
                  ServiceInstance(
                      id: 'music',
                      name: 'Music',
                      serviceType: 'lidarr',
                      isDefault: true),
                ]
              : const []),
      user: UserProfile(
          id: 1,
          username: 'test',
          role: role,
          child: child,
          permissions: const ['media:discover']),
    );

class Auth extends AuthNotifier {
  final AuthState initial;
  Auth(this.initial);
  @override
  Future<AuthState> build() async => initial;
  void change(AuthState next) => state = AsyncData(next);
}

Future<void> flush() async {
  for (var i = 0; i < 8; i++) {
    await Future<void>.delayed(Duration.zero);
  }
}

void main() {
  test('books keep exactly one page ahead and join an in-flight scroll load',
      () async {
    final service = Books();
    final pending = Completer<BookDiscoveryPage>();
    service.fetch = (page) async => page == 2 ? pending.future : bookPage(page);
    final feed = BookFeedNotifier(service, const BookBrowseQuery());
    addTearDown(feed.dispose);
    await feed.load();
    await flush();
    expect(service.calls, [1]); // An unopened tab stops here.
    feed.enablePrefetch();
    await flush();
    expect(service.calls, [1, 2]);
    expect(feed.items.length, 20);
    final scrolling = feed.load();
    await feed.load(); // A second fast scroll cannot duplicate the request.
    expect(service.calls, [1, 2]);
    pending.complete(bookPage(2));
    await scrolling;
    await flush();
    expect(feed.items.length, 40);
    expect(feed.upcoming.first.foreignId, 'ol:OL41W');
    expect(service.calls, [1, 2, 3]);
    await flush();
    expect(service.calls, [1, 2, 3]); // No catalog crawl while idle.
  });

  test(
      'speculative failures stay quiet until needed, then retry the right page',
      () async {
    final service = Books()
      ..fetch = (page) async => page == 2
          ? throw const BookDiscoveryException('Try again')
          : bookPage(page);
    final feed = BookFeedNotifier(service, const BookBrowseQuery());
    addTearDown(feed.dispose);
    await feed.load();
    feed.enablePrefetch();
    await flush();
    expect(feed.error, isNull);
    expect(feed.items.length, 20);
    await feed.load();
    expect(feed.error, isNotNull);
    expect(feed.items.length, 20);
    service.fetch = (page) async => bookPage(page);
    await feed.retry();
    await flush();
    expect(feed.items.length, 40);
    expect(service.calls, [1, 2, 2, 3]);
  });

  test(
      'refresh discards old speculative completion, including late access errors',
      () async {
    final late = Completer<BookDiscoveryPage>();
    final service = Books()
      ..fetch = (page) async => page == 2 ? late.future : bookPage(page);
    final feed = BookFeedNotifier(service, const BookBrowseQuery());
    addTearDown(feed.dispose);
    await feed.load();
    feed.enablePrefetch();
    await flush();
    service.fetch = (page) async => bookPage(page + 2);
    await feed.load(refresh: true);
    await flush();
    late.completeError(
        const BookDiscoveryException('Revoked', accessDenied: true));
    await flush();
    expect(feed.error, isNull);
    expect(feed.items.first.foreignId, 'ol:OL41W');
    expect(feed.upcoming.first.foreignId, 'ol:OL101W');
  });

  test('music consumes a ready page without fetching it again', () async {
    final service = Music();
    final feed =
        MusicFeedNotifier(service, const MusicBrowseQuery(feed: 'popular'));
    addTearDown(feed.dispose);
    await flush();
    expect(service.calls, [('popular', 1)]);
    feed.enablePrefetch();
    await flush();
    expect(feed.state.items.single.foreignId, 'album-1');
    expect(feed.state.upcoming.single.foreignId, 'album-2');
    await feed.loadMore();
    await flush();
    expect(feed.state.items.map((a) => a.foreignId), ['album-1', 'album-2']);
    expect(service.calls, [('popular', 1), ('popular', 2), ('popular', 3)]);
  });

  test(
      'access errors during lookahead clear both visible and buffered metadata',
      () async {
    final books = Books()
      ..fetch = (page) async => page == 2
          ? throw const BookDiscoveryException('Revoked', accessDenied: true)
          : bookPage(page);
    final music = Music()
      ..fetch = (_, page) async => page == 2
          ? throw DioException(
              requestOptions: RequestOptions(),
              response:
                  Response(requestOptions: RequestOptions(), statusCode: 403))
          : musicPage(page);
    final b = BookFeedNotifier(books, const BookBrowseQuery());
    final m = MusicFeedNotifier(music, const MusicBrowseQuery(feed: 'popular'));
    addTearDown(b.dispose);
    addTearDown(m.dispose);
    await b.load();
    await flush();
    b.enablePrefetch();
    m.enablePrefetch();
    await flush();
    expect(b.items, isEmpty);
    expect(b.upcoming, isEmpty);
    expect(m.state.items, isEmpty);
    expect(m.state.upcoming, isEmpty);
    expect(b.error, isNotNull);
    expect(m.state.error, isNotNull);
  });

  testWidgets(
      'opening rows warm before tabs, with two image loads and no target lookups',
      (t) async {
    final books = Books();
    final music = Music();
    final images = <ImageSource>[];
    final pending = <Completer<void>>[];
    final container = ProviderContainer(overrides: [
      authProvider.overrideWith(() => Auth(auth(grant: true))),
      bookDiscoveryServiceProvider.overrideWithValue(books),
      musicDiscoveryServiceProvider.overrideWithValue(music),
      catalogArtworkLoaderProvider.overrideWithValue((source, _) {
        images.add(source);
        final c = Completer<void>();
        pending.add(c);
        return c.future;
      }),
    ]);
    addTearDown(container.dispose);
    await container.read(authProvider.future);
    await t.pumpWidget(UncontrolledProviderScope(
        container: container,
        child: const MaterialApp(home: CatalogWarmup(child: Text('Movies')))));
    await t.pumpAndSettle();
    expect(books.calls, [1]);
    expect(music.calls, [('popular', 1), ('new-releases', 1)]);
    expect(books.targetsRead, 0);
    expect(images.length, 2);
    expect(images.every((s) => s.headers == null), isTrue);
    pending.first.complete();
    await t.pump();
    expect(images.length, 3);
    // Revocation must drop queued artwork and all warmed feeds immediately.
    (container.read(authProvider.notifier) as Auth)
        .change(auth(role: 'user', child: true));
    await t.pumpAndSettle();
    expect(container.read(catalogOpeningArtworkProvider), isEmpty);
    for (final c in pending.where((c) => !c.isCompleted)) {
      c.complete();
    }
    await t.pumpAndSettle();
    expect(images.length, 3);
  });

  for (final account in [
    auth(role: 'user'),
    auth(role: 'user', child: true),
    auth(capability: false)
  ]) {
    test(
        'warmup respects grants and older-server capability: ${account.user?.role}/${account.user?.child}/${account.connection?.adminCatalogBrowsing}',
        () async {
      final books = Books();
      final music = Music();
      final container = ProviderContainer(overrides: [
        authProvider.overrideWith(() => Auth(account)),
        bookDiscoveryServiceProvider.overrideWithValue(books),
        musicDiscoveryServiceProvider.overrideWithValue(music),
      ]);
      addTearDown(container.dispose);
      await container.read(authProvider.future);
      final sub = container.listen(catalogOpeningArtworkProvider, (_, __) {});
      addTearDown(sub.close);
      await flush();
      expect(books.calls, isEmpty);
      expect(music.calls, isEmpty);
    });
  }

  testWidgets('a horizontal book row consumes its buffer before the end',
      (t) async {
    t.view.physicalSize = const Size(500, 800);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.resetPhysicalSize);
    addTearDown(t.view.resetDevicePixelRatio);
    final books = Books();
    final container = ProviderContainer(overrides: [
      authProvider.overrideWith(() => Auth(auth())),
      bookDiscoveryServiceProvider.overrideWithValue(books),
      catalogArtworkLoaderProvider.overrideWithValue((_, __) async {}),
    ]);
    addTearDown(container.dispose);
    await container.read(authProvider.future);
    await t.pumpWidget(UncontrolledProviderScope(
        container: container,
        child: const MaterialApp(home: Scaffold(body: PopularBooksRow()))));
    await t.pumpAndSettle();
    final feed = container.read(bookFeedProvider(const BookBrowseQuery()));
    expect(books.calls, [1, 2]);
    expect(feed.items.length, 20);
    final list = find.descendant(
        of: find.byType(HorizontalItemRow<DiscoveryBook>),
        matching: find.byType(ListView));
    await t.drag(list, const Offset(-1700, 0));
    await t.pumpAndSettle();
    expect(feed.items.length, 40);
    expect(books.calls, [1, 2, 3]);
  });
}
