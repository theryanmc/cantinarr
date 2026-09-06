import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/providers/library_refresh_provider.dart';
import '../../../core/providers/realtime_provider.dart';
import '../../auth/logic/auth_provider.dart';
import '../../request/data/request_service.dart';
import '../data/music_discovery_service.dart';
import '../data/music_models.dart';
import 'music_browse_query.dart';

class MusicFeedState {
  final List<MusicAlbum> items;
  final int? nextPage;
  final bool loading;
  final String? error;
  final bool unsupported;
  final bool refreshFailed;
  final String emptyMessage;
  const MusicFeedState({
    this.items = const [],
    this.nextPage = 1,
    this.loading = false,
    this.error,
    this.unsupported = false,
    this.refreshFailed = false,
    this.emptyMessage = '',
  });
}

class MusicFeedNotifier extends StateNotifier<MusicFeedState> {
  final MusicDiscoveryService service;
  final MusicBrowseQuery query;
  MusicFeedNotifier(this.service, this.query) : super(const MusicFeedState()) {
    Future.microtask(loadMore);
  }

  Future<void> loadMore() => _load(refresh: false);
  Future<void> refresh() => _load(refresh: true);
  Future<void> retry() =>
      _load(refresh: state.refreshFailed || state.unsupported);

  Future<void> _load({required bool refresh}) async {
    if (!mounted || state.loading || (!refresh && state.nextPage == null)) {
      return;
    }
    final before = state;
    var next = refresh ? 1 : before.nextPage;
    state = MusicFeedState(
      items: before.items,
      nextPage: before.nextPage,
      loading: true,
      emptyMessage: before.emptyMessage,
    );
    try {
      final items = refresh ? <MusicAlbum>[] : [...before.items];
      final seen = items.map((a) => a.foreignId).toSet();
      var message = '';
      // Singles and repeated MBIDs can consume an entire provider page.
      // Follow its cursor, with a bound; offer Load more if the stretch lasts.
      for (var attempts = 0; attempts < 3 && next != null; attempts++) {
        final page = await service.feed(query, next);
        if (!mounted) return;
        if (page.page != next) {
          throw const FormatException('Unexpected music page');
        }
        next = page.nextPage;
        message = page.emptyMessage;
        final oldLength = items.length;
        items.addAll(page.results.where((a) => seen.add(a.foreignId)));
        if (items.length > oldLength) break;
      }
      state = MusicFeedState(
        items: items,
        nextPage: next,
        emptyMessage: message,
      );
    } catch (error) {
      if (!mounted) return;
      final forbidden =
          error is DioException && error.response?.statusCode == 403;
      state = MusicFeedState(
        items: forbidden ? const [] : before.items,
        nextPage: before.nextPage,
        unsupported: error is MusicDiscoveryUnsupported,
        refreshFailed: refresh,
        error: error is MusicDiscoveryUnsupported
            ? musicUpdateMessage
            : forbidden
                ? 'Music is no longer available for this account or library.'
                : before.items.isNotEmpty && refresh
                    ? 'Refresh failed. Your previous results are still shown.'
                    : 'Could not load music. Please retry.',
      );
    }
  }
}

final musicFeedProvider = StateNotifierProvider.autoDispose
    .family<MusicFeedNotifier, MusicFeedState, MusicBrowseQuery>((ref, query) {
  ref.watch(authProvider.select((auth) => auth.valueOrNull?.user?.id));
  return MusicFeedNotifier(ref.watch(musicDiscoveryServiceProvider), query);
});

/// Each lazily built visible card reads authoritative status for its exact
/// instance. Metadata caching never substitutes for this live status service.
final musicCardStatusProvider = FutureProvider.autoDispose
    .family<MusicRequestStatusDetail, ({String id, String instanceId})>(
        (ref, key) {
  ref.watch(libraryRefreshTickProvider);
  ref.watch(libraryChangedEventsProvider);
  return RequestService(backendDio: ref.watch(backendClientProvider))
      .checkMusicStatusDetail(key.id, instanceId: key.instanceId);
});
