import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:provider/provider.dart';
import 'package:url_launcher/url_launcher.dart';

import '../../api/client.dart';
import '../../api/models.dart';
import '../../state/app_state.dart';
import '../settings_screen.dart' show showSaved;
import '../theme.dart';
import '../widgets/common.dart';

/// A provider the wizard can connect.
class ProviderChoice {
  const ProviderChoice({
    required this.name,
    required this.title,
    required this.blurb,
    this.keyUrl,
    this.keyLabel = '',
    this.oauth = false,
    this.local = false,
    this.none = false,
  });
  final String name;
  final String title;
  final String blurb;
  final String? keyUrl;
  final String keyLabel;
  final bool oauth;
  final bool local;
  final bool none;
  bool get needsKey => !local && !none;
}

const providerChoices = [
  ProviderChoice(
    name: 'openai',
    title: 'OpenAI',
    blurb: 'GPT models. Quick filing, strong conversation. Pay per use.',
    keyUrl: 'https://platform.openai.com/api-keys',
    keyLabel: 'platform.openai.com/api-keys',
  ),
  ProviderChoice(
    name: 'anthropic',
    title: 'Anthropic',
    blurb: 'Claude models through the OpenAI-compatible endpoint.',
    keyUrl: 'https://console.anthropic.com/settings/keys',
    keyLabel: 'console.anthropic.com/settings/keys',
  ),
  ProviderChoice(
    name: 'openrouter',
    title: 'OpenRouter',
    blurb: 'One key for many models. Connect with OpenRouter, or paste a key.',
    keyUrl: 'https://openrouter.ai/keys',
    keyLabel: 'openrouter.ai/keys',
    oauth: true,
  ),
  ProviderChoice(
    name: 'ollama',
    title: 'Ollama',
    blurb: 'Local models, no key, nothing leaves your machine. Needs a capable model (8B or more) to file reliably.',
    local: true,
  ),
  ProviderChoice(
    name: 'fake',
    title: 'No model for now',
    blurb: 'Filing by rules only: tasks by keywords, everything else becomes a note. No conversation, no topics, no linking. Connect a model later in Settings.',
    none: true,
  ),
];

/// Title of a provider by name (falls back to the name).
String providerTitle(String name) =>
    providerChoices.where((c) => c.name == name).firstOrNull?.title ?? name;

enum _Step { provider, credentials, models, saving }

/// Full-screen wizard: provider → connect → models → save.
class SetupWizard extends StatefulWidget {
  const SetupWizard({
    super.key,
    this.onDone,
    this.onSkip,
    this.embedded = false,
  });

  /// Called after settings were saved (and on Cancel when embedded).
  final VoidCallback? onDone;
  final VoidCallback? onSkip;

  /// Inside Settings: no skip, smaller heading, no own scrolling.
  final bool embedded;

  @override
  State<SetupWizard> createState() => _SetupWizardState();
}

class _SetupWizardState extends State<SetupWizard> {
  _Step _step = _Step.provider;
  ProviderChoice? _choice;
  ServerSettings? _settings;
  final _key = TextEditingController();
  final _continueFocus = FocusNode();
  bool _busy = false;
  Object? _error;
  ProbeResult? _probe;

  /// Key typed in this session, kept unsaved until "Save and start filing".
  String _pendingKey = '';
  ModelList? _models;
  Object? _modelsError;
  int? _ollamaCount;
  String _triage = '';
  String _chat = '';
  bool _oauthWaiting = false;

  @override
  void initState() {
    super.initState();
    _loadSettings();
  }

  @override
  void dispose() {
    _key.dispose();
    _continueFocus.dispose();
    super.dispose();
  }

  FundusApi get _api => context.read<AppState>().api;

  Future<void> _loadSettings() async {
    try {
      final s = await _api.settings();
      if (mounted) setState(() => _settings = s);
    } catch (e) {
      if (mounted) setState(() => _error = e);
    }
  }

  ProviderInfo? get _info =>
      _choice == null ? null : _settings?.provider(_choice!.name);

  void _pick(ProviderChoice c) {
    setState(() {
      _choice = c;
      _probe = null;
      _error = null;
      _pendingKey = '';
      _models = null;
      _modelsError = null;
      _key.clear();
      _step = c.none ? _Step.saving : _Step.credentials;
    });
    if (c.none) _saveNone();
    if (c.local) _loadModels();
  }

