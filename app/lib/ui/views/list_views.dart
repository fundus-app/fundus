import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../../api/models.dart';
import '../../state/app_state.dart';
import '../blocks/block_renderer.dart';
import '../theme.dart';
import '../widgets/common.dart';

/// Title + hint for the current view.
class ViewHeader extends StatelessWidget {
  const ViewHeader({super.key, required this.view, this.count, this.trailing});
  final AppView view;
  final int? count;
  final Widget? trailing;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Padding(
      padding: const EdgeInsets.fromLTRB(4, 18, 4, 10),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.end,
        children: [
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Row(
                  crossAxisAlignment: CrossAxisAlignment.baseline,
                  textBaseline: TextBaseline.alphabetic,
                  children: [
                    Text(view.label, style: theme.textTheme.headlineMedium),
                    if (count != null) ...[
                      const SizedBox(width: 10),
                      Text('$count', style: monoStyle(context, size: 13)),
                    ],
                  ],
                ),
                const SizedBox(height: 2),
                Text(view.hint, style: theme.textTheme.bodySmall),
              ],
            ),
          ),
          ?trailing,
        ],
      ),
    );
  }
}

/// Shared list row look.
class _Row extends StatelessWidget {
  const _Row({
    required this.selected,
    required this.onTap,
    required this.child,
    this.leading,
    this.trailing,
  });
  final bool selected;
  final VoidCallback onTap;
  final Widget child;
  final Widget? leading;
  final Widget? trailing;

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    return Material(
      color: selected ? scheme.surfaceContainer : Colors.transparent,
      borderRadius: BorderRadius.circular(8),
      child: InkWell(
        onTap: onTap,
        borderRadius: BorderRadius.circular(8),
        child: Padding(
          padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 10),
          child: Row(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              if (leading != null)
                SizedBox(
                  width: 28,
                  child: Padding(
                    padding: const EdgeInsets.only(top: 2),
                    child: leading,
                  ),
                ),
              Expanded(child: child),
              if (trailing != null) ...[const SizedBox(width: 6), trailing!],
            ],
          ),
        ),
      ),
    );
  }
}

// ---------------------------------------------------------------------------
// Inbox

class InboxList extends StatelessWidget {
  const InboxList({super.key, required this.captures, required this.onOpen});
  final List<Capture> captures;
  final RefTap onOpen;

  @override
  Widget build(BuildContext context) {
    if (captures.isEmpty) {
      return const EmptyState(
        icon: Icons.inbox_outlined,
        title: 'Inbox is empty',
        hint: 'Everything you captured has been filed. Capture a thought with Ctrl K.',
      );
    }
    final state = context.watch<AppState>();
    return ListView.builder(
      padding: const EdgeInsets.only(bottom: 24),
      itemCount: captures.length,
      itemBuilder: (context, i) => _InboxRow(
        capture: captures[i],
        selected: state.selectedId == captures[i].id,
        onOpen: onOpen,
      ),
    );
  }
}

class _InboxRow extends StatefulWidget {
  const _InboxRow({
    required this.capture,
    required this.selected,
    required this.onOpen,
  });
  final Capture capture;
  final bool selected;
  final RefTap onOpen;
  @override
  State<_InboxRow> createState() => _InboxRowState();
}

class _InboxRowState extends State<_InboxRow> {
  final _answer = TextEditingController();
  bool _busy = false;

  @override
  void dispose() {
    _answer.dispose();
    super.dispose();
  }

