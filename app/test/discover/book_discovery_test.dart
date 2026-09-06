import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';
import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/network/websocket_client.dart';
import 'package:cantinarr/core/providers/library_refresh_provider.dart';
import 'package:cantinarr/core/providers/realtime_provider.dart';
import 'package:cantinarr/core/widgets/cached_image.dart';
import 'package:cantinarr/core/widgets/search_bar.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/dashboard/ui/dashboard_books_tab.dart';
import 'package:cantinarr/features/dashboard/ui/library_authors_row.dart';
import 'package:cantinarr/features/dashboard/ui/library_series_row.dart';
import 'package:cantinarr/features/dashboard/ui/recently_added_books_row.dart';
import 'package:cantinarr/features/dashboard/ui/requester_book_detail_screen.dart';
import 'package:cantinarr/features/discover/data/book_discovery_service.dart';
import 'package:cantinarr/features/discover/logic/book_discovery_provider.dart';
import 'package:cantinarr/features/discover/ui/book_browse_screen.dart';
import 'package:cantinarr/features/discover/ui/book_discovery_row.dart';
import 'package:cantinarr/navigation/app_router.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';

void main() {
  test('validated work IDs, covers and encoded browse links', () {
    expect(DiscoveryBook.fromJson({...book(1), 'cover_id': 42}).coverUrl,
        'https://covers.openlibrary.org/b/id/42-M.jpg?default=false');
    expect(
        const DiscoveryBook(foreignId: 'ol:OL1W', title: 'Book', coverId: -1)
            .coverUrl,
        isNull);
    expect(() => DiscoveryBook.fromJson({...book(1), 'foreign_id': 'ol:OL1M'}),
        throwsFormatException);
    const q = BookBrowseQuery(
        feed: 'genre', instanceId: 'books & more', genre: 'biography-memoir');
    expect(BookBrowseQuery.tryParse(Uri.parse(q.location)), q);
    expect(BookBrowseQuery.tryParse(Uri.parse('/browse/books/popular')),
        const BookBrowseQuery());
    expect(
        BookBrowseQuery.tryParse(
            Uri.parse('/browse/books/popular?instance_id=')),
        isNull);
    expect(
        BookBrowseQuery.tryParse(
            Uri.parse('/browse/books/new-releases?instance_id=books')),
        isNull);
  });
  test(
      'pagination deduplicates work IDs only and retains data on refresh failure',
      () async {
    final a = Adapter();
    final feed = BookFeedNotifier(BookDiscoveryService(dio(a)),
        const BookBrowseQuery(instanceId: 'books'));
    addTearDown(feed.dispose);
    await feed.load();
    await feed.load();
    expect(feed.items.length, 39);
    expect(feed.items.take(2).map((b) => b.foreignId), ['ol:OL1W', 'ol:OL2W']);
    expect(
        feed.items.take(2).map((b) => b.title), ['Same title', 'Same title']);
    expect(feed.nextPage, isNull);
    a.feedStatus = 503;
    await feed.load(refresh: true);
    expect(feed.items.length, 39);
    expect(feed.error, isNotNull);
    a.feedStatus = 200;
    await feed.retry();
    expect(feed.items.length, 20);
    expect(a.pages.last, 1);
    expect(feed.error, isNull);
    a.feedStatus = 403;
    await feed.load(refresh: true);
    expect(feed.items, isEmpty);
  });
  test('cold metadata must match work identity', () async {
    final a = Adapter()..wrongDetail = true;
    await expectLater(BookDiscoveryService(dio(a)).book('ol:OL1W', 'books'),
        throwsFormatException);
  });
  testWidgets('row order, genre URL and separate format labels', (t) async {
    final h = await pump(t);
    final column = t
        .widget<SingleChildScrollView>(find
            .descendant(
                of: find.byType(DashboardBooksTab),
                matching: find.byType(SingleChildScrollView))
            .first)
        .child as Column;
    expect(column.children.map((w) => w.runtimeType), [
      PopularBooksRow,
      BookGenresRow,
      RecentlyAddedBooksRow,
      LibraryAuthorsRow,
      LibrarySeriesRow
    ]);
    expect(find.text('Popular on Open Library'), findsOneWidget);
    expect(find.text('eBook: Available'), findsWidgets);
    expect(find.text('Audiobook: Available'), findsNothing);
    await t.tap(find.widgetWithText(ActionChip, 'Fantasy'));
    await t.pumpAndSettle();
    expect(h.router.routerDelegate.currentConfiguration.last.matchedLocation,
        '/browse/books/genre');
    expect(
        t.widget<BookBrowseScreen>(find.byType(BookBrowseScreen)).query.genre,
        'fantasy');
    expect(find.byType(BookBrowseScreen), findsOneWidget);
  });
  testWidgets('independent feed errors and older-server library fallback',
      (t) async {
    final a = Adapter()..feedStatus = 503;
    final h = await pump(t, adapter: a);
    expect(find.text('Could not load books. Please retry.'), findsOneWidget);
    expect(find.widgetWithText(ActionChip, 'Fantasy'), findsOneWidget);
    a.feedStatus = 404;
    unawaited(h.container
        .read(bookFeedProvider(const BookBrowseQuery(instanceId: 'books')))
        .load(refresh: true));
    await t.pumpAndSettle();
    expect(find.text(bookDiscoveryUpdateNotice), findsOneWidget);
    expect(find.byType(RecentlyAddedBooksRow), findsOneWidget);
  });
  testWidgets(
      'cold link resolves canonical target and requests only missing audio',
      (t) async {
    final h = await pump(t, location: '/detail/book/ol:OL1W?instance_id=books');
    expect(find.byType(RequesterBookDetailScreen), findsOneWidget);
    expect(find.text('Same title'), findsOneWidget);
    expect(find.text('An Author'), findsOneWidget);
    expect(find.text('Available'), findsOneWidget);
    expect(find.text('Request'), findsOneWidget);
    final img = t.widget<CachedImage>(find.byType(CachedImage).first);
    expect(img.url, isNull);
    expect(img.headers, isNull);
    expect(img.icon, Icons.menu_book);
    await t.tap(find.byKey(const ValueKey('book-format-row:audiobook')));
    await t.pumpAndSettle();
    expect(h.adapter.posts.single['foreign_id'], 'gr:1');
    expect(h.adapter.posts.single['book_format'], 'audiobook');
    expect(h.adapter.posts.single['instance_id'], 'books');
    expect(h.adapter.statusIDs.every((id) => id == 'gr:1'), isTrue);
  });
  testWidgets('approval requests stay pending and prevent duplicate submission',
      (t) async {
    final a = Adapter()..submissionStatus = 'pending';
    await pump(t,
        adapter: a, location: '/detail/book/ol:OL1W?instance_id=books');
    await t.tap(find.byKey(const ValueKey('book-format-row:audiobook')));
    await t.pumpAndSettle();
    expect(find.text('Pending Approval'), findsWidgets);
    expect(find.text('Request'), findsNothing);
    expect(a.posts.length, 1);
  });
  testWidgets('author-import waiting survives status refresh', (t) async {
    final a = Adapter()..waiting = true;
    final h = await pump(t,
        adapter: a, location: '/detail/book/ol:OL1W?instance_id=books');
    await t.tap(find.byKey(const ValueKey('book-format-row:audiobook')));
    await t.pumpAndSettle();
    expect(find.text('Waiting for library'), findsWidgets);
    h.container.read(libraryRefreshTickProvider.notifier).state++;
    await t.pump();
    await t.pump(const Duration(milliseconds: 100));
    await t.pumpAndSettle();
    expect(find.text('Waiting for library'), findsWidgets);
    expect(find.text('Request'), findsNothing);
  });
  testWidgets(
      'library canonical alias does not oscillate back to discovery target',
      (t) async {
    final a = Adapter()..canonicalId = 'gr:canonical';
    await pump(t,
        adapter: a, location: '/detail/book/ol:OL1W?instance_id=books');
    await t.pump(const Duration(milliseconds: 100));
    await t.pumpAndSettle();
    expect(a.statusIDs, ['gr:1', 'gr:canonical']);
  });
  testWidgets('visible target mappings expire within sixty seconds', (t) async {
    final h = await pump(t);
    final before = h.adapter.targetReads;
    await t.pump(const Duration(seconds: 60));
    await t.pump(const Duration(milliseconds: 100));
    await t.pumpAndSettle();
    expect(h.adapter.targetReads, greaterThan(before));
  });
  testWidgets(
      'metadata displays while resolving and provider failure offers retry',
      (t) async {
    final a = Adapter();
    final h = await pump(t, adapter: a);
    final wait = Completer<void>();
    a.targetWait = wait.future;
    h.router.push('/detail/book/ol:OL40W?instance_id=books&source=openlibrary',
        extra: const DiscoveryBook(
            foreignId: 'ol:OL40W', title: 'Immediate title'));
    await t.pump();
    await t.pump();
    expect(find.text('Immediate title'), findsOneWidget);
    expect(find.text('Request'), findsNothing);
    a.targetStatus = 503;
    wait.complete();
    await t.pumpAndSettle();
    expect(
        find.text(
            'Could not check this book in your library catalog. Please retry.'),
        findsOneWidget);
    expect(find.text('Search books'), findsNothing);
    a.targetWait = null;
    a.targetStatus = 200;
    await t.tap(find.widgetWithText(TextButton, 'Retry'));
    await t.pumpAndSettle();
    expect(find.text('Request'), findsOneWidget);
  });
  testWidgets(
      'unresolved identity fills existing book search with title and author',
      (t) async {
    final a = Adapter()..candidates = [];
    final h = await pump(t,
        adapter: a, location: '/detail/book/ol:OL1W?instance_id=books');
    expect(find.text('Request'), findsNothing);
    await t.tap(find.text('Search books'));
    await t.pumpAndSettle();
    await t.pump(const Duration(milliseconds: 450));
    await t.pumpAndSettle();
    expect(
        h.router.routeInformationProvider.value.uri.path, '/dashboard/books');
    final field = t.widget<TextField>(find.descendant(
        of: find.byType(CantinarrSearchBar), matching: find.byType(TextField)));
    expect(field.controller!.text, 'Same title An Author');
    expect(a.searchTerms, contains('Same title An Author'));
  });
  testWidgets(
      'distinct candidates require a choice and use selected canonical ID',
      (t) async {
    final a = Adapter()
      ..candidates = [
        target('gr:1', 'First edition'),
        target('hc:2', 'Second edition')
      ];
    await pump(t,
        adapter: a, location: '/detail/book/ol:OL1W?instance_id=books');
    expect(find.text('Choose a matching library title'), findsOneWidget);
    expect(find.text('Request'), findsNothing);
    await t.tap(find.text('Second edition'));
    await t.pumpAndSettle();
    await t.tap(find.byKey(const ValueKey('book-format-row:audiobook')));
    await t.pumpAndSettle();
    expect(a.posts.single['foreign_id'], 'hc:2');
  });
  testWidgets('grid keeps filters and scroll when returning from detail',
      (t) async {
    final h = await pump(t,
        location: '/browse/books/genre?instance_id=books&genre=fantasy');
    final scroll = find.descendant(
        of: find.byType(BookBrowseScreen), matching: find.byType(Scrollable));
    await t.drag(scroll, const Offset(0, -1500));
    await t.pumpAndSettle();
    final offset = t.state<ScrollableState>(scroll).position.pixels;
    h.router.push('/detail/book/ol:OL1W?instance_id=books');
    await t.pumpAndSettle();
    h.router.pop();
    await t.pumpAndSettle();
    expect(t.state<ScrollableState>(scroll).position.pixels, offset);
    expect(h.router.routeInformationProvider.value.uri.queryParameters['genre'],
        'fantasy');
  });
  testWidgets(
      'library events and requests refresh statuses; grant revocation clears cards',
      (t) async {
    final events = StreamController<WsEvent>.broadcast();
    addTearDown(events.close);
    final h = await pump(t, events: events.stream);
    final before = h.adapter.targetReads;
    h.adapter.audioAvailable = true;
    events.add(const WsEvent(
        type: 'request_status_changed', data: {'instance_id': 'books'}));
    await t.pump();
    await t.pumpAndSettle();
    await t.pump(const Duration(milliseconds: 100));
    await t.pumpAndSettle();
    expect(h.adapter.targetReads, greaterThan(before));
    expect(find.text('Audiobook: Available'), findsWidgets);
    final beforeTick = h.adapter.targetReads;
    h.container.read(libraryRefreshTickProvider.notifier).state++;
    await t.pump();
    await t.pump(const Duration(milliseconds: 100));
    await t.pumpAndSettle();
    expect(h.adapter.targetReads, greaterThan(beforeTick));
    (h.container.read(authProvider.notifier) as TestAuth)
        .replace(auth(grant: false));
    await t.pumpAndSettle();
    expect(find.byType(BookDiscoveryCard), findsNothing);
    expect(find.text('Same title'), findsNothing);
  });
}

