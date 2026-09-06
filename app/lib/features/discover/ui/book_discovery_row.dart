import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../core/providers/instance_provider.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/horizontal_item_row.dart';
import '../../../core/widgets/media_card.dart';
import '../../../core/widgets/section_header.dart';
import '../../auth/logic/auth_provider.dart';
import '../../request/data/request_service.dart';
import '../data/book_discovery_service.dart';
import '../logic/book_discovery_provider.dart';

class BookDiscoveryError extends StatelessWidget {
  final Object error;
  final VoidCallback onRetry;
  const BookDiscoveryError(this.error, {super.key, required this.onRetry});
  @override
  Widget build(BuildContext context) => Padding(
        padding: const EdgeInsets.all(16),
        child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
          Text(error is BookDiscoveryException
              ? error.toString()
              : 'Could not load books. Please retry.'),
          TextButton(onPressed: onRetry, child: const Text('Retry')),
        ]),
      );
}

/// Portrait cards keep author names and separate, explicit format labels.
/// Building a visible card starts its coalesced, instance-scoped ID lookup.
class BookDiscoveryCard extends ConsumerWidget {
  final DiscoveryBook book;
  final String instanceId;
  final double width;
  const BookDiscoveryCard(
      {super.key,
      required this.book,
      required this.instanceId,
      this.width = 120});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final value = ref.watch(discoveryBookStatusProvider(
        (foreignId: book.foreignId, instanceId: instanceId)));
    final detail = value.hasError || value.isLoading ? null : value.valueOrNull;
    return Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
      MediaCard(
          id: 0,
          title: book.title,
          subtitle: book.author,
          posterPath: book.coverUrl,
          placeholderIcon: Icons.menu_book,
          width: width,
          onTap: () =>
              context.push(book.detailLocation(instanceId), extra: book)),
      for (final format in [
        BookRequestFormat.ebook,
        BookRequestFormat.audiobook
      ])
        if (detail?.statusFor(format) != null && detail!.isCovered(format))
          Text(
              '${format == BookRequestFormat.ebook ? 'eBook' : 'Audiobook'}: ${detail.waitFor(format) == null ? detail.statusFor(format)!.label : 'Waiting for library'}',
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
              style: Theme.of(context)
                  .textTheme
                  .labelSmall
                  ?.copyWith(color: AppTheme.textSecondary)),
    ]);
  }
}

class PopularBooksRow extends ConsumerWidget {
  const PopularBooksRow({super.key});
  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final id = ref.watch(instanceProvider).activeChaptarrInstance?.id;
    final allowed = ref
            .watch(authProvider)
            .valueOrNull
            ?.user
            ?.hasPermission('media:discover') ??
        false;
    if (id == null || !allowed) return const SizedBox.shrink();
    final query = BookBrowseQuery(instanceId: id);
    final feed = ref.watch(bookFeedProvider(query));
    final width = MediaQuery.sizeOf(context).width >= 900 ? 124.0 : 108.0;
    return Padding(
        padding: const EdgeInsets.only(top: 20),
        child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
          Padding(
              padding: const EdgeInsets.symmetric(horizontal: 16),
              child: SectionHeader(
                  title: 'Popular Books',
                  trailing: TextButton(
                      onPressed: () => context.push(query.location),
                      child: const Text('See all')))),
          const Padding(
              padding: EdgeInsets.fromLTRB(32, 0, 16, 12),
              child: Text('Popular on Open Library')),
          if (feed.error != null)
            BookDiscoveryError(feed.error!,
                onRetry: () => feed.load(refresh: true)),
          if (!feed.loading && feed.error == null && feed.items.isEmpty)
            Padding(
                padding: const EdgeInsets.all(16),
                child: Text(feed.emptyMessage)),
          if (feed.loading || feed.items.isNotEmpty)
            HorizontalItemRow<DiscoveryBook>(
                items: feed.items,
                isLoading: feed.loading,
                height: width * 1.5 +
                    110 * MediaQuery.textScalerOf(context).scale(1),
                itemBuilder: (book) => SizedBox(
                    width: width,
                    child: BookDiscoveryCard(
                        key: ValueKey(book.foreignId),
                        book: book,
                        instanceId: id,
                        width: width))),
        ]));
  }
}

class BookGenresRow extends ConsumerWidget {
  const BookGenresRow({super.key});
  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final id = ref.watch(instanceProvider).activeChaptarrInstance?.id;
    final allowed = ref
            .watch(authProvider)
            .valueOrNull
            ?.user
            ?.hasPermission('media:discover') ??
        false;
    if (id == null || !allowed) return const SizedBox.shrink();
    final genres = ref.watch(bookGenresProvider(id));
    final denied = genres.error is BookDiscoveryException &&
        (genres.error as BookDiscoveryException).accessDenied;
    return Padding(
        padding: const EdgeInsets.fromLTRB(16, 20, 16, 0),
        child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
          const SectionHeader(title: 'Browse by genre'),
          const SizedBox(height: 12),
          if (genres.hasError)
            BookDiscoveryError(genres.error!,
                onRetry: () => ref.invalidate(bookGenresProvider(id))),
          if (genres.isLoading && !genres.hasValue)
            const LinearProgressIndicator(),
          if (!denied)
            Wrap(spacing: 8, runSpacing: 6, children: [
              for (final genre in genres.valueOrNull ?? <BookGenre>[])
                ActionChip(
                    label: Text(genre.name),
                    onPressed: () => context.push(BookBrowseQuery(
                            feed: 'genre', instanceId: id, genre: genre.id)
                        .location)),
            ]),
        ]));
  }
}
