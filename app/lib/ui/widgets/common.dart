import 'dart:async';
import 'dart:io' show SocketException;

import 'package:flutter/gestures.dart';
import 'package:flutter/material.dart';
import 'package:http/http.dart' show ClientException;
import 'package:intl/intl.dart';
import 'package:provider/provider.dart';

import '../../api/models.dart';
import '../../state/app_state.dart';
import '../../state/settings.dart';
import '../blocks/block_renderer.dart';
import '../blocks/ref_labels.dart';
import 'toasts.dart';
import '../settings_screen.dart';
import '../theme.dart';

String timeAgo(DateTime? t) {
  if (t == null) return '';
  final d = DateTime.now().difference(t);
  if (d.inMinutes < 1) return 'just now';
  if (d.inMinutes < 60) {
    return '${d.inMinutes} minute${d.inMinutes == 1 ? '' : 's'} ago';
  }
  if (d.inHours < 24) {
    return '${d.inHours} hour${d.inHours == 1 ? '' : 's'} ago';
  }
  if (d.inDays < 7) return '${d.inDays} day${d.inDays == 1 ? '' : 's'} ago';
  return DateFormat.yMMMd().format(t);
}

String shortDate(DateTime? t) => t == null ? '' : DateFormat.yMMMd().format(t);
String shortTime(DateTime? t) => t == null ? '' : DateFormat.Hm().format(t);

/// "Today", "Yesterday" or a date, for grouping lists.
String dayLabel(DateTime? t) {
  if (t == null) return '';
  final now = DateTime.now();
  final d0 = DateTime(t.year, t.month, t.day);
  final n0 = DateTime(now.year, now.month, now.day);
  final diff = n0.difference(d0).inDays;
  if (diff == 0) return 'Today';
  if (diff == 1) return 'Yesterday';
  return DateFormat.yMMMEd().format(t);
}

/// Readable name for a due date string (YYYY-MM-DD).
String dueLabel(String due) {
  if (due.isEmpty) return '';
  final d = DateTime.tryParse(due);
  if (d == null) return due;
  final today = DateTime.now();
  final t0 = DateTime(today.year, today.month, today.day);
  final diff = DateTime(d.year, d.month, d.day).difference(t0).inDays;
  if (diff == 0) return 'today';
  if (diff == 1) return 'tomorrow';
  if (diff == -1) return 'yesterday';
  if (diff < 0) return '${-diff} day${diff == -1 ? '' : 's'} overdue';
  if (diff < 7) return DateFormat.EEEE().format(d);
  return DateFormat.MMMd().format(d);
}

// ---------------------------------------------------------------------------
// Errors

/// True for "the daemon is not there" failures.
bool isConnectionError(Object e) =>
    e is SocketException ||
    e is TimeoutException ||
    e is ClientException ||
    (e is ApiException && e.status == 0) ||
    e.toString().contains('Connection refused') ||
    e.toString().contains('Failed host lookup');

/// One plain sentence for every user-facing error. Never a raw exception.
String describeError(Object e, {String? serverUrl}) {
  if (e is String) return e;
  if (isConnectionError(e)) {
    return serverUrl == null || serverUrl.isEmpty
        ? 'Fundus is not running or cannot be reached.'
        : 'Fundus is not running at $serverUrl.';
  }
  if (e is ApiException) {
    switch (e.status) {
      case 401:
        return 'The token was rejected.';
      case 403:
        // e.g. a pinned block that a whole-body edit would rewrite.
        return e.message.isEmpty
            ? 'You are not allowed to do that.'
            : _sentence(e.message);
      case 404:
        return 'That item no longer exists.';
      case 409:
        return switch (e.code) {
          'busy' => 'Fundus is still working on the previous turn.',
          'processing' => 'Still being processed, wait a moment.',
          'already_undone' => 'That change was already undone.',
          _ => 'This item changed meanwhile. Reloaded.',
        };
      case 400:
        return e.message.isEmpty
            ? 'The request was invalid.'
            : _sentence(e.message);
      case 502:
      case 503:
        if (e.code == 'research_unavailable') {
          return e.message.isEmpty
              ? 'Research needs a search backend — see Settings.'
              : _sentence(e.message);
        }
        return e.message.isEmpty
            ? 'The model provider did not answer.'
            : 'The model provider did not answer: ${_sentence(e.message)}';
      default:
        if (e.status >= 500) {
          return 'Fundus hit an internal error${e.message.isEmpty ? '.' : ': ${_sentence(e.message)}'}';
        }
        return e.message.isEmpty
            ? 'Something went wrong.'
            : _sentence(e.message);
    }
  }
  return 'Something went wrong.';
}

