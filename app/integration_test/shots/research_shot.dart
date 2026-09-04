// Screenshot driver, not part of the suite (no `_test` suffix). Runs the
// real shell against the in-memory fake API so research progress and a
// result note can be staged without a search backend, then captures the
// screen with grim (Wayland). FUNDUS_SHOT=research|research-note|note-edit|
// inbox-proposal|maintenance,
// FUNDUS_SHOT_OUT=<png>:
//   FUNDUS_SHOT=research FUNDUS_SHOT_OUT=/tmp/research.png \
//   flutter test integration_test/shots/research_shot.dart -d linux
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:fundus_app/api/models.dart';
import 'package:fundus_app/state/app_state.dart';
import 'package:fundus_app/state/chat_state.dart';
import 'package:fundus_app/state/settings.dart';
import 'package:fundus_app/ui/app_shell.dart';
import 'package:fundus_app/ui/theme.dart';
import 'package:fundus_app/ui/widgets/toasts.dart';
import 'package:integration_test/integration_test.dart';
import 'package:provider/provider.dart';
import 'package:window_manager/window_manager.dart';

import '../../test/fake_api.dart';

final _now = DateTime.now();

FakeApi _seed() {
  final api = FakeApi()..setupNeeded = false;
  api.serverSettings['setup_needed'] = false;
  final task = Task(
    meta: Meta(
      id: 'task_1',
      type: 'task',
      rev: 3,
      createdAt: _now.subtract(const Duration(minutes: 12)),
      updatedAt: _now,
    ),
    text: 'What do the Flutter 3.47 release notes mean for Fundus?',
    kind: 'research',
    notes: const ['note_1'],
    topics: const ['topic_1'],
    topicNames: const ['Fundus'],
  );
  final note = Note(
    meta: Meta(
      id: 'note_1',
      type: 'note',
      rev: 2,
      createdAt: _now,
      updatedAt: _now,
    ),
    kind: 'note',
    title: 'Flutter 3.47: what changed',
    topics: const ['topic_1'],
    topicNames: const ['Fundus'],
    body: const Doc([
      Block(
        id: 'b1',
        type: 'paragraph',
        text: 'Flutter 3.47 ships Dart 3.13, Impeller as the only Linux renderer and a reworked text input pipeline. For Fundus the renderer change matters most: the Linux build already runs on Impeller.',
      ),
      Block(id: 'b2', type: 'heading', level: 2, text: 'What changed'),
      Block(
        id: 'b3',
        type: 'list',
        items: [
          'Impeller is the default on Linux and Windows; the Skia flag is gone.',
          'Variable fonts render through the new text pipeline (Inter, Fraunces work).',
          'Integration tests can size desktop windows through window_manager 0.5.',
        ],
      ),
      Block(
        id: 'b4',
        type: 'callout',
        kind: 'external',
        text: 'The release post claims 30% faster first frame on desktop; that figure comes from Google\'s own benchmark app.',
      ),
      Block(id: 'b5', type: 'heading', level: 2, text: 'Sources'),
      Block(id: 'b6', type: 'source_ref', ref: 'src_1', text: 'release notes'),
      Block(
        id: 'b7',
        type: 'source_ref',
        ref: 'src_2',
        text: 'renderer announcement',
      ),
    ]),
  );
  api.objectDetails['task_1'] = ObjectDetail(
    meta: task.meta,
    raw: const {},
    task: task,
    topics: const [LinkRef(id: 'topic_1', type: 'topic', title: 'Fundus')],
    receipts: [
      Receipt(
        txnId: 'txn_1',
        at: _now.subtract(const Duration(minutes: 12)),
        actor: 'user',
        summary: 'Created task.',
      ),
    ],
  );
  api.objectDetails['note_1'] = ObjectDetail(
    meta: note.meta,
    raw: const {},
    note: note,
    topics: const [LinkRef(id: 'topic_1', type: 'topic', title: 'Fundus')],
    backlinks: const [
      LinkRef(
        id: 'task_1',
        type: 'task',
        title: 'What do the Flutter 3.47 release notes mean for Fundus?',
      ),
    ],
  );
  api.objectDetails['src_1'] = ObjectDetail(
    meta: const Meta(id: 'src_1', type: 'source'),
    raw: const {},
    source: Source(
      meta: const Meta(id: 'src_1', type: 'source'),
      url:
          'https://docs.flutter.dev/release/release-notes/release-notes-3.47.0',
      title: 'Flutter 3.47.0 release notes',
      fetchedAt: _now,
    ),
  );
  api.objectDetails['src_2'] = ObjectDetail(
    meta: const Meta(id: 'src_2', type: 'source'),
    raw: const {},
    source: Source(
      meta: const Meta(id: 'src_2', type: 'source'),
      url: 'https://medium.com/flutter/impeller-everywhere',
      title: 'Impeller everywhere',
      fetchedAt: _now.subtract(const Duration(days: 1)),
    ),
  );
  api.taskLists['open'] = [task];
  api.objectTypes['note_1'] = 'note';
  // Maintenance: two parked proposals, a schedule, a last run.
  Capture proposal(
    String id,
    String summary,
    List<String> lines,
    int minutesAgo,
  ) => Capture.fromJson({
    'id': id,
    'type': 'capture',
    'rev': 1,
    'created_at': _now
        .subtract(Duration(minutes: minutesAgo))
        .toIso8601String(),
    'text': summary,
    'source': 'maintenance',
    'status': 'needs_review',
    'result': {
      'reason': 'proposal',
      'summary': summary,
      'lines': lines,
      'core_proposal': {},
    },
  });
  api.inboxItems.addAll([
    proposal('cap_m1', 'Merge topic “Solar” into “Solar system”?', [
      'Both topics link the same three notes and one task.',
      'Notes and tasks move to “Solar system”; “Solar” stays as an alias.',
    ], 410),
    proposal('cap_m2', 'File 3 untagged notes under “Fundus”?', [
      'The notes mention the release notes, the inbox walkthrough and the e-ink remote.',
      'They get the topic; nothing else changes.',
    ], 412),
  ]);
  api.serverSettings['setup_needed'] = false;
  api.serverSettings['triage'] = <String, dynamic>{
    'provider': 'openai',
    'model': 'gpt-5.6-luna',
  };
  api.serverSettings['chat'] = <String, dynamic>{
    'provider': 'openai',
    'model': 'gpt-5.6-terra',
  };
  api.serverSettings['maintenance'] = <String, dynamic>{
    'enabled': true,
    'at': '03:30',
    'every': 0,
    'integrity': true,
    'untagged': true,
    'duplicates': true,
    'summaries': true,
    'assist': 'propose',
    'untagged_after_days': 3,
    'keep_runs': 30,
  };
  api.serverSettings['embedding'] = <String, dynamic>{
    'provider': 'openai',
    'model': 'text-embedding-4',
    'available': true,
  };
  final tomorrow = DateTime(_now.year, _now.month, _now.day + 1, 3, 30);
  api.maintenanceNext = tomorrow;
  api.maintenance = MaintenanceStatus.fromJson({
    'enabled': true,
    'running': false,
    'next': tomorrow.toIso8601String(),
    'last': {
      'id': 'run_7',
      'trigger': 'schedule',
      'started': DateTime(
        _now.year,
        _now.month,
        _now.day,
        3,
        30,
      ).toIso8601String(),
      'finished': DateTime(
        _now.year,
        _now.month,
        _now.day,
        3,
        31,
      ).toIso8601String(),
      'jobs': [
        {
          'name': 'integrity',
          'checked': 214,
          'changed': 2,
          'proposed': 0,
          'notes': ['Removed 2 links to deleted topics.'],
        },
        {'name': 'untagged', 'checked': 9, 'changed': 6, 'proposed': 1},
        {
          'name': 'duplicates',
          'checked': 214,
          'changed': 0,
          'proposed': 1,
          'notes': ['“Solar” and “Solar system” look like one topic.'],
        },
        {'name': 'summaries', 'checked': 12, 'changed': 3, 'proposed': 0},
      ],
    },
    'runs': [],
  });
  return api;
}

