import 'package:flutter/gestures.dart';
import 'package:flutter/material.dart';
import 'package:url_launcher/url_launcher.dart';

import '../../api/models.dart';
import '../theme.dart';
import 'inline.dart';
import 'ref_labels.dart';

typedef RefTap = void Function(String id);

/// Renders one block-document with the typed block model.
class DocView extends StatelessWidget {
  const DocView({
    super.key,
    required this.doc,
    this.onRef,
    this.blockDecorator,
    this.compact = false,
    this.textStyle,
  });

  final Doc doc;
  final RefTap? onRef;

  /// Optional wrapper for every block (used by the inspector for edit affordances).
  final Widget Function(BuildContext, Block, Widget)? blockDecorator;
  final bool compact;
  final TextStyle? textStyle;

  @override
  Widget build(BuildContext context) {
    if (doc.blocks.isEmpty) return const SizedBox.shrink();
    final children = <Widget>[];
    for (var i = 0; i < doc.blocks.length; i++) {
      final b = doc.blocks[i];
      Widget w = BlockView(
        block: b,
        onRef: onRef,
        compact: compact,
        textStyle: textStyle,
      );
      if (blockDecorator != null) w = blockDecorator!(context, b, w);
      children.add(w);
      if (i < doc.blocks.length - 1) {
        children.add(SizedBox(height: compact ? 6 : 12));
      }
    }
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: children,
    );
  }
}

/// Renders a single block.
class BlockView extends StatelessWidget {
  const BlockView({
    super.key,
    required this.block,
    this.onRef,
    this.compact = false,
    this.textStyle,
  });

  final Block block;
  final RefTap? onRef;
  final bool compact;
  final TextStyle? textStyle;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final scheme = theme.colorScheme;
    final base =
        textStyle ??
        (compact ? theme.textTheme.bodyMedium! : theme.textTheme.bodyLarge!);
    switch (block.type) {
      case 'heading':
        final style = switch (block.level) {
          1 => theme.textTheme.headlineMedium!,
          2 => theme.textTheme.headlineSmall!,
          _ => theme.textTheme.titleMedium!,
        };
        return Padding(
          padding: EdgeInsets.only(top: compact ? 2 : 6),
          child: InlineText(block.text, style: style, onRef: onRef),
        );
      case 'list':
        return Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            for (var i = 0; i < block.items.length; i++)
              Padding(
                padding: const EdgeInsets.only(bottom: 3),
                child: Row(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    SizedBox(
                      width: 22,
                      child: Text(
                        block.ordered ? '${i + 1}.' : '•',
                        style: base.copyWith(color: scheme.onSurfaceVariant),
                        textAlign: TextAlign.right,
                      ),
                    ),
                    const SizedBox(width: 6),
                    Expanded(
                      child: InlineText(
                        block.items[i],
                        style: base,
                        onRef: onRef,
                      ),
                    ),
                  ],
                ),
              ),
          ],
        );
      case 'quote':
        return Container(
          decoration: BoxDecoration(
            border: Border(left: BorderSide(color: scheme.outline, width: 3)),
          ),
          padding: const EdgeInsets.only(left: 12, top: 2, bottom: 2),
          child: InlineText(
            block.text,
            style: base.copyWith(
              fontStyle: FontStyle.italic,
              color: scheme.onSurfaceVariant,
            ),
            onRef: onRef,
          ),
        );
      case 'code':
        return Container(
          width: double.infinity,
          decoration: BoxDecoration(
            color: scheme.surfaceContainer,
            borderRadius: BorderRadius.circular(6),
            border: Border.all(color: scheme.outlineVariant),
          ),
          padding: const EdgeInsets.all(10),
          child: SelectableText(
            block.text,
            style: monoStyle(context, size: 12.5, color: scheme.onSurface),
          ),
        );
      case 'callout':
        final (icon, color) = switch (block.kind) {
          'warning' => (Icons.warning_amber_rounded, scheme.warning),
          'question' => (Icons.help_outline_rounded, scheme.secondary),
          'external' => (Icons.public_rounded, scheme.tertiary),
          _ => (Icons.info_outline_rounded, scheme.secondary),
        };
        return Container(
          decoration: BoxDecoration(
            color: color.withValues(alpha: 0.08),
            borderRadius: BorderRadius.circular(8),
            border: Border.all(color: color.withValues(alpha: 0.35)),
          ),
          padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 8),
          child: Row(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Icon(icon, size: 16, color: color),
              const SizedBox(width: 8),
              Expanded(
                child: InlineText(block.text, style: base, onRef: onRef),
              ),
            ],
          ),
        );
      case 'task_ref':
      case 'source_ref':
        return Wrap(
          crossAxisAlignment: WrapCrossAlignment.center,
          spacing: 8,
          children: [
            RefChip(id: block.ref, onTap: onRef),
            if (block.text.isNotEmpty)
              InlineText(block.text, style: base, onRef: onRef),
          ],
        );
      default:
        return InlineText(block.text, style: base, onRef: onRef);
    }
  }
}