String _sentence(String s) {
  final t = s.trim();
  if (t.isEmpty) return t;
  final cap = t[0].toUpperCase() + t.substring(1);
  return cap.endsWith('.') ? cap : '$cap.';
}

void showError(BuildContext context, Object e) {
  final url = context.read<Settings?>()?.serverUrl;
  final connection = isConnectionError(e);
  showToast(
    context,
    describeError(e, serverUrl: url),
    error: true,
    key: connection ? 'connection' : null,
    actionLabel: connection ? 'Settings' : null,
    onAction: connection
        ? () async {
            SettingsScreen.show(context, section: SettingsSection.connection);
            return false;
          }
        : null,
  );
}

/// What an Undo action in a toast runs; a non-null result means it worked.
typedef UndoAction = Future<Object?> Function();

/// Receipt toast. `undo: true` adds an Undo action for the receipt's txn
/// (with the conflict dialog); the toast then reads "Undone." for 2 s.
void showReceiptSnack(
  BuildContext context,
  Receipt r, {
  bool undo = false,
  UndoAction? onUndo,
  String? actionLabel,
}) {
  final state = undo ? context.read<AppState>() : null;
  final action =
      onUndo ??
      (state == null
          ? null
          : () => undoWithConfirm(context, state, r.txnId, quiet: true));
  showToast(
    context,
    r.summary,
    key: 'txn:${r.txnId}',
    actionLabel: actionLabel,
    onAction: action == null ? null : () async => await action() != null,
  );
}

/// A plain toast with an Undo action (used for deletes).
void showUndoSnack(
  BuildContext context,
  String text, {
  required UndoAction onUndo,
  String? key,
}) {
  showToast(
    context,
    text,
    key: key,
    onAction: () async => await onUndo() != null,
  );
}

/// Undo with the conflict dialog (409 undo_conflict → offer force).
Future<Receipt?> undoWithConfirm(
  BuildContext context,
  AppState state,
  String txnId, {
  bool quiet = false,
}) async {
  try {
    final r = await state.undo(txnId);
    if (context.mounted && !quiet) _undone(context, txnId);
    return r;
  } on ApiException catch (e) {
    if (!context.mounted) return null;
    if (e.isUndoConflict) {
      final objs =
          (e.details?['objects'] as List?)?.whereType<String>().toList() ??
          const [];
      List<LinkRef> refs = const [];
      try {
        refs = await state.api.resolve(objs);
      } catch (_) {}
      if (!context.mounted) return null;
      String nameOf(String id) {
        final r = refs.where((x) => x.id == id).firstOrNull;
        return r == null || r.title.isEmpty ? id : '${r.title} (${r.type})';
      }

      final ok = await showDialog<bool>(
        context: context,
        builder: (ctx) => AlertDialog(
          title: const Text('Edited since then'),
          content: Text(
            'These were edited after that change. Undoing anyway restores their '
            'earlier state; the later edits stay in the history.\n\n'
            '${objs.map(nameOf).join('\n')}',
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.pop(ctx, false),
              child: const Text('Keep'),
            ),
            FilledButton(
              onPressed: () => Navigator.pop(ctx, true),
              child: const Text('Undo anyway'),
            ),
          ],
        ),
      );
      if (ok == true) {
        try {
          final r = await state.undo(txnId, force: true);
          if (context.mounted && !quiet) _undone(context, txnId);
          return r;
        } catch (e2) {
          if (context.mounted) showError(context, e2);
        }
      }
      return null;
    }
    showError(context, e);
    return null;
  } catch (e) {
    if (context.mounted) showError(context, e);
    return null;
  }
}