  void _back() {
    setState(() {
      _error = null;
      switch (_step) {
        case _Step.provider:
          if (widget.embedded) widget.onDone?.call();
        case _Step.credentials:
          _step = _Step.provider;
        case _Step.models:
          _step = _Step.credentials;
        case _Step.saving:
          break;
      }
    });
  }

  Future<void> _saveNone() async {
    setState(() => _busy = true);
    try {
      await _api.updateSettings({
        'triage': {'provider': 'fake', 'model': 'heuristic'},
        'chat': {'provider': 'fake', 'model': 'heuristic'},
      });
      await _finish(
        'Filing by rules for now. Connect a model any time in Settings.',
      );
    } catch (e) {
      setState(() {
        _error = e;
        _busy = false;
        _step = _Step.provider;
      });
    }
  }

  /// Lists models with the typed key (never stored) and probes the model.
  Future<void> _test() async {
    final c = _choice!;
    setState(() {
      _busy = true;
      _error = null;
      _probe = null;
    });
    try {
      final typed = _key.text.trim();
      if (c.needsKey && typed.isNotEmpty) _pendingKey = typed;
      final m = await _api.setupModels(
        c.name,
        apiKey: _pendingKey.isEmpty ? null : _pendingKey,
      );
      if (!m.ok) {
        setState(() => _error = m.error);
        return;
      }
      _applyModels(m);
      // The probe needs the key on the server side only when it is stored;
      // with an unsaved key the model list is the connection test.
      if (_pendingKey.isEmpty) {
        final p = await _api.testProvider(
          c.name,
          model: _triage.isEmpty ? null : _triage,
        );
        setState(() => _probe = p);
      } else {
        setState(
          () => _probe = ProbeResult(
            reachable: true,
            structured: true,
            tools: true,
            german: true,
            latency: Duration.zero,
            mode: 'models listed (${m.models.length})',
          ),
        );
      }
      if (_probe?.usable == true) _continueFocus.requestFocus();
    } catch (e) {
      setState(() => _error = e);
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  void _applyModels(ModelList m) {
    _models = m;
    _triage = m.suggestedTriage.isNotEmpty
        ? m.suggestedTriage
        : (m.models.isNotEmpty ? m.models.first : '');
    _chat = m.suggestedChat.isNotEmpty ? m.suggestedChat : _triage;
    if (_choice?.local == true) _ollamaCount = m.models.length;
  }

  Future<void> _oauth() async {
    final c = _choice!;
    setState(() {
      _busy = true;
      _error = null;
      _oauthWaiting = true;
    });
    try {
      final url = await _api.oauthStart(c.name);
      if (url.isNotEmpty) {
        await launchUrl(Uri.parse(url), mode: LaunchMode.externalApplication);
      }
      // Fundus receives the callback itself; poll until the key is stored.
      final deadline = DateTime.now().add(const Duration(minutes: 5));
      while (mounted && _oauthWaiting && DateTime.now().isBefore(deadline)) {
        await Future<void>.delayed(const Duration(seconds: 2));
        final s = await _api.settings();
        if (s.provider(c.name)?.keyStatus == 'set') {
          setState(() {
            _settings = s;
            _oauthWaiting = false;
          });
          await _test();
          return;
        }
      }
      if (mounted && _oauthWaiting) {
        setState(
          () => _error = 'OpenRouter did not report a key. Paste one instead.',
        );
      }
    } catch (e) {
      if (mounted) setState(() => _error = e);
    } finally {
      if (mounted) {
        setState(() {
          _busy = false;
          _oauthWaiting = false;
        });
      }
    }
  }

  Future<void> _loadModels() async {
    setState(() {
      _models = null;
      _modelsError = null;
    });
    try {
      final m = await _api.setupModels(
        _choice!.name,
        apiKey: _pendingKey.isEmpty ? null : _pendingKey,
      );
      if (!m.ok) {
        setState(() => _modelsError = m.error);
        return;
      }
      setState(() => _applyModels(m));
    } catch (e) {
      setState(() => _modelsError = e);
    }
  }

  Future<void> _toModels() async {
    setState(() => _step = _Step.models);
    if (_models == null || _models!.models.isEmpty) await _loadModels();
  }

  Future<void> _save() async {
    final c = _choice!;
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      // Provider and model always travel together; the key is stored only now.
      await _api.updateSettings({
        'triage': {'provider': c.name, 'model': _triage},
        'chat': {'provider': c.name, 'model': _chat},
        if (_pendingKey.isNotEmpty)
          'providers': {
            c.name: {'api_key': _pendingKey},
          },
      });
      _pendingKey = '';
      await _finish('Connected. Pending captures are being filed.');
    } catch (e) {
      setState(() {
        _error = e;
        _busy = false;
      });
    }
  }

  Future<void> _finish(String message) async {
    final state = context.read<AppState>();
    await state.settingsChanged();
    if (!mounted) return;
    ScaffoldMessenger.maybeOf(context)
        ?.showSnackBar(SnackBar(content: Text(message)));
    setState(() {
      _busy = false;
      _step = _Step.provider;
    });
    widget.onDone?.call();
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final children = <Widget>[
      if (!widget.embedded)
        const Padding(
          padding: EdgeInsets.only(bottom: 14),
          child: Align(
            alignment: Alignment.centerLeft,
            child: FundusMark(size: 40),
          ),
        ),
      Row(
        children: [
          Expanded(
            child: Text(
              widget.embedded ? 'Model & provider' : 'Connect a model',
              style: widget.embedded
                  ? theme.textTheme.headlineMedium
                  : theme.textTheme.displayMedium,
            ),
          ),
          if (widget.embedded)
            TextButton(
              key: const Key('wizard-cancel'),
              onPressed: widget.onDone,
              child: const Text('Cancel'),
            ),
        ],
      ),
      const SizedBox(height: 8),
      Text(
        'Fundus files what you capture with a language model. Choose where it runs. Keys are stored only where Fundus runs.',
        style: theme.textTheme.bodyMedium,
      ),
      if (!widget.embedded) ...[
        const SizedBox(height: 6),
        Text(
          'You can already capture; filing starts once a model is connected.',
          style: FundusTheme.weight(
            theme.textTheme.bodySmall!,
            550,
          ).copyWith(color: theme.colorScheme.primary),
        ),
      ],
      const SizedBox(height: 24),
      if (_choice?.none != true)
        _StepDots(step: _step.index, done: _step == _Step.saving),
      const SizedBox(height: 16),
      switch (_step) {
        _Step.provider => _providerStep(),
        _Step.credentials => _credentialsStep(),
        _Step.models => _modelsStep(),
        _Step.saving => const Padding(
          padding: EdgeInsets.all(24),
          child: Center(child: CircularProgressIndicator()),
        ),
      },
      if (_error != null)
        Padding(
          padding: const EdgeInsets.only(top: 12),
          child: Text(
            describeError(_error!),
            style: theme.textTheme.bodySmall!.copyWith(
              color: theme.colorScheme.error,
            ),
          ),
        ),
      if (!widget.embedded && widget.onSkip != null) ...[
        const SizedBox(height: 28),
        Align(
          alignment: Alignment.centerLeft,
          child: TextButton(
            onPressed: widget.onSkip,
            child: const Text('Skip for now'),
          ),
        ),
      ],
    ];
    final list = widget.embedded
        ? ListView(
            shrinkWrap: true,
            physics: const NeverScrollableScrollPhysics(),
            padding: EdgeInsets.zero,
            children: children,
          )
        : ListView(
            padding: const EdgeInsets.fromLTRB(24, 40, 24, 40),
            children: children,
          );
    final body = CallbackShortcuts(
      bindings: {const SingleActivator(LogicalKeyboardKey.escape): _back},
      child: Focus(
        autofocus: !widget.embedded,
        child: widget.embedded
            ? list
            : Center(
                child: ConstrainedBox(
                  constraints: const BoxConstraints(maxWidth: 560),
                  child: list,
                ),
              ),
      ),
    );
    return widget.embedded ? body : Scaffold(body: SafeArea(child: body));
  }

  Widget _providerStep() {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        for (final c in providerChoices)
          Padding(
            padding: const EdgeInsets.only(bottom: 10),
            child: _ProviderCard(
              choice: c,
              info: _settings?.provider(c.name),
              selected: _choice?.name == c.name,
              ollamaCount: _ollamaCount,
              onTap: () => _pick(c),
            ),
          ),
      ],
    );
  }

  Widget _credentialsStep() {
    final c = _choice!;
    final theme = Theme.of(context);
    final info = _info;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        _BackRow(title: c.title, onBack: _back),
        const SizedBox(height: 12),
        if (c.local) ...[
          Text(
            'Ollama runs on this machine. Fundus talks to it at ${info?.baseUrl ?? 'http://127.0.0.1:11434/v1'}.',
            style: theme.textTheme.bodyMedium,
          ),
          const SizedBox(height: 12),
          if (_models == null && _modelsError == null)
            const LinearProgressIndicator(minHeight: 2),
          if (_modelsError != null) ...[
            const _Row(
              ok: false,
              text: 'Ollama is not running at 127.0.0.1:11434. Start it and pull a model, then retry.',
            ),
            const Padding(
              padding: EdgeInsets.only(left: 24, top: 4),
              child: _Cmd('ollama serve'),
            ),
          ],
          if (_models != null)
            _Row(
              ok: _models!.models.isNotEmpty,
              text: _models!.models.isEmpty
                  ? 'Ollama is running but has no models. Pull one first.'
                  : 'Found ${_models!.models.length} model${_models!.models.length == 1 ? '' : 's'}.',
            ),
          if (_models != null && _models!.models.isEmpty)
            const Padding(
              padding: EdgeInsets.only(left: 24, top: 4),
              child: _Cmd('ollama pull qwen3:8b'),
            ),
          const SizedBox(height: 12),
          Row(
            children: [
              FilledButton(
                key: const Key('continue'),
                focusNode: _continueFocus,
                onPressed: _models != null && _models!.models.isNotEmpty
                    ? () => setState(() => _step = _Step.models)
                    : null,
                child: const Text('Continue'),
              ),
              const SizedBox(width: 8),
              TextButton(onPressed: _loadModels, child: const Text('Retry')),
            ],
          ),
        ] else ...[
          if (c.oauth) ...[
            FilledButton.icon(
              onPressed: _busy ? null : _oauth,
              icon: const Icon(Icons.link_rounded, size: 16),
              label: Text(
                _oauthWaiting
                    ? 'Waiting for OpenRouter…'
                    : 'Connect with OpenRouter',
              ),
            ),
            const SizedBox(height: 8),
            Text('Or paste a key', style: theme.textTheme.labelSmall),
            const SizedBox(height: 6),
          ],
          if (info?.hasKey == true && _pendingKey.isEmpty)
            Padding(
              padding: const EdgeInsets.only(bottom: 8),
              child: _Row(
                ok: true,
                text:
                    'A key is already stored (${info!.keyStatus == 'env' ? 'from the environment' : 'ends with ${info.keyHint}'}). Paste a new one to replace it, or just test.',
              ),
            ),
          TextField(
            key: const Key('api-key'),
            controller: _key,
            obscureText: true,
            autofocus: true,
            decoration: InputDecoration(
              labelText: 'API key',
              hintText: 'sk-…',
              suffixIcon: IconButton(
                tooltip: 'Paste',
                icon: const Icon(Icons.content_paste_rounded, size: 18),
                onPressed: () async {
                  final d = await Clipboard.getData('text/plain');
                  if (d?.text != null) {
                    setState(() => _key.text = d!.text!.trim());
                  }
                },
              ),
            ),
            onChanged: (_) => setState(() {}),
            onSubmitted: (_) => _test(),
          ),
          if (c.keyUrl != null)
            Padding(
              padding: const EdgeInsets.only(top: 6),
              child: InkWell(
                onTap: () => launchUrl(
                  Uri.parse(c.keyUrl!),
                  mode: LaunchMode.externalApplication,
                ),
                child: Text(
                  'Get a key at ${c.keyLabel}',
                  style: theme.textTheme.labelSmall!.copyWith(
                    color: theme.colorScheme.secondary,
                    decoration: TextDecoration.underline,
                  ),
                ),
              ),
            ),
          const SizedBox(height: 12),
          Row(
            children: [
              FilledButton.tonal(
                key: const Key('test-connection'),
                onPressed:
                    _busy || (_key.text.trim().isEmpty && info?.hasKey != true)
                    ? null
                    : _test,
                child: Text(
                  _busy && !_oauthWaiting ? 'Testing…' : 'Test connection',
                ),
              ),
              const SizedBox(width: 8),
              FilledButton(
                key: const Key('continue'),
                focusNode: _continueFocus,
                onPressed: _probe?.usable == true ? _toModels : null,
                child: const Text('Continue'),
              ),
            ],
          ),
          if (_probe != null) ...[
            const SizedBox(height: 12),
            ProbeView(probe: _probe!),
          ],
        ],
      ],
    );
  }

  Widget _modelsStep() {
    final c = _choice!;
    final theme = Theme.of(context);
    final m = _models;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        _BackRow(title: '${c.title} · models', onBack: _back),
        const SizedBox(height: 12),
        if (m == null && _modelsError == null)
          const LinearProgressIndicator(minHeight: 2),
        if (_modelsError != null) ...[
          _Row(ok: false, text: describeError(_modelsError!)),
          const SizedBox(height: 8),
          TextButton(onPressed: _loadModels, child: const Text('Retry')),
        ],
        if (m != null) ...[
          Text(
            'The filing model reads every capture: pick something fast and cheap. The conversation model answers questions: pick something capable.',
            style: theme.textTheme.bodySmall,
          ),
          const SizedBox(height: 14),
          ModelPicker(
            key: const Key('model-triage'),
            label: 'Filing model (fast)',
            models: m.models,
            value: _triage,
            onChanged: (v) => setState(() => _triage = v),
          ),
          const SizedBox(height: 12),
          ModelPicker(
            key: const Key('model-chat'),
            label: 'Conversation model (capable)',
            models: m.models,
            value: _chat,
            onChanged: (v) => setState(() => _chat = v),
          ),
          const SizedBox(height: 18),
          Row(
            children: [
              FilledButton(
                key: const Key('save'),
                onPressed: _busy || _triage.isEmpty || _chat.isEmpty
                    ? null
                    : _save,
                child: Text(_busy ? 'Saving…' : 'Save and start filing'),
              ),
            ],
          ),
        ],
      ],
    );
  }
}

