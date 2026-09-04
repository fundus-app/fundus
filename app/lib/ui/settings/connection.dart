import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../../state/app_state.dart';
import '../../state/settings.dart';
import '../theme.dart';
import '../widgets/common.dart';
import 'rows.dart';

/// Connection: where Fundus runs. One status row; "Change…" reveals the
/// address and token fields with the reconnect action.
class ConnectionSection extends StatefulWidget {
  const ConnectionSection({super.key});
  @override
  State<ConnectionSection> createState() => _ConnectionSectionState();
}

class _ConnectionSectionState extends State<ConnectionSection> {
  late final TextEditingController _url;
  late final TextEditingController _token;
  bool _editing = false;

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

  Future<void> _save() async {
    final settings = context.read<Settings>();
    if (_url.text.trim() != settings.serverUrl ||
        _token.text.trim() != settings.token) {
      await settings.setServer(_url.text, _token.text);
      if (mounted) showSaved(context);
    }
    if (mounted) setState(() => _editing = false);
  }

  @override
  Widget build(BuildContext context) {
    final settings = context.watch<Settings>();
    final state = context.watch<AppState>();
    final scheme = Theme.of(context).colorScheme;
    final host =
        Uri.tryParse(settings.serverUrl)?.authority ?? settings.serverUrl;
    final status = state.connected
        ? 'Connected to $host · live updates on'
        : state.reachable
        ? 'Connected to $host · live updates reconnecting'
        : describeError(
            state.lastError ?? 'unreachable',
            serverUrl: settings.serverUrl,
          );
    return SettingsPage(
      title: 'Connection',
      children: [
        SettingsRow(
          leading: Icon(
            Icons.circle,
            size: 10,
            color: state.connected
                ? scheme.success
                : (state.reachable ? scheme.warning : scheme.error),
          ),
          label: status,
          hint: settings.token.isEmpty ? null : 'Access token stored',
          trailing: _editing
              ? null
              : RowButton(
                  'Change…',
                  key: const Key('connection-change'),
                  onPressed: () => setState(() => _editing = true),
                ),
          child: _editing
              ? Column(
                  crossAxisAlignment: CrossAxisAlignment.stretch,
                  children: [
                    TextField(
                      key: const Key('connection-url'),
                      controller: _url,
                      autofocus: true,
                      decoration: fieldDecoration(
                        context,
                        label: 'Fundus address',
                        hint: 'http://127.0.0.1:7433',
                      ),
                      onSubmitted: (_) => _save(),
                    ),
                    const SizedBox(height: 10),
                    TextField(
                      key: const Key('connection-token'),
                      controller: _token,
                      obscureText: true,
                      decoration: fieldDecoration(
                        context,
                        label: 'Access token',
                        helper: 'Not needed on this machine.',
                      ),
                      onSubmitted: (_) => _save(),
                    ),
                    const SizedBox(height: 10),
                    Row(
                      children: [
                        FilledButton(
                          key: const Key('connection-save'),
                          onPressed: _save,
                          child: const Text('Save and reconnect'),
                        ),
                        const SizedBox(width: 6),
                        TextButton(
                          onPressed: () => setState(() {
                            _url.text = settings.serverUrl;
                            _token.text = settings.token;
                            _editing = false;
                          }),
                          child: const Text('Cancel'),
                        ),
                      ],
                    ),
                  ],
                )
              : null,
        ),
      ],
    );
  }
}
