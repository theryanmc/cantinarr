import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../core/providers/instance_provider.dart';
import '../data/music_discovery_service.dart';
import '../data/music_models.dart';
import '../logic/discovery_access.dart';

typedef CatalogSearchKey = ({
  String mediaType,
  String query,
  String? instanceId,
  int page
});

final catalogSearchProvider = FutureProvider.autoDispose.family<
    ({List<Object> results, int? nextPage, String message}),
    CatalogSearchKey>((ref, key) async {
  ref.watch(catalogDiscoveryScopeProvider);
  final serviceType = key.mediaType == 'book' ? 'chaptarr' : 'lidarr';
  if (!ref
      .watch(discoveryAccessProvider)
      .canBrowse(serviceType, key.instanceId)) {
    throw StateError('This catalog is not available for your account.');
  }
  // Typing changes the family key immediately; disposed queries must not
  // consume public-provider rate limits after their debounce completes.
  var cancelled = false;
  ref.onDispose(() => cancelled = true);
  await Future<void>.delayed(const Duration(milliseconds: 300));
  if (cancelled) throw StateError('Search changed');
  if (key.mediaType == 'book') throw StateError('catalog_retired');

  final page = await ref
      .read(musicDiscoveryServiceProvider)
      .search(key.query, key.instanceId, page: key.page);
  return (
    results: page.results,
    nextPage: page.nextPage,
    message: page.emptyMessage
  );
});

/// Both searches run independently. Wide layouts show both sources; compact
/// layouts give each result list its own tab without coupling their failures.
class CatalogSearchResults extends ConsumerStatefulWidget {
  final String mediaType;
  final String query;
  final Widget libraryResults;
  final VoidCallback? onResultTap;
  const CatalogSearchResults(
      {super.key,
      required this.mediaType,
      required this.query,
      required this.libraryResults,
      this.onResultTap});
  @override
  ConsumerState<CatalogSearchResults> createState() =>
      _CatalogSearchResultsState();
}

class _CatalogSearchResultsState extends ConsumerState<CatalogSearchResults> {
  int _page = 1;
  String? _scope;
  @override
  Widget build(BuildContext context) {
    final instances = ref.watch(instanceProvider);
    final instanceId = widget.mediaType == 'book'
        ? instances.activeChaptarrInstance?.id
        : instances.activeLidarrInstance?.id;
    final scope =
        '${widget.mediaType}:${widget.query}:$instanceId:${ref.watch(catalogDiscoveryScopeProvider)}';
    if (_scope != scope) {
      _scope = scope;
      _page = 1;
    }
    final key = (
      mediaType: widget.mediaType,
      query: widget.query.trim(),
      instanceId: instanceId,
      page: _page
    );
    final result = ref.watch(catalogSearchProvider(key));
    const label = 'MusicBrainz';
    final publicResults = result.when(
      skipLoadingOnRefresh: false,
      loading: () => const Center(child: CircularProgressIndicator()),
      error: (error, _) => Center(
          child: Column(mainAxisSize: MainAxisSize.min, children: [
        Text('Could not search $label.'),
        TextButton(
            onPressed: () => ref.invalidate(catalogSearchProvider(key)),
            child: const Text('Retry')),
      ])),
      data: (page) => ListView(children: [
        if (page.results.isEmpty)
          Padding(padding: const EdgeInsets.all(24), child: Text(page.message)),
        for (final item in page.results)
          if (item is MusicAlbum)
            ListTile(
                leading: const Icon(Icons.album),
                title: Text(item.title),
                subtitle: Text([item.subtitle, item.disambiguation]
                    .where((s) => s.isNotEmpty)
                    .join(' · ')),
                onTap: () {
                  widget.onResultTap?.call();
                  context.push(item.detailLocation(instanceId), extra: item);
                }),
        Row(mainAxisAlignment: MainAxisAlignment.center, children: [
          if (_page > 1)
            TextButton(
                onPressed: () => setState(() => _page--),
                child: const Text('Previous')),
          Text('Page $_page'),
          if (page.nextPage != null)
            TextButton(
                onPressed: () => setState(() => _page = page.nextPage!),
                child: const Text('Next')),
        ]),
      ]),
    );
    return Material(
      type: MaterialType.transparency,
      child: LayoutBuilder(builder: (context, constraints) {
        if (constraints.maxWidth >= 900) {
          Widget panel(String title, Widget body) => Column(children: [
                Padding(
                    padding: const EdgeInsets.all(12),
                    child: Text(title,
                        style: Theme.of(context).textTheme.titleMedium)),
                Expanded(child: body)
              ]);
          return Row(children: [
            Expanded(child: panel(label, publicResults)),
            const VerticalDivider(),
            Expanded(
                child: panel('Your library catalog', widget.libraryResults))
          ]);
        }
        return DefaultTabController(
            length: 2,
            child: Column(children: [
              TabBar(
                  tabs: [Tab(text: label), const Tab(text: 'Library catalog')]),
              Expanded(
                  child: TabBarView(
                      children: [publicResults, widget.libraryResults])),
            ]));
      }),
    );
  }
}