void _undone(BuildContext context, String txnId) => showToast(
  context,
  'Undone.',
  key: 'txn:$txnId',
  duration: ToastController.settledDuration,
);

// ---------------------------------------------------------------------------
// States

/// A calm empty state with one line of guidance.
class EmptyState extends StatelessWidget {
  const EmptyState({
    super.key,
    required this.icon,
    required this.title,
    this.hint,
  });
  final IconData icon;
  final String title;
  final String? hint;

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(32),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(icon, size: 28, color: scheme.outline),
            const SizedBox(height: 12),
            Text(
              title,
              style: Theme.of(context).textTheme.titleMedium,
              textAlign: TextAlign.center,
            ),
            if (hint != null) ...[
              const SizedBox(height: 6),
              Text(
                hint!,
                style: Theme.of(context).textTheme.bodySmall,
                textAlign: TextAlign.center,
              ),
            ],
          ],
        ),
      ),
    );
  }
}

/// A request failed: one sentence, Retry, Settings.
class ErrorState extends StatelessWidget {
  const ErrorState({super.key, required this.error, required this.onRetry});
  final Object error;
  final VoidCallback onRetry;

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    final url = context.read<Settings?>()?.serverUrl;
    final startError = context.read<AppState?>()?.startError;
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(32),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(Icons.cloud_off_rounded, size: 28, color: scheme.error),
            const SizedBox(height: 12),
            Text(
              describeError(error, serverUrl: url),
              style: Theme.of(context).textTheme.titleMedium,
              textAlign: TextAlign.center,
            ),
            if (startError != null || isConnectionError(error)) ...[
              const SizedBox(height: 6),
              Text(
                startError != null
                    ? describeError(startError)
                    : 'Start it with:',
                style: Theme.of(context).textTheme.bodySmall,
                textAlign: TextAlign.center,
              ),
              const SizedBox(height: 4),
              SelectableText(
                'fundus serve',
                style: monoStyle(context, size: 12.5, color: scheme.onSurface),
              ),
            ],
            const SizedBox(height: 12),
            Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                FilledButton.tonal(
                  onPressed: onRetry,
                  child: const Text('Retry'),
                ),
                const SizedBox(width: 8),
                TextButton(
                  onPressed: () => SettingsScreen.show(
                    context,
                    section: SettingsSection.connection,
                  ),
                  child: const Text('Settings'),
                ),
              ],
            ),
          ],
        ),
      ),
    );
  }
}

/// Section label: "Notes · 1" in small caps with a hairline under it.
class SectionLabel extends StatelessWidget {
  const SectionLabel(
    this.text, {
    super.key,
    this.count,
    this.trailing,
    this.top = 28,
    this.onTap,
    this.collapsed = false,
  });
  final String text;
  final int? count;
  final Widget? trailing;

  /// Space above the label.
  final double top;

  /// Makes the label a toggle with a chevron (collapsible section).
  final VoidCallback? onTap;
  final bool collapsed;
  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    final style = Theme.of(context).textTheme.labelSmall!.copyWith(
      fontSize: 11,
      letterSpacing: 1.2,
      color: scheme.onSurfaceVariant,
    );
    final row = Row(
      children: [
        if (onTap != null)
          Padding(
            padding: const EdgeInsets.only(right: 2),
            child: Icon(
              collapsed
                  ? Icons.chevron_right_rounded
                  : Icons.expand_more_rounded,
              size: 16,
              color: scheme.onSurfaceVariant,
            ),
          ),
        Expanded(
          child: Text(
            (count == null ? text : '$text · $count').toUpperCase(),
            style: style,
          ),
        ),
        ?trailing,
      ],
    );
    return Padding(
      padding: EdgeInsets.only(top: top, bottom: 8),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          if (onTap == null)
            row
          else
            InkWell(
              onTap: onTap,
              borderRadius: BorderRadius.circular(4),
              child: row,
            ),
          const SizedBox(height: 6),
          Divider(height: 1, thickness: 1, color: scheme.outlineVariant),
        ],
      ),
    );
  }
}

