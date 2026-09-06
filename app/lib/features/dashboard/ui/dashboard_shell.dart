import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/models/app_module.dart';
import '../../../core/widgets/module_scaffold.dart';
import '../../discover/logic/discovery_access.dart';
import '../../discover/ui/catalog_prefetch.dart';

/// Admins see every catalog; requesters need book/music grants. Visible tabs
/// map onto fixed router branches, including when only Music is granted.
/// Pages render as bottom navigation on mobile and sidebar items on desktop.
class DashboardShell extends ConsumerWidget {
  final int currentIndex;
  final ValueChanged<int> onTabChanged;
  final Widget child;

  const DashboardShell({
    super.key,
    required this.currentIndex,
    required this.onTabChanged,
    required this.child,
  });

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final access = ref.watch(discoveryAccessProvider);
    final showBooks = access.showBooks;
    final showMusic = access.showMusic;
    final indices = [0, 1, 2, if (showBooks) 3, if (showMusic) 4];
    return CatalogWarmup(
        child: ModuleScaffold(
      pages: modulePagesFor(ModuleType.dashboard,
          includeBooks: showBooks, includeMusic: showMusic),
      currentIndex: indices.indexOf(currentIndex).clamp(0, indices.length - 1),
      onTabChanged: (index) => onTabChanged(indices[index]),
      child: child,
    ));
  }
}