/// Text with the inline subset rendered (bold, italic, code, links, refs).
class InlineText extends StatelessWidget {
  const InlineText(
    this.text, {
    super.key,
    this.style,
    this.onRef,
    this.maxLines,
    this.selectable = true,
  });

  final String text;
  final TextStyle? style;
  final RefTap? onRef;
  final int? maxLines;
  final bool selectable;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final scheme = theme.colorScheme;
    final base = style ?? theme.textTheme.bodyMedium!;
    final spans = <InlineSpan>[];
    for (final n in parseInline(text)) {
      switch (n.kind) {
        case InlineKind.text:
          spans.add(TextSpan(text: n.text));
        case InlineKind.bold:
          spans.add(
            TextSpan(text: n.text, style: FundusTheme.weight(base, 650)),
          );
        case InlineKind.italic:
          spans.add(
            TextSpan(
              text: n.text,
              style: const TextStyle(fontStyle: FontStyle.italic),
            ),
          );
        case InlineKind.code:
          spans.add(
            TextSpan(
              text: n.text,
              style: monoStyle(
                context,
                size: (base.fontSize ?? 14) - 1.5,
                color: scheme.onSurface,
              ).copyWith(backgroundColor: scheme.surfaceContainer),
            ),
          );
        case InlineKind.link:
          spans.add(
            TextSpan(
              text: n.text,
              style: TextStyle(
                color: scheme.secondary,
                decoration: TextDecoration.underline,
                decorationColor: scheme.secondary.withValues(alpha: 0.5),
              ),
              recognizer: TapGestureRecognizer()
                ..onTap = () => launchUrl(
                  Uri.parse(n.url),
                  mode: LaunchMode.externalApplication,
                ),
            ),
          );
        case InlineKind.ref:
          spans.add(
            WidgetSpan(
              alignment: PlaceholderAlignment.middle,
              child: Padding(
                padding: const EdgeInsets.symmetric(horizontal: 2),
                child: RefChip(id: n.text, onTap: onRef, dense: true),
              ),
            ),
          );
      }
    }
    final span = TextSpan(style: base, children: spans);
    if (selectable && maxLines == null) {
      return SelectableText.rich(span);
    }
    return Text.rich(
      span,
      maxLines: maxLines,
      overflow: maxLines == null ? null : TextOverflow.ellipsis,
    );
  }
}

/// A tappable chip for an object id ([[note_…]]).
class RefChip extends StatelessWidget {
  const RefChip({
    super.key,
    required this.id,
    this.onTap,
    this.dense = false,
    this.label,
  });

  final String id;
  final RefTap? onTap;
  final bool dense;
  final String? label;

  static IconData iconFor(String id) {
    final p = id.split('_').first;
    return switch (p) {
      'note' => Icons.notes_rounded,
      'task' => Icons.check_circle_outline_rounded,
      'topic' => Icons.tag_rounded,
      'cap' => Icons.inbox_rounded,
      'src' => Icons.link_rounded,
      'conv' => Icons.forum_outlined,
      _ => Icons.data_object_rounded,
    };
  }

  /// What to call an object whose title is not known yet.
  static String kindWord(String id) => switch (id.split('_').first) {
    'note' => 'Note',
    'task' => 'Task',
    'topic' => 'Topic',
    'cap' => 'Capture',
    'src' => 'Source',
    'conv' => 'Conversation',
    _ => 'Item',
  };

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    var label = this.label;
    if (label == null) {
      final src = RefLabels.maybeOf(context);
      if (src != null) {
        final resolved = src.labelFor(id);
        if (resolved == null) {
          src.request([id]);
        } else if (resolved.isNotEmpty) {
          label = resolved;
        }
      }
    }
    return Semantics(
      button: true,
      label: 'Open ${label ?? kindWord(id)}',
      child: InkWell(
        onTap: onTap == null ? null : () => onTap!(id),
        borderRadius: BorderRadius.circular(6),
        child: Container(
          padding: EdgeInsets.symmetric(
            horizontal: dense ? 5 : 7,
            vertical: dense ? 1 : 3,
          ),
          decoration: BoxDecoration(
            color: scheme.secondaryContainer.withValues(alpha: 0.6),
            borderRadius: BorderRadius.circular(6),
          ),
          child: Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              Icon(
                iconFor(id),
                size: dense ? 12 : 14,
                color: scheme.onSecondaryContainer,
              ),
              const SizedBox(width: 4),
              Text(
                label ?? kindWord(id),
                style: Theme.of(context).textTheme.labelMedium!.copyWith(
                  color: scheme.onSecondaryContainer,
                  fontStyle: label == null ? FontStyle.italic : null,
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}
