import 'dart:async';

import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../api/models.dart';
import '../state/app_state.dart';
import 'settings/rows.dart';
import 'widgets/common.dart';
import 'widgets/toasts.dart';

/// Settings → Maintenance: the nightly schedule, its jobs, how much the
/// model may do with open tasks, "Run now" with live progress, and the
/// last run. Everything saves on change.
class MaintenanceSection extends StatefulWidget {
  const MaintenanceSection({super.key});
  @override
  State<MaintenanceSection> createState() => _MaintenanceSectionState();
}

class _MaintenanceSectionState extends State<MaintenanceSection> {
  ServerSettings? _s;
  MaintenanceStatus? _status;
  Object? _error;
  final _time = TextEditingController();
  final _hours = TextEditingController();
  int _seenRuns = -1;
  bool _running = false;
  bool _editSchedule = false;

  @override
  void initState() {
    super.initState();
    _load();
  }

  @override
  void dispose() {
    _time.dispose();
    _hours.dispose();
    super.dispose();
  }

  Future<void> _load() async {
    final api = context.read<AppState>().api;
    try {
      final s = await api.settings();
      MaintenanceStatus? st;
      try {
        st = await api.maintenanceStatus();
      } catch (_) {
        st = null;
      }
      if (!mounted) return;
      setState(() {
        _s = s;
        _status = st;
        _error = null;
        _time.text = s.maintenance.at;
        _hours.text = s.maintenance.every > 0 ? '${s.maintenance.every}' : '6';
      });
    } catch (e) {
      if (mounted) setState(() => _error = e);
    }
  }

  Future<void> _save(Map<String, dynamic> patch) async {
    try {
      final s = await context.read<AppState>().api.updateSettings(patch);
      if (!mounted) return;
      setState(() => _s = s);
      showSaved(context);
      unawaited(_reloadStatus());
    } catch (e) {
      if (mounted) showError(context, e);
    }
  }

  Future<void> _reloadStatus() async {
    try {
      final st = await context.read<AppState>().api.maintenanceStatus();
      if (mounted) setState(() => _status = st);
    } catch (_) {}
  }

  void _saveMaintenance(Map<String, dynamic> m) => _save({'maintenance': m});

  void _saveTime() {
    final v = _time.text.trim();
    if (!RegExp(r'^\d{1,2}:\d{2}$').hasMatch(v)) {
      showError(context, 'Use a time like 03:30.');
      return;
    }
    final parts = v.split(':');
    final at = '${parts[0].padLeft(2, '0')}:${parts[1]}';
    if (at != _s?.maintenance.at || (_s?.maintenance.every ?? 0) > 0) {
      _saveMaintenance({'at': at, 'every': 0});
    }
  }

  void _saveHours() {
    final n = int.tryParse(_hours.text.trim());
    if (n == null || n < 1 || n > 168) {
      showError(context, 'Use a number of hours between 1 and 168.');
      return;
    }
    if (n != _s?.maintenance.every) _saveMaintenance({'every': n});
  }