/// Muted one-liner for empty sections.
class EmptyLine extends StatelessWidget {
  const EmptyLine(this.text, {super.key});
  final String text;
  @override
  Widget build(BuildContext context) => Padding(
    padding: const EdgeInsets.symmetric(vertical: 10),
    child: Text(text, style: secondaryStyle(context)),
  );
}

/// The one secondary text style: 13px, muted.
TextStyle secondaryStyle(BuildContext context) => Theme.of(context)
    .textTheme
    .bodySmall!
    .copyWith(
      fontSize: 13,
      height: 1.4,
      color: Theme.of(context).colorScheme.onSurfaceVariant,
    );

/// A calm list row: 28px icon column, 15px title, 13px secondary line,
/// subtle hover tint, no elevation.
class ListRow extends StatefulWidget {
  const ListRow({
    super.key,
    required this.title,
    this.secondary,
    this.icon,
    this.leading,
    this.trailing,
    this.onTap,
    this.strike = false,
  });
  final String title;
  final String? secondary;
  final IconData? icon;
  final Widget? leading;
  final Widget? trailing;
  final VoidCallback? onTap;
  final bool strike;
  @override
  State<ListRow> createState() => _ListRowState();
}

class _ListRowState extends State<ListRow> {
  bool _hover = false;
  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final scheme = theme.colorScheme;
    final title = theme.textTheme.bodyLarge!.copyWith(
      fontSize: 15,
      height: 1.4,
      decoration: widget.strike ? TextDecoration.lineThrough : null,
      color: widget.strike ? scheme.onSurfaceVariant : scheme.onSurface,
    );
    return MouseRegion(
      onEnter: (_) => setState(() => _hover = true),
      onExit: (_) => setState(() => _hover = false),
      child: Material(
        color: _hover && widget.onTap != null
            ? scheme.surfaceContainerLow
            : Colors.transparent,
        borderRadius: BorderRadius.circular(6),
        child: InkWell(
          onTap: widget.onTap,
          borderRadius: BorderRadius.circular(6),
          hoverColor: Colors.transparent,
          child: Padding(
            padding: const EdgeInsets.symmetric(vertical: 10, horizontal: 4),
            child: Row(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                SizedBox(
                  width: 28,
                  child: Padding(
                    padding: const EdgeInsets.only(top: 2),
                    child:
                        widget.leading ??
                        (widget.icon == null
                            ? null
                            : Icon(
                                widget.icon,
                                size: 18,
                                color: scheme.onSurfaceVariant,
                              )),
                  ),
                ),
                Expanded(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text(
                        widget.title,
                        style: title,
                        maxLines: 2,
                        overflow: TextOverflow.ellipsis,
                      ),
                      if (widget.secondary != null &&
                          widget.secondary!.isNotEmpty)
                        Padding(
                          padding: const EdgeInsets.only(top: 2),
                          child: Text(
                            widget.secondary!,
                            style: secondaryStyle(context),
                            maxLines: 2,
                            overflow: TextOverflow.ellipsis,
                          ),
                        ),
                    ],
                  ),
                ),
                if (widget.trailing != null) ...[
                  const SizedBox(width: 8),
                  widget.trailing!,
                ],
              ],
            ),
          ),
        ),
      ),
    );
  }
}

/// Text with inline links (primary color, underline on hover) that wrap
/// naturally inside the sentence.
class LinkedText extends StatefulWidget {
  const LinkedText({super.key, required this.parts, this.style});

