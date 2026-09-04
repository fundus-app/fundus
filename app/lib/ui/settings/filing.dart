import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../../api/models.dart';
import '../../state/app_state.dart';
import '../widgets/common.dart';
import 'rows.dart';

/// Filing (autonomy): what the model may do on its own, and the time zone
/// due dates are read in.
class FilingSection extends StatefulWidget {
  const FilingSection({super.key});
  @override
  State<FilingSection> createState() => _FilingSectionState();
}

class _FilingSectionState extends State<FilingSection> {
  Autonomy? _a;
  Object? _error;
  final _tz = TextEditingController();
  final _topics = TextEditingController();

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
    try {
      final s = await context.read<AppState>().api.updateSettings(patch);
      if (!mounted) return;
      setState(() => _a = s.autonomy);
      showSaved(context);
      await context.read<AppState>().checkHealth();
    } catch (e) {
      if (mounted) showError(context, e);
    }
  }

  void _saveAutonomy(Autonomy a) {
    setState(() => _a = a);
    _save({'autonomy': a.toJson()});
  }

  void _saveTopics() {
    final a = _a;
    final n = int.tryParse(_topics.text.trim());
    if (a == null || n == null || n < 0 || n == a.maxNewTopicsPerCapture) {
      return;
    }
    _saveAutonomy(a.copyWith(maxNewTopicsPerCapture: n));
  }

  void _saveTimezone() {
    final tz = _tz.text.trim();
    if (tz.isEmpty) return;
    _save({'timezone': tz});
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final a = _a;
    if (a == null) {
      return SettingsPage(
        title: 'Filing',
        children: [
          _error != null
              ? Text(describeError(_error!), style: theme.textTheme.bodySmall)
              : const LinearProgressIndicator(minHeight: 2),
        ],
      );
    }
    return SettingsPage(
      title: 'Filing',
      children: [
        SettingsRow(
          label: 'Create notes and tasks without asking',
          hint: 'Off: every capture waits in the inbox as a proposal.',
          trailing: Switch(
            key: const Key('filing-auto'),
            value: a.autoCreate,
            onChanged: (v) => _saveAutonomy(a.copyWith(autoCreate: v)),
          ),
        ),
        SettingsRow(
          label:
              'Park captures below ${(a.minConfidence * 100).round()}% confidence',
          hint: 'Below this, the model asks instead of filing.',
          trailing: SizedBox(
            width: 220,
            child: Slider(
              key: const Key('filing-confidence'),
              value: a.minConfidence.clamp(0, 1),
              min: 0,
              max: 1,
              divisions: 20,
              label: '${(a.minConfidence * 100).round()}%',
              onChanged: (v) =>
                  setState(() => _a = a.copyWith(minConfidence: v)),
              onChangeEnd: (v) => _saveAutonomy(a.copyWith(minConfidence: v)),
            ),
          ),
        ),
        SettingsRow(
          label: 'Topics the model may create per capture',
          trailing: SizedBox(
            width: 80,
            child: Focus(
              onFocusChange: (f) {
                if (!f) _saveTopics();
              },
              child: TextField(
                key: const Key('filing-topics'),
                controller: _topics,
                keyboardType: TextInputType.number,
                textAlign: TextAlign.center,
                decoration: fieldDecoration(context),
                onSubmitted: (_) => _saveTopics(),
              ),
            ),
          ),
        ),
        SettingsRow(
          label: 'Time zone',
          hint: 'For due dates like “tomorrow”, e.g. Europe/Berlin.',
          trailing: SizedBox(
            width: 220,
            child: Focus(
              onFocusChange: (f) {
                if (!f) _saveTimezone();
              },
              child: TextField(
                key: const Key('filing-timezone'),
                controller: _tz,
                decoration: fieldDecoration(context),
                onSubmitted: (_) => _saveTimezone(),
              ),
            ),
          ),
        ),
      ],
    );
  }
}