/// A command the user should run, selectable and monospace.
class _Cmd extends StatelessWidget {
  const _Cmd(this.text);
  final String text;
  @override
  Widget build(BuildContext context) => SelectableText(
    text,
    style: monoStyle(
      context,
      size: 12.5,
      color: Theme.of(context).colorScheme.onSurface,
    ),
  );
}

class _StepDots extends StatelessWidget {
  const _StepDots({required this.step, required this.done});
  final int step;
  final bool done;
  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    const labels = ['Provider', 'Connect', 'Models'];
    return Row(
      children: [
        for (var i = 0; i < labels.length; i++) ...[
          Container(
            width: 8,
            height: 8,
            decoration: BoxDecoration(
              shape: BoxShape.circle,
              color: i <= step || done ? scheme.primary : scheme.outlineVariant,
            ),
          ),
          const SizedBox(width: 6),
          Text(
            labels[i],
            style: Theme.of(context).textTheme.labelSmall!
                .copyWith(color: i == step && !done ? scheme.onSurface : null),
          ),
          if (i < labels.length - 1) const SizedBox(width: 16),
        ],
      ],
    );
  }
}

class _BackRow extends StatelessWidget {
  const _BackRow({required this.title, required this.onBack});
  final String title;
  final VoidCallback onBack;
  @override
  Widget build(BuildContext context) => Row(
    children: [
      IconButton(
        key: const Key('wizard-back'),
        tooltip: 'Back (Esc)',
        icon: const Icon(Icons.arrow_back_rounded, size: 18),
        onPressed: onBack,
      ),
      const SizedBox(width: 4),
      Text(title, style: Theme.of(context).textTheme.titleMedium),
    ],
  );
}