  /// Alternating plain and link segments; a link has an [onTap].
  final List<TextPart> parts;
  final TextStyle? style;
  @override
  State<LinkedText> createState() => _LinkedTextState();
}

class TextPart {
  const TextPart(this.text, {this.onTap, this.glue = false, this.onRemove});
  final String text;
  final VoidCallback? onTap;

  /// Attach to the previous part without a separator (used by fact rows).
  final bool glue;

  /// A link that can be taken away: a small × follows it on hover.
  final VoidCallback? onRemove;
  bool get isLink => onTap != null;
}

class _LinkedTextState extends State<LinkedText> {
  int? _hovered;
  final _recognizers = <int, TapGestureRecognizer>{};

  @override
  void dispose() {
    for (final r in _recognizers.values) {
      r.dispose();
    }
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    final base = widget.style ?? Theme.of(context).textTheme.bodyMedium!;
    final spans = <InlineSpan>[];
    for (var i = 0; i < widget.parts.length; i++) {
      final p = widget.parts[i];
      if (!p.isLink) {
        spans.add(TextSpan(text: p.text));
        continue;
      }
      final rec = _recognizers.putIfAbsent(i, TapGestureRecognizer.new)
        ..onTap = p.onTap;
      spans.add(
        TextSpan(
          text: p.text,
          style: base.copyWith(
            color: scheme.primary,
            decoration: _hovered == i
                ? TextDecoration.underline
                : TextDecoration.none,
            decorationColor: scheme.primary,
          ),
          recognizer: rec,
          mouseCursor: SystemMouseCursors.click,
          onEnter: (_) => setState(() => _hovered = i),
          onExit: (_) => setState(() => _hovered = null),
        ),
      );
      if (p.onRemove != null) {
        spans.add(
          WidgetSpan(
            alignment: PlaceholderAlignment.middle,
            child: MouseRegion(
              onEnter: (_) => setState(() => _hovered = i),
              onExit: (_) => setState(() => _hovered = null),
              child: AnimatedOpacity(
                opacity: _hovered == i ? 1 : 0,
                duration: const Duration(milliseconds: 120),
                child: Padding(
                  padding: const EdgeInsets.only(left: 2),
                  child: InkWell(
                    onTap: p.onRemove,
                    borderRadius: BorderRadius.circular(8),
                    child: Tooltip(
                      message: 'Unlink',
                      child: Icon(
                        Icons.close_rounded,
                        size: 13,
                        color: scheme.onSurfaceVariant,
                      ),
                    ),
                  ),
                ),
              ),
            ),
          ),
        );
      }
    }
    return Text.rich(TextSpan(style: base, children: spans));
  }
}

/// Who did it: you / Fundus / system.
class ActorBadge extends StatelessWidget {
  const ActorBadge(this.receipt, {super.key});
  final Receipt receipt;
  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    final (bg, fg, icon, label) = receipt.isModel
        ? (
            scheme.tertiaryContainer,
            scheme.onTertiaryContainer,
            Icons.auto_awesome_rounded,
            'Fundus',
          )
        : receipt.isUser
        ? (
            scheme.secondaryContainer,
            scheme.onSecondaryContainer,
            Icons.person_outline_rounded,
            'You',
          )
        : (
            scheme.surfaceContainerHigh,
            scheme.onSurfaceVariant,
            Icons.settings_outlined,
            'System',
          );
    final tip = receipt.isModel
        ? 'Fundus${receipt.modelName.isEmpty ? '' : ' · ${receipt.modelName}'}'
        : label;
    return Tooltip(
      message: tip,
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
        decoration: BoxDecoration(
          color: bg,
          borderRadius: BorderRadius.circular(5),
        ),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(icon, size: 11, color: fg),
            const SizedBox(width: 4),
            Text(
              label,
              style: Theme.of(context).textTheme.labelSmall!
                  .copyWith(color: fg),
            ),
          ],
        ),
      ),
    );
  }
}