  Future<void> _retry() async {
    setState(() => _busy = true);
    try {
      await context.read<AppState>().retryCapture(
        widget.capture,
        answer: _answer.text,
      );
      _answer.clear();
    } on ApiException catch (e) {
      if (mounted) {
        showError(
          context,
          e.code == 'processing' ? 'Still being processed, wait a moment.' : e,
        );
      }
    } catch (e) {
      if (mounted) showError(context, e);
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _dismiss(AppState state, Capture c) async {
    try {
      await state.dismissCapture(c);
    } catch (e) {
      if (mounted) showError(context, e);
    }
  }

  Future<void> _accept() async {
    setState(() => _busy = true);
    final state = context.read<AppState>();
    try {
      final c = await state.acceptCapture(widget.capture);
      final r = c.filingReceipt;
      if (mounted && r != null) {
        showReceiptSnack(
          context,
          r,
          onUndo: () => undoWithConfirm(context, state, r.txnId),
        );
      }
    } catch (e) {
      if (mounted) showError(context, e);
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final scheme = theme.colorScheme;
    final c = widget.capture;
    final state = context.read<AppState>();
    return _Row(
      selected: widget.selected,
      onTap: () => widget.onOpen(c.id),
      leading: StatusDot(c.status),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            c.text,
            style: theme.textTheme.bodyMedium,
            maxLines: 3,
            overflow: TextOverflow.ellipsis,
          ),
          const SizedBox(height: 3),
          Row(
            children: [
              Text(
                timeAgo(c.meta.createdAt),
                style: theme.textTheme.labelSmall,
              ),
              Text(
                '  ·  ${captureSourceLabel(c.source)}',
                style: theme.textTheme.labelSmall,
              ),
              if (c.status == 'processing')
                Text(
                  '  ·  being filed…',
                  style: theme.textTheme.labelSmall!.copyWith(
                    color: scheme.primary,
                  ),
                ),
            ],
          ),
          if (c.status == 'needs_review') ...[
            const SizedBox(height: 6),
            _ParkedHeader(capture: c, onOpen: widget.onOpen),
            const SizedBox(height: 12),
            Row(
              children: [
                Expanded(
                  child: TextField(
                    controller: _answer,
                    decoration: InputDecoration(
                      hintText: 'Answer or add context…',
                      isDense: true,
                    ),
                    style: theme.textTheme.bodySmall!.copyWith(
                      color: scheme.onSurface,
                    ),
                    onChanged: (_) => setState(() {}),
                    onSubmitted: (_) => _retry(),
                  ),
                ),
                const SizedBox(width: 6),
                if (c.canAccept) ...[
                  FilledButton(
                    style: FilledButton.styleFrom(
                      visualDensity: VisualDensity.compact,
                    ),
                    onPressed: _busy ? null : _accept,
                    child: const Text('Accept'),
                  ),
                  const SizedBox(width: 4),
                  // With a proposal, the model only runs again with new input.
                  TextButton(
                    style: TextButton.styleFrom(
                      visualDensity: VisualDensity.compact,
                    ),
                    onPressed: _busy || _answer.text.trim().isEmpty
                        ? null
                        : _retry,
                    child: const Text('Answer'),
                  ),
                ] else
                  FilledButton(
                    style: FilledButton.styleFrom(
                      visualDensity: VisualDensity.compact,
                    ),
                    onPressed: _busy ? null : _retry,
                    child: const Text('File'),
                  ),
                TextButton(
                  style: TextButton.styleFrom(
                    visualDensity: VisualDensity.compact,
                    foregroundColor: scheme.onSurfaceVariant,
                  ),
                  onPressed: _busy ? null : () => _dismiss(state, c),
                  child: const Text('Dismiss'),
                ),
              ],
            ),
          ],
          if (c.status == 'failed') ...[
            const SizedBox(height: 6),
            if (c.isRetrying)
              Row(
                children: [
                  SizedBox(
                    width: 12,
                    height: 12,
                    child: CircularProgressIndicator(
                      strokeWidth: 1.5,
                      color: scheme.onSurfaceVariant,
                    ),
                  ),
                  const SizedBox(width: 6),
                  Expanded(
                    child: Text(
                      'Provider unavailable, will retry automatically. ${c.result?.error ?? ''}',
                      style: theme.textTheme.bodySmall,
                      maxLines: 2,
                      overflow: TextOverflow.ellipsis,
                    ),
                  ),
                ],
              )
            else
              Text(
                c.result?.error ?? 'Processing failed',
                style: theme.textTheme.bodySmall!.copyWith(color: scheme.error),
                maxLines: 3,
                overflow: TextOverflow.ellipsis,
              ),
            if (c.hasProposal) ...[
              const SizedBox(height: 6),
              ProposalView(ops: c.result!.proposal, onOpen: widget.onOpen),
            ],
            Row(
              children: [
                if (c.canAccept)
                  FilledButton(
                    style: FilledButton.styleFrom(
                      visualDensity: VisualDensity.compact,
                    ),
                    onPressed: _busy ? null : _accept,
                    child: const Text('Accept'),
                  ),
                TextButton(
                  style: TextButton.styleFrom(
                    visualDensity: VisualDensity.compact,
                  ),
                  onPressed: _busy ? null : _retry,
                  child: Text(c.isRetrying ? 'Retry now' : 'Retry'),
                ),
                TextButton(
                  style: TextButton.styleFrom(
                    visualDensity: VisualDensity.compact,
                    foregroundColor: scheme.onSurfaceVariant,
                  ),
                  onPressed: _busy ? null : () => _dismiss(state, c),
                  child: const Text('Dismiss'),
                ),
              ],
            ),
          ],
        ],
      ),
    );
  }
}

