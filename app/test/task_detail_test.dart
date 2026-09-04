// Task detail header, Delete, Done view, topic Done section, unlink/link.
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:fundus_app/api/models.dart';
import 'package:fundus_app/state/app_state.dart';
import 'package:fundus_app/ui/inspector/inspector.dart';
import 'package:fundus_app/ui/theme.dart';
import 'package:fundus_app/ui/widgets/toasts.dart';
import 'package:fundus_app/ui/views/list_views.dart';
import 'package:provider/provider.dart';

import 'fake_api.dart';

final _now = DateTime.now();

Task _task(
  String id, {
  String state = 'open',
  DateTime? done,
  List<String> topicNames = const [],
}) => Task(
  meta: Meta(id: id, type: 'task', rev: 2, createdAt: _now, updatedAt: _now),
  text: 'Call the dentist $id',
  state: state,
  completedAt: done,
  topicNames: topicNames,
);

const _topicRef = LinkRef(id: 'top1', type: 'topic', title: 'Fundus');

ObjectDetail _taskDetail(String id) => ObjectDetail(
  meta: Meta(id: id, type: 'task', rev: 2),
  raw: const {},
  task: _task(id),
  topics: const [_topicRef],
);

ObjectDetail _noteDetail(String id) => ObjectDetail(
  meta: Meta(id: id, type: 'note', rev: 3),
  raw: const {},
  note: Note(
    meta: Meta(id: id, type: 'note', rev: 3, updatedAt: _now),
    kind: 'note',
    title: 'Receipts as sentences',
  ),
  topics: const [_topicRef],
);

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

Future<void> _pickMenu(WidgetTester tester, String tooltip, String item) async {
  await tester.tap(find.byTooltip(tooltip));
  await tester.pumpAndSettle();
  await tester.tap(find.text(item).last);
  await tester.pumpAndSettle();
}

