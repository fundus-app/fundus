// Research: models, task detail button + progress line + result, row menu,
// done/error notices, external callouts, source lines, settings block.
import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:fundus_app/api/models.dart';
import 'package:fundus_app/state/app_state.dart';
import 'package:fundus_app/ui/blocks/block_renderer.dart';
import 'package:fundus_app/ui/inspector/inspector.dart';
import 'package:fundus_app/ui/settings/research.dart';
import 'package:fundus_app/ui/theme.dart';
import 'package:fundus_app/ui/views/list_views.dart';
import 'package:fundus_app/ui/widgets/toasts.dart';
import 'package:provider/provider.dart';

import 'fake_api.dart';

final _now = DateTime.now();

Task _task(
  String id, {
  String text = 'What changed in Flutter 3.47?',
  String kind = 'research',
  String state = 'open',
  List<String> notes = const [],
}) => Task(
  meta: Meta(id: id, type: 'task', rev: 2, createdAt: _now, updatedAt: _now),
  text: text,
  kind: kind,
  state: state,
  notes: notes,
);

ObjectDetail _detail(Task t) =>
    ObjectDetail(meta: t.meta, raw: const {}, task: t);

Widget _app(AppState state, Widget child) => ChangeNotifierProvider.value(
  value: state,
  child: MaterialApp(
    theme: FundusTheme.light(),
    builder: toastBuilder,
    home: Scaffold(body: child),
  ),
);

void _wide(WidgetTester tester) {
  tester.view.physicalSize = const Size(1400, 900);
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.reset);
}

Future<AppState> _open(WidgetTester tester, FakeApi api, String id) async {
  _wide(tester);
  final state = AppState(api);
  await tester.pumpWidget(_app(state, Inspector(onOpen: (_) {})));
  await tester.pump();
  await state.select(id);
  await tester.pump();
  return state;
}

