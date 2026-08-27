import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/network/websocket_client.dart';
import 'package:cantinarr/core/providers/instance_provider.dart';
import 'package:cantinarr/core/providers/library_refresh_provider.dart';
import 'package:cantinarr/core/providers/realtime_provider.dart';
import 'package:cantinarr/core/theme/app_theme.dart';
import 'package:cantinarr/core/widgets/search_bar.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/chaptarr/data/chaptarr_models.dart';
import 'package:cantinarr/features/dashboard/ui/requester_book_detail_screen.dart';
import 'package:cantinarr/features/discover/ui/book_search_results_view.dart';
import 'package:cantinarr/navigation/app_router.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';

void main() {
  testWidgets('a fully requested search row still opens rich book detail',
      (tester) async {
    _usePhoneSize(tester);
    final (:router, :container, :adapter) = await _pumpRouter(tester);
    router.go('/dashboard/books');
    await tester.pumpAndSettle();

    final searchField = find.byWidgetPredicate(
      (widget) =>
          widget is TextField &&
          widget.decoration?.hintText == 'Search books or authors…',
    );
    expect(searchField, findsOneWidget);
    await tester.enterText(searchField, 'meditations');
    await tester.pump(const Duration(milliseconds: 450));
    await tester.pumpAndSettle();

    expect(find.text('Meditations'), findsOneWidget);
    // Both formats are covered, so no redundant aggregate status/action sits
    // beside the row. Per-format truth is on the detail surface.
    expect(find.text('Requested'), findsNothing);
    expect(find.byIcon(Icons.chevron_right), findsWidgets);

    expect(adapter.statusRequests, 0);
    container.read(libraryRefreshTickProvider.notifier).state++;
    await tester.pumpAndSettle();
    expect(adapter.statusRequests, 0);

    await tester.tap(
      find.byKey(const ValueKey('book-result:book-1:book-1:lookup:0')),
    );
    await tester.pumpAndSettle();

    expect(find.byType(RequesterBookDetailScreen), findsOneWidget);
    expect(adapter.statusRequests, greaterThan(0));
    expect(find.text('Marcus Aurelius'), findsOneWidget);
    expect(find.text('2002 · 304 pages'), findsOneWidget);
    expect(find.text('A practical guide to Stoic philosophy.'), findsOneWidget);
    expect(find.text('Requested'), findsNWidgets(2));
  });

  testWidgets('request controls live on book detail, not search results',
      (tester) async {
    _usePhoneSize(tester);
    final (:router, container: _, :adapter) = await _pumpRouter(tester);
    router.go('/dashboard/books');
    await tester.pumpAndSettle();

    final searchField = find.byWidgetPredicate(
      (widget) =>
          widget is TextField &&
          widget.decoration?.hintText == 'Search books or authors…',
    );
    await tester.enterText(searchField, 'meditations');
    await tester.pump(const Duration(milliseconds: 450));
    await tester.pumpAndSettle();

    expect(find.text('Request'), findsNothing);

    final secondResult =
        find.byKey(const ValueKey('book-result:book-2:book-2:lookup:1'));
    await tester.tap(secondResult);
    await tester.pumpAndSettle();

    expect(find.byType(RequesterBookDetailScreen), findsOneWidget);
    expect(find.text('Letters from a Stoic'), findsOneWidget);
    // Both formats are still open, so each row carries its own request action.
    expect(find.text('eBook'), findsOneWidget);
    expect(find.text('Audiobook'), findsOneWidget);
    expect(find.text('Request'), findsNWidgets(2));

    final libraryRequestsBefore = adapter.libraryRequests;
    await tester.tap(find.byKey(const ValueKey('book-format-row:ebook')));
    await tester.pumpAndSettle();
    expect(adapter.libraryRequests, greaterThan(libraryRequestsBefore));
  });

  testWidgets(
      'fuzzy ownership keeps lookup metadata but uses the canonical library id',
      (tester) async {
    _usePhoneSize(tester);
    final (:router, container: _, :adapter) =
        await _pumpRouter(tester, mismatchedIdentity: true);
    router.go('/dashboard/books');
    await tester.pumpAndSettle();

    final searchField = find.byWidgetPredicate(
      (widget) =>
          widget is TextField &&
          widget.decoration?.hintText == 'Search books or authors…',
    );
    await tester.enterText(searchField, 'flock');
    await tester.pump(const Duration(milliseconds: 450));
    await tester.pumpAndSettle();

    expect(adapter.statusForeignIds, isEmpty);
    expect(
      find.byKey(
        const ValueKey('book-result:lookup-flock:library-flock:lookup:0'),
      ),
      findsOneWidget,
    );
    // The normal-row test above proves the tile gesture. Continue this case
    // through the exact route/extra the mismatched row owns so the remainder
    // can assert detail identity and mutation payload end to end.
    router.go(
      '/detail/book/library-flock?title=Flock&instance_id=books',
      extra: ChaptarrBook.fromJson({
        'title': 'Flock',
        'foreignBookId': 'lookup-flock',
        'author': {'authorName': 'Kate Stewart'},
      }),
    );
    await tester.pumpAndSettle();

    expect(adapter.statusForeignIds, isNotEmpty);
    expect(adapter.statusForeignIds, everyElement('library-flock'));
    expect(router.routeInformationProvider.value.uri.path,
        '/detail/book/library-flock');
    expect(
      router.routeInformationProvider.value.uri.queryParameters['instance_id'],
      'books',
    );
    final screen = tester.widget<RequesterBookDetailScreen>(
      find.byType(RequesterBookDetailScreen),
    );
    expect(screen.foreignId, 'library-flock');
    expect(screen.initialBook?.foreignBookId, 'lookup-flock');

    final ebookRow = find.byKey(const ValueKey('book-format-row:ebook'));
    await tester.scrollUntilVisible(
      ebookRow,
      250,
      scrollable: find.descendant(
        of: find.byType(RequesterBookDetailScreen),
        matching: find.byType(Scrollable),
      ),
    );
    await tester.tap(ebookRow);
    await tester.pumpAndSettle();

    expect(adapter.requestBodies, hasLength(1));
    expect(adapter.requestBodies.single['foreign_id'], 'library-flock');
    expect(adapter.requestBodies.single['instance_id'], 'books');
    expect(adapter.requestBodies.single['book_format'], 'ebook');
  });

  testWidgets(
      'an unresolved fuzzy match keeps its canonical id and blocks requests',
      (tester) async {
    _usePhoneSize(tester);
    final (:router, container: _, :adapter) =
        await _pumpRouter(tester, unresolvedIdentity: true);
    router.go('/dashboard/books');
    await tester.pumpAndSettle();

    final searchField = find.byWidgetPredicate(
      (widget) =>
          widget is TextField &&
          widget.decoration?.hintText == 'Search books or authors…',
    );
    await tester.enterText(searchField, 'flock');
    await tester.pump(const Duration(milliseconds: 450));
    await tester.pumpAndSettle();

    final row = find.byKey(
      const ValueKey('book-result:lookup-flock:library-flock:lookup:0'),
    );
    expect(row, findsOneWidget);
    expect(adapter.statusForeignIds, isEmpty);
    expect(
      find.descendant(
        of: row,
        matching: find.text('Ask an admin to check this book’s format'),
      ),
      findsNothing,
    );
    expect(find.text('Request'), findsNothing);

    router.go(
      '/detail/book/library-flock?title=Flock&instance_id=books',
      extra: ChaptarrBook.fromJson({
        'title': 'Flock',
        'foreignBookId': 'lookup-flock',
        'author': {'authorName': 'Kate Stewart'},
      }),
    );
    await tester.pumpAndSettle();

    expect(adapter.statusForeignIds, isNotEmpty);
    expect(adapter.statusForeignIds, everyElement('library-flock'));
    expect(find.byType(RequesterBookDetailScreen), findsOneWidget);
    expect(find.text('Format needs attention'), findsNWidgets(2));
    expect(
      find.text('Ask an admin to check this book’s format'),
      findsOneWidget,
    );
    expect(find.text('Request'), findsNothing);
    expect(adapter.requestBodies, isEmpty);
  });

  testWidgets('a mixed available and requested ownership chip stays requested',
      (tester) async {
    _usePhoneSize(tester);
    final (:router, container: _, adapter: _) =
        await _pumpRouter(tester, mixedOwnership: true);
    router.go('/dashboard/books');
    await tester.pumpAndSettle();

    final searchField = find.byWidgetPredicate(
      (widget) =>
          widget is TextField &&
          widget.decoration?.hintText == 'Search books or authors…',
    );
    await tester.enterText(searchField, 'meditations');
    await tester.pump(const Duration(milliseconds: 450));
    await tester.pumpAndSettle();

    final chip = tester.widget<Text>(
      find.text('eBook available · Audiobook requested'),
    );
    expect(chip.style?.color, AppTheme.requested);
  });

  testWidgets(
      'two lookup rows cannot silently bind to one canonical library record',
      (tester) async {
    _usePhoneSize(tester);
    final (:router, container: _, :adapter) =
        await _pumpRouter(tester, ambiguousLookup: true);
    router.go('/dashboard/books');
    await tester.pumpAndSettle();

    final searchField = find.byWidgetPredicate(
      (widget) =>
          widget is TextField &&
          widget.decoration?.hintText == 'Search books or authors…',
    );
    await tester.enterText(searchField, 'flock');
    await tester.pump(const Duration(milliseconds: 450));
    await tester.pumpAndSettle();

    expect(
      find.text('May be the same as a book listed above'),
      findsNWidgets(2),
    );
    final firstAmbiguous = find.byKey(
      const ValueKey('book-result:lookup-flock:lookup-flock:lookup:0'),
    );
    expect(firstAmbiguous, findsOneWidget);
    expect(
      find.byKey(
          const ValueKey('book-result:lookup-flock:lookup-flock:lookup:1')),
      findsOneWidget,
    );
    // The record the guidance points at is on screen, above both lookup rows.
    expect(
      find.byKey(
          const ValueKey('book-result:library-flock:library-flock:library:0')),
      findsOneWidget,
    );
    expect(adapter.statusForeignIds, isEmpty);

    // An unbindable row is still a real metadata record: it opens, addressed by
    // its own lookup id rather than a guessed library one.
    await tester.tap(firstAmbiguous);
    await tester.pumpAndSettle();

    final screen = tester.widget<RequesterBookDetailScreen>(
      find.byType(RequesterBookDetailScreen),
    );
    expect(screen.foreignId, 'lookup-flock');
    expect(adapter.statusForeignIds, everyElement('lookup-flock'));

    // The page that could not bind to its own library record points at the
    // record it may duplicate — in requester words, with the record's real
    // state — before any Request can be tapped.
    expect(
      find.text('Your library may already have this book'),
      findsOneWidget,
    );
    final lookalike =
        find.byKey(const ValueKey('book-lookalike:library-flock'));
    expect(lookalike, findsOneWidget);
    expect(
      find.descendant(
          of: lookalike, matching: find.text('Audiobook requested')),
      findsOneWidget,
    );

    // Tapping it lands on the record whose request state is real.
    await tester.tap(lookalike);
    await tester.pumpAndSettle();
    final opened = tester.widget<RequesterBookDetailScreen>(
      find.byType(RequesterBookDetailScreen).last,
    );
    expect(opened.foreignId, 'library-flock');
    // A page bound to its own record needs no pointer.
    expect(
      find.text('Your library may already have this book'),
      findsNothing,
    );
  });

  testWidgets('an exact library id outranks a same-title sibling row',
      (tester) async {
    _usePhoneSize(tester);
    final (:router, container: _, :adapter) =
        await _pumpRouter(tester, aliasSibling: true);
    router.go('/dashboard/books');
    await tester.pumpAndSettle();

    final searchField = find.byWidgetPredicate(
      (widget) =>
          widget is TextField &&
          widget.decoration?.hintText == 'Search books or authors…',
    );
    await tester.enterText(searchField, 'flock');
    await tester.pump(const Duration(milliseconds: 450));
    await tester.pumpAndSettle();

    // The row carrying the library's own id binds to it and reports its state;
    // the sibling's resemblance no longer cancels that identity.
    expect(
      find.byKey(
          const ValueKey('book-result:library-flock:library-flock:lookup:0')),
      findsOneWidget,
    );
    expect(find.text('Audiobook requested'), findsOneWidget);
    // With the record spoken for, the sibling is simply a book they don't have.
    expect(
      find.byKey(
          const ValueKey('book-result:lookup-flock:lookup-flock:lookup:1')),
      findsOneWidget,
    );
    expect(find.text('May be the same as a book listed above'), findsNothing);
    // The bound record is not repeated as a library row of its own.
    expect(
      find.byKey(
          const ValueKey('book-result:library-flock:library-flock:library:0')),
      findsNothing,
    );
    expect(adapter.statusForeignIds, isEmpty);
  });

  testWidgets('same-title library records are surfaced separately',
      (tester) async {
    _usePhoneSize(tester);
    final (:router, container: _, :adapter) =
        await _pumpRouter(tester, duplicateLibraryRecords: true);
    router.go('/dashboard/books');
    await tester.pumpAndSettle();

    final searchField = find.byWidgetPredicate(
      (widget) =>
          widget is TextField &&
          widget.decoration?.hintText == 'Search books or authors…',
    );
    await tester.enterText(searchField, 'flock');
    await tester.pump(const Duration(milliseconds: 450));
    await tester.pumpAndSettle();

    expect(
      find.text('May be the same as a book listed above'),
      findsOneWidget,
    );
    expect(
      find.byKey(const ValueKey('book-result:library-a:library-a:library:0')),
      findsOneWidget,
    );
    expect(
      find.byKey(const ValueKey('book-result:library-b:library-b:library:1')),
      findsOneWidget,
    );
    expect(adapter.statusForeignIds, isEmpty);
  });

  testWidgets('a lookup row without a canonical id explains why it is blocked',
      (tester) async {
    _usePhoneSize(tester);
    final (:router, container: _, :adapter) =
        await _pumpRouter(tester, blankIdentity: true);
    router.go('/dashboard/books');
    await tester.pumpAndSettle();

    final searchField = find.byWidgetPredicate(
      (widget) =>
          widget is TextField &&
          widget.decoration?.hintText == 'Search books or authors…',
    );
    await tester.enterText(searchField, 'flock');
    await tester.pump(const Duration(milliseconds: 450));
    await tester.pumpAndSettle();

    final row = find.byKey(const ValueKey('book-result:::lookup:0'));
    expect(row, findsOneWidget);
    expect(
      find.descendant(
        of: row,
        matching: find.text('Ask an admin to check this book’s library record'),
      ),
      findsOneWidget,
    );
    expect(tester.widget<ListTile>(row).onTap, isNull);
    expect(adapter.statusForeignIds, isEmpty);
    expect(adapter.requestBodies, isEmpty);
  });

  testWidgets('book status and guidance fit a narrow phone at 200 percent text',
      (tester) async {
    tester.view.physicalSize = const Size(320, 700);
    tester.view.devicePixelRatio = 1;
    tester.platformDispatcher.textScaleFactorTestValue = 2;
    addTearDown(() {
      tester.view.resetPhysicalSize();
      tester.view.resetDevicePixelRatio();
      tester.platformDispatcher.clearTextScaleFactorTestValue();
    });
    final (:router, container: _, adapter: _) =
        await _pumpRouter(tester, unresolvedIdentity: true);
    router.go('/dashboard/books');
    await tester.pumpAndSettle();
    expect(tester.takeException(), isNull);

    final searchField = find.byWidgetPredicate(
      (widget) =>
          widget is TextField &&
          widget.decoration?.hintText == 'Search books or authors…',
    );
    await tester.enterText(searchField, 'flock');
    await tester.pump(const Duration(milliseconds: 450));
    await tester.pumpAndSettle();
    expect(tester.takeException(), isNull);
    expect(find.text('Ask an admin to check this book’s format'), findsNothing);

    router.go('/detail/book/library-flock?title=Flock&instance_id=books');
    await tester.pumpAndSettle();
    expect(tester.takeException(), isNull);
    expect(find.text('Format needs attention'), findsNWidgets(2));
  });

  testWidgets(
      'the Books tab browse rows still refresh on resume, library-changed '
      'events and an instance switch', (tester) async {
    _usePhoneSize(tester);
    final events = StreamController<WsEvent>.broadcast();
    addTearDown(events.close);

    final (:router, :container, :adapter) = await _pumpRouter(
      tester,
      events: events.stream,
      authState: _twoInstanceBooksState,
    );
    router.go('/dashboard/books');
    await tester.pumpAndSettle();

    // Baseline after the idle tab's initial mount, before any trigger fires.
    var libraryBefore = adapter.libraryRequests;
    var recentBefore = adapter.recentRequests;
    var authorBefore = adapter.authorRequests;
    var seriesBefore = adapter.seriesRequests;

    // Trigger 1: app resume -> didChangeAppLifecycleState -> _refreshBookTruth().
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
    await tester.pumpAndSettle();

    expect(
      adapter.libraryRequests,
      greaterThan(libraryBefore),
      reason: 'TAB-02 (app resume): owned-books truth re-pulls on app resume',
    );
    expect(
      adapter.recentRequests,
      greaterThan(recentBefore),
      reason: 'TAB-02 (app resume): recently-added row re-pulls on resume',
    );
    expect(
      adapter.authorRequests,
      greaterThan(authorBefore),
      reason: 'TAB-02 (app resume): authors row re-pulls on resume',
    );
    expect(
      adapter.seriesRequests,
      greaterThan(seriesBefore),
      reason: 'TAB-02 (app resume): series row re-pulls on resume',
    );

    libraryBefore = adapter.libraryRequests;
    recentBefore = adapter.recentRequests;
    authorBefore = adapter.authorRequests;
    seriesBefore = adapter.seriesRequests;

    // Trigger 2: a library-changed websocket event ->
    // libraryChangedEventsProvider listener -> _refreshBookTruth().
    events.add(const WsEvent(type: 'request_status_changed', data: {}));
    await tester.pumpAndSettle();

    expect(
      adapter.libraryRequests,
      greaterThan(libraryBefore),
      reason: 'TAB-02 (library-changed event): owned-books truth re-pulls',
    );
    expect(
      adapter.recentRequests,
      greaterThan(recentBefore),
      reason: 'TAB-02 (library-changed event): recently-added row re-pulls',
    );
    expect(
      adapter.authorRequests,
      greaterThan(authorBefore),
      reason: 'TAB-02 (library-changed event): authors row re-pulls',
    );
    expect(
      adapter.seriesRequests,
      greaterThan(seriesBefore),
      reason: 'TAB-02 (library-changed event): series row re-pulls',
    );

    libraryBefore = adapter.libraryRequests;
    recentBefore = adapter.recentRequests;
    authorBefore = adapter.authorRequests;
    seriesBefore = adapter.seriesRequests;

    // Trigger 3: switching the active Chaptarr instance. The browse-row
    // providers (ownedBooksProvider/recentBooksProvider/bookAuthorsProvider/
    // bookSeriesProvider) each `ref.watch(instanceProvider)` directly, so a
    // state change re-runs all four against the new instance even though
    // DashboardBooksTab's own instance-switch listener only explicitly
    // invalidates ownedBooksProvider.
    container
        .read(instanceProvider.notifier)
        .setActiveChaptarrInstance('books-2');
    await tester.pumpAndSettle();

    expect(
      adapter.libraryRequests,
      greaterThan(libraryBefore),
      reason: 'TAB-02 (instance switch): owned-books truth re-pulls against '
          'the new instance',
    );
    expect(
      adapter.recentRequests,
      greaterThan(recentBefore),
      reason: 'TAB-02 (instance switch): recently-added row re-pulls '
          'against the new instance',
    );
    expect(
      adapter.authorRequests,
      greaterThan(authorBefore),
      reason: 'TAB-02 (instance switch): authors row re-pulls against the '
          'new instance',
    );
    expect(
      adapter.seriesRequests,
      greaterThan(seriesBefore),
      reason: 'TAB-02 (instance switch): series row re-pulls against the '
          'new instance',
    );
  });

  testWidgets(
      'a book typed in the top bar opens the requester book detail carrying '
      'the term that found it', (tester) async {
    _usePhoneSize(tester);
    final (:router, container: _, adapter: _) =
        await _pumpRouter(tester, mixedOwnership: true);
    router.go('/dashboard/books');
    await tester.pumpAndSettle();

    // The in-page field still exists at this point in the phase, so
    // find.byType(TextField).first would be ambiguous — locate the shell
    // toolbar specifically via its CantinarrSearchBar ancestor.
    final toolbar = find.descendant(
      of: find.byType(CantinarrSearchBar),
      matching: find.byType(TextField),
    );
    expect(toolbar, findsOneWidget);
    await tester.enterText(toolbar, 'meditations');
    await tester.pump(const Duration(milliseconds: 500));
    await tester.pumpAndSettle();

    expect(
      find.descendant(
        of: find.byType(BookSearchResultsView),
        matching: find.text('Meditations'),
      ),
      findsOneWidget,
    );
    expect(
      find.text('eBook available · Audiobook requested'),
      findsOneWidget,
    );

    await tester.tap(
      find.byKey(const ValueKey('book-result:book-1:book-1:lookup:0')),
    );
    await tester.pumpAndSettle();

    expect(find.byType(RequesterBookDetailScreen), findsOneWidget);
    // context.push (not router.go) doesn't update
    // router.routeInformationProvider in this harness, so read the pushed
    // route's own resolved q= param off the screen it built instead — the
    // same signal BOOK-04 promises the request carries.
    final screen = tester.widget<RequesterBookDetailScreen>(
      find.byType(RequesterBookDetailScreen),
    );
    expect(screen.searchTerm, 'meditations');
  });

  const noInstanceMessage = 'No Chaptarr instance is available.';
  const forbiddenMessage =
      'You do not have access to search this book library.';
  const requestFailedMessage =
      'Books could not be searched. Check the connection and try again.';
  const noBooksMessage = 'No books found. Try a different search.';

  Finder overlayText(String message) => find.descendant(
        of: find.byType(BookSearchResultsView),
        matching: find.text(message),
      );

  testWidgets(
      'a book search with no Chaptarr instance says so instead of showing '
      'an empty list', (tester) async {
    _usePhoneSize(tester);
    final (:router, container: _, adapter: _) = await _pumpRouter(
      tester,
      authState: _noInstanceBooksState,
    );
    router.go('/dashboard/books');
    await tester.pumpAndSettle();

    final toolbar = find.descendant(
      of: find.byType(CantinarrSearchBar),
      matching: find.byType(TextField),
    );
    await tester.enterText(toolbar, 'meditations');
    await tester.pump(const Duration(milliseconds: 500));
    await tester.pumpAndSettle();

    expect(overlayText(noInstanceMessage), findsOneWidget);
    expect(overlayText(forbiddenMessage), findsNothing);
    expect(overlayText(requestFailedMessage), findsNothing);
    expect(overlayText(noBooksMessage), findsNothing);
  });

  testWidgets(
      'a book search rejected for access says the user has no access to '
      'that book library', (tester) async {
    _usePhoneSize(tester);
    final (:router, container: _, adapter: _) =
        await _pumpRouter(tester, forbidden: true);
    router.go('/dashboard/books');
    await tester.pumpAndSettle();

    final toolbar = find.descendant(
      of: find.byType(CantinarrSearchBar),
      matching: find.byType(TextField),
    );
    await tester.enterText(toolbar, 'meditations');
    await tester.pump(const Duration(milliseconds: 500));
    await tester.pumpAndSettle();

    expect(overlayText(forbiddenMessage), findsOneWidget);
    expect(overlayText(noInstanceMessage), findsNothing);
    expect(overlayText(requestFailedMessage), findsNothing);
    expect(overlayText(noBooksMessage), findsNothing);
  });

  testWidgets('a book search that fails for any other reason invites a retry',
      (tester) async {
    _usePhoneSize(tester);
    final (:router, container: _, adapter: _) =
        await _pumpRouter(tester, serverError: true);
    router.go('/dashboard/books');
    await tester.pumpAndSettle();

    final toolbar = find.descendant(
      of: find.byType(CantinarrSearchBar),
      matching: find.byType(TextField),
    );
    await tester.enterText(toolbar, 'meditations');
    await tester.pump(const Duration(milliseconds: 500));
    await tester.pumpAndSettle();

    expect(overlayText(requestFailedMessage), findsOneWidget);
    expect(overlayText(noInstanceMessage), findsNothing);
    expect(overlayText(forbiddenMessage), findsNothing);
    expect(overlayText(noBooksMessage), findsNothing);
  });

  testWidgets(
      'a book search that ran and matched nothing says no books were found',
      (tester) async {
    _usePhoneSize(tester);
    final (:router, container: _, adapter: _) =
        await _pumpRouter(tester, emptyLookup: true);
    router.go('/dashboard/books');
    await tester.pumpAndSettle();

    final toolbar = find.descendant(
      of: find.byType(CantinarrSearchBar),
      matching: find.byType(TextField),
    );
    await tester.enterText(toolbar, 'meditations');
    await tester.pump(const Duration(milliseconds: 500));
    await tester.pumpAndSettle();

    expect(overlayText(noBooksMessage), findsOneWidget);
    expect(overlayText(noInstanceMessage), findsNothing);
    expect(overlayText(forbiddenMessage), findsNothing);
    expect(overlayText(requestFailedMessage), findsNothing);
  });
}

