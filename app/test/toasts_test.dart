// The toast system: lifetime, ×, Esc, hover pause, stacking, replace-by-key,
// Undo turning into "Undone.".
import 'package:flutter/gestures.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:fundus_app/ui/theme.dart';
import 'package:fundus_app/ui/widgets/toasts.dart';

Widget _app(ToastController c) => MaterialApp(
  theme: FundusTheme.light(),
  builder: (context, child) => ToastScope(controller: c, child: child!),
  home: const Scaffold(body: SizedBox.expand()),
);

void main() {
  testWidgets('informational toast hides after 4 s, action toast after 8 s', (
    tester,
  ) async {
    final c = ToastController();
    await tester.pumpWidget(_app(c));
    c.show('Saved.');
    c.show('Deleted “x”', onAction: () async => true);
    await tester.pump();
    expect(find.text('Saved.'), findsOneWidget);
    expect(find.text('Deleted “x”'), findsOneWidget);
    await tester.pump(const Duration(seconds: 4, milliseconds: 100));
    expect(find.text('Saved.'), findsNothing);
    expect(find.text('Deleted “x”'), findsOneWidget);
    await tester.pump(const Duration(seconds: 4));
    expect(find.text('Deleted “x”'), findsNothing);
    expect(c.toasts, isEmpty);
    c.dispose();
  });

  testWidgets('× and Esc dismiss; Esc takes the newest first', (tester) async {
    final c = ToastController();
    await tester.pumpWidget(_app(c));
    final a = c.show('first');
    c.show('second');
    await tester.pump();
    await tester.sendKeyEvent(LogicalKeyboardKey.escape);
    await tester.pump();
    expect(find.text('second'), findsNothing);
    expect(find.text('first'), findsOneWidget);
    await tester.tap(find.byKey(Key('toast-close-$a')));
    await tester.pump();
    expect(find.text('first'), findsNothing);
    c.dispose();
  });

  testWidgets('hovering pauses the timer', (tester) async {
    final c = ToastController();
    await tester.pumpWidget(_app(c));
    final id = c.show('hover me');
    await tester.pump();
    final mouse = await tester.createGesture(kind: PointerDeviceKind.mouse);
    await mouse.addPointer(location: Offset.zero);
    addTearDown(mouse.removePointer);
    await mouse.moveTo(tester.getCenter(find.byKey(Key('toast-$id'))));
    await tester.pump();
    await tester.pump(const Duration(seconds: 6));
    expect(find.byKey(Key('toast-$id')), findsOneWidget, reason: 'paused');
    await mouse.moveTo(Offset.zero);
    await tester.pump();
    await tester.pump(const Duration(seconds: 5));
    expect(find.byKey(Key('toast-$id')), findsNothing);
    c.dispose();
  });

  testWidgets('at most three, newest at the bottom; same key replaces', (
    tester,
  ) async {
    final c = ToastController();
    await tester.pumpWidget(_app(c));
    for (final t in ['one', 'two', 'three', 'four']) {
      c.show(t);
    }
    await tester.pump();
    expect(find.text('one'), findsNothing);
    expect(c.toasts.map((t) => t.text), ['two', 'three', 'four']);
    expect(
      tester.getTopLeft(find.text('four')).dy,
      greaterThan(tester.getTopLeft(find.text('two')).dy),
    );
    c.show('Linked task to Fundus.', key: 'txn:1');
    c.show('Unlinked task from Fundus.', key: 'txn:1');
    await tester.pump();
    expect(find.text('Linked task to Fundus.'), findsNothing);
    expect(find.text('Unlinked task from Fundus.'), findsOneWidget);
    expect(c.toasts.length, 3);
    c.dispose();
  });

  testWidgets('Undo runs the action, then the toast reads "Undone." for 2 s', (
    tester,
  ) async {
    final c = ToastController();
    await tester.pumpWidget(_app(c));
    var ran = 0;
    final id = c.show(
      'Deleted “x”',
      onAction: () async {
        ran++;
        return true;
      },
    );
    await tester.pump();
    await tester.tap(find.byKey(Key('toast-action-$id')));
    await tester.pump();
    expect(ran, 1);
    expect(find.text('Undone.'), findsOneWidget);
    expect(find.text('Undo'), findsNothing);
    await tester.pump(const Duration(seconds: 2, milliseconds: 100));
    expect(find.text('Undone.'), findsNothing);
    c.dispose();
  });

  testWidgets('errors keep their red edge and a close button', (tester) async {
    final c = ToastController();
    await tester.pumpWidget(_app(c));
    final id = c.show('Cannot reach Fundus', kind: ToastKind.error);
    await tester.pump();
    expect(c.toasts.single.kind, ToastKind.error);
    expect(find.byKey(Key('toast-close-$id')), findsOneWidget);
    await tester.pump(const Duration(seconds: 8, milliseconds: 100));
    expect(find.text('Cannot reach Fundus'), findsNothing);
    c.dispose();
  });
}