// ---------------------------------------------------------------------------
// Tasks

class TaskList extends StatelessWidget {
  const TaskList({
    super.key,
    required this.tasks,
    required this.onOpen,
    this.showReasons = false,
    this.emptyTitle = 'No tasks',
    this.emptyHint,
  });
  final List<Task> tasks;
  final RefTap onOpen;
  final bool showReasons;
  final String emptyTitle;
  final String? emptyHint;

  @override
  Widget build(BuildContext context) {
    if (tasks.isEmpty) {
      return EmptyState(
        icon: Icons.check_circle_outline_rounded,
        title: emptyTitle,
        hint: emptyHint ?? 'Nothing here yet. Capture a thought with Ctrl K.',
      );
    }
    final state = context.watch<AppState>();
    return ListView.builder(
      padding: const EdgeInsets.only(bottom: 24),
      itemCount: tasks.length,
      itemBuilder: (context, i) => TaskRow(
        task: tasks[i],
        selected: state.selectedId == tasks[i].id,
        onOpen: onOpen,
        showReasons: showReasons,
      ),
    );
  }
}

class TaskRow extends StatelessWidget {
  const TaskRow({
    super.key,
    required this.task,
    required this.selected,
    required this.onOpen,
    this.showReasons = false,
  });
  final Task task;
  final bool selected;
  final RefTap onOpen;
  final bool showReasons;

