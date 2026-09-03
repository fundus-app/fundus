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
    name: 'gemini',
    title: 'Google Gemini',
    blurb:
        'Gemini models. Fast filing, capable conversation, dictation included.',
    keyUrl: 'https://aistudio.google.com/apikey',
    keyLabel: 'aistudio.google.com/apikey',
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

/// "gpt-5.4-mini (OpenAI)" or "rules", from a `provider/model` pair or string.
String modelDisplay(String providerModel, [String? model]) {
  var provider = providerModel;
  var m = model;
  if (m == null) {
    final i = providerModel.indexOf('/');
    provider = i < 0 ? '' : providerModel.substring(0, i);
    m = i < 0 ? providerModel : providerModel.substring(i + 1);
  }
  if (provider == 'fake' || m == 'heuristic') return 'rules';
  if (provider.isEmpty) return m;
  return '$m (${providerTitle(provider)})';
}

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
  String _transcribe = '';
  bool _oauthWaiting = false;
  final _ollamaUrl = TextEditingController();
  bool _ollamaOther = false;

  @override
  void initState() {
    super.initState();
    _loadSettings();
  }

  @override
  void dispose() {
    _key.dispose();
    _ollamaUrl.dispose();
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
    if (c.local) {
      _ollamaUrl.text =
          _settings?.provider('ollama')?.baseUrl ?? 'http://127.0.0.1:11434/v1';
      _loadModels();
    }
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
    _transcribe = m.suggestedTranscribe;
    if (_choice?.local == true) _ollamaCount = m.models.length;
  }

  /// Saves a new Ollama address (no key needed) and lists its models.
  Future<void> _saveOllamaUrl() async {
    final url = _ollamaUrl.text.trim();
    final current = _settings?.provider('ollama')?.baseUrl ?? '';
    if (url.isEmpty || url == current) return;
    setState(() => _busy = true);
    try {
      _settings = await _api.updateSettings({
        'providers': {
          'ollama': {'base_url': url},
        },
      });
    } catch (e) {
      setState(() => _error = e);
    } finally {
      if (mounted) setState(() => _busy = false);
    }
    await _loadModels();
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
        'dictation': {
          'provider': _transcribe.isEmpty ? '' : c.name,
          'model': _transcribe,
        },
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
          if (!_ollamaOther)
            LinkedText(
              style: theme.textTheme.bodyMedium,
              parts: [
                TextPart(
                  'Fundus talks to Ollama at ${_ollamaUrl.text.isEmpty ? 'http://127.0.0.1:11434/v1' : _ollamaUrl.text}. ',
                ),
                TextPart(
                  'Use another machine',
                  onTap: () => setState(() => _ollamaOther = true),
                ),
              ],
            )
          else
            TextField(
              key: const Key('ollama-url'),
              controller: _ollamaUrl,
              decoration: const InputDecoration(
                labelText: 'Ollama address',
                hintText: 'http://127.0.0.1:11434/v1',
                isDense: true,
              ),
              onSubmitted: (_) => _saveOllamaUrl(),
              onEditingComplete: _saveOllamaUrl,
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
            'Recommended models are preselected. You can also type a model name that is not in the list.',
            style: theme.textTheme.bodySmall,
          ),
          const SizedBox(height: 14),
          RoleModelPicker(
            key: const Key('model-triage'),
            label: 'Filing',
            hint: 'reads every capture: fast and cheap',
            models: m.models,
            recommended: m.suggestedTriage,
            value: _triage,
            onChanged: (v) => setState(() => _triage = v),
          ),
          const SizedBox(height: 12),
          RoleModelPicker(
            key: const Key('model-chat'),
            label: 'Conversation',
            hint: 'answers questions: capable',
            models: m.models,
            recommended: m.suggestedChat,
            value: _chat,
            onChanged: (v) => setState(() => _chat = v),
          ),
          const SizedBox(height: 12),
          if (m.suggestedTranscribe.isNotEmpty)
            RoleModelPicker(
              key: const Key('model-transcribe'),
              label: 'Dictation',
              hint: 'turns recordings into text',
              models: m.models,
              recommended: m.suggestedTranscribe,
              value: _transcribe,
              onChanged: (v) => setState(() => _transcribe = v),
            )
          else
            Text(
              'Dictation: not available with this provider.',
              style: secondaryStyle(context),
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

/// One role's model: a text field with a filtered list of the provider's
/// models; the recommended one is preselected and marked. Any name may be
/// typed, even one not in the list.
class RoleModelPicker extends StatefulWidget {
  const RoleModelPicker({
    super.key,
    required this.label,
    required this.models,
    required this.value,
    required this.onChanged,
    this.recommended = '',
    this.hint = '',
  });
  final String label;
  final String hint;
  final List<String> models;
  final String recommended;
  final String value;
  final ValueChanged<String> onChanged;
  @override
  State<RoleModelPicker> createState() => _RoleModelPickerState();
}

class _RoleModelPickerState extends State<RoleModelPicker> {
  late final TextEditingController _ctrl = TextEditingController(
    text: widget.value,
  );
  final _focus = FocusNode();

  @override
  void dispose() {
    _ctrl.dispose();
    _focus.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final scheme = theme.colorScheme;
    final many = widget.models.length > 20;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Row(
          children: [
            Text(widget.label, style: theme.textTheme.titleSmall),
            if (widget.hint.isNotEmpty) ...[
              const SizedBox(width: 8),
              Expanded(
                child: Text(
                  widget.hint,
                  style: secondaryStyle(context),
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                ),
              ),
            ],
          ],
        ),
        const SizedBox(height: 6),
        RawAutocomplete<String>(
          textEditingController: _ctrl,
          focusNode: _focus,
          optionsBuilder: (v) {
            final q = v.text.trim().toLowerCase();
            final all = widget.models;
            final list = q.isEmpty || !many
                ? all
                : all.where((m) => m.toLowerCase().contains(q)).toList();
            final sorted = [...list]
              ..sort((a, b) {
                if (a == widget.recommended) return -1;
                if (b == widget.recommended) return 1;
                return a.compareTo(b);
              });
            return sorted;
          },
          onSelected: (m) {
            _ctrl.text = m;
            widget.onChanged(m);
          },
          fieldViewBuilder: (context, ctrl, focus, onSubmit) => TextField(
            controller: ctrl,
            focusNode: focus,
            style: monoStyle(context, size: 13, color: scheme.onSurface),
            decoration: InputDecoration(
              hintText: widget.recommended.isEmpty
                  ? 'model name'
                  : widget.recommended,
              isDense: true,
              suffixIcon: const Icon(Icons.arrow_drop_down_rounded),
              helperText: many
                  ? 'Type to search ${widget.models.length} models'
                  : null,
            ),
            onChanged: widget.onChanged,
            onSubmitted: (_) => onSubmit(),
          ),
          optionsViewBuilder: (context, onSelected, options) => Align(
            alignment: Alignment.topLeft,
            child: Material(
              elevation: 2,
              borderRadius: BorderRadius.circular(8),
              color: scheme.surfaceContainerLowest,
              child: ConstrainedBox(
                constraints: const BoxConstraints(
                  maxHeight: 280,
                  maxWidth: 512,
                ),
                child: ListView(
                  shrinkWrap: true,
                  padding: const EdgeInsets.symmetric(vertical: 4),
                  children: [
                    for (final m in options)
                      InkWell(
                        onTap: () => onSelected(m),
                        child: Padding(
                          padding: const EdgeInsets.symmetric(
                            horizontal: 12,
                            vertical: 8,
                          ),
                          child: Row(
                            children: [
                              Expanded(
                                child: Text(
                                  m,
                                  style: monoStyle(
                                    context,
                                    size: 13,
                                    color: scheme.onSurface,
                                  ),
                                ),
                              ),
                              if (m == widget.recommended)
                                Text(
                                  'recommended',
                                  style: secondaryStyle(context)
                                      .copyWith(color: scheme.primary),
                                ),
                            ],
                          ),
                        ),
                      ),
                  ],
                ),
              ),
            ),
          ),
        ),
      ],
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
            'Filing model: ${modelDisplay(s.triage.provider, s.triage.model)}',
            style: mono,
          ),
          Text(
            'Conversation model: ${modelDisplay(s.chat.provider, s.chat.model)}',
            style: mono,
          ),
          Text(
            s.dictation.model.isEmpty
                ? 'Dictation: not available with ${providerTitle(s.triage.provider)}'
                : 'Dictation: ${modelDisplay(s.dictation.provider, s.dictation.model)}',
            style: mono,
          ),
          if (s.triage.provider == 'ollama') ...[
            const SizedBox(height: 8),
            _OllamaAddress(current: prov?.baseUrl ?? '', onSaved: _load),
          ],
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
        Text(
          'Configuration file: ${s.path}',
          style: theme.textTheme.labelSmall,
        ),
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

/// Ollama address for the settings screen; saves on submit or blur.
class _OllamaAddress extends StatefulWidget {
  const _OllamaAddress({required this.current, required this.onSaved});
  final String current;
  final VoidCallback onSaved;
  @override
  State<_OllamaAddress> createState() => _OllamaAddressState();
}

class _OllamaAddressState extends State<_OllamaAddress> {
  late final TextEditingController _ctrl = TextEditingController(
    text: widget.current,
  );
  @override
  void dispose() {
    _ctrl.dispose();
    super.dispose();
  }

  Future<void> _save() async {
    final url = _ctrl.text.trim();
    if (url.isEmpty || url == widget.current) return;
    try {
      await context.read<AppState>().api.updateSettings({
        'providers': {
          'ollama': {'base_url': url},
        },
      });
      if (mounted) {
        showSaved(context);
        widget.onSaved();
      }
    } catch (e) {
      if (mounted) showError(context, e);
    }
  }

  @override
  Widget build(BuildContext context) => Focus(
    onFocusChange: (f) {
      if (!f) _save();
    },
    child: TextField(
      key: const Key('ollama-url'),
      controller: _ctrl,
      decoration: const InputDecoration(
        labelText: 'Ollama address',
        hintText: 'http://127.0.0.1:11434/v1',
        isDense: true,
      ),
      onSubmitted: (_) => _save(),
    ),
  );
}