class _Row extends StatelessWidget {
  const _Row({required this.ok, required this.text});
  final bool ok;
  final String text;
  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    return Row(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Icon(
          ok ? Icons.check_circle_rounded : Icons.error_outline_rounded,
          size: 16,
          color: ok ? scheme.success : scheme.error,
        ),
        const SizedBox(width: 8),
        Expanded(
          child: Text(
            text,
            style: Theme.of(context).textTheme.bodySmall!
                .copyWith(color: scheme.onSurface),
          ),
        ),
      ],
    );
  }
}

/// Probe result as green/red rows.
class ProbeView extends StatelessWidget {
  const ProbeView({super.key, required this.probe});
  final ProbeResult probe;
  @override
  Widget build(BuildContext context) {
    final p = probe;
    final latency = p.latency == Duration.zero
        ? ''
        : ' (${p.latency.inMilliseconds} ms)';
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        _Row(
          ok: p.reachable,
          text: p.reachable ? 'Reachable$latency' : 'Not reachable',
        ),
        _Row(
          ok: p.structured,
          text: p.structured
              ? 'Structured output works${p.mode.isEmpty ? '' : ' (${p.mode})'}'
              : 'Structured output failed: this model cannot file captures reliably',
        ),
        _Row(
          ok: p.tools,
          text: p.tools
              ? 'Tool calls work (conversation)'
              : 'No tool calls: conversation will be limited',
        ),
        _Row(
          ok: p.german,
          text: p.german
              ? 'Understood a German test sentence'
              : 'Did not understand a German test sentence',
        ),
        for (final e in p.errors)
          Padding(
            padding: const EdgeInsets.only(left: 24, top: 2),
            child: Text(e, style: Theme.of(context).textTheme.labelSmall),
          ),
      ],
    );
  }
}

