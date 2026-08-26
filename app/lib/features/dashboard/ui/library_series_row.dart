import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../core/providers/instance_provider.dart';
import '../../../core/widgets/horizontal_item_row.dart';
import '../../../core/widgets/media_card.dart';
import '../../../core/widgets/section_header.dart';
import '../../../core/widgets/section_sort_menu.dart';
import '../../chaptarr/data/chaptarr_image.dart';
import '../data/book_series_service.dart';

/// The Books tab's "Series" row: which series the library actually holds books
/// of, and how complete each one is.
///
/// It answers the question the Authors row raises but cannot: an author card
/// says you hold 6 of their 61 books, this one says which run those 6 belong to
/// and how much of it is missing.
///
/// Cards are poster-shaped rather than circular like the Authors row — a series
/// is recognised by its first book's cover, not by a face.
class LibrarySeriesRow extends ConsumerWidget {
  const LibrarySeriesRow({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final instanceId = ref.watch(instanceProvider).activeChaptarrInstance?.id;
    if (instanceId == null) return const SizedBox.shrink();

    final seriesAsync = ref.watch(bookSeriesProvider);
    final page = seriesAsync.valueOrNull;
    final series = page?.series ?? const <LibrarySeries>[];
    // Same rule as the other book rows: nothing to show, no access, or an
    // unreadable library all look the same — no row. Shows nothing while it
    // loads rather than a shelf it may be about to withdraw.
    if (seriesAsync.hasError || series.isEmpty) {
      return const SizedBox.shrink();
    }

    final viewportWidth = MediaQuery.sizeOf(context).width;
    final cardWidth =
        viewportWidth >= 900 ? 124.0 : (viewportWidth >= 600 ? 116.0 : 108.0);

    return Padding(
      padding: const EdgeInsets.only(top: 20),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Padding(
            padding: EdgeInsets.symmetric(
              horizontal: viewportWidth >= 900 ? 24 : 16,
            ),
            child: SectionHeader(
              title: 'Series',
              trailing: Row(
                mainAxisSize: MainAxisSize.min,
                children: [
                  SectionTruncationNote(
                    shown: series.length,
                    total: page?.total ?? series.length,
                  ),
                  SectionSortMenu<SeriesSort>(
                    tooltip: 'Sort series',
                    options: SeriesSort.values,
                    selected: ref.watch(bookSeriesSortProvider),
                    labelOf: (option) => option.label,
                    onSelected: (next) =>
                        ref.read(bookSeriesSortProvider.notifier).state = next,
                  ),
                ],
              ),
            ),
          ),
          const SizedBox(height: 12),
          HorizontalItemRow<LibrarySeries>(
            items: series,
            isLoading: false,
            // Taller than the Recently Added row by one text line: the count
            // is allowed to wrap, because "9 of 41 books available" cut to
            // "9 of 41 books avail…" is the half that carried the meaning.
            height: cardWidth * 1.5 + 82,
            itemBuilder: (entry) {
              final cover = chaptarrImageSource(ref, entry.cover, instanceId);
              return MediaCard(
                // The name is the identity here; there is no series id to key
                // a card by.
                id: entry.name.hashCode,
                title: entry.name,
                posterPath: cover?.url,
                posterHeaders: cover?.headers,
                placeholderIcon: Icons.auto_stories,
                subtitle: entry.countLabel,
                subtitleMaxLines: 2,
                width: cardWidth,
                onTap: () => context.push(
                  '/detail/series/${Uri.encodeComponent(entry.name)}'
                  '?instance_id=${Uri.encodeQueryComponent(instanceId)}',
                ),
              );
            },
          ),
        ],
      ),
    );
  }
}