Map<String, dynamic> book(int id) => {
      'foreign_id': 'ol:OL${id}W',
      'title': id <= 2 ? 'Same title' : 'Book $id',
      'authors': ['An Author'],
      'year': 2001,
      'description': 'A book description.'
    };
Map<String, dynamic> target(String id, String title) =>
    {'foreign_id': id, 'title': title, 'author': 'An Author'};
AuthState auth({bool grant = true}) => AuthState(
    connection: BackendConnection(
        serverUrl: 'http://localhost',
        accessToken: 'access',
        refreshToken: 'refresh',
        services: AvailableServices(chaptarr: grant),
        instances: [
          if (grant)
            const ServiceInstance(
                id: 'books',
                name: 'Books',
                serviceType: 'chaptarr',
                isDefault: true)
        ]),
    user: const UserProfile(
        id: 1,
        username: 'test',
        role: 'user',
        permissions: ['media:discover', 'media:request']));

class TestAuth extends AuthNotifier {
  @override
  Future<AuthState> build() async => auth();
  void replace(AuthState next) {
    state = AsyncData(next);
  }
}

Dio dio(Adapter a) =>
    Dio(BaseOptions(baseUrl: 'http://localhost'))..httpClientAdapter = a;
Future<({GoRouter router, ProviderContainer container, Adapter adapter})> pump(
    WidgetTester t,
    {Adapter? adapter,
    String location = '/dashboard/books',
    Stream<WsEvent>? events}) async {
  t.view.physicalSize = const Size(390, 900);
  t.view.devicePixelRatio = 1;
  addTearDown(() {
    t.view.resetPhysicalSize();
    t.view.resetDevicePixelRatio();
  });
  final a = adapter ?? Adapter();
  final c = ProviderContainer(overrides: [
    authProvider.overrideWith(TestAuth.new),
    backendClientProvider.overrideWithValue(dio(a)),
    libraryChangedEventsProvider
        .overrideWith((_) => events ?? const Stream.empty())
  ]);
  addTearDown(c.dispose);
  await c.read(authProvider.future);
  await c.pump();
  final r = c.read(appRouterProvider)..go(location);
  await t.pumpWidget(UncontrolledProviderScope(
      container: c,
      child: OwnedTestContainer(
          container: c, child: MaterialApp.router(routerConfig: r))));
  await t.pumpAndSettle();
  return (router: r, container: c, adapter: a);
}