/// A searchable model dropdown.
class ModelPicker extends StatelessWidget {
  const ModelPicker({
    super.key,
    required this.label,
    required this.models,
    required this.value,
    required this.onChanged,
  });
  final String label;
  final List<String> models;
  final String value;
  final ValueChanged<String> onChanged;
  @override
  Widget build(BuildContext context) {
    return DropdownMenu<String>(
      label: Text(label),
      initialSelection: models.contains(value) ? value : null,
      enableFilter: true,
      enableSearch: true,
      requestFocusOnTap: true,
      expandedInsets: EdgeInsets.zero,
      menuHeight: 320,
      textStyle: monoStyle(
        context,
        size: 13,
        color: Theme.of(context).colorScheme.onSurface,
      ),
      dropdownMenuEntries: [
        for (final m in models) DropdownMenuEntry(value: m, label: m),
      ],
      onSelected: (v) {
        if (v != null) onChanged(v);
      },
    );
  }
}

class _ProviderCard extends StatelessWidget {
  const _ProviderCard({
    required this.choice,
    required this.info,
    required this.selected,
    required this.onTap,
    this.ollamaCount,
  });
  final ProviderChoice choice;
  final ProviderInfo? info;
  final bool selected;
  final VoidCallback onTap;
  final int? ollamaCount;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final scheme = theme.colorScheme;
    final icon = switch (choice.name) {
      'ollama' => Icons.dns_outlined,
      'fake' => Icons.rule_rounded,
      'openrouter' => Icons.hub_outlined,
      _ => Icons.cloud_outlined,
    };
    return Material(
      color: selected ? scheme.surfaceContainer : scheme.surfaceContainerLowest,
      borderRadius: BorderRadius.circular(10),
      child: InkWell(
        key: Key('provider-${choice.name}'),
        onTap: onTap,
        borderRadius: BorderRadius.circular(10),
        child: Container(
          padding: const EdgeInsets.all(14),
          decoration: BoxDecoration(
            borderRadius: BorderRadius.circular(10),
            border: Border.all(
              color: selected ? scheme.primary : scheme.outlineVariant,
              width: selected ? 1.5 : 1,
            ),
          ),
          child: Row(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Icon(icon, size: 20, color: scheme.onSurfaceVariant),
              const SizedBox(width: 12),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Row(
                      children: [
                        Text(choice.title, style: theme.textTheme.titleMedium),
                        const SizedBox(width: 8),
                        if (info?.hasKey == true)
                          Text(
                            'key stored',
                            style: theme.textTheme.labelSmall!.copyWith(
                              color: scheme.success,
                            ),
                          ),
                      ],
                    ),
                    const SizedBox(height: 2),
                    Text(choice.blurb, style: theme.textTheme.bodySmall),
                    if (choice.keyUrl != null)
                      Padding(
                        padding: const EdgeInsets.only(top: 4),
                        child: InkWell(
                          onTap: () => launchUrl(
                            Uri.parse(choice.keyUrl!),
                            mode: LaunchMode.externalApplication,
                          ),
                          child: Text(
                            'Get a key at ${choice.keyLabel}',
                            style: theme.textTheme.labelSmall!.copyWith(
                              color: scheme.secondary,
                              decoration: TextDecoration.underline,
                            ),
                          ),
                        ),
                      ),
                    if (choice.local)
                      Padding(
                        padding: const EdgeInsets.only(top: 4),
                        child: Text(
                          ollamaCount == null
                              ? 'Expected at 127.0.0.1:11434'
                              : 'Found $ollamaCount model${ollamaCount == 1 ? '' : 's'}',
                          style: theme.textTheme.labelSmall,
                        ),
                      ),
                  ],
                ),
              ),
              Icon(Icons.chevron_right_rounded, color: scheme.onSurfaceVariant),
            ],
          ),
        ),
      ),
    );
  }
}

