import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:url_launcher/url_launcher.dart';
import '../../../core/layout/adaptive.dart';
import '../../../core/theme/app_theme.dart';
import '../../auth/logic/auth_provider.dart';
import '../data/media_access_service.dart';
import 'media_server_password_sheet.dart';

/// Requester-focused guide for the media servers (Jellyfin, Emby) shared with this
/// account: create the account with a password only they know, see where to
/// sign in, install the app, start watching. Everything here is re-read from
/// the server on every open and on pull-to-refresh: the rows behind it are an
/// action log and the media server is the truth, which is also why an
/// unconfirmed account is said to be unconfirmed rather than shown as fact.
class MediaAccessGuide extends ConsumerStatefulWidget {
  const MediaAccessGuide({super.key});

  @override
  ConsumerState<MediaAccessGuide> createState() => _MediaAccessGuideState();
}

class _MediaAccessGuideState extends ConsumerState<MediaAccessGuide> {
  List<MediaServerAccess>? _servers;
  bool _failed = false;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    try {
      final servers = await ref.read(mediaAccessServiceProvider).listMine();
      if (!mounted) return;
      setState(() {
        _servers = servers;
        _failed = false;
      });
    } catch (_) {
      if (!mounted) return;
      if (_servers == null) {
        setState(() => _failed = true);
        return;
      }
      // A refresh that failed keeps what was shown; say so rather than
      // swapping a working screen for an error.
      ScaffoldMessenger.of(context).showSnackBar(const SnackBar(
        content: Text("Couldn't refresh your media servers."),
      ));
    }
  }

  void _retry() {
    setState(() => _failed = false);
    _load();
  }

  Future<void> _createAccount(MediaServerAccess server, String username) async {
    final outcome = await showMediaServerPasswordSheet(
      context,
      server: server,
      username: username,
    );
    if (!mounted || outcome == null) return;
    ScaffoldMessenger.of(context).showSnackBar(SnackBar(
      content: Text(switch (outcome) {
        MediaServerPasswordSheetOutcome.created =>
          'Account created. Sign in with your new password.',
        MediaServerPasswordSheetOutcome.accountExists =>
          'You already have an account here.',
      }),
    ));
    await _load();
  }

  Future<void> _copy(String text, String confirmation) async {
    await Clipboard.setData(ClipboardData(text: text));
    if (!mounted) return;
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(content: Text(confirmation)),
    );
  }

  Future<void> _open(String address) async {
    final uri = Uri.tryParse(address);
    if (uri == null) return;
    await launchUrl(uri, mode: LaunchMode.externalApplication);
  }

  @override
  Widget build(BuildContext context) {
    final auth = ref.watch(authProvider).valueOrNull;
    final user = auth?.user;
    final servers = _servers;
    // Until the live list arrives, the granted set from /api/config names
    // the same servers, so the title never flashes a placeholder.
    final types = (servers != null
            ? servers.map((server) => server.serviceType)
            : (auth?.connection?.mediaServerInstances ?? const [])
                .map((instance) => instance.serviceType))
        .toSet();

    return Scaffold(
      appBar: AppBar(title: Text(mediaServerGuideTitle(types))),
      body: CenteredContent(
        child: servers == null
            ? (_failed ? _buildLoadFailure() : _buildLoading())
            : RefreshIndicator(
                onRefresh: _load,
                child: ListView(
                  padding: const EdgeInsets.all(24),
                  children: servers.isEmpty
                      ? [_buildEmpty(isAdmin: user?.isAdmin == true)]
                      : _buildGuide(
                          servers,
                          username: user?.username ?? '',
                          types: types,
                        ),
                ),
              ),
      ),
    );
  }

  Widget _buildLoading() => const Center(child: CircularProgressIndicator());

  Widget _buildLoadFailure() {
    return Center(
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          const Text(
            "Couldn't load your media servers.",
            style: TextStyle(color: AppTheme.textSecondary),
          ),
          const SizedBox(height: 16),
          ElevatedButton(onPressed: _retry, child: const Text('Retry')),
        ],
      ),
    );
  }

  /// Nothing granted. An admin can fix that themselves, so they are told
  /// where; a requester can only ask.
  Widget _buildEmpty({required bool isAdmin}) {
    return Padding(
      padding: const EdgeInsets.only(top: 48),
      child: Column(
        children: [
          const Icon(Icons.live_tv_outlined,
              size: 40, color: AppTheme.textMuted),
          const SizedBox(height: 12),
          Text(
            isAdmin
                ? 'No media server is shared with your account yet. Open the '
                    'instance under Settings and add yourself under User '
                    'Access.'
                : 'No media server is shared with you yet. Ask your admin '
                    'for access.',
            style: const TextStyle(
              color: AppTheme.textSecondary,
              fontSize: 14,
              height: 1.5,
            ),
            textAlign: TextAlign.center,
          ),
        ],
      ),
    );
  }

  List<Widget> _buildGuide(
    List<MediaServerAccess> servers, {
    required String username,
    required Set<String> types,
  }) {
    final labels = mediaServerTypeLabels(types);
    final single = labels.length == 1;
    // "Jellyfin", "Emby", or "Jellyfin or Emby": the granted set decides.
    final names = mediaServerNamesPhrase(types);
    final where = single ? labels.single : 'your media server';
    final whereOpening = single ? labels.single : 'Your media server';
    final includesEmby = types.contains('emby');
    return [
      Text(
        'Cantinarr is where you request. $whereOpening is where you watch. '
        'Create your account once, then sign in on any device.',
        style: const TextStyle(
          color: AppTheme.textSecondary,
          fontSize: 14,
          height: 1.5,
        ),
      ),
      const SizedBox(height: 24),
      const _SectionHeader(number: 1, title: 'Your account'),
      const SizedBox(height: 12),
      for (final server in servers)
        Padding(
          padding: const EdgeInsets.only(left: 44, bottom: 12),
          child: _buildAccountCard(server, username),
        ),
      const SizedBox(height: 12),
      _GuideSection(
        number: 2,
        title: 'Install the $names app',
        steps: [
          // Jellyfin's apps are free; Emby's are free to install but ask for
          // an unlock or Premiere to play video on phones and tablets, so
          // "free" is said only for a Jellyfin-only set.
          if (single && !includesEmby)
            'Download the free $names app from the App Store or Google Play'
          else
            'Download the $names app from the App Store or Google Play',
          if (single)
            '$names is also on Apple TV, Android TV, Roku, Fire TV, and most '
                'smart TVs'
          else
            '${labels.length == 2 ? 'Both' : 'All of them'} are also on '
                'Apple TV, Android TV, Roku, Fire TV, and most smart TVs',
          if (includesEmby)
            'On a phone or tablet, Emby may ask for a one-time unlock or Emby '
                'Premiere before it plays video.',
          'On a computer there is nothing to install: open the sign-in '
              'address in your browser',
        ],
      ),
      const SizedBox(height: 24),
      const _GuideSection(
        number: 3,
        title: 'Sign in',
        steps: [
          'Open the app and enter the sign-in address from your account '
              'card above',
          'Sign in with your username and the password you chose when you '
              'created the account',
          'Forgot the password? Your admin can reset it on the server',
        ],
      ),
      const SizedBox(height: 24),
      _GuideSection(
        number: 4,
        title: 'Start watching',
        steps: [
          'Everything you request in Cantinarr shows up in $where once '
              'it is Available',
          'Missing something? Ask your admin',
        ],
      ),
      const SizedBox(height: 24),
      const _TipCard(
        title: 'Request here, watch there',
        message: 'When a request shows as Available in Cantinarr, it is '
            'ready to play on your server.',
      ),
    ];
  }

  /// One server's account state: nothing yet (create it), turned off (ask
  /// the admin), or active (username, where to sign in, and whether the
  /// server confirmed the account just now).
  Widget _buildAccountCard(MediaServerAccess server, String username) {
    final account = server.account;
    final Widget body;
    if (account == null) {
      body = Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            'You have access to ${server.name}. Create your account to '
            'start watching.',
            style: const TextStyle(
              color: AppTheme.textSecondary,
              fontSize: 14,
              height: 1.4,
            ),
          ),
          const SizedBox(height: 12),
          ElevatedButton.icon(
            onPressed: () => _createAccount(server, username),
            icon: const Icon(Icons.person_add_alt_1_outlined, size: 18),
            label: const Text('Create my account'),
          ),
        ],
      );
    } else if (account.disabled) {
      body = Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          const Padding(
            padding: EdgeInsets.only(top: 1),
            child: Icon(Icons.block, color: AppTheme.unavailable, size: 18),
          ),
          const SizedBox(width: 8),
          Expanded(
            child: Text(
              'Your access to ${server.name} is turned off. Ask your admin '
              "if you think that's a mistake.",
              style: const TextStyle(
                color: AppTheme.textSecondary,
                fontSize: 14,
                height: 1.4,
              ),
            ),
          ),
        ],
      );
    } else {
      body = Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              const Icon(Icons.check_circle,
                  color: AppTheme.available, size: 18),
              const SizedBox(width: 8),
              Expanded(
                child: Text(
                  server.name,
                  style: const TextStyle(
                    color: AppTheme.textPrimary,
                    fontWeight: FontWeight.w500,
                  ),
                ),
              ),
            ],
          ),
          const SizedBox(height: 10),
          Row(
            children: [
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    const Text(
                      'Username',
                      style: TextStyle(
                          color: AppTheme.textSecondary, fontSize: 12),
                    ),
                    Text(
                      account.username,
                      style: const TextStyle(
                        color: AppTheme.textPrimary,
                        fontWeight: FontWeight.w500,
                      ),
                    ),
                  ],
                ),
              ),
              IconButton(
                tooltip: 'Copy username',
                icon: const Icon(Icons.copy, size: 18),
                onPressed: () => _copy(account.username, 'Username copied'),
              ),
            ],
          ),
          const SizedBox(height: 8),
          if (server.publicAddress.isNotEmpty) ...[
            Text(
              'Sign in at ${server.publicAddress}',
              style: const TextStyle(
                color: AppTheme.textPrimary,
                fontSize: 13,
                height: 1.4,
              ),
            ),
            Wrap(
              spacing: 8,
              children: [
                TextButton.icon(
                  onPressed: () =>
                      _copy(server.publicAddress, 'Address copied'),
                  icon: const Icon(Icons.copy, size: 16),
                  label: const Text('Copy address'),
                ),
                TextButton.icon(
                  onPressed: () => _open(server.publicAddress),
                  icon: const Icon(Icons.open_in_new, size: 16),
                  label: const Text('Open'),
                ),
              ],
            ),
          ] else
            const Text(
              "Your admin hasn't shared the sign-in address yet. Ask them "
              'where to sign in.',
              style: TextStyle(
                color: AppTheme.textSecondary,
                fontSize: 13,
                height: 1.4,
              ),
            ),
          if (!account.verified) ...[
            const SizedBox(height: 8),
            const Row(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Padding(
                  padding: EdgeInsets.only(top: 1),
                  child: Icon(Icons.info_outline,
                      color: AppTheme.warning, size: 16),
                ),
                SizedBox(width: 6),
                Expanded(
                  child: Text(
                    "We couldn't confirm this account with the server just "
                    'now. Signing in should still work.',
                    style: TextStyle(
                      color: AppTheme.warning,
                      fontSize: 12,
                      height: 1.4,
                    ),
                  ),
                ),
              ],
            ),
          ],
        ],
      );
    }
    return Container(
      width: double.infinity,
      padding: const EdgeInsets.all(16),
      decoration: BoxDecoration(
        color: AppTheme.accent.withValues(alpha: 0.08),
        borderRadius: BorderRadius.circular(12),
        border: Border.all(color: AppTheme.accent.withValues(alpha: 0.2)),
      ),
      child: body,
    );
  }
}