/// A quiet Undo button (outlined, muted) used in lists.
class QuietUndoButton extends StatelessWidget {
  const QuietUndoButton({
    super.key,
    required this.onPressed,
    this.label = 'Undo',
  });
  final VoidCallback onPressed;
  final String label;
  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    return Semantics(
      label: '$label this change',
      child: OutlinedButton(
        style: OutlinedButton.styleFrom(
          visualDensity: VisualDensity.compact,
          padding: const EdgeInsets.symmetric(horizontal: 10),
          foregroundColor: scheme.onSurfaceVariant,
          side: BorderSide(color: scheme.outlineVariant),
          minimumSize: const Size(32, 32),
        ),
        onPressed: onPressed,
        child: Text(label),
      ),
    );
  }
}

/// One receipt line: the quoted object title becomes an inline link.
/// `Created idea “X” in Fundus.` → Created idea [X] in Fundus.
class ReceiptLineView extends StatelessWidget {
  const ReceiptLineView({
    super.key,
    required this.line,
    this.onOpen,
    this.style,
  });
  final ReceiptLine line;
  final RefTap? onOpen;
  final TextStyle? style;

  @override
  Widget build(BuildContext context) {
    final base =
        style ?? Theme.of(context).textTheme.bodyMedium!.copyWith(height: 1.5);
    final text = line.text;
    var q1 = text.indexOf('“');
    var q2 = q1 >= 0 ? text.indexOf('”', q1 + 1) : -1;
    if (q1 < 0 || q2 < 0) {
      q1 = text.indexOf('"');
      q2 = q1 >= 0 ? text.indexOf('"', q1 + 1) : -1;
    }
    if (line.objectId.isEmpty || q1 < 0 || q2 < 0 || onOpen == null) {
      return Text(text, style: base);
    }
    final quoted = text.substring(q1 + 1, q2);
    final resolved = RefLabels.maybeOf(context)?.labelFor(line.objectId);
    final title = resolved == null || resolved.isEmpty ? quoted : resolved;
    final before = text.substring(0, q1);
    final after = text.substring(q2 + 1);
    return LinkedText(
      style: base,
      parts: [
        if (before.isNotEmpty) TextPart(before),
        TextPart(title, onTap: () => onOpen!(line.objectId)),
        if (after.isNotEmpty) TextPart(after),
      ],
    );
  }
}

/// One receipt: who · when · model on the first line with a small Undo,
/// then the effects as plain sentences.
class ReceiptTile extends StatelessWidget {
  const ReceiptTile({
    super.key,
    required this.receipt,
    this.onOpen,
    this.showActor = true,
    this.dense = false,
    this.allowUndo = true,
    this.showTime = true,
  });
  final Receipt receipt;
  final RefTap? onOpen;
  final bool showActor;
  final bool dense;
  final bool allowUndo;
  final bool showTime;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final scheme = theme.colorScheme;
    final state = context.read<AppState>();
    final undone = receipt.isUndone;
    final line = theme.textTheme.bodyMedium!.copyWith(
      fontSize: 14,
      height: 1.5,
      decoration: undone ? TextDecoration.lineThrough : null,
      color: undone ? scheme.onSurfaceVariant : scheme.onSurface,
    );
    final meta = <String>[
      if (showTime) timeAgo(receipt.at),
      if (receipt.modelName.isNotEmpty) receipt.modelName,
      if (undone) 'undone',
      if (receipt.isUndo) 'undo',
    ];
    return Padding(
      padding: EdgeInsets.symmetric(vertical: dense ? 6 : 10),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Row(
            children: [
              if (showActor) ...[ActorBadge(receipt), const SizedBox(width: 8)],
              Expanded(
                child: Text(
                  meta.join(' · '),
                  style: secondaryStyle(context)
                      .copyWith(color: undone ? scheme.warning : null),
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                ),
              ),
              if (allowUndo && !undone && receipt.undoable)
                Semantics(
                  label: 'Undo this change',
                  child: TextButton(
                    style: TextButton.styleFrom(
                      visualDensity: VisualDensity.compact,
                      padding: const EdgeInsets.symmetric(horizontal: 8),
                      minimumSize: const Size(0, 28),
                      textStyle: theme.textTheme.labelMedium,
                    ),
                    onPressed: () =>
                        undoWithConfirm(context, state, receipt.txnId),
                    child: const Text('Undo'),
                  ),
                ),
            ],
          ),
          const SizedBox(height: 4),
          if (receipt.lines.isEmpty)
            Text(receipt.summary, style: line)
          else
            for (var i = 0; i < receipt.lines.length; i++)
              Padding(
                padding: EdgeInsets.only(top: i == 0 ? 0 : 4),
                child: ReceiptLineView(
                  line: receipt.lines[i],
                  onOpen: undone ? null : onOpen,
                  style: line,
                ),
              ),
        ],
      ),
    );
  }
}