// ---------------------------------------------------------------------------
// Settings sections

/// "Model & provider" for the Settings screen: current binding, key status,
/// replace key + test, or run the wizard again. Saves on change.
class ProviderSection extends StatefulWidget {
  const ProviderSection({super.key});
  @override
  State<ProviderSection> createState() => _ProviderSectionState();
}

class _ProviderSectionState extends State<ProviderSection> {
  ServerSettings? _s;
  Object? _error;
  bool _wizard = false;
  ProbeResult? _probe;
  bool _busy = false;
  final _key = TextEditingController();

  @override
  void initState() {
    super.initState();
    _load();
  }

  @override
  void dispose() {
    _key.dispose();
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

  Future<void> _saveKeyAndTest() async {
    final s = _s;
    if (s == null) return;
    setState(() {
      _busy = true;
      _probe = null;
      _error = null;
    });
    final api = context.read<AppState>().api;
    try {
      if (_key.text.trim().isNotEmpty) {
        _s = await api.updateSettings({
          'providers': {
            s.triage.provider: {'api_key': _key.text.trim()},
          },
        });
        _key.clear();
        if (mounted) showSaved(context);
      }
      final p = await api.testProvider(
        s.triage.provider,
        model: s.triage.model,
      );
      setState(() => _probe = p);
    } catch (e) {
      setState(() => _error = e);
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final s = _s;
    if (_wizard) {
      return SetupWizard(
        embedded: true,
        onDone: () {
          setState(() => _wizard = false);
          _load();
        },
      );
    }
    if (s == null) {
      return _error != null
          ? Text(describeError(_error!), style: theme.textTheme.bodySmall)
          : const LinearProgressIndicator(minHeight: 2);
    }
    final prov = s.provider(s.triage.provider);
    final mono = monoStyle(
      context,
      size: 12.5,
      color: theme.colorScheme.onSurface,
    );
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        if (s.setupNeeded || s.triage.provider.isEmpty)
          Text(
            'No model connected yet. Captures wait in the inbox.',
            style: theme.textTheme.bodyMedium,
          )
        else ...[
          Text(
            'Filing model: ${modelLabel(s.triage.provider, s.triage.model)}',
            style: mono,
          ),
          Text(
            'Conversation model: ${modelLabel(s.chat.provider, s.chat.model)}',
            style: mono,
          ),
          if (prov != null && prov.needsKey)
            Text(
              prov.hasKey
                  ? 'Key ${prov.keyStatus == 'env' ? 'from the environment' : 'stored, ends with ${prov.keyHint}'}'
                  : 'No key stored',
              style: theme.textTheme.labelSmall,
            ),
        ],
        const SizedBox(height: 10),
        if (prov != null && prov.needsKey) ...[
          TextField(
            controller: _key,
            obscureText: true,
            onChanged: (_) => setState(() {}),
            onSubmitted: (_) => _saveKeyAndTest(),
            decoration: InputDecoration(
              labelText: 'Replace ${providerTitle(prov.name)} key',
              hintText: 'sk-…',
              isDense: true,
            ),
          ),
          const SizedBox(height: 8),
        ],
        Row(
          children: [
            if (prov != null && !s.setupNeeded)
              FilledButton.tonal(
                onPressed: _busy ? null : _saveKeyAndTest,
                child: Text(
                  _busy
                      ? 'Testing…'
                      : (_key.text.trim().isEmpty
                            ? 'Test connection'
                            : 'Save and test'),
                ),
              ),
            const SizedBox(width: 8),
            OutlinedButton(
              onPressed: () => setState(() => _wizard = true),
              child: Text(
                s.setupNeeded ? 'Connect a model' : 'Change provider or model',
              ),
            ),
          ],
        ),
        if (_probe != null) ...[
          const SizedBox(height: 10),
          ProbeView(probe: _probe!),
        ],
        if (_error != null)
          Padding(
            padding: const EdgeInsets.only(top: 8),
            child: Text(
              describeError(_error!),
              style: theme.textTheme.bodySmall!.copyWith(
                color: theme.colorScheme.error,
              ),
            ),
          ),
        Text('Config file: ${s.path}', style: theme.textTheme.labelSmall),
      ],
    );
  }
}

