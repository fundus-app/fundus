import 'package:fundus_app/api/models.dart';
import 'package:fundus_app/state/ref_resolver.dart';
import 'package:fundus_app/ui/blocks/block_renderer.dart';
import 'package:fundus_app/ui/widgets/common.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import 'fake_api.dart';

void main() {
  testWidgets(
    'receipt line: quoted title becomes a chip, glued punctuation goes',
    (tester) async {
      const line = ReceiptLine(
        op: 'note.create',
        objectId: 'note_1',
        objectType: 'note',
        text: 'Created note "Grafana". Linked to Home.',
      );
      await tester.pumpWidget(
        const MaterialApp(
          home: Scaffold(body: ReceiptLineView(line: line)),
        ),
      );
      expect(find.byType(RefChip), findsOneWidget);
      final rich = tester.widget<Text>(find.byType(Text).first);
      final plain = rich.textSpan!.toPlainText(includePlaceholders: false);
      expect(plain, 'Created note  Linked to Home.');
      expect(plain.contains(' .'), isFalse);
    },
  );

  testWidgets('receipt line parses curly quotes', (tester) async {
    const line = ReceiptLine(
      op: 'note.create',
      objectId: 'note_1',
      objectType: 'note',
      text: 'Created idea “Heizung”. Linked to Home.',
    );
    await tester.pumpWidget(
      const MaterialApp(
        home: Scaffold(body: ReceiptLineView(line: line)),
      ),
    );
    expect(find.byType(RefChip), findsOneWidget);
    final rich = tester.widget<Text>(find.byType(Text).first);
    expect(
      rich.textSpan!.toPlainText(includePlaceholders: false),
      'Created idea  Linked to Home.',
    );
  });

  test('capture chips show the start of the text and relative time', () {
    final resolver = RefResolver(FakeApi());
    resolver.put(
      LinkRef(
        id: 'cap_1',
        type: 'capture',
        title: 'x',
        preview: 'Ich muss beim Deye noch prüfen, warum der zweite String manchmal keinen Strom liefert',
        createdAt: DateTime.now().subtract(const Duration(minutes: 5)),
      ),
    );
    expect(
      resolver.labelFor('cap_1'),
      'Ich muss beim Deye noch prüfen, warum d… · 5 minutes ago',
    );
    resolver.put(const LinkRef(id: 'note_1', type: 'note', title: 'Title'));
    expect(resolver.labelFor('note_1'), 'Title');
    resolver.dispose();
  });
}
