import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../../api/models.dart';
import '../../state/app_state.dart';
import '../setup/setup_wizard.dart' show RoleModelPicker, providerTitle;
import '../widgets/common.dart';
import '../widgets/toasts.dart';
import 'rows.dart';

/// Models: one row per role with the model in use and a Change button that
/// opens the picker (provider + model list with recommendations).
class ModelsSection extends StatefulWidget {
  const ModelsSection({super.key});
  @override
  State<ModelsSection> createState() => _ModelsSectionState();
}

class _ModelsSectionState extends State<ModelsSection> {
  ServerSettings? _s;
  Object? _error;
  bool _testing = false;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    try {
      final s = await context.read<AppState>().api.settings();
      if (mounted) setState(() => _s = s);
    } catch (e) {
      if (mounted) setState(() => _error = e);
    }
  }

  Future<void> _change(String role) async {
    final s = _s;
    if (s == null) return;
    final picked = await showDialog<RoleRef>(
      context: context,
      builder: (_) => RolePickerDialog(role: role, settings: s),
    );
    if (picked == null || !mounted) return;
    final patch = switch (role) {
      'research' => {
        'research': {'provider': picked.provider, 'model': picked.model},
      },
      'embedding' => {
        'embedding': {'provider': picked.provider, 'model': picked.model},
      },
      _ => {
        role: {'provider': picked.provider, 'model': picked.model},
      },
    };
    try {
      final updated = await context.read<AppState>().api.updateSettings(patch);
      if (!mounted) return;
      setState(() => _s = updated);
      showSaved(context);
      await context.read<AppState>().checkHealth();
    } catch (e) {
      if (mounted) showError(context, e);
    }
  }

  Future<void> _test() async {
    final s = _s;
    if (s == null || s.triage.provider.isEmpty) return;
    setState(() => _testing = true);
    try {
      final p = await context.read<AppState>().api.testProvider(
        s.triage.provider,
        model: s.triage.model,
      );
      if (!mounted) return;
      final ms = p.latency.inMilliseconds;
      showToast(
        context,
        p.reachable
            ? '${providerTitle(s.triage.provider)} answered in $ms ms${p.structured ? '' : ' but without structured output'}.'
            : 'No answer from ${providerTitle(s.triage.provider)}: ${p.errors.join(', ')}',
        key: 'probe',
        error: !p.reachable,
      );
    } catch (e) {
      if (mounted) showError(context, e);
    } finally {
      if (mounted) setState(() => _testing = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final s = _s;
    if (s == null) {
      return SettingsPage(
        title: 'Models',
        children: [
          _error != null
              ? Text(describeError(_error!), style: theme.textTheme.bodySmall)
              : const LinearProgressIndicator(minHeight: 2),
        ],
      );
    }
    final fallback = s.chat;
    String value(RoleRef r) {
      if (r.model.isEmpty) {
        return fallback.model.isEmpty
            ? 'Not set'
            : '${roleValue(fallback)} · same as conversation';
      }
      return roleValue(r);
    }

    Widget role(
      String key,
      String label,
      String hint,
      RoleRef r, {
      String? unavailable,
    }) => SettingsRow(
      key: Key('role-$key'),
      label: label,
      hint: hint,
      trailing: unavailable != null
          ? ValueWithChange(
              value: unavailable,
              muted: true,
              onChange: () => _change(key),
              buttonKey: Key('change-$key'),
            )
          : ValueWithChange(
              value: value(r),
              muted: r.model.isEmpty,
              onChange: () => _change(key),
              buttonKey: Key('change-$key'),
            ),
    );
    final dictationProvider = s.dictation.provider.isNotEmpty
        ? s.dictation.provider
        : s.triage.provider;
    final dictationUnavailable =
        s.dictation.model.isEmpty &&
        dictationProvider.isNotEmpty &&
        !(s.provider(dictationProvider)?.canTranscribe ?? true);
    return SettingsPage(
      title: 'Models',
      footer: Align(
        alignment: Alignment.centerLeft,
        child: TextButton(
          key: const Key('models-test'),
          onPressed: _testing || s.triage.provider.isEmpty ? null : _test,
          child: Text(_testing ? 'Testing…' : 'Test connection'),
        ),
      ),
      children: [
        if (s.setupNeeded || s.triage.provider.isEmpty)
          const SettingsRow(
            label: 'No model connected yet',
            hint: 'Captures wait in the inbox. Add a provider key under Providers, then pick models here.',
          ),
        role('triage', 'Filing', 'sorts captures; fast and cheap', s.triage),
        role('chat', 'Conversation', 'answers and files in chat', s.chat),
        role(
          'dictation',
          'Dictation',
          'speech to text',
          s.dictation,
          unavailable: dictationUnavailable
              ? 'Not available with ${providerTitle(dictationProvider)}'
              : null,
        ),
        role(
          'research',
          'Research',
          'reads the web',
          RoleRef(provider: s.research.provider, model: s.research.model),
        ),
        role(
          'embedding',
          'Semantic search',
          'embeddings for search and duplicates',
          RoleRef(provider: s.embedding.provider, model: s.embedding.model),
          unavailable: s.embedding.model.isEmpty
              ? 'Not set · search matches words only'
              : null,
        ),
      ],
    );
  }
}

/// Provider dropdown + model list with the recommendation for one role.
class RolePickerDialog extends StatefulWidget {
  const RolePickerDialog({
    super.key,
    required this.role,
    required this.settings,
  });
  final String role;
  final ServerSettings settings;
  @override
  State<RolePickerDialog> createState() => _RolePickerDialogState();
}

class _RolePickerDialogState extends State<RolePickerDialog> {
  late String _provider;
  String _model = '';
  ModelList? _models;
  bool _loading = false;

  static const titles = {
    'triage': 'Filing model',
    'chat': 'Conversation model',
    'dictation': 'Dictation model',
    'research': 'Research model',
    'embedding': 'Embedding model',
  };

  RoleRef get _current => switch (widget.role) {
    'triage' => widget.settings.triage,
    'chat' => widget.settings.chat,
    'dictation' => widget.settings.dictation,
    'research' => RoleRef(
      provider: widget.settings.research.provider,
      model: widget.settings.research.model,
    ),
    _ => RoleRef(
      provider: widget.settings.embedding.provider,
      model: widget.settings.embedding.model,
    ),
  };

  /// Providers with a key (or local), minus those that cannot do the role.
  List<ProviderInfo> get _providers =>
      widget.settings.providers.values.where((p) {
        if (p.needsKey && !p.hasKey) return false;
        if (widget.role == 'dictation' && !p.canTranscribe) return false;
        return true;
      }).toList();

  @override
  void initState() {
    super.initState();
    final c = _current;
    _provider = c.provider.isNotEmpty
        ? c.provider
        : widget.settings.chat.provider;
    _model = c.model;
    _list();
  }

  Future<void> _list() async {
    if (_provider.isEmpty) return;
    setState(() => _loading = true);
    try {
      final m = await context.read<AppState>().api.setupModels(_provider);
      if (!mounted) return;
      setState(() {
        _models = m;
        if (_model.isEmpty) _model = _suggested(m);
      });
    } catch (_) {
      if (mounted) setState(() => _models = const ModelList());
    } finally {
      if (mounted) setState(() => _loading = false);
    }
  }

  String _suggested(ModelList m) => switch (widget.role) {
    'triage' => m.suggestedTriage,
    'dictation' => m.suggestedTranscribe,
    'embedding' => m.suggestedEmbed,
    _ => m.suggestedChat,
  };

  @override
  Widget build(BuildContext context) {
    final m = _models;
    final providers = _providers;
    return AlertDialog(
      title: Text(titles[widget.role] ?? 'Model'),
      content: SizedBox(
        width: 480,
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            DropdownButtonFormField<String>(
              key: const Key('picker-provider'),
              initialValue: providers.any((p) => p.name == _provider)
                  ? _provider
                  : null,
              isExpanded: true,
              decoration: fieldDecoration(context, label: 'Provider'),
              items: [
                for (final p in providers)
                  DropdownMenuItem(
                    value: p.name,
                    child: Text(providerTitle(p.name)),
                  ),
              ],
              onChanged: (v) {
                if (v == null) return;
                setState(() {
                  _provider = v;
                  _model = '';
                  _models = null;
                });
                _list();
              },
            ),
            const SizedBox(height: 12),
            if (_loading) const LinearProgressIndicator(minHeight: 2),
            if (m != null && !m.ok)
              Padding(
                padding: const EdgeInsets.only(bottom: 8),
                child: Text(
                  m.error,
                  style: Theme.of(context).textTheme.bodySmall,
                ),
              ),
            RoleModelPicker(
              key: const Key('picker-model'),
              label: 'Model',
              models: m?.models ?? const [],
              recommended: m == null ? '' : _suggested(m),
              value: _model,
              onChanged: (v) => _model = v,
              onSubmitted: (v) {
                _model = v;
                if (v.trim().isNotEmpty) {
                  Navigator.pop(
                    context,
                    RoleRef(provider: _provider, model: v.trim()),
                  );
                }
              },
            ),
            if (widget.role != 'triage' && widget.role != 'chat')
              Padding(
                padding: const EdgeInsets.only(top: 8),
                child: Align(
                  alignment: Alignment.centerLeft,
                  child: TextButton(
                    onPressed: () => Navigator.pop(
                      context,
                      RoleRef(provider: _provider, model: ''),
                    ),
                    child: Text(
                      widget.role == 'dictation'
                          ? 'Turn dictation off'
                          : 'Same as conversation',
                    ),
                  ),
                ),
              ),
          ],
        ),
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.pop(context),
          child: const Text('Cancel'),
        ),
        FilledButton(
          key: const Key('picker-save'),
          onPressed: _model.trim().isEmpty && widget.role != 'dictation'
              ? null
              : () => Navigator.pop(
                  context,
                  RoleRef(provider: _provider, model: _model.trim()),
                ),
          child: const Text('Save'),
        ),
      ],
    );
  }
}

/// "gpt-5.6-luna · OpenAI"; "rules" for the built-in fallback.
String roleValue(RoleRef r) {
  if (r.provider == 'fake' || r.model == 'heuristic') return 'rules';
  return r.provider.isEmpty
      ? r.model
      : '${r.model} · ${providerTitle(r.provider)}';
}