void main() {
  testWidgets(
    'task header: checkbox completes, no segmented bar, state chip menu',
    (tester) async {
      final api = FakeApi()..objectDetails['t1'] = _taskDetail('t1');
      final state = await _open(tester, api, 't1');
      expect(find.byType(SegmentedButton<String>), findsNothing);
      expect(find.byKey(const Key('task-check')), findsOneWidget);
      expect(find.text('Open'), findsOneWidget); // the state chip
      expect(find.textContaining('in '), findsWidgets);

      await tester.tap(find.byKey(const Key('task-check')));
      await tester.pump();
      expect(api.ops.last['op'], 'task.update');
      expect(api.ops.last['state'], 'done');
      expect(api.ops.last['expected_rev'], 2);

      await tester.tap(find.byKey(const Key('task-state')));
      await tester.pumpAndSettle();
      await tester.tap(find.text('Later').last);
      await tester.pumpAndSettle();
      expect(api.ops.last['state'], 'later');

      // Waiting asks whom or what for and sends both in one op.
      await tester.tap(find.byKey(const Key('task-state')));
      await tester.pumpAndSettle();
      await tester.tap(find.text('Waiting').last);
      await tester.pumpAndSettle();
      expect(find.text('Waiting on'), findsOneWidget);
      await tester.enterText(find.byType(TextField).last, 'Thomas');
      await tester.tap(find.text('Save'));
      await tester.pumpAndSettle();
      expect(api.ops.last['state'], 'waiting');
      expect(api.ops.last['waiting_on'], 'Thomas');
      state.dispose();
    },
  );

  testWidgets(
    'delete from the ⋯ menu: archive op, detail closes, toast with Undo',
    (tester) async {
      final api = FakeApi()..objectDetails['n1'] = _noteDetail('n1');
      final state = await _open(tester, api, 'n1');
      expect(find.text('Receipts as sentences'), findsOneWidget);
      await _pickMenu(tester, 'Note actions', 'Delete');
      expect(api.ops.last, {
        'op': 'object.archive',
        'id': 'n1',
        'expected_rev': 3,
      });
      expect(state.selectedId, isNull);
      expect(find.text('Deleted “Receipts as sentences”'), findsOneWidget);
      await tester.tap(find.text('Undo'));
      await tester.pumpAndSettle();
      expect(api.undone, ['txn_c${api.ops.length}']);
      state.dispose();
    },
  );

  testWidgets('task and topic menus end with Delete after a divider', (
    tester,
  ) async {
    final api = FakeApi()..objectDetails['t1'] = _taskDetail('t1');
    final state = await _open(tester, api, 't1');
    await tester.tap(find.byTooltip('Task actions'));
    await tester.pumpAndSettle();
    final items = tester
        .widgetList<PopupMenuItem<String>>(find.byType(PopupMenuItem<String>))
        .map((i) => i.value)
        .toList();
    expect(items, ['research', 'link_topic', 'copy_id', 'delete']);
    await tester.tap(find.text('Delete'));
    await tester.pumpAndSettle();
    expect(api.ops.last['op'], 'object.archive');
    expect(api.ops.last['id'], 't1');
    state.dispose();
  });

  testWidgets('unlink × in the fact row sends remove_topics', (tester) async {
    final api = FakeApi()..objectDetails['n1'] = _noteDetail('n1');
    final state = await _open(tester, api, 'n1');
    expect(find.textContaining('Fundus', findRichText: true), findsOneWidget);
    await tester.tap(find.byTooltip('Unlink'), warnIfMissed: false);
    await tester.pump();
    expect(api.ops.last['op'], 'note.update');
    expect(api.ops.last['remove_topics'], ['top1']);
    state.dispose();
  });

  testWidgets('Link to topic… lists topics and sends add_topics', (
    tester,
  ) async {
    final api = FakeApi()
      ..objectDetails['t1'] = _taskDetail('t1')
      ..topicList.addAll([
        Topic(
          meta: const Meta(id: 'top1', type: 'topic'),
          kind: 'project',
          name: 'Fundus',
        ),
        Topic(
          meta: const Meta(id: 'top2', type: 'topic'),
          kind: 'topic',
          name: 'Solar',
        ),
        Topic(
          meta: const Meta(id: 'top3', type: 'person'),
          kind: 'person',
          name: 'Thomas',
        ),
      ]);
    final state = await _open(tester, api, 't1');
    await _pickMenu(tester, 'Task actions', 'Link to topic…');
    expect(find.text('Link to which topic?'), findsOneWidget);
    expect(find.text('Fundus'), findsNothing); // already linked
    await tester.enterText(find.byType(TextField).last, 'sol');
    await tester.pumpAndSettle();
    expect(find.text('Thomas'), findsNothing);
    await tester.tap(find.text('Solar'));
    await tester.pumpAndSettle();
    expect(api.ops.last['op'], 'task.update');
    expect(api.ops.last['add_topics'], ['top2']);
    state.dispose();
  });

  testWidgets(
    'topic page: Tasks count excludes done, Done section collapsed then expands',
    (tester) async {
      final api = FakeApi()
        ..objectTypes['top1'] = 'topic'
        ..objectDetails['top1'] = ObjectDetail(
          meta: const Meta(id: 'top1', type: 'topic'),
          raw: const {},
          topic: Topic(
            meta: Meta(id: 'top1', type: 'topic', createdAt: _now),
            kind: 'project',
            name: 'Fundus',
          ),
        )
        ..topicPages['top1'] = TopicPage(
          topic: Topic(
            meta: const Meta(id: 'top1', type: 'topic'),
            kind: 'project',
            name: 'Fundus',
          ),
          tasks: [_task('t1')],
          doneTasks: [
            _task('t2', state: 'done', done: _now),
            _task('t3', state: 'done', done: _now),
          ],
        );
      final state = await _open(tester, api, 'top1');
      expect(find.text('TASKS · 1'), findsOneWidget);
      expect(find.text('DONE · 2'), findsOneWidget);
      expect(find.text('Call the dentist t2'), findsNothing);
      await tester.tap(find.byKey(const Key('topic-done')));
      await tester.pump();
      expect(find.text('Call the dentist t2'), findsOneWidget);
      expect(find.text('Call the dentist t3'), findsOneWidget);
      state.dispose();
    },
  );

  testWidgets(
    'Done view asks for state=done and groups by day; checkbox reopens',
    (tester) async {
      final yesterday = _now.subtract(const Duration(days: 1));
      final api = FakeApi()
        ..taskLists['done'] = [
          _task('t1', state: 'done', done: _now, topicNames: ['Fundus']),
          _task('t2', state: 'done', done: yesterday),
        ];
      final state = AppState(api);
      await tester.pumpWidget(
        _app(state, DoneList(tasks: const [], onOpen: (_) {})),
      );
      expect(find.text('Nothing completed yet.'), findsOneWidget);
      await state.setView(AppView.done);
      expect(api.tasksRequested.last, ['done']);
      await tester.pumpWidget(
        _app(state, DoneList(tasks: state.tasks, onOpen: (_) {})),
      );
      await tester.pump();
      expect(find.text('Today'), findsOneWidget);
      expect(find.text('Yesterday'), findsOneWidget);
      expect(find.textContaining('in Fundus'), findsOneWidget);
      await tester.tap(find.byKey(const Key('reopen-t2')));
      await tester.pump();
      expect(api.ops.last['id'], 't2');
      expect(api.ops.last['state'], 'open');
      expect(
        AppView.values.indexOf(AppView.done),
        AppView.values.indexOf(AppView.later) + 1,
      );
      state.dispose();
    },
  );

  testWidgets('search results tag done tasks', (tester) async {
    final api = FakeApi();
    final state = AppState(api);
    await tester.pumpWidget(
      _app(
        state,
        SearchResults(
          hits: const [
            SearchHit(id: 't1', type: 'task', title: 'Old task', state: 'done'),
            SearchHit(
              id: 't2',
              type: 'task',
              title: 'Fresh task',
              state: 'open',
            ),
          ],
          onOpen: (_) {},
          query: 'task',
        ),
      ),
    );
    expect(find.text('done'), findsOneWidget);
    expect(find.text('task · open'), findsOneWidget);
    state.dispose();
  });
}