  Future<void> _setState(BuildContext context, String s) async {
    final state = context.read<AppState>();
    try {
      final r = await state.setTaskState(task, s);
      if (context.mounted) {
        showReceiptSnack(
          context,
          r,
          onUndo: () => undoWithConfirm(context, state, r.txnId),
        );
      }
    } catch (e) {
      if (context.mounted) showError(context, e);
    }
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final scheme = theme.colorScheme;
    final done = task.state == 'done';
    final overdue =
        task.due.isNotEmpty &&
        (DateTime.tryParse(task.due)
                ?.isBefore(DateTime.now().subtract(const Duration(days: 1))) ??
            false);
    return _Row(
      selected: selected,
      onTap: () => onOpen(task.id),
      leading: Semantics(
        label: done ? 'Reopen task' : 'Complete task',
        button: true,
        child: SizedBox(
          width: 32,
          height: 32,
          child: InkWell(
            onTap: () => _setState(context, done ? 'open' : 'done'),
            borderRadius: BorderRadius.circular(16),
            child: Icon(
              done ? Icons.check_circle_rounded : Icons.circle_outlined,
              size: 20,
              color: done ? scheme.success : scheme.outline,
            ),
          ),
        ),
      ),
      trailing: PopupMenuButton<String>(
        tooltip: 'Task actions',
        icon: const Icon(Icons.more_horiz_rounded, size: 18),
        onSelected: (s) => _setState(context, s),
        itemBuilder: (_) => [
          if (task.state != 'open')
            const PopupMenuItem(value: 'open', child: Text('Reopen')),
          if (task.state != 'done')
            const PopupMenuItem(value: 'done', child: Text('Done')),
          if (task.state != 'later')
            const PopupMenuItem(value: 'later', child: Text('Later')),
          if (task.state != 'waiting')
            const PopupMenuItem(value: 'waiting', child: Text('Waiting')),
        ],
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            task.text,
            style: theme.textTheme.bodyMedium!.copyWith(
              decoration: done ? TextDecoration.lineThrough : null,
              color: done ? scheme.onSurfaceVariant : scheme.onSurface,
            ),
          ),
          const SizedBox(height: 4),
          Wrap(
            spacing: 6,
            runSpacing: 4,
            crossAxisAlignment: WrapCrossAlignment.center,
            children: [
              if (task.due.isNotEmpty)
                _Meta(
                  icon: Icons.event_rounded,
                  text: dueLabel(task.due),
                  color: overdue ? scheme.error : null,
                ),
              if (task.effortMinutes > 0)
                _Meta(
                  icon: Icons.timer_outlined,
                  text: '${task.effortMinutes} min',
                ),
              if (task.importance == 3)
                _Meta(
                  icon: Icons.priority_high_rounded,
                  text: 'important',
                  color: scheme.primary,
                ),
              if (task.waitingOn.isNotEmpty)
                _Meta(
                  icon: Icons.hourglass_empty_rounded,
                  text: task.waitingOn,
                ),
              if (task.topicNames.isNotEmpty)
                TopicChips(
                  ids: task.topics,
                  names: task.topicNames,
                  onTap: onOpen,
                ),
              if (showReasons && task.reasons.isNotEmpty)
                Tooltip(
                  message: 'Attention score ${task.score.toStringAsFixed(1)}',
                  child: Text(
                    task.reasons.join(' · '),
                    style: theme.textTheme.labelSmall,
                  ),
                ),
            ],
          ),
        ],
      ),
    );
  }
}

class _Meta extends StatelessWidget {
  const _Meta({required this.icon, required this.text, this.color});
  final IconData icon;
  final String text;
  final Color? color;
  @override
  Widget build(BuildContext context) {
    final t = Theme.of(context).textTheme.labelSmall!;
    final c = color ?? t.color;
    return Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        Icon(icon, size: 12, color: c),
        const SizedBox(width: 3),
        Text(text, style: t.copyWith(color: c)),
      ],
    );
  }
}

// ---------------------------------------------------------------------------
// Notes

class NoteList extends StatelessWidget {
  const NoteList({
    super.key,
    required this.notes,
    required this.onOpen,
    required this.kind,
  });
  final List<Note> notes;
  final RefTap onOpen;
  final String kind;

  @override
  Widget build(BuildContext context) {
    if (notes.isEmpty) {
      return EmptyState(
        icon: kind == 'idea'
            ? Icons.lightbulb_outline_rounded
            : Icons.notes_rounded,
        title: kind == 'idea' ? 'No ideas yet' : 'No notes yet',
        hint: 'Nothing here yet. Capture a thought with Ctrl K.',
      );
    }
    final state = context.watch<AppState>();
    return ListView.builder(
      padding: const EdgeInsets.only(bottom: 24),
      itemCount: notes.length,
      itemBuilder: (context, i) {
        final n = notes[i];
        return _Row(
          selected: state.selectedId == n.id,
          onTap: () => onOpen(n.id),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(n.title, style: Theme.of(context).textTheme.titleSmall),
              if (remainderAfterTitle(n.title, n.preview) != null) ...[
                const SizedBox(height: 2),
                Text(
                  remainderAfterTitle(n.title, n.preview)!,
                  style: Theme.of(context).textTheme.bodySmall,
                  maxLines: 2,
                  overflow: TextOverflow.ellipsis,
                ),
              ],
              const SizedBox(height: 4),
              Wrap(
                spacing: 6,
                runSpacing: 4,
                crossAxisAlignment: WrapCrossAlignment.center,
                children: [
                  Text(
                    timeAgo(n.meta.updatedAt),
                    style: Theme.of(context).textTheme.labelSmall,
                  ),
                  TopicChips(ids: n.topics, names: n.topicNames, onTap: onOpen),
                ],
              ),
            ],
          ),
        );
      },
    );
  }
}