class Adapter implements HttpClientAdapter {
  int feedStatus = 200, targetStatus = 200, targetReads = 0;
  bool wrongDetail = false, audioAvailable = false;
  bool waiting = false;
  String submissionStatus = 'requested';
  String? canonicalId;
  Map<String, dynamic> get waits => {
        'audiobook': {
          'reason': 'author_import',
          'waiting_since': '2026-09-06T00:00:00Z'
        }
      };
  Future<void>? targetWait;
  List<Map<String, dynamic>> candidates = [target('gr:1', 'Catalog title')];
  final List<Map<String, dynamic>> posts = [];
  final List<String> statusIDs = [], searchTerms = [];
  final List<int> pages = [];
  @override
  Future<ResponseBody> fetch(RequestOptions o, Stream<Uint8List>? requestStream,
      Future<void>? cancelFuture) async {
    Object data = <String, dynamic>{};
    var code = 200;
    if (o.path.startsWith('/api/discover/books/')) {
      code = feedStatus;
      final p = o.queryParameters['page'] as int;
      pages.add(p);
      data = {
        'page': p,
        'total_results': 40,
        if (p == 1) 'next_page': 2,
        'results': [
          for (var i = 0; i < 20; i++)
            book(p == 2 && i == 0 ? 20 : (p - 1) * 20 + i + 1)
        ]
      };
    } else if (o.path == '/api/genres/book') {
      data = {
        'genres': [
          {'id': 'fantasy', 'name': 'Fantasy'},
          {'id': 'biography-memoir', 'name': 'Biography & Memoir'}
        ]
      };
    } else if (o.path.endsWith('/request-target')) {
      targetReads++;
      if (targetWait != null) await targetWait;
      code = targetStatus;
      data = {'candidates': candidates};
    } else if (o.path.startsWith('/api/media/book/')) {
      final id = int.parse(RegExp(r'OL(\d+)W').firstMatch(o.path)!.group(1)!);
      data = book(wrongDetail ? 2 : id);
    } else if (o.path == '/api/requests/book-status') {
      statusIDs.add(o.queryParameters['foreign_id'] as String);
      data = {
        'status': 'available',
        'status_known': true,
        if (canonicalId != null) 'canonical_foreign_id': canonicalId,
        if (waiting && posts.isNotEmpty) 'book_format_waits': waits,
        'book_formats': {
          'ebook': 'available',
          'audiobook': audioAvailable
              ? 'available'
              : posts.isNotEmpty
                  ? submissionStatus
                  : 'unavailable'
        }
      };
    } else if (o.path == '/api/requests' && o.method == 'POST') {
      posts.add(Map<String, dynamic>.from(o.data as Map));
      data = {
        'status': submissionStatus,
        'book_formats': {'audiobook': submissionStatus},
        if (waiting) 'book_format_waits': waits
      };
    } else if (o.path == '/api/requests/book-library') {
      data = {'titles': []};
    } else if (o.path == '/api/requests/book-recent') {
      data = {'books': []};
    } else if (o.path == '/api/requests/book-authors') {
      data = {'authors': []};
    } else if (o.path == '/api/requests/book-series') {
      data = {'series': []};
    } else if (o.path.contains('/api/v1/')) {
      if (o.path.endsWith('/lookup')) {
        searchTerms.add(o.queryParameters['term'] as String);
      }
      data = [];
    } else if (o.path == '/api/requests' || o.path.contains('/library')) {
      data = [];
    } else if (o.path.startsWith('/api/discover') ||
        o.path.startsWith('/api/trakt')) {
      data = {'results': [], 'page': 1, 'total_pages': 1};
    }
    return ResponseBody.fromString(jsonEncode(data), code, headers: {
      Headers.contentTypeHeader: [Headers.jsonContentType]
    });
  }

  @override
  void close({bool force = false}) {}
}

class OwnedTestContainer extends StatefulWidget {
  final ProviderContainer container;
  final Widget child;
  const OwnedTestContainer(
      {super.key, required this.container, required this.child});
  @override
  State<OwnedTestContainer> createState() => OwnedTestContainerState();
}

class OwnedTestContainerState extends State<OwnedTestContainer> {
  @override
  void dispose() {
    widget.container.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => widget.child;
}
