import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:provider/provider.dart';
import 'package:url_launcher/url_launcher.dart';

import '../state/app_state.dart';
import '../state/settings.dart';
import 'setup/setup_wizard.dart';
import 'theme.dart';
import 'widgets/common.dart';

/// Settings: server, model & provider, autonomy, theme, backup, about.
/// Everything saves on change and confirms with a "Saved" toast.
class SettingsScreen extends StatefulWidget {
  const SettingsScreen({super.key});

  static Future<void> show(BuildContext context) => showDialog<void>(
    context: context,
    builder: (_) =>
        const Dialog(child: SizedBox(width: 560, child: SettingsScreen())),
  );

  @override
  State<SettingsScreen> createState() => _SettingsScreenState();
}

class _SettingsScreenState extends State<SettingsScreen> {
  late final TextEditingController _url;
  late final TextEditingController _token;

  @override
  void initState() {
    super.initState();
    final s = context.read<Settings>();
    _url = TextEditingController(text: s.serverUrl);
    _token = TextEditingController(text: s.token);
  }

  @override
  void dispose() {
    _url.dispose();
    _token.dispose();
    super.dispose();
  }

  Future<void> _saveServer() async {
    final settings = context.read<Settings>();
    if (_url.text.trim() == settings.serverUrl &&
        _token.text.trim() == settings.token) {
      return;
    }
    await settings.setServer(_url.text, _token.text);
    if (mounted) showSaved(context);
  }

  @override
  Widget build(BuildContext context) {
    final settings = context.watch<Settings>();
    final state = context.watch<AppState>();
    final theme = Theme.of(context);
    final scheme = theme.colorScheme;
    return SingleChildScrollView(
      padding: const EdgeInsets.all(24),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        mainAxisSize: MainAxisSize.min,
        children: [
          Row(
            children: [
              Expanded(
                child: Text('Settings', style: theme.textTheme.headlineMedium),
              ),
              IconButton(
                tooltip: 'Close',
                icon: const Icon(Icons.close_rounded),
                onPressed: () => Navigator.of(context).maybePop(),
              ),
            ],
          ),
          const SectionLabel('Fundus'),
          Focus(
            onFocusChange: (f) {
              if (!f) _saveServer();
            },
            child: TextField(
              controller: _url,
              decoration: const InputDecoration(
                labelText: 'Fundus address',
                hintText: 'http://127.0.0.1:7433',
              ),
              onSubmitted: (_) => _saveServer(),
            ),
          ),
          const SizedBox(height: 10),
          Focus(
            onFocusChange: (f) {
              if (!f) _saveServer();
            },
            child: TextField(
              controller: _token,
              obscureText: true,
              decoration: const InputDecoration(
                labelText: 'Access token (not needed on this machine)',
              ),
              onSubmitted: (_) => _saveServer(),
            ),
          ),
          const SizedBox(height: 10),
          Row(
            children: [
              Icon(
                Icons.circle,
                size: 10,
                color: state.connected
                    ? scheme.success
                    : (state.reachable ? scheme.warning : scheme.error),
              ),
              const SizedBox(width: 8),
              Expanded(
                child: Text(
                  state.connected
                      ? 'Connected. Live updates are on.'
                      : state.reachable
                      ? 'Reachable. The event stream is reconnecting.'
                      : describeError(
                          state.lastError ?? 'unreachable',
                          serverUrl: settings.serverUrl,
                        ),
                  style: theme.textTheme.bodySmall,
                  maxLines: 2,
                  overflow: TextOverflow.ellipsis,
                ),
              ),
              TextButton(
                onPressed: _saveServer,
                child: const Text('Save and reconnect'),
              ),
            ],
          ),
          const SectionLabel('Model & provider'),
          const ProviderSection(),
          const SectionLabel('Autonomy'),
          const AutonomySection(),
          const SectionLabel('Appearance'),
          SegmentedButton<ThemeMode>(
            segments: const [
              ButtonSegment(
                value: ThemeMode.system,
                label: Text('System'),
                icon: Icon(Icons.brightness_auto_rounded, size: 16),
              ),
              ButtonSegment(
                value: ThemeMode.light,
                label: Text('Paper'),
                icon: Icon(Icons.light_mode_outlined, size: 16),
              ),
              ButtonSegment(
                value: ThemeMode.dark,
                label: Text('Ink'),
                icon: Icon(Icons.dark_mode_outlined, size: 16),
              ),
            ],
            selected: {settings.themeMode},
            showSelectedIcon: false,
            onSelectionChanged: (s) async {
              await settings.setThemeMode(s.first);
              if (context.mounted) showSaved(context);
            },
          ),
          const SectionLabel('Export'),
          Text(
            'Your data in open formats. JSON is the complete model; Markdown is a readable copy with the ids kept in each file\'s header.',
            style: theme.textTheme.bodySmall,
          ),
          const SizedBox(height: 8),
          Row(
            children: [
              OutlinedButton.icon(
                icon: const Icon(Icons.data_object_rounded, size: 16),
                label: const Text('JSON'),
                onPressed: () => launchUrl(
                  Uri.parse(state.api.exportUrl('json')),
                  mode: LaunchMode.externalApplication,
                ),
              ),
              const SizedBox(width: 8),
              OutlinedButton.icon(
                icon: const Icon(Icons.description_outlined, size: 16),
                label: const Text('Markdown (zip)'),
                onPressed: () => launchUrl(
                  Uri.parse(state.api.exportUrl('markdown')),
                  mode: LaunchMode.externalApplication,
                ),
              ),
            ],
          ),
          const SectionLabel('Backup'),
          Text(
            'A consistent zip of the event log and the latest snapshot. Restore it by unpacking into an empty data directory.',
            style: theme.textTheme.bodySmall,
          ),
          const SizedBox(height: 8),
          Row(
            children: [
              OutlinedButton.icon(
                icon: const Icon(Icons.archive_outlined, size: 16),
                label: const Text('Download backup'),
                onPressed: () => launchUrl(
                  Uri.parse(state.api.backupUrl()),
                  mode: LaunchMode.externalApplication,
                ),
              ),
              const SizedBox(width: 8),
              if (state.daemonLogPath != null)
                OutlinedButton.icon(
                  icon: const Icon(Icons.folder_open_outlined, size: 16),
                  label: const Text('Open log folder'),
                  onPressed: () {
                    final dir = Uri.file(state.daemonLogPath!).resolve('.');
                    launchUrl(
                      dir,
                      mode: LaunchMode.externalApplication,
                    ).catchError((_) => false);
                  },
                ),
            ],
          ),
          if (state.daemonLogPath != null)
            Padding(
              padding: const EdgeInsets.only(top: 6),
              child: SelectableText(
                'Log: ${state.daemonLogPath}',
                style: monoStyle(context, size: 11),
              ),
            ),
          if (!kIsWeb)
            Padding(
              padding: const EdgeInsets.only(top: 6),
              child: SelectableText(
                'curl -o fundus-backup.zip ${state.api.backupUrl()}',
                style: monoStyle(context, size: 11),
              ),
            ),
          const SectionLabel('About'),
          Row(
            children: [
              const FundusMark(size: 48),
              const SizedBox(width: 14),
              Text('Fundus', style: theme.textTheme.displaySmall),
            ],
          ),
          const SizedBox(height: 10),
          DefaultTextStyle(
            style: theme.textTheme.bodySmall!,
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  'Fundus ${state.health?.version ?? ''}  ·  ${kIsWeb ? 'web app' : 'desktop app'}',
                ),
                if (state.health != null) ...[
                  _AboutRow('Filing model', modelDisplay(state.health!.triage)),
                  _AboutRow(
                    'Conversation model',
                    modelDisplay(state.health!.chat),
                  ),
                  Text(
                    'Event log: ${_thousands(state.health!.seq)} ${state.health!.seq == 1 ? 'entry' : 'entries'}',
                  ),
                  if (state.health!.timezone.isNotEmpty)
                    Text('Time zone: ${state.health!.timezone}'),
                ],
                const SizedBox(height: 6),
                const Text(
                  'Capture anything. Let your AI maintain the rest. Keep every fact under your control.',
                ),
              ],
            ),
          ),
          const SectionLabel('Keyboard'),
          Wrap(
            spacing: 14,
            runSpacing: 6,
            children: const [
              _Key('Ctrl K', 'capture'),
              _Key('Ctrl N', 'capture'),
              _Key('Ctrl F', 'search'),
              _Key('Ctrl 1…9', 'views'),
              _Key('Ctrl 0 / Ctrl J', 'conversation'),
              _Key('Ctrl Z', 'undo the latest change'),
              _Key('Ctrl ,', 'settings'),
              _Key('Ctrl ↵', 'save in an editor'),
              _Key('Esc', 'close'),
            ],
          ),
        ],
      ),
    );
  }
}

