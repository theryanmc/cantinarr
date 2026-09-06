import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../auth/logic/auth_provider.dart';

/// Opens the existing setup form without replacing the current catalog route.
class CatalogSetupButton extends ConsumerWidget {
  final String serviceType;
  const CatalogSetupButton({super.key, required this.serviceType});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    if (!(ref.watch(authProvider).valueOrNull?.user?.isAdmin ?? false)) {
      return const SizedBox.shrink();
    }
    final label = switch (serviceType) {
      'radarr' => 'Connect Radarr to request movies',
      'sonarr' => 'Connect Sonarr to request TV shows',
      'chaptarr' => 'Connect Chaptarr to request books',
      'lidarr' => 'Connect Lidarr to request music',
      _ => throw ArgumentError.value(serviceType),
    };
    return OutlinedButton.icon(
      icon: const Icon(Icons.add_link),
      label: Text(label, textAlign: TextAlign.center),
      onPressed: () async {
        final saved =
            await context.push<bool>('/settings/instance/new', extra: {
          'service_type': serviceType,
          'refresh_config_after_return': true,
        });
        // Finish the pop before config refresh reparses the router's stack.
        await WidgetsBinding.instance.endOfFrame;
        if (saved == true && context.mounted) {
          await ref.read(authProvider.notifier).refreshConfig();
        }
      },
    );
  }
}
