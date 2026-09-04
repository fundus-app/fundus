import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:provider/provider.dart';
import 'package:url_launcher/url_launcher.dart';

import '../../api/models.dart';
import '../../state/app_state.dart';
import '../widgets/common.dart';
import 'rows.dart';

/// About: version, what is on, where things live, export and backup.
class AboutSection extends StatefulWidget {
  const AboutSection({super.key});
  @override
  State<AboutSection> createState() => _AboutSectionState();
}

class _AboutSectionState extends State<AboutSection> {
  ServerSettings? _s;

  @override
  void initState() {
    super.initState();
    context
        .read<AppState>()
        .api
        .settings()
        .then((s) {
          if (mounted) setState(() => _s = s);
        })
        .catchError((_) {});
  }

  @override
  Widget build(BuildContext context) {
    final state = context.watch<AppState>();
    final theme = Theme.of(context);
    final h = state.health;
    final version =
        'Fundus ${h?.version ?? ''}  ·  ${kIsWeb ? 'web app' : 'desktop app'}';
    return SettingsPage(
      title: 'About',
      children: [
        SettingsRow(
          label: version,
          hint: 'Capture anything. Let your AI maintain the rest. Keep every fact under your control.',
          trailing: LinkedText(
            style: theme.textTheme.bodyMedium,
            parts: [
              TextPart(
                'fundus-app.de',
                onTap: () => launchUrl(
                  Uri.parse('https://fundus-app.de'),
                  mode: LaunchMode.externalApplication,
                ),
              ),
            ],
          ),
        ),
        SettingsRow(
          label: 'Semantic search',
          hint: 'Needs an embedding model under Models.',
          trailing: RowValue(h?.embedding == true ? 'on' : 'off'),
        ),
        SettingsRow(
          label: 'Event log',
          trailing: RowValue(
            '${thousands(h?.seq ?? 0)} ${h?.seq == 1 ? 'entry' : 'entries'}',
          ),
        ),
        if (_s?.path.isNotEmpty ?? false)
          SettingsRow(
            label: 'Configuration file',
            trailing: CopyableLine(_s!.path),
          ),
        if (state.daemonLogPath != null)
          SettingsRow(
            label: 'Log file',
            trailing: Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                Flexible(child: CopyableLine(state.daemonLogPath!)),
                RowButton(
                  'Open folder',
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
          ),
        SettingsRow(
          label: 'Backup',
          hint: 'A consistent zip of the event log and the latest snapshot. Restore it by unpacking into an empty data directory.',
          trailing: Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              if (!kIsWeb)
                Flexible(
                  child: CopyableLine(
                    'curl -o fundus-backup.zip ${state.api.backupUrl()}',
                  ),
                ),
              RowButton(
                'Download',
                onPressed: () => launchUrl(
                  Uri.parse(state.api.backupUrl()),
                  mode: LaunchMode.externalApplication,
                ),
              ),
            ],
          ),
        ),
        SettingsRow(
          label: 'Export',
          hint: 'JSON is the complete model; Markdown is a readable copy with the ids in each file\'s header.',
          trailing: Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              RowButton(
                'JSON',
                onPressed: () => launchUrl(
                  Uri.parse(state.api.exportUrl('json')),
                  mode: LaunchMode.externalApplication,
                ),
              ),
              RowButton(
                'Markdown (zip)',
                onPressed: () => launchUrl(
                  Uri.parse(state.api.exportUrl('markdown')),
                  mode: LaunchMode.externalApplication,
                ),
              ),
            ],
          ),
        ),
        const SettingsRow(
          label: 'Keyboard',
          hint: 'Ctrl K capture · Ctrl F search · Ctrl 1…9 views · Ctrl 0 / Ctrl J conversation · Ctrl Z undo · Ctrl , settings · Ctrl ↵ save in an editor · Ctrl Shift K dictate · Esc close',
        ),
        SettingsRow(
          label: 'License',
          trailing: LinkedText(
            style: theme.textTheme.bodyMedium,
            parts: [
              TextPart(
                'AGPL-3.0',
                onTap: () => launchUrl(
                  Uri.parse('https://www.gnu.org/licenses/agpl-3.0.html'),
                  mode: LaunchMode.externalApplication,
                ),
              ),
            ],
          ),
        ),
      ],
    );
  }
}

/// 1234 → "1,234".
String thousands(int n) {
  final s = n.toString();
  final out = StringBuffer();
  for (var i = 0; i < s.length; i++) {
    if (i > 0 && (s.length - i) % 3 == 0) out.write(',');
    out.write(s[i]);
  }
  return out.toString();
}
