import 'dart:async';
import 'dart:io' show SocketException;

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:http/http.dart' show ClientException;
import 'package:intl/intl.dart';
import 'package:provider/provider.dart';

import '../../api/models.dart';
import '../../state/app_state.dart';
import '../../state/settings.dart';
import '../blocks/block_renderer.dart';
import '../blocks/ref_labels.dart';
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
        return 'You are not allowed to do that.';
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
  final messenger = ScaffoldMessenger.maybeOf(context);
  messenger?.hideCurrentSnackBar();
  messenger?.showSnackBar(
    SnackBar(
      content: Text(describeError(e, serverUrl: url)),
      backgroundColor: Theme.of(context).colorScheme.error,
      action: isConnectionError(e)
          ? SnackBarAction(
              label: 'Settings',
              textColor: Theme.of(context).colorScheme.onError,
              onPressed: () => SettingsScreen.show(context),
            )
          : null,
    ),
  );
}

void showReceiptSnack(
  BuildContext context,
  Receipt r, {
  VoidCallback? onUndo,
  String? actionLabel,
}) {
  final messenger = ScaffoldMessenger.maybeOf(context);
  messenger?.hideCurrentSnackBar();
  messenger?.showSnackBar(
    SnackBar(
      content: Text(r.summary, maxLines: 2, overflow: TextOverflow.ellipsis),
      action: onUndo == null
          ? null
          : SnackBarAction(label: actionLabel ?? 'Undo', onPressed: onUndo),
      duration: const Duration(seconds: 5),
    ),
  );
}

/// Undo with the conflict dialog (409 undo_conflict → offer force).
Future<Receipt?> undoWithConfirm(
  BuildContext context,
  AppState state,
  String txnId,
) async {
  try {
    final r = await state.undo(txnId);
    if (context.mounted) showReceiptSnack(context, r);
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
          if (context.mounted) showReceiptSnack(context, r);
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
                  onPressed: () => SettingsScreen.show(context),
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

/// Small-caps section label.
class SectionLabel extends StatelessWidget {
  const SectionLabel(this.text, {super.key, this.trailing});
  final String text;
  final Widget? trailing;
  @override
  Widget build(BuildContext context) {
    final t = Theme.of(context).textTheme.labelSmall!;
    return Padding(
      padding: const EdgeInsets.only(top: 18, bottom: 6),
      child: Row(
        children: [
          Expanded(
            child: Text(
              text.toUpperCase(),
              style: t.copyWith(letterSpacing: 1.1),
            ),
          ),
          ?trailing,
        ],
      ),
    );
  }
}

/// The object id as a small chip: short id, full id in the tooltip, tap copies.
class IdChip extends StatelessWidget {
  const IdChip(this.id, {super.key, this.rev});
  final String id;
  final int? rev;
  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    return Tooltip(
      message: '$id${rev == null ? '' : '\nrevision $rev'}\nTap to copy',
      child: Semantics(
        button: true,
        label: 'Copy id $id',
        child: InkWell(
          borderRadius: BorderRadius.circular(5),
          onTap: () async {
            await Clipboard.setData(ClipboardData(text: id));
            if (context.mounted) {
              ScaffoldMessenger.maybeOf(context)?.showSnackBar(
                SnackBar(
                  content: Text('Copied $id'),
                  duration: const Duration(seconds: 2),
                ),
              );
            }
          },
          child: Container(
            padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 3),
            decoration: BoxDecoration(
              color: scheme.surfaceContainer,
              borderRadius: BorderRadius.circular(5),
            ),
            child: Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                Icon(
                  RefChip.iconFor(id),
                  size: 11,
                  color: scheme.onSurfaceVariant,
                ),
                const SizedBox(width: 4),
                Text(RefChip.shortId(id), style: monoStyle(context, size: 11)),
              ],
            ),
          ),
        ),
      ),
    );
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
        ? '${receipt.actorLabel}${receipt.modelName.isEmpty ? '' : ' · ${receipt.modelName}'}\n${receipt.actor}'
        : receipt.actor;
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

/// One receipt line: the object as a titled chip inside the sentence.
/// `Created idea "X". Linked to Y.` → Created idea [X]. Linked to Y.
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
    final base = style ?? Theme.of(context).textTheme.bodyMedium!;
    final text = line.text;
    // New receipts use curly quotes; older ones straight quotes.
    var q1 = text.indexOf('“');
    var q2 = q1 >= 0 ? text.indexOf('”', q1 + 1) : -1;
    if (q1 < 0 || q2 < 0) {
      q1 = text.indexOf('"');
      q2 = q1 >= 0 ? text.indexOf('"', q1 + 1) : -1;
    }
    if (line.objectId.isEmpty || q1 < 0 || q2 < 0) {
      return Text(text, style: base);
    }
    final quoted = text.substring(q1 + 1, q2);
    // Swallow punctuation glued to the closing quote (`"X".` → chip, then
    // continue with the next sentence).
    var rest = text.substring(q2 + 1);
    while (rest.isNotEmpty && '.,;:'.contains(rest[0])) {
      rest = rest.substring(1);
    }
    rest = rest.trimLeft();
    final chipLabel = RefLabels.maybeOf(context)?.labelFor(line.objectId);
    return Text.rich(
      TextSpan(
        style: base,
        children: [
          TextSpan(text: '${text.substring(0, q1).trimRight()} '),
          WidgetSpan(
            alignment: PlaceholderAlignment.middle,
            child: RefChip(
              id: line.objectId,
              label: chipLabel == null || chipLabel.isEmpty
                  ? quoted
                  : chipLabel,
              onTap: onOpen,
              dense: true,
            ),
          ),
          if (rest.isNotEmpty) TextSpan(text: ' $rest'),
        ],
      ),
    );
  }
}

/// One receipt: actor, time, lines with object chips, undo.
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
    final style =
        (dense ? theme.textTheme.bodySmall : theme.textTheme.bodyMedium)!
            .copyWith(
              decoration: undone ? TextDecoration.lineThrough : null,
              color: undone ? scheme.onSurfaceVariant : scheme.onSurface,
            );
    final metaBits = <String>[
      if (showTime) timeAgo(receipt.at),
      if (receipt.modelName.isNotEmpty) receipt.modelName,
      if (undone) 'undone',
      if (receipt.isUndo) 'undo',
    ];
    return Padding(
      padding: EdgeInsets.symmetric(vertical: dense ? 4 : 8),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          if (showActor) ...[
            Padding(
              padding: const EdgeInsets.only(top: 2),
              child: ActorBadge(receipt),
            ),
            const SizedBox(width: 8),
          ],
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                if (receipt.lines.isEmpty)
                  Text(receipt.summary, style: style)
                else
                  for (final l in receipt.lines)
                    Padding(
                      padding: const EdgeInsets.only(bottom: 2),
                      child: ReceiptLineView(
                        line: l,
                        onOpen: undone ? null : onOpen,
                        style: style,
                      ),
                    ),
                if (metaBits.isNotEmpty)
                  Text(
                    metaBits.join('  ·  '),
                    style: theme.textTheme.labelSmall!.copyWith(
                      color: undone ? scheme.warning : null,
                    ),
                  ),
              ],
            ),
          ),
          if (allowUndo && !undone && receipt.undoable) ...[
            const SizedBox(width: 6),
            QuietUndoButton(
              onPressed: () => undoWithConfirm(context, state, receipt.txnId),
            ),
          ],
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
