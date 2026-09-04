// Whole-body editing of notes and topic summaries, and topic merge.
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:fundus_app/api/models.dart';
import 'package:fundus_app/state/app_state.dart';
import 'package:fundus_app/ui/inspector/inspector.dart';
import 'package:fundus_app/ui/theme.dart';
import 'package:fundus_app/ui/widgets/toasts.dart';
import 'package:provider/provider.dart';

import 'fake_api.dart';

final _now = DateTime.now();

ObjectDetail _note(String id, String md) => ObjectDetail(
  meta: Meta(id: id, type: 'note', rev: 3),
  raw: const {},
  note: Note(
    meta: Meta(id: id, type: 'note', rev: 3, updatedAt: _now),
    kind: 'note',
    title: 'Heat pumps',
    body: Doc([Block(id: 'b1', type: 'paragraph', text: md)]),
  ),
);

Topic _topic(String id, String name, {int rev = 4}) => Topic(
  meta: Meta(id: id, type: 'topic', rev: rev, createdAt: _now),
  kind: 'topic',
  name: name,
);

Widget _app(AppState state, Widget child) => ChangeNotifierProvider.value(
  value: state,
  child: MaterialApp(
    theme: FundusTheme.light(),
    builder: toastBuilder,
    home: Scaffold(body: child),
  ),
);

Future<AppState> _open(WidgetTester tester, FakeApi api, String id) async {
  tester.view.physicalSize = const Size(1400, 900);
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.reset);
  final state = AppState(api);
  await tester.pumpWidget(_app(state, Inspector(onOpen: (_) {})));
  await tester.pump();
  await state.select(id);
  await tester.pump();
  return state;
}

Future<void> _menu(WidgetTester tester, String tooltip, String item) async {
  await tester.tap(find.byTooltip(tooltip));
  await tester.pumpAndSettle();
  await tester.tap(find.text(item).last);
  await tester.pumpAndSettle();
}