/// Autonomy toggles and time zone, saved on change via PUT /v1/settings.
class AutonomySection extends StatefulWidget {
  const AutonomySection({super.key});
  @override
  State<AutonomySection> createState() => _AutonomySectionState();
}

class _AutonomySectionState extends State<AutonomySection> {
  ServerSettings? _s;
  Autonomy? _a;
  late final TextEditingController _tz = TextEditingController();
  late final TextEditingController _topics = TextEditingController();
  Object? _error;

  @override
  void initState() {
    super.initState();
    _load();
  }

  @override
  void dispose() {
    _tz.dispose();
    _topics.dispose();
    super.dispose();
  }

  Future<void> _load() async {
    try {
      final s = await context.read<AppState>().api.settings();
      if (mounted) {
        setState(() {
          _s = s;
          _a = s.autonomy;
          _tz.text = s.timezone;
          _topics.text = '${s.autonomy.maxNewTopicsPerCapture}';
        });
      }
    } catch (e) {
      if (mounted) setState(() => _error = e);
    }
  }

  Future<void> _save(Map<String, dynamic> patch) async {
    setState(() => _error = null);
    try {
      final s = await context.read<AppState>().api.updateSettings(patch);
      if (mounted) {
        setState(() {
          _s = s;
          _a = s.autonomy;
        });
        showSaved(context);
        context.read<AppState>().checkHealth();
      }
    } catch (e) {
      if (mounted) setState(() => _error = e);
    }
  }

