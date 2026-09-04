// Maintenance: inbox proposal cards, Settings → Maintenance, About, blocks.
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:fundus_app/api/models.dart';
import 'package:fundus_app/state/app_state.dart';
import 'package:fundus_app/ui/blocks/block_renderer.dart';
import 'package:fundus_app/ui/settings/models.dart';
import 'package:fundus_app/ui/settings_maintenance.dart';
import 'package:fundus_app/ui/theme.dart';
import 'package:fundus_app/ui/views/list_views.dart';
import 'package:fundus_app/ui/widgets/toasts.dart';
import 'package:provider/provider.dart';

import 'fake_api.dart';

final _now = DateTime.now();

Capture _proposal(String id) => Capture.fromJson({
  'id': id,
  'type': 'capture',
  'rev': 1,
  'created_at': _now.toIso8601String(),
  'text': 'Merge topic “Solar” into “Solar system”?',
  'source': 'maintenance',
  'status': 'needs_review',
  'result': {
    'reason': 'proposal',
    'summary': 'Merge topic “Solar” into “Solar system”?',
    'lines': [
      'Both topics link the same three notes.',
      'Notes and tasks move to “Solar system”; “Solar” stays as an alias.',
    ],
    'core_proposal': {'ops': []},
  },
});

Widget _app(AppState state, Widget child) => ChangeNotifierProvider.value(
  value: state,
  child: MaterialApp(
    theme: FundusTheme.light(),
    builder: toastBuilder,
    home: Scaffold(body: child),
  ),
);

void _wide(WidgetTester tester) {
  tester.view.physicalSize = const Size(1400, 1000);
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.reset);
}

