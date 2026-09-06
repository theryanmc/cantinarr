import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../data/book_discovery_service.dart';
import '../logic/book_discovery_provider.dart';
import 'book_discovery_row.dart';

class BookBrowseScreen extends ConsumerStatefulWidget {
  final BookBrowseQuery query;
  const BookBrowseScreen({super.key, required this.query});
  @override
  ConsumerState<BookBrowseScreen> createState() => _BookBrowseScreenState();
}

class _BookBrowseScreenState extends ConsumerState<BookBrowseScreen> {
  final _scroll = ScrollController();
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
    final feed = ref.read(bookFeedProvider(widget.query));
    if (feed.error == null) feed.load();
  }

  @override
  Widget build(BuildContext context) {
    final feed = ref.watch(bookFeedProvider(widget.query));
    final genres =
        ref.watch(bookGenresProvider(widget.query.instanceId)).valueOrNull ??
            <BookGenre>[];
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
                              instanceId: widget.query.instanceId,
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