// ---------------------------------------------------------------------------
// Topics

class TopicList extends StatelessWidget {
  const TopicList({super.key, required this.topics, required this.onOpen});
  final List<Topic> topics;
  final RefTap onOpen;

  @override
  Widget build(BuildContext context) {
    if (topics.isEmpty) {
      return const EmptyState(
        icon: Icons.tag_rounded,
        title: 'No topics yet',
        hint: 'Fundus creates topics when a subject comes up more than once.',
      );
    }
    final state = context.watch<AppState>();
    final theme = Theme.of(context);
    return ListView.builder(
      padding: const EdgeInsets.only(bottom: 24),
      itemCount: topics.length,
      itemBuilder: (context, i) {
        final t = topics[i];
        final icon = switch (t.kind) {
          'person' => Icons.person_outline_rounded,
          'project' => Icons.folder_outlined,
          _ => Icons.tag_rounded,
        };
        final meta = [
          if (t.noteCount > 0)
            '${t.noteCount} note${t.noteCount == 1 ? '' : 's'}',
          if (t.openTaskCount > 0)
            '${t.openTaskCount} open task${t.openTaskCount == 1 ? '' : 's'}',
          if (t.aliases.isNotEmpty) 'aka ${t.aliases.join(', ')}',
          if (t.lastActivity != null) timeAgo(t.lastActivity),
        ].join('  ·  ');
        return _Row(
          selected: state.selectedId == t.id,
          onTap: () => onOpen(t.id),
          leading: Icon(
            icon,
            size: 18,
            color: theme.colorScheme.onSurfaceVariant,
          ),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(t.name, style: theme.textTheme.titleSmall),
              const SizedBox(height: 2),
              Text(meta, style: theme.textTheme.labelSmall),
            ],
          ),
        );
      },
    );
  }
}

// ---------------------------------------------------------------------------
// Changes

class ChangesList extends StatelessWidget {
  const ChangesList({super.key, required this.receipts, required this.onOpen});
  final List<Receipt> receipts;
  final RefTap onOpen;

  @override
  Widget build(BuildContext context) {
    if (receipts.isEmpty) {
      return const EmptyState(
        icon: Icons.history_rounded,
        title: 'No changes yet',
        hint: 'Every filing, edit and undo will show up here.',
      );
    }
    final rows = <Widget>[];
    String? day;
    for (final r in receipts) {
      final d = dayLabel(r.at);
      if (d != day) {
        day = d;
        rows.add(SectionLabel(d));
      } else {
        rows.add(const Divider());
      }
      rows.add(ReceiptTile(receipt: r, onOpen: onOpen));
    }
    return ListView(
      padding: const EdgeInsets.only(bottom: 24, left: 4, right: 4),
      children: rows,
    );
  }
}

// ---------------------------------------------------------------------------
// Search

class SearchResults extends StatelessWidget {
  const SearchResults({
    super.key,
    required this.hits,
    required this.onOpen,
    required this.query,
  });
  final List<SearchHit> hits;
  final RefTap onOpen;
  final String query;

