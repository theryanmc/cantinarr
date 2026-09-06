import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../core/models/backend_connection.dart';
import '../../../core/models/user_profile.dart';
import '../../../core/providers/instance_provider.dart';
import '../../auth/logic/auth_provider.dart';

const adminCatalogUpdateMessage =
    'Update your Cantinarr server to browse this catalog before connecting a service.';

/// Browsing permission and a usable library are separate capabilities.
class DiscoveryAccess {
  final UserProfile? user;
  final BackendConnection? connection;
  final InstanceState instances;
  const DiscoveryAccess(this.user, this.connection, this.instances);

  bool get isAdmin => user?.isAdmin ?? false;
  bool get showBooks => isAdmin || (connection?.services.chaptarr ?? false);
  bool get showMusic => isAdmin || (connection?.services.lidarr ?? false);

  String? activeId(String serviceType) => switch (serviceType) {
        'radarr' => instances.activeRadarrInstance?.id,
        'sonarr' => instances.activeSonarrInstance?.id,
        'chaptarr' => instances.activeChaptarrInstance?.id,
        'lidarr' => instances.activeLidarrInstance?.id,
        _ => null,
      };

  bool hasInstance(String serviceType, String? id) =>
      id != null &&
      (connection?.instances
              .any((i) => i.id == id && i.serviceType == serviceType) ??
          false);

  bool canBrowse(String serviceType, String? id) =>
      (user?.hasPermission('media:discover') ?? false) &&
      (id == null
          ? isAdmin && (connection?.adminCatalogBrowsing ?? false)
          : hasInstance(serviceType, id));

  bool needsUpdate(String? id) =>
      isAdmin && id == null && !(connection?.adminCatalogBrowsing ?? false);

  /// Catalog/status state must never survive a change of account or access.
  String get scope {
    final ids =
        connection?.instances.map((i) => '${i.serviceType}:${i.id}').toList() ??
            <String>[];
    ids.sort();
    return '${connection?.serverUrl}|${user?.id}|${user?.role}|${user?.child}|${user?.permissions.join(',')}|${connection?.adminCatalogBrowsing}|${ids.join(',')}';
  }
}

final discoveryAccessProvider = Provider((ref) {
  final auth = ref.watch(authProvider).valueOrNull;
  return DiscoveryAccess(
      auth?.user, auth?.connection, ref.watch(instanceProvider));
});

final catalogDiscoveryScopeProvider = Provider((ref) =>
    ref.watch(discoveryAccessProvider.select((access) => access.scope)));