  Future<void> _runNow() async {
    final state = context.read<AppState>();
    setState(() => _running = true);
    try {
      await state.api.runMaintenance();
      if (mounted) {
        showToast(context, 'Maintenance started.', key: 'maintenance');
      }
    } on ApiException catch (e) {
      if (!mounted) return;
      showToast(
        context,
        e.code == 'already_running'
            ? 'Maintenance is already running.'
            : describeError(e),
        key: 'maintenance',
        error: e.code != 'already_running',
      );
    } catch (e) {
      if (mounted) showError(context, e);
    } finally {
      if (mounted) setState(() => _running = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final state = context.watch<AppState>();
    final s = _s;
    if (s == null) {
      return SettingsPage(
        title: 'Maintenance',
        children: [
          _error != null
              ? Text(describeError(_error!), style: theme.textTheme.bodySmall)
              : const LinearProgressIndicator(minHeight: 2),
        ],
      );
    }
    if (state.maintenanceRuns != _seenRuns) {
      _seenRuns = state.maintenanceRuns;
      if (_seenRuns > 0) {
        WidgetsBinding.instance.addPostFrameCallback((_) => _reloadStatus());
      }
    }
    final m = s.maintenance;
    final progress = state.maintenanceProgress;
    final st = _status;
    Widget job(String key, String title, String hint, bool value) =>
        SettingsRow(
          label: title,
          hint: hint,
          trailing: Switch(
            key: Key('job-$key'),
            value: value,
            onChanged: (v) => _saveMaintenance({key: v}),
          ),
        );
    return SettingsPage(
      title: 'Maintenance',
      children: [
        SettingsRow(
          label: 'Run maintenance',
          hint: m.enabled
              ? '${m.scheduleLabel}${st?.next != null ? '  ·  next run ${nextRunLabel(st!.next)}' : ''}'
              : 'Off. The jobs below only run when you press Run now.',
          trailing: Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              RowValue(m.scheduleLabel, muted: !m.enabled),
              RowButton(
                _editSchedule ? 'Done' : 'Change',
                key: const Key('maintenance-schedule-change'),
                onPressed: () => setState(() => _editSchedule = !_editSchedule),
              ),
              Switch(
                key: const Key('maintenance-enabled'),
                value: m.enabled,
                onChanged: (v) => _saveMaintenance({'enabled': v}),
              ),
            ],
          ),
          child: !_editSchedule
              ? null
              : Row(
                  children: [
                    SegmentedButton<bool>(
                      key: const Key('maintenance-mode'),
                      segments: const [
                        ButtonSegment(value: false, label: Text('Daily at')),
                        ButtonSegment(value: true, label: Text('Every hours')),
                      ],
                      selected: {m.every > 0},
                      showSelectedIcon: false,
                      style: const ButtonStyle(
                        visualDensity: VisualDensity.compact,
                      ),
                      onSelectionChanged: (sel) {
                        if (sel.first) {
                          _saveHours();
                        } else {
                          _saveTime();
                        }
                      },
                    ),
                    const SizedBox(width: 10),
                    SizedBox(
                      width: 110,
                      child: m.every > 0
                          ? Focus(
                              onFocusChange: (f) {
                                if (!f) _saveHours();
                              },
                              child: TextField(
                                key: const Key('maintenance-hours'),
                                controller: _hours,
                                keyboardType: TextInputType.number,
                                textAlign: TextAlign.center,
                                decoration: fieldDecoration(
                                  context,
                                  suffix: const Padding(
                                    padding: EdgeInsets.only(right: 8, top: 12),
                                    child: Text('h'),
                                  ),
                                ),
                                onSubmitted: (_) => _saveHours(),
                              ),
                            )
                          : Focus(
                              onFocusChange: (f) {
                                if (!f) _saveTime();
                              },
                              child: TextField(
                                key: const Key('maintenance-time'),
                                controller: _time,
                                textAlign: TextAlign.center,
                                decoration: fieldDecoration(
                                  context,
                                  hint: '03:30',
                                ),
                                onSubmitted: (_) => _saveTime(),
                              ),
                            ),
                    ),
                  ],
                ),
        ),
        job(
          'integrity',
          'Integrity',
          'removes links to deleted topics, reports oddities',
          m.integrity,
        ),
        job(
          'untagged',
          'Untagged',
          'files notes and tasks without a topic',
          m.untagged,
        ),
        job(
          'duplicates',
          'Duplicates',
          'links likely duplicates and proposes merges in the inbox',
          m.duplicates,
        ),
        job(
          'summaries',
          'Summaries',
          'keeps a short automatic summary on each topic',
          m.summaries,
        ),
        SettingsRow(
          label: 'Help with open tasks',
          hint: 'The model drafts from your notes and starts research; everything has a receipt and undo.',
          trailing: SegmentedButton<String>(
            key: const Key('maintenance-assist'),
            segments: const [
              ButtonSegment(value: 'off', label: Text('Off')),
              ButtonSegment(
                value: 'propose',
                label: Text('Propose in the inbox'),
              ),
              ButtonSegment(value: 'auto', label: Text('Do it')),
            ],
            selected: {m.assist},
            showSelectedIcon: false,
            style: const ButtonStyle(visualDensity: VisualDensity.compact),
            onSelectionChanged: (sel) =>
                _saveMaintenance({'assist': sel.first}),
          ),
        ),
        SettingsRow(
          label: 'Run now',
          hint: progress == null
              ? (st?.next != null && m.enabled
                    ? 'Next run: ${nextRunLabel(st!.next)}'
                    : 'Runs every job that is switched on.')
              : null,
          trailing: FilledButton.tonal(
            key: const Key('maintenance-run'),
            onPressed: _running || progress != null ? null : _runNow,
            child: Text(progress != null ? 'Running…' : 'Run now'),
          ),
          child: progress == null
              ? null
              : Row(
                  key: const Key('maintenance-progress'),
                  children: [
                    SizedBox(
                      width: 12,
                      height: 12,
                      child: CircularProgressIndicator(
                        strokeWidth: 1.5,
                        color: theme.colorScheme.primary,
                      ),
                    ),
                    const SizedBox(width: 8),
                    Expanded(
                      child: Text(
                        progress.summary.isEmpty
                            ? '${MaintenanceJob(name: progress.job).label}…'
                            : '${MaintenanceJob(name: progress.job).label}: ${progress.summary}',
                        style: secondaryStyle(context),
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                      ),
                    ),
                  ],
                ),
        ),
        if (st?.last != null) _LastRun(run: st!.last!),
      ],
    );
  }
}

