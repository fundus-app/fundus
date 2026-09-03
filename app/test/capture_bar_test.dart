import 'package:fundus_app/api/models.dart';
import 'package:fundus_app/state/app_state.dart';
import 'package:fundus_app/ui/capture_bar.dart';
import 'package:fundus_app/ui/theme.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:provider/provider.dart';

import 'fake_api.dart';

void main() {
  testWidgets('capture bar submits on Enter and shows the receipt pill', (
    tester,
  ) async {
    final api = FakeApi();
    final state = AppState(api);
    final focus = FocusNode();
    await tester.pumpWidget(
      ChangeNotifierProvider<AppState>.value(
        value: state,
        child: MaterialApp(
          theme: FundusTheme.light(),
          home: Scaffold(body: CaptureBar(focusNode: focus, autofocus: true)),
        ),
      ),
    );
    await tester.pump();

    await tester.enterText(
      find.byKey(const Key('capture-field')),
      'Ich muss die Steuer machen',
    );
    await tester.sendKeyEvent(LogicalKeyboardKey.enter);
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 50));

    expect(api.captured, ['Ich muss die Steuer machen']);
    expect(state.pending.length, 1);
    expect(find.textContaining('Filing'), findsOneWidget);

    // The daemon reports the capture as changed; the pill updates to the receipt.
    api.eventBus.add(
      ServerEvent('capture.changed', {
        'id': state.pending.first.capture.id,
        'type': 'capture',
        'status': 'processed',
        'text': 'x',
      }),
    );
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 50));
    await tester.pump(const Duration(milliseconds: 300));

    expect(find.textContaining('Created task'), findsOneWidget);
    expect(find.text('Undo'), findsOneWidget);
    // The field is cleared for the next thought.
    expect(
      tester
          .widget<TextField>(find.byKey(const Key('capture-field')))
          .controller!
          .text,
      '',
    );

    state.dispose();
    focus.dispose();
  });

  testWidgets('empty text is not submitted', (tester) async {
    final api = FakeApi();
    final state = AppState(api);
    final focus = FocusNode();
    await tester.pumpWidget(
      ChangeNotifierProvider<AppState>.value(
        value: state,
        child: MaterialApp(
          home: Scaffold(body: CaptureBar(focusNode: focus, autofocus: true)),
        ),
      ),
    );
    await tester.pump();
    await tester.enterText(find.byKey(const Key('capture-field')), '   ');
    await tester.sendKeyEvent(LogicalKeyboardKey.enter);
    await tester.pump();
    expect(api.captured, isEmpty);
    expect(state.pending, isEmpty);
    state.dispose();
    focus.dispose();
  });
}
