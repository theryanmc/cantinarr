import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../data/book_discovery_service.dart';
import '../logic/book_discovery_provider.dart';
import '../logic/discovery_access.dart';
import 'book_discovery_row.dart';

class BookBrowseScreen extends ConsumerStatefulWidget {
  final BookBrowseQuery query;
  const BookBrowseScreen({super.key, required this.query});
  @override
  ConsumerState<BookBrowseScreen> createState() => _BookBrowseScreenState();
}

class _BookBrowseScreenState extends ConsumerState<BookBrowseScreen> {
  final _scroll = ScrollController();
  BookBrowseQuery? _query;
  double? _setupReturnOffset;
  @override
  void initState() {
    super.initState();
    _scroll.addListener(_more);
  }

  @override
  void dispose() {
    _scroll.dispose();
    super.dispose();
  }

  void _more() {
    if (!_scroll.hasClients || _scroll.position.extentAfter > 400) return;
    if (_query == null) return;
    final feed = ref.read(bookFeedProvider(_query!));
    if (feed.error == null) feed.load();
  }

  void _restoreAfterSetup(BookFeedNotifier feed) {
    if (_setupReturnOffset == null || feed.loading || feed.error != null) {
      return;
    }
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!mounted || !_scroll.hasClients || _setupReturnOffset == null) return;
      final offset = _setupReturnOffset!;
      if (_scroll.position.maxScrollExtent < offset && feed.nextPage != null) {
        feed.load();
        return;
      }
      _setupReturnOffset = null;
      _scroll.jumpTo(offset.clamp(0, _scroll.position.maxScrollExtent));
    });
  }

  @override
  Widget build(BuildContext context) {
    final access = ref.watch(discoveryAccessProvider);
    final id = widget.query.instanceId ?? access.activeId('chaptarr');
    if (_query != null &&
        _query!.instanceId == null &&
        id != null &&
        _scroll.hasClients) {
      _setupReturnOffset = _scroll.offset;
    }
    _query = null;
    if (!access.canBrowse('chaptarr', id)) {
      _setupReturnOffset = null;
      return Scaffold(
          appBar: AppBar(title: const Text('Books')),
          body: Center(
              child: Text(access.needsUpdate(id)
                  ? adminCatalogUpdateMessage
                  : 'Books are not available for this account.')));
    }
    final query = BookBrowseQuery(
        feed: widget.query.feed, genre: widget.query.genre, instanceId: id);
    _query = query;
    final feed = ref.watch(bookFeedProvider(query));
    _restoreAfterSetup(feed);
    final genres =
        ref.watch(bookGenresProvider(id)).valueOrNull ?? <BookGenre>[];
    var title =
        widget.query.feed == 'popular' ? 'Popular Books' : 'Books by genre';
    for (final g in genres) {
      if (g.id == widget.query.genre) title = g.name;
    }
    final scale = MediaQuery.textScalerOf(context).scale(1);
    return Scaffold(
        appBar: AppBar(title: Text(title)),
        body: RefreshIndicator(
          onRefresh: () => feed.load(refresh: true),
          child: CustomScrollView(
            key: PageStorageKey(widget.query.location),
            controller: _scroll,
            slivers: [
              const SliverToBoxAdapter(
                  child: Padding(
                      padding: EdgeInsets.all(16),
                      child: Text('Open Library'))),
              if (feed.error != null)
                SliverToBoxAdapter(
                    child:
                        BookDiscoveryError(feed.error!, onRetry: feed.retry)),
              if (feed.items.isEmpty && !feed.loading && feed.error == null)
                SliverToBoxAdapter(
                    child: Padding(
                        padding: const EdgeInsets.all(24),
                        child: Text(feed.emptyMessage))),
              SliverPadding(
                  padding: const EdgeInsets.symmetric(horizontal: 16),
                  sliver: SliverLayoutBuilder(builder: (context, constraints) {
                    final columns =
                        (constraints.crossAxisExtent / 146).floor().clamp(2, 8);
                    final width =
                        (constraints.crossAxisExtent - (columns - 1) * 14) /
                            columns;
                    return SliverGrid(
                        delegate: SliverChildBuilderDelegate((context, index) {
                          final book = feed.items[index];
                          return BookDiscoveryCard(
                              key: ValueKey(book.foreignId),
                              book: book,
                              instanceId: id,
                              width: width);
                        }, childCount: feed.items.length),
                        gridDelegate: SliverGridDelegateWithFixedCrossAxisCount(
                            crossAxisCount: columns,
                            crossAxisSpacing: 14,
                            mainAxisSpacing: 16,
                            mainAxisExtent: width * 1.5 + 110 * scale));
                  })),
              SliverToBoxAdapter(
                  child: Padding(
                      padding: const EdgeInsets.all(24),
                      child: Center(
                          child: feed.loading
                              ? const CircularProgressIndicator()
                              : feed.nextPage != null
                                  ? TextButton(
                                      onPressed: () => feed.load(),
                                      child: const Text('Load more'))
                                  : const SizedBox.shrink()))),
            ],
          ),
        ));
  }
}