void main() {
  IntegrationTestWidgetsFlutterBinding.ensureInitialized();
  final env = Platform.environment;
  testWidgets('screenshot', (tester) async {
    final api = _seed();
    final state = AppState(api);
    final which = env['FUNDUS_SHOT'] ?? 'research';
    await tester.pumpWidget(
      MultiProvider(
        providers: [
          ChangeNotifierProvider<Settings>.value(
            value: Settings.memory(serverUrl: 'http://127.0.0.1:7433'),
          ),
          ChangeNotifierProvider<AppState>.value(value: state),
          ChangeNotifierProvider<ChatState>(create: (_) => ChatState(api)),
        ],
        child: MaterialApp(
          debugShowCheckedModeBanner: false,
          theme: FundusTheme.light(),
          builder: toastBuilder,
          home: AppShell(
            initialView: switch (which) {
              'research' => 'open',
              'inbox-proposal' || 'maintenance' => 'inbox',
              _ => 'notes',
            },
          ),
        ),
      ),
    );
    try {
      await windowManager.ensureInitialized();
      await windowManager.setSize(const Size(1440, 900));
      for (var i = 0; i < 10; i++) {
        await tester.pump(const Duration(milliseconds: 100));
        if (tester.view.physicalSize.width / tester.view.devicePixelRatio >=
            1000) {
          break;
        }
      }
    } catch (_) {}
    await tester.pump(const Duration(milliseconds: 300));
    if (which == 'research') {
      await state.select('task_1');
      await tester.pump(const Duration(milliseconds: 200));
      api.eventBus.add(
        const ServerEvent('research.progress', {
          'task_id': 'task_1',
          'step': 'read',
          'summary': 'docs.flutter.dev',
        }),
      );
      await tester.pump(const Duration(milliseconds: 200));
    } else if (which == 'research-note') {
      await state.select('note_1');
      await tester.pump(const Duration(milliseconds: 200));
    } else if (which == 'inbox-proposal') {
      await tester.pump(const Duration(milliseconds: 300));
    } else if (which == 'maintenance') {
      await tester.tap(find.byIcon(Icons.settings_outlined));
      for (var i = 0; i < 8; i++) {
        await tester.pump(const Duration(milliseconds: 100));
      }
      final scrollable = find.byType(Scrollable).last;
      await tester.scrollUntilVisible(
        find.text('MAINTENANCE'),
        200,
        scrollable: scrollable,
      );
      await tester.drag(scrollable, const Offset(0, -140));
      for (var i = 0; i < 5; i++) {
        await tester.pump(const Duration(milliseconds: 100));
      }
    } else {
      await state.select('note_1');
      await tester.pump(const Duration(milliseconds: 200));
      await tester.tap(find.byTooltip('Note actions'));
      await tester.pump(const Duration(milliseconds: 300));
      await tester.tap(find.text('Edit as text'));
      await tester.pump(const Duration(milliseconds: 300));
    }
    for (var i = 0; i < 6; i++) {
      await tester.pump(const Duration(milliseconds: 100));
    }
    await tester.runAsync(() async {
      await Future<void>.delayed(const Duration(seconds: 1));
      final r = await Process.run('grim', [env['FUNDUS_SHOT_OUT']!]);
      // ignore: avoid_print
      print('[shot] grim exit ${r.exitCode} ${r.stderr}');
    });
    expect(find.byType(AppShell), findsOneWidget);
  });
}