void _usePhoneSize(WidgetTester tester) {
  tester.view.physicalSize = const Size(390, 844);
  tester.view.devicePixelRatio = 1;
  addTearDown(() {
    tester.view.resetPhysicalSize();
    tester.view.resetDevicePixelRatio();
  });
}

const _booksState = AuthState(
  connection: BackendConnection(
    serverUrl: 'http://localhost',
    accessToken: 'access',
    refreshToken: 'refresh',
    services: AvailableServices(chaptarr: true),
    instances: [
      ServiceInstance(
        id: 'books',
        serviceType: 'chaptarr',
        name: 'Books',
        isDefault: true,
      ),
    ],
  ),
  user: UserProfile(id: 1, username: 'tester', role: 'user'),
);

/// TAB-02 instance-switch fixture: two Chaptarr instances so
/// `setActiveChaptarrInstance` has a second instance to switch to.
const _twoInstanceBooksState = AuthState(
  connection: BackendConnection(
    serverUrl: 'http://localhost',
    accessToken: 'access',
    refreshToken: 'refresh',
    services: AvailableServices(chaptarr: true),
    instances: [
      ServiceInstance(
        id: 'books',
        serviceType: 'chaptarr',
        name: 'Books',
        isDefault: true,
      ),
      ServiceInstance(
        id: 'books-2',
        serviceType: 'chaptarr',
        name: 'Books 2',
      ),
    ],
  ),
  user: UserProfile(id: 1, username: 'tester', role: 'user'),
);