void main() {
  testWidgets(
    'Edit as text: 15 px mono editor, Ctrl+Enter sends note.set_markdown',
    (tester) async {
      final api = FakeApi()..objectDetails['n1'] = _note('n1', 'Old body.');
      final state = await _open(tester, api, 'n1');
      await _menu(tester, 'Note actions', 'Edit as text');
      final field = tester.widget<TextField>(
        find.byKey(const Key('markdown-editor')),
      );
      expect(field.style!.fontSize, 15);
      expect(field.maxLines, isNull, reason: 'grows with content');
      expect(field.controller!.text, 'Old body.');
      await tester.enterText(
        find.byKey(const Key('markdown-editor')),
        'New body.\n\n- one\n- two',
      );
      await tester.sendKeyDownEvent(LogicalKeyboardKey.controlLeft);
      await tester.sendKeyEvent(LogicalKeyboardKey.enter);
      await tester.sendKeyUpEvent(LogicalKeyboardKey.controlLeft);
      await tester.pumpAndSettle();
      expect(api.ops.last, {
        'op': 'note.set_markdown',
        'id': 'n1',
        'expected_rev': 3,
        'markdown': 'New body.\n\n- one\n- two',
      });
      expect(find.byKey(const Key('markdown-editor')), findsNothing);
      state.dispose();
    },
  );

  testWidgets(
    'no-op save sends nothing; Esc asks when dirty; 403 shows the message',
    (tester) async {
      final api = FakeApi()..objectDetails['n1'] = _note('n1', 'Same body.');
      final state = await _open(tester, api, 'n1');
      await _menu(tester, 'Note actions', 'Edit as text');
      await tester.tap(find.text('Save'));
      await tester.pumpAndSettle();
      expect(api.ops, isEmpty);
      expect(find.byKey(const Key('markdown-editor')), findsNothing);

      await _menu(tester, 'Note actions', 'Edit as text');
      await tester.enterText(
        find.byKey(const Key('markdown-editor')),
        'Changed.',
      );
      await tester.sendKeyEvent(LogicalKeyboardKey.escape);
      await tester.pumpAndSettle();
      expect(find.text('Discard your edits?'), findsOneWidget);
      await tester.tap(find.text('Discard'));
      await tester.pumpAndSettle();
      expect(find.byKey(const Key('markdown-editor')), findsNothing);

      await _menu(tester, 'Note actions', 'Edit as text');
      await tester.enterText(
        find.byKey(const Key('markdown-editor')),
        'Touches a pinned block.',
      );
      api.commandsError = const ApiException(
        403,
        'forbidden',
        'Block b1 is pinned and cannot be rewritten.',
      );
      await tester.tap(find.text('Save'));
      await tester.pumpAndSettle();
      expect(
        find.textContaining('pinned and cannot be rewritten'),
        findsOneWidget,
      );
      expect(
        find.byKey(const Key('markdown-editor')),
        findsOneWidget,
        reason: 'the text stays for another try',
      );
      state.dispose();
    },
  );

  testWidgets('topic summary: Edit summary as text sends topic.set_summary', (
    tester,
  ) async {
    final api = FakeApi()
      ..objectTypes['top1'] = 'topic'
      ..objectDetails['top1'] = ObjectDetail(
        meta: const Meta(id: 'top1', type: 'topic', rev: 4),
        raw: const {},
        topic: _topic('top1', 'Solar'),
      );
    final state = await _open(tester, api, 'top1');
    await _menu(tester, 'Topic actions', 'Edit summary as text');
    await tester.enterText(
      find.byKey(const Key('markdown-editor')),
      'Balcony solar notes.',
    );
    await tester.tap(find.text('Save'));
    await tester.pumpAndSettle();
    expect(api.ops.last, {
      'op': 'topic.set_summary',
      'id': 'top1',
      'expected_rev': 4,
      'markdown': 'Balcony solar notes.',
    });
    state.dispose();
  });

  testWidgets(
    'Merge into…: filter list, confirm sheet with counts, op id=survivor from=this, navigates',
    (tester) async {
      final solar = _topic('top1', 'Solar');
      final system = _topic('top2', 'Solar system', rev: 7);
      final api = FakeApi()
        ..objectTypes['top1'] = 'topic'
        ..objectTypes['top2'] = 'topic'
        ..objectDetails['top1'] = ObjectDetail(
          meta: solar.meta,
          raw: const {},
          topic: solar,
        )
        ..objectDetails['top2'] = ObjectDetail(
          meta: system.meta,
          raw: const {},
          topic: system,
        )
        ..topicList.addAll([solar, system, _topic('top3', 'Thomas')])
        ..topicPages['top1'] = TopicPage(
          topic: solar,
          notes: [
            for (var i = 0; i < 3; i++)
              Note(
                meta: Meta(id: 'n$i', type: 'note', updatedAt: _now),
                kind: 'note',
                title: 'Note $i',
              ),
          ],
          tasks: [
            Task(
              meta: Meta(id: 't1', type: 'task', createdAt: _now),
              text: 'Task one',
            ),
          ],
          doneTasks: [
            Task(
              meta: Meta(id: 't2', type: 'task', createdAt: _now),
              text: 'Task two',
              state: 'done',
            ),
          ],
        );
      final state = await _open(tester, api, 'top1');
      await _menu(tester, 'Topic actions', 'Merge into…');
      expect(find.text('Merge “Solar” into which topic?'), findsOneWidget);
      await tester.enterText(find.byType(TextField).last, 'sys');
      await tester.pumpAndSettle();
      expect(find.text('Thomas'), findsNothing);
      await tester.tap(find.text('Solar system'));
      await tester.pumpAndSettle();
      expect(
        find.text(
          'Move 3 notes and 2 tasks from “Solar” into “Solar system” and keep “Solar” as an alias?',
        ),
        findsOneWidget,
      );
      await tester.tap(find.text('Merge'));
      await tester.pumpAndSettle();
      expect(api.ops.last, {
        'op': 'topic.merge',
        'id': 'top2',
        'expected_rev': 7,
        'from': 'top1',
      });
      expect(state.selectedId, 'top2');
      expect(find.text('Undo'), findsOneWidget);
      state.dispose();
    },
  );
}
