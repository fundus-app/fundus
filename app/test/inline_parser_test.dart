import 'package:fundus_app/ui/blocks/inline.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  group('parseInline', () {
    test('plain text', () {
      expect(parseInline('hello world'), [
        const InlineNode(InlineKind.text, 'hello world'),
      ]);
    });

    test('bold and italic', () {
      expect(parseInline('a **b** c *d* e _f_'), [
        const InlineNode(InlineKind.text, 'a '),
        const InlineNode(InlineKind.bold, 'b'),
        const InlineNode(InlineKind.text, ' c '),
        const InlineNode(InlineKind.italic, 'd'),
        const InlineNode(InlineKind.text, ' e '),
        const InlineNode(InlineKind.italic, 'f'),
      ]);
    });

    test('underscore inside words is not italic', () {
      expect(parseInline('snake_case_name'), [
        const InlineNode(InlineKind.text, 'snake_case_name'),
      ]);
    });

    test('code', () {
      expect(parseInline('run `go test` now'), [
        const InlineNode(InlineKind.text, 'run '),
        const InlineNode(InlineKind.code, 'go test'),
        const InlineNode(InlineKind.text, ' now'),
      ]);
    });

    test('markdown link and bare url', () {
      expect(
        parseInline('see [docs](https://example.com/x) or https://a.b/c.'),
        [
          const InlineNode(InlineKind.text, 'see '),
          const InlineNode(InlineKind.link, 'docs', 'https://example.com/x'),
          const InlineNode(InlineKind.text, ' or '),
          const InlineNode(InlineKind.link, 'https://a.b/c', 'https://a.b/c'),
          const InlineNode(InlineKind.text, '.'),
        ],
      );
    });

    test('object refs', () {
      expect(parseInline('linked [[note_01ABC]] and [[task_9Z]]'), [
        const InlineNode(InlineKind.text, 'linked '),
        const InlineNode(InlineKind.ref, 'note_01ABC'),
        const InlineNode(InlineKind.text, ' and '),
        const InlineNode(InlineKind.ref, 'task_9Z'),
      ]);
    });

    test('unknown bracket text stays text', () {
      expect(parseInline('[[not a ref]] and [x]'), [
        const InlineNode(InlineKind.text, '[[not a ref]] and [x]'),
      ]);
    });

    test('unterminated markers stay text', () {
      expect(parseInline('a **b and `c'), [
        const InlineNode(InlineKind.text, 'a **b and `c'),
      ]);
    });

    test('stripInline removes markup', () {
      expect(stripInline('**bold** `code` [[note_1]]'), 'bold code note_1');
    });
  });
}
