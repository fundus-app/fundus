import 'package:fundus_app/api/models.dart';
import 'package:fundus_app/ui/blocks/block_renderer.dart';
import 'package:fundus_app/ui/theme.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  testWidgets('renders every block type and routes ref taps', (tester) async {
    final doc = Doc.fromJson({
      'blocks': [
        {'id': 'b1', 'type': 'heading', 'level': 1, 'text': 'Title'},
        {
          'id': 'b2',
          'type': 'paragraph',
          'text': 'Plain with **bold** and `code` and [[note_01X]]',
        },
        {
          'id': 'b3',
          'type': 'list',
          'items': ['one', 'two'],
          'ordered': true,
        },
        {'id': 'b4', 'type': 'quote', 'text': 'quoted'},
        {'id': 'b5', 'type': 'code', 'lang': 'go', 'text': 'fmt.Println()'},
        {'id': 'b6', 'type': 'callout', 'kind': 'warning', 'text': 'watch out'},
        {'id': 'b7', 'type': 'task_ref', 'ref': 'task_01Y'},
        {'id': 'b8', 'type': 'source_ref', 'ref': 'src_01Z', 'text': 'excerpt'},
      ],
    });
    String? tapped;
    await tester.pumpWidget(
      MaterialApp(
        theme: FundusTheme.light(),
        home: Scaffold(
          body: SingleChildScrollView(
            child: DocView(doc: doc, onRef: (id) => tapped = id),
          ),
        ),
      ),
    );
    await tester.pump();

    expect(find.text('Title'), findsOneWidget);
    expect(find.text('quoted'), findsOneWidget);
    expect(find.text('fmt.Println()'), findsOneWidget);
    expect(find.text('watch out'), findsOneWidget);
    expect(find.text('1.'), findsOneWidget);
    expect(find.text('two'), findsOneWidget);
    expect(find.byIcon(Icons.warning_amber_rounded), findsOneWidget);
    // Two ref chips from refs plus one inline ref.
    expect(find.byType(RefChip), findsNWidgets(3));

    await tester.tap(find.byType(RefChip).at(1));
    await tester.pump();
    expect(tapped, 'task_01Y');
  });

  testWidgets('empty document renders nothing', (tester) async {
    await tester.pumpWidget(
      const MaterialApp(
        home: Scaffold(body: DocView(doc: Doc.empty)),
      ),
    );
    expect(find.byType(BlockView), findsNothing);
  });
}