/// Receipt-style lines for a parked proposal.
class ProposalView extends StatelessWidget {
  const ProposalView({super.key, required this.ops, this.onOpen});
  final List<ProposalOp> ops;
  final RefTap? onOpen;

  @override
  Widget build(BuildContext context) {
    if (ops.isEmpty) return const SizedBox.shrink();
    final theme = Theme.of(context);
    final scheme = theme.colorScheme;
    final refs = context.read<AppState>().refs;
    refs.request({
      for (final o in ops) ...[
        ...o.topics.where((t) => t.startsWith('topic_')),
        if (o.noteId.isNotEmpty) o.noteId,
        if (o.taskId.isNotEmpty) o.taskId,
      ],
    });
    String label(String id) {
      final l = refs.labelFor(id);
      return l == null || l.isEmpty ? id : l;
    }

    IconData iconFor(ProposalOp o) => switch (o.op) {
      'note.create' =>
        o.kind == 'idea'
            ? Icons.lightbulb_outline_rounded
            : Icons.notes_rounded,
      'note.append' => Icons.playlist_add_rounded,
      'task.create' || 'task.update' => Icons.check_circle_outline_rounded,
      'task.complete' => Icons.check_circle_rounded,
      'task.mention' => Icons.repeat_rounded,
      'topic.create' => Icons.tag_rounded,
      _ => Icons.auto_awesome_rounded,
    };
    return ListenableBuilder(
      listenable: refs,
      builder: (context, _) => Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          for (final o in ops)
            Padding(
              padding: const EdgeInsets.only(bottom: 3),
              child: Row(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Padding(
                    padding: const EdgeInsets.only(top: 2),
                    child: Icon(iconFor(o), size: 14, color: scheme.tertiary),
                  ),
                  const SizedBox(width: 6),
                  Expanded(
                    child: Text(
                      'Would ${o.describe(label)}',
                      style: theme.textTheme.bodySmall!.copyWith(
                        color: scheme.onSurface,
                      ),
                    ),
                  ),
                ],
              ),
            ),
        ],
      ),
    );
  }
}

/// Topic chips resolved to names.
class TopicChips extends StatelessWidget {
  const TopicChips({
    super.key,
    required this.ids,
    required this.names,
    this.onTap,
    this.dense = true,
  });
  final List<String> ids;
  final List<String> names;
  final RefTap? onTap;
  final bool dense;

  @override
  Widget build(BuildContext context) {
    if (ids.isEmpty && names.isEmpty) return const SizedBox.shrink();
    final n = ids.isNotEmpty ? ids.length : names.length;
    return Wrap(
      spacing: 4,
      runSpacing: 4,
      children: [
        for (var i = 0; i < n; i++)
          RefChip(
            id: i < ids.length ? ids[i] : 'topic_',
            label: i < names.length ? names[i] : null,
            onTap: onTap,
            dense: dense,
          ),
      ],
    );
  }
}

