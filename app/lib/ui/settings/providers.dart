import 'package:flutter/material.dart';
import 'package:provider/provider.dart';
import 'package:url_launcher/url_launcher.dart';

import '../../api/models.dart';
import '../../state/app_state.dart';
import '../setup/setup_wizard.dart' show providerChoices, providerTitle;
import '../widgets/common.dart';
import 'rows.dart';

/// Providers: one row per provider with its key status; Change expands the
/// row for the key, OAuth, or the Ollama address. Keys live only here.
class ProvidersSection extends StatefulWidget {
  const ProvidersSection({super.key});
  @override
  State<ProvidersSection> createState() => _ProvidersSectionState();
}

class _ProvidersSectionState extends State<ProvidersSection> {
  ServerSettings? _s;
  Object? _error;
  String? _open;
  final _key = TextEditingController();
  final _url = TextEditingController();
  bool _busy = false;
  bool _oauthWaiting = false;

  @override
  void initState() {
    super.initState();
    _load();
  }

  @override
  void dispose() {
    _oauthWaiting = false;
    _key.dispose();
    _url.dispose();
    super.dispose();
  }

  Future<void> _load() async {
    try {
      final s = await context.read<AppState>().api.settings();
      if (mounted) setState(() => _s = s);
    } catch (e) {
      if (mounted) setState(() => _error = e);
    }
  }

  Future<void> _patch(String name, Map<String, dynamic> fields) async {
    setState(() => _busy = true);
    try {
      final s = await context.read<AppState>().api.updateSettings({
        'providers': {name: fields},
      });
      if (!mounted) return;
      setState(() {
        _s = s;
        _key.clear();
      });
      showSaved(context);
      await context.read<AppState>().checkHealth();
    } catch (e) {
      if (mounted) showError(context, e);
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _oauth(String name) async {
    final api = context.read<AppState>().api;
    setState(() {
      _busy = true;
      _oauthWaiting = true;
    });
    try {
      final url = await api.oauthStart(name);
      if (url.isNotEmpty) {
        await launchUrl(Uri.parse(url), mode: LaunchMode.externalApplication);
      }
      final deadline = DateTime.now().add(const Duration(minutes: 5));
      while (mounted && _oauthWaiting && DateTime.now().isBefore(deadline)) {
        await Future<void>.delayed(const Duration(seconds: 2));
        final s = await api.settings();
        if (s.provider(name)?.keyStatus == 'set') {
          if (mounted) {
            setState(() => _s = s);
            showSaved(context);
          }
          return;
        }
      }
      if (mounted && _oauthWaiting) {
        showError(
          context,
          'OpenRouter did not report a key. Paste one instead.',
        );
      }
    } catch (e) {
      if (mounted) showError(context, e);
    } finally {
      if (mounted) {
        setState(() {
          _busy = false;
          _oauthWaiting = false;
        });
      }
    }
  }

  String _status(ProviderInfo? p, bool local, bool oauth) {
    if (local) {
      return 'Running at ${_host(p?.baseUrl ?? 'http://127.0.0.1:11434/v1')}';
    }
    if (p == null) return 'No key';
    return switch (p.keyStatus) {
      'env' => 'From environment',
      'set' =>
        oauth && p.keyHint.isEmpty
            ? 'Connected'
            : 'Key stored · ends with ${p.keyHint}',
      _ => 'No key',
    };
  }

  String _host(String url) => Uri.tryParse(url)?.authority ?? url;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final s = _s;
    if (s == null) {
      return SettingsPage(
        title: 'Providers',
        children: [
          _error != null
              ? Text(describeError(_error!), style: theme.textTheme.bodySmall)
              : const LinearProgressIndicator(minHeight: 2),
        ],
      );
    }
    return SettingsPage(
      title: 'Providers',
      children: [
        for (final c in providerChoices.where((c) => !c.none))
          _row(context, s, c.name, c.local, c.oauth, c.keyUrl, c.keyLabel),
      ],
    );
  }

  Widget _row(
    BuildContext context,
    ServerSettings s,
    String name,
    bool local,
    bool oauth,
    String? keyUrl,
    String keyLabel,
  ) {
    final theme = Theme.of(context);
    final p = s.provider(name);
    final open = _open == name;
    final hasKey = p?.hasKey ?? false;
    return SettingsRow(
      key: Key('provider-$name'),
      label: providerTitle(name),
      hint: _status(p, local, oauth),
      trailing: RowButton(
        open ? 'Close' : 'Change',
        key: Key('provider-change-$name'),
        onPressed: () => setState(() {
          _open = open ? null : name;
          _key.clear();
          _url.text = p?.baseUrl ?? 'http://127.0.0.1:11434/v1';
        }),
      ),
      child: !open
          ? null
          : Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                if (local) ...[
                  TextField(
                    key: const Key('ollama-url'),
                    controller: _url,
                    decoration: fieldDecoration(
                      context,
                      label: 'Ollama address',
                      hint: 'http://127.0.0.1:11434/v1',
                    ),
                    onSubmitted: (v) => _patch(name, {'base_url': v.trim()}),
                  ),
                  const SizedBox(height: 8),
                  Row(
                    children: [
                      TextButton(
                        onPressed: _busy
                            ? null
                            : () =>
                                  _patch(name, {'base_url': _url.text.trim()}),
                        child: const Text('Save address'),
                      ),
                    ],
                  ),
                ] else ...[
                  TextField(
                    key: Key('provider-key-$name'),
                    controller: _key,
                    obscureText: true,
                    autofocus: true,
                    decoration: fieldDecoration(
                      context,
                      label: hasKey
                          ? 'New ${providerTitle(name)} key'
                          : '${providerTitle(name)} key',
                      hint: 'sk-…',
                    ),
                    onChanged: (_) => setState(() {}),
                    onSubmitted: (v) {
                      if (v.trim().isNotEmpty) {
                        _patch(name, {'api_key': v.trim()});
                      }
                    },
                  ),
                  const SizedBox(height: 8),
                  Row(
                    children: [
                      TextButton(
                        key: Key('provider-replace-$name'),
                        onPressed: _busy || _key.text.trim().isEmpty
                            ? null
                            : () => _patch(name, {'api_key': _key.text.trim()}),
                        child: Text(hasKey ? 'Replace' : 'Save key'),
                      ),
                      if (hasKey && p?.keyStatus != 'env')
                        TextButton(
                          key: Key('provider-remove-$name'),
                          onPressed: _busy
                              ? null
                              : () => _patch(name, {'api_key': ''}),
                          child: const Text('Remove'),
                        ),
                      if (oauth)
                        TextButton(
                          key: Key('provider-oauth-$name'),
                          onPressed: _busy ? null : () => _oauth(name),
                          child: Text(
                            _oauthWaiting
                                ? 'Waiting for ${providerTitle(name)}…'
                                : 'Connect with ${providerTitle(name)}',
                          ),
                        ),
                      const Spacer(),
                      if (keyUrl != null)
                        LinkedText(
                          style: theme.textTheme.labelSmall,
                          parts: [
                            const TextPart('Get a key at '),
                            TextPart(
                              keyLabel,
                              glue: true,
                              onTap: () => launchUrl(
                                Uri.parse(keyUrl),
                                mode: LaunchMode.externalApplication,
                              ),
                            ),
                          ],
                        ),
                    ],
                  ),
                ],
              ],
            ),
    );
  }
}
