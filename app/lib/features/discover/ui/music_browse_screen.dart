import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import '../../../core/providers/instance_provider.dart';
import '../../../core/providers/library_refresh_provider.dart';
import '../../../core/widgets/media_card.dart';
import '../logic/music_browse_query.dart';
import '../logic/music_feed_provider.dart';
import 'music_discovery_row.dart';

class MusicBrowseScreen extends ConsumerStatefulWidget {
  final MusicBrowseQuery query;
  const MusicBrowseScreen({super.key, required this.query});
  @override
  ConsumerState<MusicBrowseScreen> createState() => _MusicBrowseScreenState();
}

class _MusicBrowseScreenState extends ConsumerState<MusicBrowseScreen>
    with WidgetsBindingObserver {
  final _scroll = ScrollController();
  MusicBrowseQuery? _query;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    _scroll.addListener(_nearEnd);
  }

  @override
  void didUpdateWidget(covariant MusicBrowseScreen oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.query != widget.query) {
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (mounted && _scroll.hasClients) _scroll.jumpTo(0);
      });
    }
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    _scroll.dispose();
    super.dispose();
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.resumed) {
      ref.read(libraryRefreshTickProvider.notifier).state++;
    }
  }

  void _nearEnd() {
    if (_query == null ||
        !_scroll.hasClients ||
        _scroll.position.extentAfter > 400) {
      return;
    }
    final state = ref.read(musicFeedProvider(_query!));
    if (state.error == null && !state.loading) {
      ref.read(musicFeedProvider(_query!).notifier).loadMore();
    }
  }

  @override
  Widget build(BuildContext context) {
    final instances = ref.watch(instanceProvider);
    final id = widget.query.instanceId.isEmpty
        ? instances.activeLidarrInstance?.id
        : widget.query.instanceId;
    if (id == null ||
        !instances.lidarrInstances.any((instance) => instance.id == id)) {
      return Scaffold(
          appBar: AppBar(title: Text(widget.query.title)),
          body: const Center(
              child: Text('This music library is not available to you.')));
    }
    final query = widget.query.withInstance(id);
    _query = query;
    final state = ref.watch(musicFeedProvider(query));
    final notifier = ref.read(musicFeedProvider(query).notifier);
    return Scaffold(
      appBar: AppBar(title: Text(query.title), actions: [
        IconButton(
            tooltip: 'Refresh ${query.title}',
            icon: const Icon(Icons.refresh),
            onPressed: state.loading
                ? null
                : () {
                    ref.read(libraryRefreshTickProvider.notifier).state++;
                    notifier.refresh();
                  }),
      ]),
      body: RefreshIndicator(
        onRefresh: () async {
          ref.read(libraryRefreshTickProvider.notifier).state++;
          await notifier.refresh();
        },
        child: CustomScrollView(
          key: PageStorageKey(query.location),
          controller: _scroll,
          physics: const AlwaysScrollableScrollPhysics(),
          slivers: [
            SliverToBoxAdapter(
                child: Padding(
              padding: const EdgeInsets.all(16),
              child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(query.description),
                    if (query.feed == 'popular')
                      MusicPeriodSelector(
                          period: query.period,
                          onChanged: (period) {
                            context.replace(query.withPeriod(period).location);
                          }),
                  ]),
            )),
            SliverToBoxAdapter(
                child: MusicFeedNotice(state: state, retry: notifier.retry)),
            SliverPadding(
              padding: const EdgeInsets.symmetric(horizontal: 16),
              sliver: SliverLayoutBuilder(builder: (context, constraints) {
                final columns =
                    (constraints.crossAxisExtent / 156).floor().clamp(2, 8);
                final width =
                    (constraints.crossAxisExtent - (columns - 1) * 14) /
                        columns;
                return SliverGrid(
                  gridDelegate: SliverGridDelegateWithFixedCrossAxisCount(
                    crossAxisCount: columns,
                    crossAxisSpacing: 14,
                    mainAxisSpacing: 18,
                    mainAxisExtent: width + MediaCard.subtitleRowExtraHeight,
                  ),
                  delegate: SliverChildBuilderDelegate(
                    (context, index) => MusicDiscoveryCard(
                        key: ValueKey(state.items[index].foreignId),
                        album: state.items[index],
                        instanceId: id,
                        width: width),
                    childCount: state.items.length,
                  ),
                );
              }),
            ),
            SliverToBoxAdapter(
                child: Padding(
              padding: const EdgeInsets.all(24),
              child: Center(
                  child: state.loading
                      ? const CircularProgressIndicator()
                      : state.nextPage != null && state.error == null
                          ? OutlinedButton(
                              onPressed: notifier.loadMore,
                              child: const Text('Load more'))
                          : const SizedBox.shrink()),
            )),
          ],
        ),
      ),
    );
  }
}