  void _saveAutonomy(Autonomy a) {
    setState(() => _a = a);
    _save({'autonomy': a.toJson()});
  }

  void _saveTopics() {
    final n = int.tryParse(_topics.text.trim());
    final a = _a;
    if (n == null || a == null || n == a.maxNewTopicsPerCapture) return;
    _saveAutonomy(a.copyWith(maxNewTopicsPerCapture: n));
  }

  void _saveTimezone() {
    final tz = _tz.text.trim();
    if (tz == (_s?.timezone ?? '')) return;
    _save({'timezone': tz});
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final a = _a;
    if (a == null) {
      return _error != null
          ? Text(describeError(_error!), style: theme.textTheme.bodySmall)
          : const LinearProgressIndicator(minHeight: 2);
    }
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        SwitchListTile(
          contentPadding: EdgeInsets.zero,
          dense: true,
          title: const Text('Create notes and tasks without asking'),
          subtitle: Text(
            'Off: every capture waits in the inbox as a proposal.',
            style: theme.textTheme.labelSmall,
          ),
          value: a.autoCreate,
          onChanged: (v) => _saveAutonomy(a.copyWith(autoCreate: v)),
        ),
        Text(
          'Park captures below ${(a.minConfidence * 100).round()}% confidence',
          style: theme.textTheme.bodyMedium,
        ),
        Slider(
          value: a.minConfidence.clamp(0, 1),
          min: 0,
          max: 1,
          divisions: 20,
          label: '${(a.minConfidence * 100).round()}%',
          onChanged: (v) => setState(() => _a = a.copyWith(minConfidence: v)),
          onChangeEnd: (v) => _saveAutonomy(a.copyWith(minConfidence: v)),
        ),
        Row(
          children: [
            Expanded(
              child: Text(
                'Topics the model may create per capture',
                style: theme.textTheme.bodyMedium,
              ),
            ),
            SizedBox(
              width: 72,
              child: Focus(
                onFocusChange: (f) {
                  if (!f) _saveTopics();
                },
                child: TextField(
                  controller: _topics,
                  keyboardType: TextInputType.number,
                  textAlign: TextAlign.center,
                  decoration: const InputDecoration(isDense: true),
                  onSubmitted: (_) => _saveTopics(),
                ),
              ),
            ),
          ],
        ),
        const SizedBox(height: 10),
        Focus(
          onFocusChange: (f) {
            if (!f) _saveTimezone();
          },
          child: TextField(
            controller: _tz,
            decoration: const InputDecoration(
              labelText: 'Time zone, e.g. Europe/Berlin',
              isDense: true,
            ),
            onSubmitted: (_) => _saveTimezone(),
          ),
        ),
        if (_error != null)
          Padding(
            padding: const EdgeInsets.only(top: 8),
            child: Text(
              describeError(_error!),
              style: theme.textTheme.bodySmall!.copyWith(
                color: theme.colorScheme.error,
              ),
            ),
          ),
      ],
    );
  }
}