/// FAIL-01 fixture: the books grant is present (so the route stays
/// reachable — app_router.dart:155-165 keys off the grant, not the instance
/// list) but zero Chaptarr instances are configured.
const _noInstanceBooksState = AuthState(
  connection: BackendConnection(
    serverUrl: 'http://localhost',
    accessToken: 'access',
    refreshToken: 'refresh',
    services: AvailableServices(chaptarr: true),
    instances: [],
  ),
  user: UserProfile(id: 1, username: 'tester', role: 'user'),
);

Future<
    ({
      ProviderContainer container,
      GoRouter router,
      _BooksSearchAdapter adapter,
    })> _pumpRouter(
  WidgetTester tester, {
  bool mismatchedIdentity = false,
  bool unresolvedIdentity = false,
  bool mixedOwnership = false,
  bool ambiguousLookup = false,
  bool aliasSibling = false,
  bool duplicateLibraryRecords = false,
  bool blankIdentity = false,
  bool forbidden = false,
  bool serverError = false,
  bool emptyLookup = false,
  Stream<WsEvent>? events,
  AuthState? authState,
}) async {
  final dio = Dio(BaseOptions(baseUrl: 'http://localhost'));
  final adapter = _BooksSearchAdapter(
    mismatchedIdentity: mismatchedIdentity,
    unresolvedIdentity: unresolvedIdentity,
    mixedOwnership: mixedOwnership,
    ambiguousLookup: ambiguousLookup,
    aliasSibling: aliasSibling,
    duplicateLibraryRecords: duplicateLibraryRecords,
    blankIdentity: blankIdentity,
    forbidden: forbidden,
    serverError: serverError,
    emptyLookup: emptyLookup,
  );
  dio.httpClientAdapter = adapter;
  final container = ProviderContainer(
    overrides: [
      authProvider.overrideWith(
        () => _FakeAuthNotifier(authState ?? _booksState),
      ),
      backendClientProvider.overrideWithValue(dio),
      realtimeEventsProvider
          .overrideWithValue(events ?? const Stream<WsEvent>.empty()),
    ],
  );
  addTearDown(container.dispose);

  await container.read(authProvider.future);
  await container.pump();
  final router = container.read(appRouterProvider);
  await tester.pumpWidget(
    UncontrolledProviderScope(
      container: container,
      child: MaterialApp.router(routerConfig: router),
    ),
  );
  await tester.pumpAndSettle();
  return (container: container, router: router, adapter: adapter);
}

