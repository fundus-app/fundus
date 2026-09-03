/// Parser for the inline Markdown subset used inside blocks:
/// **bold**, *italic* / _italic_, `code`, [text](url), and [[object refs]].
///
/// Pure Dart, no Flutter dependency, so it can be unit-tested directly.
library;

enum InlineKind { text, bold, italic, code, link, ref }

class InlineNode {
  final InlineKind kind;
  final String text;
  final String url;
  const InlineNode(this.kind, this.text, [this.url = '']);

  @override
  bool operator ==(Object other) =>
      other is InlineNode &&
      other.kind == kind &&
      other.text == text &&
      other.url == url;

  @override
  int get hashCode => Object.hash(kind, text, url);

  @override
  String toString() => url.isEmpty ? '$kind($text)' : '$kind($text -> $url)';
}

final _refRe = RegExp(
  r'\[\[((?:note|task|topic|cap|src|conv)_[0-9A-Za-z]+)\]\]',
);
final _linkRe = RegExp(r'\[([^\]]+)\]\((https?://[^\s)]+)\)');
final _urlRe = RegExp(r'https?://[^\s<>()\]]+');

/// Parses [s] into a flat list of nodes. Nesting is not supported (bold inside
/// italic renders as text), which matches what the core produces.
List<InlineNode> parseInline(String s) {
  final out = <InlineNode>[];
  final buf = StringBuffer();
  void flush() {
    if (buf.isNotEmpty) {
      out.add(InlineNode(InlineKind.text, buf.toString()));
      buf.clear();
    }
  }

  var i = 0;
  while (i < s.length) {
    // [[ref]]
    if (s.startsWith('[[', i)) {
      final m = _refRe.matchAsPrefix(s, i);
      if (m != null) {
        flush();
        out.add(InlineNode(InlineKind.ref, m.group(1)!));
        i = m.end;
        continue;
      }
    }
    // [text](url)
    if (s[i] == '[') {
      final m = _linkRe.matchAsPrefix(s, i);
      if (m != null) {
        flush();
        out.add(InlineNode(InlineKind.link, m.group(1)!, m.group(2)!));
        i = m.end;
        continue;
      }
    }
    // bare URL
    if (s.startsWith('http://', i) || s.startsWith('https://', i)) {
      final m = _urlRe.matchAsPrefix(s, i);
      if (m != null) {
        flush();
        var url = m.group(0)!;
        // trailing punctuation is rarely part of the URL
        while (url.isNotEmpty && '.,;:!?'.contains(url[url.length - 1])) {
          url = url.substring(0, url.length - 1);
        }
        out.add(InlineNode(InlineKind.link, url, url));
        i += url.length;
        continue;
      }
    }
    // `code`
    if (s[i] == '`') {
      final end = s.indexOf('`', i + 1);
      if (end > i + 1) {
        flush();
        out.add(InlineNode(InlineKind.code, s.substring(i + 1, end)));
        i = end + 1;
        continue;
      }
    }
    // **bold**
    if (s.startsWith('**', i)) {
      final end = s.indexOf('**', i + 2);
      if (end > i + 2) {
        flush();
        out.add(InlineNode(InlineKind.bold, s.substring(i + 2, end)));
        i = end + 2;
        continue;
      }
    }
    // *italic* or _italic_ (word-boundary for underscore)
    if (s[i] == '*' || (s[i] == '_' && (i == 0 || !_isWord(s[i - 1])))) {
      final marker = s[i];
      final end = s.indexOf(marker, i + 1);
      if (end > i + 1 &&
          s[i + 1].trim().isNotEmpty &&
          (marker == '*' || end + 1 >= s.length || !_isWord(s[end + 1]))) {
        final inner = s.substring(i + 1, end);
        if (!inner.contains('\n')) {
          flush();
          out.add(InlineNode(InlineKind.italic, inner));
          i = end + 1;
          continue;
        }
      }
    }
    buf.write(s[i]);
    i++;
  }
  flush();
  return out;
}

bool _isWord(String ch) => RegExp(r'[\p{L}\p{N}]', unicode: true).hasMatch(ch);

/// Plain text of the inline string with markup removed.
String stripInline(String s) => parseInline(s).map((n) => n.text).join();