String _thousands(int n) {
  final s = n.toString();
  final out = StringBuffer();
  for (var i = 0; i < s.length; i++) {
    if (i > 0 && (s.length - i) % 3 == 0) out.write(',');
    out.write(s[i]);
  }
  return out.toString();
}

/// "Saved" toast used by every setting that saves on change.
void showSaved(BuildContext context) {
  final m = ScaffoldMessenger.maybeOf(context);
  m?.hideCurrentSnackBar();
  m?.showSnackBar(
    const SnackBar(content: Text('Saved.'), duration: Duration(seconds: 2)),
  );
}

/// Label (muted) and value (regular) on one line.
class _AboutRow extends StatelessWidget {
  const _AboutRow(this.label, this.value);
  final String label, value;
  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Padding(
      padding: const EdgeInsets.only(bottom: 2),
      child: Row(
        children: [
          SizedBox(
            width: 150,
            child: Text(label, style: secondaryStyle(context)),
          ),
          Expanded(child: Text(value, style: theme.textTheme.bodyMedium)),
        ],
      ),
    );
  }
}

class _Key extends StatelessWidget {
  const _Key(this.k, this.what);
  final String k, what;
  @override
  Widget build(BuildContext context) => Row(
    mainAxisSize: MainAxisSize.min,
    children: [
      KeyHint(k),
      const SizedBox(width: 6),
      Text(what, style: Theme.of(context).textTheme.labelSmall),
    ],
  );
}
