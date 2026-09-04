import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../../api/models.dart';
import '../../state/app_state.dart';
import '../widgets/common.dart';
import 'rows.dart';

/// Research: automatic start, which search backend, and its credentials
/// (shown only for the chosen backend). The research model lives under
/// Models.
class ResearchSection extends StatefulWidget {
  const ResearchSection({super.key});
  @override
  State<ResearchSection> createState() => _ResearchSectionState();
}

class _ResearchSectionState extends State<ResearchSection> {
  ServerSettings? _s;
  Object? _error;
  final _brave = TextEditingController();
  final _searxng = TextEditingController();

  static const backends = {
    'auto': 'Automatic',
    'openai': 'OpenAI search',
    'openrouter': 'OpenRouter',
    'brave': 'Brave',
    'searxng': 'SearXNG',
  };

  static String statusFor(String backend) => switch (backend) {
    'openai' => "OpenAI's built-in web search.",
    'openrouter' => "OpenRouter's web search plugin.",
    'brave' => 'Brave Search API, with the key below.',
    'searxng' => 'Your own SearXNG instance, at the address below.',
    _ => "Automatic: uses OpenAI's search when a key is stored, else Brave or SearXNG if set up.",
  };

  @override
  void initState() {
    super.initState();
    _load();
  }

  @override
  void dispose() {
    _brave.dispose();
    _searxng.dispose();
    super.dispose();
  }

  Future<void> _load() async {
    try {
      final s = await context.read<AppState>().api.settings();
      if (mounted) {
        setState(() {
          _s = s;
          _searxng.text = s.research.searxngUrl;
        });
      }
    } catch (e) {
      if (mounted) setState(() => _error = e);
    }
  }

  Future<void> _save(Map<String, dynamic> patch) async {
    try {
      final s = await context.read<AppState>().api.updateSettings({
        'research': patch,
      });
      if (!mounted) return;
      setState(() => _s = s);
      showSaved(context);
      await context.read<AppState>().checkHealth();
    } catch (e) {
      if (mounted) showError(context, e);
    }
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final s = _s;
    if (s == null) {
      return SettingsPage(
        title: 'Research',
        children: [
          _error != null
              ? Text(describeError(_error!), style: theme.textTheme.bodySmall)
              : const LinearProgressIndicator(minHeight: 2),
        ],
      );
    }
    final r = s.research;
    final backend = backends.containsKey(r.backend) ? r.backend : 'auto';
    return SettingsPage(
      title: 'Research',
      children: [
        SettingsRow(
          label: 'Start research automatically when you ask for it',
          hint: 'Off: research tasks wait for “Research this”.',
          trailing: Switch(
            key: const Key('research-auto'),
            value: r.auto,
            onChanged: (v) => _save({'auto': v}),
          ),
        ),
        SettingsRow(
          label: 'Search through',
          hint: r.available
              ? statusFor(backend)
              : '${statusFor(backend)} Not available yet.',
          trailing: SizedBox(
            width: 220,
            child: DropdownButtonFormField<String>(
              key: const Key('research-backend'),
              initialValue: backend,
              isExpanded: true,
              decoration: fieldDecoration(context),
              items: [
                for (final e in backends.entries)
                  DropdownMenuItem(value: e.key, child: Text(e.value)),
              ],
              onChanged: (v) => _save({'backend': v ?? 'auto'}),
            ),
          ),
        ),
        if (backend == 'brave')
          SettingsRow(
            label: 'Brave key',
            hint: switch (r.braveKeyStatus) {
              'env' => 'From environment',
              'set' => 'Key stored',
              _ => 'No key · free at brave.com/search/api',
            },
            trailing: SizedBox(
              width: 260,
              child: TextField(
                key: const Key('research-brave-key'),
                controller: _brave,
                obscureText: true,
                decoration: fieldDecoration(
                  context,
                  hint: r.braveKeySet ? 'Replace key' : 'BSA…',
                ),
                onSubmitted: (v) {
                  if (v.trim().isEmpty) return;
                  _brave.clear();
                  _save({'brave_api_key': v.trim()});
                },
              ),
            ),
          ),
        if (backend == 'searxng')
          SettingsRow(
            label: 'SearXNG address',
            hint: r.searxngUrl.isEmpty ? 'Not set' : null,
            trailing: SizedBox(
              width: 260,
              child: TextField(
                key: const Key('research-searxng'),
                controller: _searxng,
                decoration: fieldDecoration(
                  context,
                  hint: 'https://searx.example.org',
                ),
                onSubmitted: (v) => _save({'searxng_url': v.trim()}),
              ),
            ),
          ),
      ],
    );
  }
}