class _FakeAuthNotifier extends AuthNotifier {
  _FakeAuthNotifier(this._initial);

  final AuthState _initial;

  @override
  Future<AuthState> build() async => _initial;
}

class _BooksSearchAdapter implements HttpClientAdapter {
  _BooksSearchAdapter({
    this.mismatchedIdentity = false,
    this.unresolvedIdentity = false,
    this.mixedOwnership = false,
    this.ambiguousLookup = false,
    this.aliasSibling = false,
    this.duplicateLibraryRecords = false,
    this.blankIdentity = false,
    this.forbidden = false,
    this.serverError = false,
    this.emptyLookup = false,
  });

  final bool mismatchedIdentity;
  final bool unresolvedIdentity;
  final bool mixedOwnership;
  final bool ambiguousLookup;

  /// FAIL-02 (access): book/lookup answers 403.
  final bool forbidden;

  /// FAIL-02 (retry): book/lookup answers 500.
  final bool serverError;

  /// FAIL-03: book/lookup succeeds with an empty list.
  final bool emptyLookup;

  /// Two lookup rows for one title where the first carries the library's own
  /// foreignBookId and the second is a same-title provider sibling.
  final bool aliasSibling;
  final bool duplicateLibraryRecords;
  final bool blankIdentity;
  int statusRequests = 0;
  int libraryRequests = 0;
  int recentRequests = 0;
  int authorRequests = 0;
  int seriesRequests = 0;
  bool ebookSubmitted = false;
  final statusForeignIds = <String>[];
  final requestBodies = <Map<String, dynamic>>[];

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    Object body;
    if (options.path == '/api/requests' && options.method == 'POST') {
      final bytes = <int>[];
      if (requestStream != null) {
        await for (final chunk in requestStream) {
          bytes.addAll(chunk);
        }
      }
      final request = jsonDecode(utf8.decode(bytes)) as Map<String, dynamic>;
      requestBodies.add(request);
      ebookSubmitted = true;
      body = {
        'status': 'requested',
        'book_formats': {'ebook': 'requested'},
      };
    } else if (options.path == '/api/requests/book-library') {
      libraryRequests++;
      body = duplicateLibraryRecords
          ? {
              'titles': [
                {
                  'title': 'Flock',
                  'author': 'Kate Stewart',
                  'foreign_book_id': 'library-a',
                  'ebook': {'monitored': true, 'downloaded': true},
                  'audiobook': {'monitored': false, 'downloaded': false},
                },
                {
                  'title': 'Flock',
                  'author': 'Kate Stewart',
                  'foreign_book_id': 'library-b',
                  'ebook': {'monitored': false, 'downloaded': false},
                  'audiobook': {'monitored': true, 'downloaded': false},
                },
              ],
            }
          : unresolvedIdentity
              ? {
                  'titles': [
                    {
                      'title': 'Flock',
                      'author': 'Kate Stewart',
                      'foreign_book_id': 'library-flock',
                      'status_known': false,
                      'ebook': {'monitored': false, 'downloaded': false},
                      'audiobook': {'monitored': false, 'downloaded': false},
                    },
                  ],
                }
              : (mismatchedIdentity || ambiguousLookup || aliasSibling)
                  ? {
                      'titles': [
                        {
                          'title': 'Flock',
                          'author': 'Kate Stewart',
                          'foreign_book_id': 'library-flock',
                          'ebook': {
                            'monitored': ebookSubmitted,
                            'downloaded': false,
                          },
                          'audiobook': {'monitored': true, 'downloaded': false},
                        },
                      ],
                    }
                  : mixedOwnership
                      ? {
                          'titles': [
                            {
                              'title': 'Meditations',
                              'author': 'Marcus Aurelius',
                              'foreign_book_id': 'book-1',
                              'ebook': {'monitored': true, 'downloaded': true},
                              'audiobook': {
                                'monitored': true,
                                'downloaded': false
                              },
                            },
                          ],
                        }
                      : {'titles': <Object>[]};
    } else if (options.path == '/api/requests/book-status') {
      statusRequests++;
      final foreignId = options.queryParameters['foreign_id'].toString();
      statusForeignIds.add(foreignId);
      body = unresolvedIdentity && foreignId == 'library-flock'
          ? {
              'status': 'unavailable',
              'book_formats': {
                'ebook': 'unavailable',
                'audiobook': 'unavailable',
              },
            }
          : (mismatchedIdentity || ambiguousLookup) &&
                  foreignId == 'library-flock'
              ? {
                  'status': 'requested',
                  'book_formats': {
                    if (ebookSubmitted) 'ebook': 'requested',
                    'audiobook': 'requested',
                  },
                }
              : foreignId == 'book-1'
                  ? {
                      'status': 'requested',
                      'book_formats': {
                        'ebook': 'requested',
                        'audiobook': 'requested',
                      },
                    }
                  : {'status': 'unavailable'};
    } else if (options.path.endsWith('/api/v1/book/lookup')) {
      // FAIL-02/FAIL-03 widget cases: these short-circuit with their own
      // status code rather than falling through to the shared 200 return
      // below.
      if (forbidden) {
        return ResponseBody.fromString('forbidden', 403);
      }
      if (serverError) {
        return ResponseBody.fromString('server error', 500);
      }
      if (emptyLookup) {
        return ResponseBody.fromString(
          '[]',
          200,
          headers: {
            'content-type': ['application/json'],
          },
        );
      }
      body = (mismatchedIdentity ||
              unresolvedIdentity ||
              ambiguousLookup ||
              aliasSibling ||
              duplicateLibraryRecords ||
              blankIdentity)
          ? [
              {
                'title': 'Flock',
                'foreignBookId': blankIdentity
                    ? ''
                    : aliasSibling
                        ? 'library-flock'
                        : 'lookup-flock',
                'year': 2024,
                'author': {
                  'id': 0,
                  'authorName': 'Kate Stewart',
                  'foreignAuthorId': 'author-flock',
                },
              },
              if (ambiguousLookup || aliasSibling)
                {
                  'title': 'Flock',
                  'foreignBookId': 'lookup-flock',
                  'year': 2024,
                  'author': {
                    'id': 0,
                    'authorName': 'Kate Stewart',
                    'foreignAuthorId': 'author-flock',
                  },
                },
            ]
          : [
              {
                'title': 'Meditations',
                'foreignBookId': 'book-1',
                'year': 2002,
                'pageCount': 304,
                'overview': 'A practical guide to Stoic philosophy.',
                'genres': ['Philosophy'],
                'author': {
                  'id': 0,
                  'authorName': 'Marcus Aurelius',
                  'foreignAuthorId': 'author-1',
                },
              },
              {
                'title': 'Letters from a Stoic',
                'foreignBookId': 'book-2',
                'year': 1965,
                'pageCount': 254,
                'overview': 'Seneca on living with wisdom and courage.',
                'genres': ['Philosophy'],
                'author': {
                  'id': 0,
                  'authorName': 'Seneca',
                  'foreignAuthorId': 'author-2',
                },
              },
            ];
    } else if (options.path == '/api/requests/book-recent') {
      recentRequests++;
      body = {'items': <Object>[]};
    } else if (options.path == '/api/requests/book-authors') {
      authorRequests++;
      body = {'authors': <Object>[], 'total': 0};
    } else if (options.path == '/api/requests/book-series') {
      seriesRequests++;
      body = {'series': <Object>[], 'total': 0};
    } else if (options.path == '/api/trakt/anticipated') {
      body = <Object>[];
    } else if (options.path == '/api/search') {
      // The dual dispatch (assumption A-05) means the TMDB call fires on
      // this route too, even though nothing here renders it.
      body = {
        'page': 1,
        'results': <Object>[],
        'total_pages': 1,
        'total_results': 0,
      };
    } else {
      body = {
        'page': 1,
        'results': <Object>[],
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