void main() {
  test('models: health.research, research settings, progress, source', () {
    expect(Health.fromJson({'ok': true, 'research': true}).research, isTrue);
    expect(Health.fromJson({'ok': true}).research, isFalse);
    final s = ServerSettings.fromJson({
      'research': {
        'provider': 'openai',
        'model': 'gpt-5.6-terra',
        'backend': 'brave',
        'brave_key_status': 'set',
        'searxng_url': '',
        'available': true,
      },
    });
    expect(s.research.backend, 'brave');
    expect(s.research.braveKeySet, isTrue);
    expect(s.research.available, isTrue);
    final p = ResearchProgress.fromJson({
      'task_id': 't1',
      'step': 'read',
      'summary': 'developer.android.com',
    });
    expect(p.line, 'Reading developer.android.com');
    expect(p.running, isTrue);
    final d = ResearchProgress.fromJson({
      'task_id': 't1',
      'step': 'done',
      'note_id': 'n9',
      'sources': 4,
    });
    expect(d.running, isFalse);
    expect(d.noteId, 'n9');
    final src = ObjectDetail.fromJson({
      'object': {
        'id': 'src_1',
        'type': 'source',
        'url': 'https://docs.flutter.dev/release',
        'title': 'Flutter 3.47',
        'fetched_at': '2026-09-03T10:00:00Z',
        'excerpt': 'x',
      },
    }).source!;
    expect(src.title, 'Flutter 3.47');
    expect(src.host, 'docs.flutter.dev');
    expect(
      Task.fromJson({
        'id': 't',
        'type': 'task',
        'text': 'Was ändert sich?',
        'kind': 'research',
      }).isResearch,
      isTrue,
    );
    expect(
      Task.fromJson({'id': 't', 'type': 'task', 'text': 'Research: old data'})
          .isResearch,
      isTrue,
      reason: 'prefix fallback',
    );
    expect(
      Task.fromJson({'id': 't', 'type': 'task', 'text': 'Call mum'}).isResearch,
      isFalse,
    );
  });

  testWidgets(
    'task detail: Research this starts research, progress line follows, result label',
    (tester) async {
      final api = FakeApi()..objectDetails['t1'] = _detail(_task('t1'));
      final state = await _open(tester, api, 't1');
      expect(find.byKey(const Key('research-button')), findsOneWidget);
      await tester.tap(find.byKey(const Key('research-button')));
      await tester.pump();
      expect(api.researchStarted, ['t1']);
      expect(find.text('Researching…'), findsOneWidget);
      expect(find.byKey(const Key('research-line')), findsOneWidget);

      api.eventBus.add(
        const ServerEvent('research.progress', {
          'task_id': 't1',
          'step': 'search',
          'summary': 'flutter 3.47 release notes',
        }),
      );
      await tester.pump();
      expect(
        find.text('Searching: flutter 3.47 release notes'),
        findsOneWidget,
      );
      api.eventBus.add(
        const ServerEvent('research.progress', {
          'task_id': 't1',
          'step': 'store',
        }),
      );
      await tester.pump();
      expect(find.text('Writing the note…'), findsOneWidget);

      final notices = <AppNotice>[];
      final sub = state.notices.listen(notices.add);
      api.objectDetails['t1'] = _detail(
        _task('t1', state: 'done', notes: ['n9']),
      );
      api.eventBus.add(
        const ServerEvent('research.progress', {
          'task_id': 't1',
          'step': 'done',
          'note_id': 'n9',
          'sources': 3,
        }),
      );
      await tester.pump();
      await tester.pump(const Duration(milliseconds: 300));
      expect(notices.single.noteId, 'n9');
      expect(notices.single.text, contains('3 sources'));
      expect(find.byKey(const Key('research-line')), findsNothing);
      expect(find.text('RESULT · 1'), findsOneWidget);
      expect(
        find.byKey(const Key('research-button')),
        findsNothing,
        reason: 'done tasks cannot be researched',
      );
      unawaited(sub.cancel());
      state.dispose();
    },
  );

  testWidgets(
    'research errors: 409 already running, 503 unavailable, error step',
    (tester) async {
      final api = FakeApi()..objectDetails['t1'] = _detail(_task('t1'));
      final state = await _open(tester, api, 't1');
      api.researchError = const ApiException(
        409,
        'already_running',
        'Research is running.',
      );
      await tester.tap(find.byKey(const Key('research-button')));
      await tester.pump();
      expect(
        find.text('Research is already running for this task.'),
        findsOneWidget,
      );
      api.researchError = const ApiException(
        503,
        'research_unavailable',
        'No search backend is configured.',
      );
      await tester.tap(find.byKey(const Key('research-button')));
      await tester.pump();
      expect(
        find.textContaining('No search backend is configured'),
        findsOneWidget,
      );

      final notices = <AppNotice>[];
      final sub = state.notices.listen(notices.add);
      api.eventBus.add(
        const ServerEvent('research.progress', {
          'task_id': 't1',
          'step': 'error',
          'summary': 'Brave answered 401.',
        }),
      );
      await tester.pump();
      await tester.pump(const Duration(milliseconds: 100));
      expect(find.text('Brave answered 401.'), findsWidgets);
      expect(notices.single.error, isTrue);
      unawaited(sub.cancel());
      state.dispose();
    },
  );

  testWidgets(
    'hidden without a search backend; menu item disabled with a hint',
    (tester) async {
      final api = FakeApi()
        ..researchOn = false
        ..objectDetails['t1'] = _detail(_task('t1'));
      final state = await _open(tester, api, 't1');
      expect(find.byKey(const Key('research-button')), findsNothing);
      await tester.tap(find.byTooltip('Task actions'));
      await tester.pumpAndSettle();
      final item = tester.widget<PopupMenuItem<String>>(
        find.widgetWithText(PopupMenuItem<String>, 'Research this'),
      );
      expect(item.enabled, isFalse);
      expect(
        find.byTooltip('Research needs a search backend — see Settings'),
        findsOneWidget,
      );
      state.dispose();
    },
  );

  testWidgets('task row menu: Research this sends the request', (tester) async {
    final api = FakeApi();
    final state = AppState(api);
    _wide(tester);
    await tester.pumpWidget(
      _app(
        state,
        TaskList(
          tasks: [
            _task('t1', text: 'Compare heat pumps', kind: ''),
            _task('t2'),
          ],
          onOpen: (_) {},
        ),
      ),
    );
    await tester.pump();
    expect(
      find.byKey(const Key('research-tag')),
      findsOneWidget,
      reason: 'only the research task carries the tag',
    );
    await tester.tap(find.byTooltip('Task actions').first);
    await tester.pumpAndSettle();
    await tester.tap(find.text('Research this'));
    await tester.pumpAndSettle();
    expect(api.researchStarted, ['t1']);
    expect(find.text('Researching “Compare heat pumps”…'), findsOneWidget);
    state.dispose();
  });

  testWidgets(
    'blocks: external callout has its label; [[src_…]] renders a source line',
    (tester) async {
      final api = FakeApi()
        ..objectDetails['src_1'] = ObjectDetail(
          meta: const Meta(id: 'src_1', type: 'source'),
          raw: const {},
          source: Source(
            meta: const Meta(id: 'src_1', type: 'source'),
            url: 'https://docs.flutter.dev/release',
            title: 'Flutter 3.47 release notes',
            fetchedAt: DateTime(2026, 9, 3),
          ),
        );
      final state = AppState(api);
      await tester.pumpWidget(
        _app(
          state,
          ListView(
            children: const [
              BlockView(
                block: Block(
                  id: 'b1',
                  type: 'callout',
                  kind: 'external',
                  text: 'Vendors claim 30% better efficiency.',
                ),
              ),
              BlockView(
                block: Block(
                  id: 'b2',
                  type: 'source_ref',
                  ref: 'src_1',
                  text: '',
                ),
              ),
              BlockView(
                block: Block(
                  id: 'b3',
                  type: 'paragraph',
                  text: 'See [[src_1]] for details.',
                ),
              ),
            ],
          ),
        ),
      );
      await tester.pump();
      await tester.pump();
      expect(find.text('EXTERNAL'), findsOneWidget);
      expect(find.text('Flutter 3.47 release notes'), findsNWidgets(2));
      expect(find.text('retrieved 3 Sep 2026'), findsOneWidget);
      expect(
        api.objectRequests.where((id) => id == 'src_1').length,
        1,
        reason: 'sources are cached',
      );
      state.dispose();
    },
  );

  testWidgets(
    'settings: research section saves nested patches, shows backend rows',
    (tester) async {
      final api = FakeApi();
      api.serverSettings['setup_needed'] = false;
      api.serverSettings['research'] = <String, dynamic>{
        'provider': '',
        'model': '',
        'backend': 'auto',
        'search_model': '',
        'searxng_url': '',
        'brave_key_status': 'none',
        'available': false,
        'auto': true,
      };
      final state = AppState(api);
      _wide(tester);
      await tester.pumpWidget(_app(state, const ResearchSection()));
      await tester.pump();
      await tester.pump();
      expect(
        find.text('Start research automatically when you ask for it'),
        findsOneWidget,
      );
      expect(find.byKey(const Key('research-brave-key')), findsNothing);
      await tester.tap(find.byKey(const Key('research-auto')));
      await tester.pump();
      expect(api.settingsPatches.last, {
        'research': {'auto': false},
      });
      await tester.tap(find.byKey(const Key('research-backend')));
      await tester.pumpAndSettle();
      await tester.tap(find.text('Brave').last);
      await tester.pumpAndSettle();
      expect(api.settingsPatches.last, {
        'research': {'backend': 'brave'},
      });
      expect(
        find.byKey(const Key('research-brave-key')),
        findsOneWidget,
        reason: 'Brave key row only for Brave',
      );
      expect(find.byKey(const Key('research-searxng')), findsNothing);
      await tester.enterText(
        find.byKey(const Key('research-brave-key')),
        'BSA-secret',
      );
      await tester.testTextInput.receiveAction(TextInputAction.done);
      await tester.pump();
      expect(api.settingsPatches.last, {
        'research': {'brave_api_key': 'BSA-secret'},
      });
      state.dispose();
    },
  );
}