/// Coloured status dot for captures.
class StatusDot extends StatelessWidget {
  const StatusDot(this.status, {super.key});
  final String status;
  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    final label = 'Status: ${captureStatusLabel(status)}';
    if (status == 'pending' || status == 'processing') {
      return Semantics(
        label: label,
        child: SizedBox(
          width: 14,
          height: 14,
          child: CircularProgressIndicator(
            strokeWidth: 1.6,
            color: scheme.primary,
          ),
        ),
      );
    }
    final color = switch (status) {
      'processed' => scheme.success,
      'needs_review' => scheme.warning,
      'failed' => scheme.error,
      _ => scheme.outline,
    };
    final icon = switch (status) {
      'processed' => Icons.check_rounded,
      'needs_review' => Icons.help_outline_rounded,
      'failed' => Icons.error_outline_rounded,
      'dismissed' => Icons.close_rounded,
      _ => Icons.circle_outlined,
    };
    return Semantics(
      label: label,
      child: Icon(icon, size: 16, color: color),
    );
  }
}

/// Keyboard hint like "Ctrl K".
class KeyHint extends StatelessWidget {
  const KeyHint(this.keys, {super.key});
  final String keys;
  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 5, vertical: 1),
      decoration: BoxDecoration(
        border: Border.all(color: scheme.outline),
        borderRadius: BorderRadius.circular(4),
      ),
      child: Text(keys, style: monoStyle(context, size: 10.5)),
    );
  }
}

/// A title that shows a pencil on hover and opens an editor on tap.
class EditableTitle extends StatefulWidget {
  const EditableTitle({
    super.key,
    required this.text,
    required this.style,
    required this.onEdit,
    this.tooltip = 'Edit',
  });
  final String text;
  final TextStyle style;
  final VoidCallback onEdit;
  final String tooltip;
  @override
  State<EditableTitle> createState() => _EditableTitleState();
}

class _EditableTitleState extends State<EditableTitle> {
  bool _hover = false;
  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    return MouseRegion(
      onEnter: (_) => setState(() => _hover = true),
      onExit: (_) => setState(() => _hover = false),
      child: Tooltip(
        message: widget.tooltip,
        waitDuration: const Duration(milliseconds: 700),
        child: InkWell(
          onTap: widget.onEdit,
          borderRadius: BorderRadius.circular(6),
          child: Row(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Expanded(child: Text(widget.text, style: widget.style)),
              AnimatedOpacity(
                opacity: _hover ? 1 : 0,
                duration: const Duration(milliseconds: 120),
                child: Padding(
                  padding: const EdgeInsets.only(left: 8, top: 6),
                  child: Icon(
                    Icons.edit_outlined,
                    size: 16,
                    color: scheme.onSurfaceVariant,
                  ),
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

/// The part of [secondary] that is not already said by [title]: when the
/// secondary text starts with the title (or the title is its first sentence,
/// possibly cut with an ellipsis), only the remainder is returned; null when
/// nothing new would be shown.
String? remainderAfterTitle(String title, String secondary) {
  final sec = secondary.trim();
  if (sec.isEmpty) return null;
  var t = title.trim();
  while (t.endsWith('…') || t.endsWith('.') || t.endsWith(' ')) {
    t = t.substring(0, t.length - 1);
  }
  if (t.isEmpty) return sec;
  if (!sec.startsWith(t)) return sec;
  var rest = sec.substring(t.length).trimLeft();
  while (rest.isNotEmpty && '.,;:!?…'.contains(rest[0])) {
    rest = rest.substring(1).trimLeft();
  }
  return rest.isEmpty ? null : rest;
}

/// Whether two Markdown texts say the same thing (line endings, trailing
/// spaces and blank-line runs ignored) — used to skip no-op saves.
bool sameMarkdown(String a, String b) {
  String norm(String s) => s
      .replaceAll('\r\n', '\n')
      .split('\n')
      .map((l) => l.trimRight())
      .join('\n')
      .replaceAll(RegExp(r'\n{3,}'), '\n\n')
      .trim();
  return norm(a) == norm(b);
}