  @override
  Widget build(BuildContext context) {
    if (query.trim().isEmpty) {
      return const EmptyState(
        icon: Icons.search_rounded,
        title: 'Search',
        hint: 'Notes, ideas, tasks and topics. Matches words, not meaning.',
      );
    }
    if (hits.isEmpty) {
      return EmptyState(
        icon: Icons.search_off_rounded,
        title: 'Nothing found for “$query”',
        hint: 'Try another word, or ask in the conversation.',
      );
    }
    final theme = Theme.of(context);
    return ListView.builder(
      padding: const EdgeInsets.only(bottom: 24),
      itemCount: hits.length,
      itemBuilder: (context, i) {
        final h = hits[i];
        final meta = [
          h.type,
          if (h.kind.isNotEmpty) h.kind,
          if (h.state.isNotEmpty) h.state,
        ].join(' · ');
        return _Row(
          selected: false,
          onTap: () => onOpen(h.id),
          leading: Icon(
            RefChip.iconFor(h.id),
            size: 18,
            color: theme.colorScheme.onSurfaceVariant,
          ),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(h.title, style: theme.textTheme.titleSmall),
              if (h.preview.isNotEmpty)
                Text(
                  h.preview,
                  style: theme.textTheme.bodySmall,
                  maxLines: 2,
                  overflow: TextOverflow.ellipsis,
                ),
              Text(meta, style: theme.textTheme.labelSmall),
            ],
          ),
        );
      },
    );
  }
}

/// Why a capture waits, driven by result.reason.
class _ParkedHeader extends StatelessWidget {
  const _ParkedHeader({required this.capture, required this.onOpen});
  final Capture capture;
  final RefTap onOpen;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final scheme = theme.colorScheme;
    final r = capture.result;
    if (r == null) return const SizedBox.shrink();
    final pct = (r.confidence * 100).round();
    Widget line(
      IconData icon,
      Color color,
      String text, {
      bool strong = false,
    }) => Row(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Icon(icon, size: 15, color: color),
        const SizedBox(width: 6),
        Expanded(
          child: Text(
            text,
            style: strong
                ? FundusTheme.weight(
                    theme.textTheme.bodySmall!,
                    550,
                  ).copyWith(color: scheme.onSurface)
                : theme.textTheme.bodySmall!.copyWith(color: scheme.onSurface),
          ),
        ),
      ],
    );
    final head = switch (r.reason) {
      'unclear' => line(
        Icons.help_outline_rounded,
        scheme.warning,
        r.question.isEmpty
            ? 'Fundus needs an answer before filing this.'
            : r.question,
        strong: true,
      ),
      'low_confidence' => line(
        Icons.tune_rounded,
        scheme.onSurfaceVariant,
        'Fundus was unsure ($pct%) and did not file this.',
      ),
      'proposal' => line(
        Icons.pending_actions_rounded,
        scheme.onSurfaceVariant,
        'Waiting for your approval.',
      ),
      'undone' => line(
        Icons.undo_rounded,
        scheme.onSurfaceVariant,
        'Filing was undone.',
      ),
      'discard' => line(
        Icons.delete_sweep_outlined,
        scheme.onSurfaceVariant,
        'Fundus suggests discarding this.',
      ),
      _ =>
        r.question.isNotEmpty
            ? line(
                Icons.help_outline_rounded,
                scheme.warning,
                r.question,
                strong: true,
              )
            : line(
                Icons.tune_rounded,
                scheme.onSurfaceVariant,
                r.summary.isEmpty ? 'Waiting for you.' : r.summary,
              ),
    };
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        head,
        if (r.reason == 'low_confidence' &&
            r.summary.isNotEmpty &&
            r.proposal.isEmpty)
          Padding(
            padding: const EdgeInsets.only(left: 21, top: 2),
            child: Text(r.summary, style: theme.textTheme.bodySmall),
          ),
        if (capture.hasProposal) ...[
          const SizedBox(height: 6),
          ProposalView(ops: r.proposal, onOpen: onOpen),
        ],
      ],
    );
  }
}