/// Numbered section header: the accent number bubble plus the section title.
class _SectionHeader extends StatelessWidget {
  final int number;
  final String title;

  const _SectionHeader({required this.number, required this.title});

  @override
  Widget build(BuildContext context) {
    return Row(
      children: [
        Container(
          width: 32,
          height: 32,
          decoration: BoxDecoration(
            color: AppTheme.accent.withValues(alpha: 0.15),
            shape: BoxShape.circle,
          ),
          child: Center(
            child: Text(
              '$number',
              style: const TextStyle(
                color: AppTheme.accent,
                fontWeight: FontWeight.bold,
              ),
            ),
          ),
        ),
        const SizedBox(width: 12),
        Expanded(
          child: Text(
            title,
            style: const TextStyle(
              color: AppTheme.textPrimary,
              fontSize: 18,
              fontWeight: FontWeight.w600,
            ),
          ),
        ),
      ],
    );
  }
}

class _GuideSection extends StatelessWidget {
  final int number;
  final String title;
  final List<String> steps;

  const _GuideSection({
    required this.number,
    required this.title,
    required this.steps,
  });

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        _SectionHeader(number: number, title: title),
        const SizedBox(height: 12),
        ...steps.map((step) => Padding(
              padding: const EdgeInsets.only(left: 44, bottom: 8),
              child: Row(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  const Text('• ',
                      style: TextStyle(color: AppTheme.textSecondary)),
                  Expanded(
                    child: Text(
                      step,
                      style: const TextStyle(
                        color: AppTheme.textSecondary,
                        fontSize: 14,
                        height: 1.4,
                      ),
                    ),
                  ),
                ],
              ),
            )),
      ],
    );
  }
}

class _TipCard extends StatelessWidget {
  final String title;
  final String message;

  const _TipCard({required this.title, required this.message});

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(16),
      decoration: BoxDecoration(
        color: AppTheme.accent.withValues(alpha: 0.08),
        borderRadius: BorderRadius.circular(12),
        border: Border.all(color: AppTheme.accent.withValues(alpha: 0.2)),
      ),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          const Icon(Icons.lightbulb_outline, color: AppTheme.accent, size: 20),
          const SizedBox(width: 12),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(title,
                    style: const TextStyle(
                        color: AppTheme.accent, fontWeight: FontWeight.w600)),
                const SizedBox(height: 4),
                Text(message,
                    style: const TextStyle(
                        color: AppTheme.textSecondary,
                        fontSize: 13,
                        height: 1.4)),
              ],
            ),
          ),
        ],
      ),
    );
  }
}