void main() {
  test(
    'models: maintenance/embedding settings, health, status, progress, labels',
    () {
      final s = ServerSettings.fromJson({
        'maintenance': {
          'enabled': true,
          'at': '03:30',
          'every': 0,
          'integrity': true,
          'untagged': false,
          'duplicates': true,
          'summaries': true,
          'assist': 'propose',
          'untagged_after_days': 3,
          'keep_runs': 30,
        },
        'embedding': {
          'provider': 'openai',
          'model': 'text-embedding-4',
          'available': true,
        },
      });
      expect(s.maintenance.scheduleLabel, 'Daily at 03:30');
      expect(s.maintenance.untagged, isFalse);
      expect(s.maintenance.assist, 'propose');
      expect(const MaintenanceSettings(every: 6).scheduleLabel, 'Every 6 h');
      expect(s.embedding.available, isTrue);
      final h = Health.fromJson({
        'ok': true,
        'maintenance': {
          'enabled': true,
          'running': false,
          'next': '2026-09-05T01:30:00Z',
        },
        'embedding': true,
      });
      expect(h.maintenance.enabled, isTrue);
      expect(h.embedding, isTrue);
      final st = MaintenanceStatus.fromJson({
        'enabled': true,
        'running': false,
        'next': '2026-09-05T01:30:00Z',
        'last': {
          'id': 'run_1',
          'trigger': 'schedule',
          'started': '2026-09-04T01:30:00Z',
          'finished': '2026-09-04T01:31:00Z',
          'jobs': [
            {
              'name': 'integrity',
              'checked': 120,
              'changed': 2,
              'proposed': 0,
              'notes': ['Removed 2 links to deleted topics.'],
            },
            {'name': 'duplicates', 'skipped': true},
          ],
        },
        'runs': [],
      });
      expect(st.last!.jobs.first.label, 'Integrity');
      expect(st.last!.jobs.last.skipped, isTrue);
      final p = MaintenanceProgress.fromJson({
        'run_id': 'run_2',
        'job': 'untagged',
        'summary': '3 of 12',
        'done': false,
      });
      expect(p.job, 'untagged');
      final now = DateTime(2026, 9, 4, 10);
      expect(
        nextRunLabel(DateTime(2026, 9, 5, 3, 30), now: now),
        'tomorrow 03:30',
      );
      expect(
        nextRunLabel(DateTime(2026, 9, 4, 18, 0), now: now),
        'today 18:00',
      );
      expect(
        nextRunLabel(DateTime(2026, 9, 7, 3, 30), now: now),
        'Mon, 7 Sep 03:30',
      );
      final c = _proposal('cap_1');
      expect(c.isMaintenance, isTrue);
      expect(c.canAccept, isTrue);
      expect(c.result!.lines.length, 2);
      expect(captureSourceLabel('maintenance'), 'from maintenance');
    },
  );

  testWidgets('inbox: maintenance proposal card with Accept / Dismiss only', (
    tester,
  ) async {
    final api = FakeApi()..inboxItems.add(_proposal('cap_1'));
    final state = AppState(api);
    _wide(tester);
    await state.setView(AppView.inbox);
    await tester.pumpWidget(
      _app(state, InboxList(captures: state.inbox, onOpen: (_) {})),
    );
    await tester.pump();
    expect(find.byKey(const Key('maintenance-cap_1')), findsOneWidget);
    expect(find.text('maintenance'), findsOneWidget);
    expect(
      find.text('Merge topic “Solar” into “Solar system”?'),
      findsOneWidget,
    );
    expect(find.text('Both topics link the same three notes.'), findsOneWidget);
    expect(find.byType(TextField), findsNothing, reason: 'no answer field');
    expect(find.text('Answer'), findsNothing);
    expect(find.textContaining('Would create'), findsNothing);
    await tester.tap(find.byKey(const Key('accept-cap_1')));
    await tester.pump();
    expect(api.accepted, ['cap_1']);
    state.dispose();
  });

  testWidgets('inbox: accept conflict shows the message; dismiss dismisses', (
    tester,
  ) async {
    final api = FakeApi()
      ..inboxItems.addAll([_proposal('cap_1'), _proposal('cap_2')]);
    final state = AppState(api);
    _wide(tester);
    await state.setView(AppView.inbox);
    await tester.pumpWidget(
      _app(state, InboxList(captures: state.inbox, onOpen: (_) {})),
    );
    await tester.pump();
    api.acceptError = const ApiException(
      409,
      'conflict',
      'The topics changed since this was proposed.',
    );
    await tester.tap(find.byKey(const Key('accept-cap_1')));
    await tester.pump();
    expect(
      find.textContaining('The topics changed since this was proposed'),
      findsOneWidget,
    );
    expect(api.accepted, isEmpty);
    await tester.tap(find.byKey(const Key('dismiss-cap_2')));
    await tester.pump();
    expect(api.dismissed, ['cap_2']);
    state.dispose();
  });

  testWidgets(
    'settings: toggles, jobs, assist and schedule save nested patches; run now; progress; last run',
    (tester) async {
      final api = FakeApi();
      api.serverSettings['setup_needed'] = false;
      api.serverSettings['chat'] = <String, dynamic>{
        'provider': 'openai',
        'model': 'gpt-5.6-terra',
      };
      api.maintenance = MaintenanceStatus.fromJson({
        'enabled': false,
        'running': false,
        'last': {
          'id': 'run_1',
          'trigger': 'manual',
          'started': _now.subtract(const Duration(hours: 2)).toIso8601String(),
          'finished': _now.subtract(const Duration(hours: 2)).toIso8601String(),
          'jobs': [
            {
              'name': 'integrity',
              'checked': 120,
              'changed': 2,
              'proposed': 0,
              'notes': ['Removed 2 links to deleted topics.'],
            },
            {'name': 'duplicates', 'checked': 40, 'changed': 0, 'proposed': 1},
          ],
        },
      });
      final state = AppState(api);
      _wide(tester);
      await tester.pumpWidget(
        _app(state, const SingleChildScrollView(child: MaintenanceSection())),
      );
      await tester.pump();
      await tester.pump();
      await tester.tap(find.byKey(const Key('maintenance-enabled')));
      await tester.pump();
      expect(api.settingsPatches.last, {
        'maintenance': {'enabled': true},
      });
      await tester.tap(find.byKey(const Key('job-untagged')));
      await tester.pump();
      expect(api.settingsPatches.last, {
        'maintenance': {'untagged': false},
      });
      await tester.tap(find.text('Propose in the inbox'));
      await tester.pump();
      expect(api.settingsPatches.last, {
        'maintenance': {'assist': 'propose'},
      });
      await tester.tap(find.byKey(const Key('maintenance-schedule-change')));
      await tester.pump();
      await tester.enterText(find.byKey(const Key('maintenance-time')), '4:15');
      await tester.testTextInput.receiveAction(TextInputAction.done);
      await tester.pump();
      expect(api.settingsPatches.last, {
        'maintenance': {'at': '04:15', 'every': 0},
      });
      await tester.tap(find.text('Every hours'));
      await tester.pump();
      expect(api.settingsPatches.last, {
        'maintenance': {'every': 6},
      });
      await tester.pump();
      expect(find.textContaining('Every 6 h'), findsOneWidget);

      // Last run table and notes.
      expect(find.byKey(const Key('maintenance-last')), findsOneWidget);
      expect(find.text('Integrity'), findsWidgets);
      expect(find.text('120'), findsOneWidget);
      expect(
        find.text('Integrity: Removed 2 links to deleted topics.'),
        findsOneWidget,
      );

      // Run now, then progress from SSE, then done reloads the status.
      await tester.tap(find.byKey(const Key('maintenance-run')));
      await tester.pump();
      expect(api.maintenanceRunsRequested.length, 1);
      expect(find.text('Maintenance started.'), findsOneWidget);
      api.eventBus.add(
        const ServerEvent('maintenance.progress', {
          'run_id': 'run_2',
          'job': 'untagged',
          'summary': '3 of 12',
          'done': false,
        }),
      );
      await tester.pump();
      expect(find.text('Untagged: 3 of 12'), findsOneWidget);
      expect(find.text('Running…'), findsOneWidget);
      api.eventBus.add(
        const ServerEvent('maintenance.progress', {
          'run_id': 'run_2',
          'job': '',
          'summary': '',
          'done': true,
        }),
      );
      await tester.pump();
      await tester.pump();
      expect(find.byKey(const Key('maintenance-progress')), findsNothing);
      expect(find.text('Run now'), findsOneWidget);
      api.maintenanceRunError = const ApiException(
        409,
        'already_running',
        'running',
      );
      await tester.tap(find.byKey(const Key('maintenance-run')));
      await tester.pump();
      expect(find.text('Maintenance is already running.'), findsOneWidget);
      state.dispose();
    },
  );

  testWidgets(
    'models: Semantic search row opens the picker and saves the embedding model',
    (tester) async {
      final api = FakeApi();
      api.serverSettings['setup_needed'] = false;
      api.serverSettings['triage'] = <String, dynamic>{
        'provider': 'openai',
        'model': 'gpt-5.6-luna',
      };
      api.serverSettings['chat'] = <String, dynamic>{
        'provider': 'openai',
        'model': 'gpt-5.6-terra',
      };
      (api.serverSettings['providers']
          as Map<String, dynamic>)['openai'] = <String, dynamic>{
        'type': 'openai',
        'key_status': 'set',
        'key_hint': '…aRyP',
        'transcription': 'audio',
      };
      final state = AppState(api);
      _wide(tester);
      await tester.pumpWidget(_app(state, const ModelsSection()));
      await tester.pump();
      await tester.pump();
      expect(find.text('gpt-5.6-luna · OpenAI'), findsOneWidget);
      expect(find.text('Not set · search matches words only'), findsOneWidget);
      await tester.tap(find.byKey(const Key('change-embedding')));
      await tester.pumpAndSettle();
      expect(find.text('Embedding model'), findsOneWidget);
      expect(api.modelsRequested, ['openai']);
      await tester.enterText(
        find.descendant(
          of: find.byKey(const Key('picker-model')),
          matching: find.byType(TextField),
        ),
        'text-embedding-4',
      );
      await tester.tap(find.byKey(const Key('picker-save')));
      await tester.pumpAndSettle();
      expect(api.settingsPatches.last, {
        'embedding': {'provider': 'openai', 'model': 'text-embedding-4'},
      });
      expect(find.text('text-embedding-4 · OpenAI'), findsOneWidget);
      state.dispose();
    },
  );

  testWidgets(
    'blocks: automatic summaries carry a muted auto label with a tooltip',
    (tester) async {
      final state = AppState(FakeApi());
      await tester.pumpWidget(
        _app(
          state,
          const BlockView(
            block: Block(
              id: 'b1',
              type: 'callout',
              kind: 'info',
              text: 'Automatic summary (3 Sep): balcony solar, 3 kWh a day.',
            ),
          ),
        ),
      );
      await tester.pump();
      expect(find.byKey(const Key('auto-label')), findsOneWidget);
      expect(
        find.byTooltip(
          'Written by maintenance; edit or pin it to keep your own words',
        ),
        findsOneWidget,
      );
      state.dispose();
    },
  );
}