/// The last run: when, then job · checked · changed · proposed, with the
/// notes as muted lines.
class _LastRun extends StatelessWidget {
  const _LastRun({required this.run});
  final MaintenanceRun run;
  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final scheme = theme.colorScheme;
    final body = theme.textTheme.bodyMedium!;
    final head = theme.textTheme.labelSmall!.copyWith(
      letterSpacing: 1.0,
      color: scheme.onSurfaceVariant,
    );
    Widget cell(
      String t, {
      TextStyle? style,
      TextAlign align = TextAlign.right,
    }) => Padding(
      padding: const EdgeInsets.symmetric(vertical: 4, horizontal: 6),
      child: Text(t, style: style ?? body, textAlign: align),
    );
    return Padding(
      key: const Key('maintenance-last'),
      padding: const EdgeInsets.symmetric(vertical: 10),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            'Last run ${timeAgo(run.finished ?? run.started)}${run.trigger == 'manual' ? ' (manual)' : ''}',
            style: theme.textTheme.bodyLarge!.copyWith(fontSize: 15),
          ),
          const SizedBox(height: 6),
          Table(
            columnWidths: const {
              0: FlexColumnWidth(),
              1: IntrinsicColumnWidth(),
              2: IntrinsicColumnWidth(),
              3: IntrinsicColumnWidth(),
            },
            children: [
              TableRow(
                children: [
                  cell('JOB', style: head, align: TextAlign.left),
                  cell('CHECKED', style: head),
                  cell('CHANGED', style: head),
                  cell('PROPOSED', style: head),
                ],
              ),
              for (final j in run.jobs)
                TableRow(
                  children: [
                    cell(
                      j.skipped ? '${j.label} (skipped)' : j.label,
                      style: body.copyWith(
                        color: j.error.isNotEmpty ? scheme.error : null,
                      ),
                      align: TextAlign.left,
                    ),
                    cell(j.skipped ? '–' : '${j.checked}'),
                    cell(j.skipped ? '–' : '${j.changed}'),
                    cell(j.skipped ? '–' : '${j.proposed}'),
                  ],
                ),
            ],
          ),
          for (final j in run.jobs) ...[
            if (j.error.isNotEmpty)
              Padding(
                padding: const EdgeInsets.only(left: 6, top: 2),
                child: Text(
                  '${j.label}: ${j.error}',
                  style: theme.textTheme.bodySmall!.copyWith(
                    color: scheme.error,
                  ),
                ),
              ),
            for (final n in j.notes)
              Padding(
                padding: const EdgeInsets.only(left: 6, top: 2),
                child: Text(
                  '${j.label}: $n',
                  style: theme.textTheme.bodySmall!.copyWith(
                    color: scheme.onSurfaceVariant,
                  ),
                ),
              ),
          ],
        ],
      ),
    );
  }
}
