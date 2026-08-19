import 'package:flutter/material.dart';
import 'package:go_router/go_router.dart';
import '../../../core/widgets/horizontal_item_row.dart';
import '../../../core/widgets/media_card.dart';
import '../../../core/widgets/section_header.dart';
import '../data/tmdb_models.dart';
import '../logic/search_library_status.dart';

/// A titled horizontal row of media items (e.g. "Trending Now").
class CategoryRow extends StatelessWidget {
  final String title;
  final List<MediaItem> items;
  final bool isLoading;
  final void Function(MediaItem)? onLoadMore;

  /// Availability badge data keyed by (media type, TMDB id). Defaults to
  /// empty so every existing call site keeps compiling and a row not yet fed
  /// simply renders no badge on any of its posters.
  final Map<(MediaType, int), LibraryStatus> libraryStatus;

  const CategoryRow({
    super.key,
    required this.title,
    required this.items,
    required this.isLoading,
    this.onLoadMore,
    this.libraryStatus = const {},
  });

  @override
  Widget build(BuildContext context) {
    final width = MediaQuery.sizeOf(context).width;
    final cardWidth = width >= 900 ? 124.0 : (width >= 600 ? 116.0 : 108.0);

    return Padding(
      padding: const EdgeInsets.only(top: 20),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          const SizedBox(height: 4),
          Padding(
            padding: EdgeInsets.symmetric(
              horizontal: MediaQuery.sizeOf(context).width >= 900 ? 24 : 16,
            ),
            child: SectionHeader(title: title),
          ),
          const SizedBox(height: 12),
          HorizontalItemRow<MediaItem>(
            items: items,
            isLoading: isLoading,
            height: cardWidth * 1.5 + 54,
            onItemAppear: onLoadMore,
            itemBuilder: (item) {
              final status = libraryStatus[(item.mediaType, item.id)];
              return MediaCard(
                id: item.id,
                title: item.title,
                posterPath: item.posterPath,
                rating: item.voteAverage,
                statusLabel: status?.label,
                statusColor: status?.color,
                width: cardWidth,
                onTap: () => context.push(
                  '/detail/${item.mediaType.name}/${item.id}',
                ),
              );
            },
          ),
        ],
      ),
    );
  }
}
