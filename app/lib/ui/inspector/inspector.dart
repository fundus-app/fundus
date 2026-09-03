import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:provider/provider.dart';

import '../../api/models.dart';
import '../../state/app_state.dart';
import '../blocks/block_renderer.dart';
import '../blocks/ref_labels.dart';
import '../theme.dart';
import '../widgets/common.dart';

/// The detail pane: read and directly edit the selected object.
class Inspector extends StatelessWidget {
  const Inspector({super.key, required this.onOpen, this.onClose});
  final RefTap onOpen;
  final VoidCallback? onClose;

  @override
  Widget build(BuildContext context) {
    final state = context.watch<AppState>();
    final d = state.detail;
    final removed = state.removedNotice;
    if (removed != null && removed.id == state.selectedId) {
      return _RemovedView(
        notice: removed,
        onClose: onClose ?? state.clearSelection,
      );
    }
    if (state.selectedId == null || d == null) {
      if (state.detailLoading) {
        return const Center(child: CircularProgressIndicator());
      }
      // The primer doubles as the empty state: what to do, from any view.
      return const _Primer();
    }
    final body = switch (d.type) {
      'note' => NoteInspector(detail: d, onOpen: onOpen),
      'task' => TaskInspector(detail: d, onOpen: onOpen),
      'topic' => TopicInspector(
        detail: d,
        page: state.topicPage,
        onOpen: onOpen,
      ),
      'capture' => CaptureInspector(detail: d, onOpen: onOpen),
      'conversation' => _ConversationInspector(detail: d),
      _ => SelectableText(d.raw.toString()),
    };
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Padding(
          padding: const EdgeInsets.fromLTRB(20, 6, 8, 0),
          child: Row(
            children: [
              const Spacer(),
              if (onClose != null)
                IconButton(
                  tooltip: 'Close (Esc)',
                  icon: const Icon(Icons.close_rounded, size: 18),
                  onPressed: onClose,
                ),
            ],
          ),
        ),
        Expanded(
          child: Stack(
            children: [
              SingleChildScrollView(
                padding: const EdgeInsets.fromLTRB(20, 0, 20, 40),
                child: Center(
                  child: ConstrainedBox(
                    constraints: const BoxConstraints(maxWidth: 680),
                    child: body,
                  ),
                ),
              ),
              if (state.detailLoading)
                const Positioned(
                  top: 0,
                  left: 0,
                  right: 0,
                  child: LinearProgressIndicator(minHeight: 2),
                ),
            ],
          ),
        ),
      ],
    );
  }
}

/// First-run primer shown in the empty inspector.
class _Primer extends StatelessWidget {
  const _Primer();
  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Center(
      child: ConstrainedBox(
        constraints: const BoxConstraints(maxWidth: 440),
        child: Padding(
          padding: const EdgeInsets.all(32),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              const FundusMark(size: 48),
              const SizedBox(height: 14),
              Text('Write.', style: theme.textTheme.displayMedium),
              Text('Fundus files it.', style: theme.textTheme.displayMedium),
              Text(
                'Every change has a receipt.',
                style: theme.textTheme.displayMedium,
              ),
              const SizedBox(height: 18),
              Row(
                children: [
                  const KeyHint('Ctrl K'),
                  const SizedBox(width: 8),
                  Text(
                    'captures a thought from any view.',
                    style: theme.textTheme.bodySmall,
                  ),
                ],
              ),
              const SizedBox(height: 6),
              Text(
                'Notes, ideas, tasks and topics appear on the left as you go. Nothing you type is ever rewritten; everything the model does can be undone.',
                style: theme.textTheme.bodySmall,
              ),
            ],
          ),
        ),
      ),
    );
  }
}

/// The selected object was removed by an undo.
class _RemovedView extends StatelessWidget {
  const _RemovedView({required this.notice, required this.onClose});
  final RemovedNotice notice;
  final VoidCallback onClose;
  @override
  Widget build(BuildContext context) {
    final state = context.read<AppState>();
    final theme = Theme.of(context);
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(32),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(
              Icons.undo_rounded,
              size: 28,
              color: theme.colorScheme.outline,
            ),
            const SizedBox(height: 12),
            Text(
              'This item was removed by an undo',
              style: theme.textTheme.titleMedium,
            ),
            if (notice.receipt != null) ...[
              const SizedBox(height: 6),
              Text(
                notice.receipt!.summary,
                style: theme.textTheme.bodySmall,
                textAlign: TextAlign.center,
              ),
            ],
            const SizedBox(height: 12),
            Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                FilledButton.tonal(
                  onPressed: () {
                    onClose();
                    state.setView(AppView.changes);
                  },
                  child: const Text('View change'),
                ),
                const SizedBox(width: 8),
                TextButton(onPressed: onClose, child: const Text('Close')),
              ],
            ),
          ],
        ),
      ),
    );
  }
}

Future<String?> _promptText(
  BuildContext context, {
  required String title,
  String initial = '',
  bool multiline = false,
  String hint = '',
}) {
  final ctrl = TextEditingController(text: initial);
  return showDialog<String>(
    context: context,
    builder: (ctx) => AlertDialog(
      title: Text(title),
      content: SizedBox(
        width: 520,
        child: TextField(
          controller: ctrl,
          autofocus: true,
          minLines: multiline ? 4 : 1,
          maxLines: multiline ? 14 : 1,
          decoration: InputDecoration(hintText: hint),
          style: multiline
              ? monoStyle(
                  ctx,
                  size: 13,
                  color: Theme.of(ctx).colorScheme.onSurface,
                )
              : null,
          onSubmitted: multiline ? null : (v) => Navigator.pop(ctx, v),
        ),
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.pop(ctx),
          child: const Text('Cancel'),
        ),
        FilledButton(
          onPressed: () => Navigator.pop(ctx, ctrl.text),
          child: const Text('Save'),
        ),
      ],
    ),
  );
}

Future<bool> _confirm(
  BuildContext context,
  String title,
  String body, {
  String action = 'Delete',
}) async {
  final ok = await showDialog<bool>(
    context: context,
    builder: (ctx) => AlertDialog(
      title: Text(title),
      content: Text(body),
      actions: [
        TextButton(
          onPressed: () => Navigator.pop(ctx, false),
          child: const Text('Cancel'),
        ),
        FilledButton(
          onPressed: () => Navigator.pop(ctx, true),
          child: Text(action),
        ),
      ],
    ),
  );
  return ok == true;
}

Future<void> _run(BuildContext context, Future<Receipt> Function() fn) async {
  final state = context.read<AppState>();
  try {
    final r = await fn();
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

/// Title row: editable title, then kind/meta chips including the id chip.
class _Header extends StatelessWidget {
  const _Header({
    required this.title,
    required this.onEdit,
    required this.menu,
    required this.chips,
    this.strike = false,
  });
  final String title;
  final VoidCallback onEdit;
  final Widget menu;
  final List<Widget> chips;
  final bool strike;
  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Row(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Expanded(
              child: EditableTitle(
                text: title,
                style: theme.textTheme.headlineMedium!.copyWith(
                  decoration: strike ? TextDecoration.lineThrough : null,
                ),
                onEdit: onEdit,
              ),
            ),
            menu,
          ],
        ),
        const SizedBox(height: 6),
        Wrap(
          spacing: 8,
          runSpacing: 6,
          crossAxisAlignment: WrapCrossAlignment.center,
          children: chips,
        ),
      ],
    );
  }
}

/// History section shared by all inspectors.
class _History extends StatelessWidget {
  const _History({required this.receipts, required this.onOpen});
  final List<Receipt> receipts;
  final RefTap onOpen;
  @override
  Widget build(BuildContext context) {
    if (receipts.isEmpty) return const SizedBox.shrink();
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        const SectionLabel('History'),
        for (final r in receipts)
          ReceiptTile(receipt: r, onOpen: onOpen, dense: true),
      ],
    );
  }
}

class _Origins extends StatelessWidget {
  const _Origins({
    required this.ids,
    required this.onOpen,
    this.label = 'Captured from',
  });
  final List<String> ids;
  final RefTap onOpen;
  final String label;
  @override
  Widget build(BuildContext context) {
    if (ids.isEmpty) return const SizedBox.shrink();
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        SectionLabel(label),
        Wrap(
          spacing: 6,
          runSpacing: 6,
          children: [for (final id in ids) RefChip(id: id, onTap: onOpen)],
        ),
      ],
    );
  }
}

class _Backlinks extends StatelessWidget {
  const _Backlinks({required this.links, required this.onOpen});
  final List<LinkRef> links;
  final RefTap onOpen;
  @override
  Widget build(BuildContext context) {
    if (links.isEmpty) return const SizedBox.shrink();
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        const SectionLabel('Linked from'),
        Wrap(
          spacing: 6,
          runSpacing: 6,
          children: [
            for (final l in links)
              RefChip(id: l.id, label: l.title, onTap: onOpen),
          ],
        ),
      ],
    );
  }
}

// ---------------------------------------------------------------------------
// Markdown editing

/// Inline monospace editor: Ctrl+Enter saves, Esc cancels.
class MarkdownEditor extends StatefulWidget {
  const MarkdownEditor({
    super.key,
    required this.initial,
    required this.onSave,
    required this.onCancel,
  });
  final String initial;
  final Future<void> Function(String markdown) onSave;
  final VoidCallback onCancel;
  @override
  State<MarkdownEditor> createState() => _MarkdownEditorState();
}

class _MarkdownEditorState extends State<MarkdownEditor> {
  late final TextEditingController _ctrl = TextEditingController(
    text: widget.initial,
  );
  bool _saving = false;

  @override
  void dispose() {
    _ctrl.dispose();
    super.dispose();
  }

  Future<void> _save() async {
    if (_saving) return;
    setState(() => _saving = true);
    try {
      await widget.onSave(_ctrl.text);
    } finally {
      if (mounted) setState(() => _saving = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return CallbackShortcuts(
      bindings: {
        const SingleActivator(LogicalKeyboardKey.enter, control: true): _save,
        const SingleActivator(LogicalKeyboardKey.enter, meta: true): _save,
        const SingleActivator(LogicalKeyboardKey.escape): widget.onCancel,
      },
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          TextField(
            controller: _ctrl,
            autofocus: true,
            minLines: 6,
            maxLines: 40,
            style: monoStyle(
              context,
              size: 13,
              color: theme.colorScheme.onSurface,
            ),
            decoration: const InputDecoration(
              hintText: 'Markdown: paragraphs, - lists, > quotes, # headings',
            ),
          ),
          const SizedBox(height: 8),
          Row(
            children: [
              FilledButton(
                onPressed: _saving ? null : _save,
                child: const Text('Save'),
              ),
              const SizedBox(width: 6),
              TextButton(
                onPressed: widget.onCancel,
                child: const Text('Cancel'),
              ),
              const Spacer(),
              const KeyHint('Ctrl ↵ save'),
              const SizedBox(width: 6),
              const KeyHint('Esc'),
            ],
          ),
          const SizedBox(height: 4),
          Text(
            'Unchanged blocks keep their provenance and pins.',
            style: theme.textTheme.labelSmall,
          ),
        ],
      ),
    );
  }
}

/// Wraps each block with edit / pin / delete affordances and provenance.
class EditableDoc extends StatelessWidget {
  const EditableDoc({
    super.key,
    required this.doc,
    required this.onOpen,
    required this.apply,
    this.compact = false,
  });
  final Doc doc;
  final RefTap onOpen;

  /// Applies a list of edits ({action, block_id, markdown}) and returns the receipt.
  final Future<Receipt> Function(List<Map<String, dynamic>> edits) apply;
  final bool compact;

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        if (doc.blocks.isEmpty)
          Text('No content yet.', style: Theme.of(context).textTheme.bodySmall),
        DocView(
          doc: doc,
          onRef: onOpen,
          compact: compact,
          blockDecorator: (ctx, b, child) =>
              _BlockShell(block: b, onOpen: onOpen, apply: apply, child: child),
        ),
        const SizedBox(height: 8),
        Align(
          alignment: Alignment.centerLeft,
          child: TextButton.icon(
            icon: const Icon(Icons.add_rounded, size: 16),
            label: const Text('Add paragraph'),
            onPressed: () async {
              final md = await _promptText(
                context,
                title: 'Add to the end',
                multiline: true,
                hint: 'Markdown: paragraphs, - lists, > quotes, # headings',
              );
              if (md != null && md.trim().isNotEmpty && context.mounted) {
                await _run(
                  context,
                  () => apply([
                    {'action': 'append', 'markdown': md.trim()},
                  ]),
                );
              }
            },
          ),
        ),
      ],
    );
  }
}

class _BlockShell extends StatefulWidget {
  const _BlockShell({
    required this.block,
    required this.child,
    required this.onOpen,
    required this.apply,
  });
  final Block block;
  final Widget child;
  final RefTap onOpen;
  final Future<Receipt> Function(List<Map<String, dynamic>> edits) apply;
  @override
  State<_BlockShell> createState() => _BlockShellState();
}

class _BlockShellState extends State<_BlockShell> {
  bool _hover = false;
  bool _focused = false;

  Future<void> _edit() async {
    final b = widget.block;
    final md = await _promptText(
      context,
      title: 'Edit block',
      initial: b.toMarkdown(),
      multiline: true,
    );
    if (md == null || md.trim().isEmpty || md.trim() == b.toMarkdown()) return;
    if (!mounted) return;
    await _run(
      context,
      () => widget.apply([
        {'action': 'replace', 'block_id': b.id, 'markdown': md.trim()},
      ]),
    );
  }

  Future<void> _delete() async {
    final ok = await _confirm(
      context,
      'Delete this block?',
      'The block is removed from the note. The change is recorded and can be undone.',
    );
    if (!ok || !mounted) return;
    await _run(
      context,
      () => widget.apply([
        {'action': 'delete', 'block_id': widget.block.id},
      ]),
    );
  }

  Future<void> _togglePin() => _run(
    context,
    () => widget.apply([
      {
        'action': widget.block.pinned ? 'unpin' : 'pin',
        'block_id': widget.block.id,
      },
    ]),
  );

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final scheme = theme.colorScheme;
    final b = widget.block;
    final touch = switch (theme.platform) {
      TargetPlatform.android ||
      TargetPlatform.iOS ||
      TargetPlatform.fuchsia => true,
      _ => false,
    };
    final visible = _hover || _focused || touch || b.pinned;
    final labels = RefLabels.maybeOf(context);
    labels?.request(b.sources);
    final sourceTip = b.sources.isEmpty
        ? ''
        : 'From: ${b.sources.map((s) {
            final l = labels?.labelFor(s);
            return l == null || l.isEmpty ? s : l;
          }).join(', ')}';
    final toolbar = AnimatedOpacity(
      opacity: visible ? 1 : 0,
      duration: const Duration(milliseconds: 120),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          if (b.sources.isNotEmpty)
            _tiny(
              context,
              Icons.fingerprint_rounded,
              sourceTip,
              () => widget.onOpen(b.sources.first),
            ),
          _tiny(
            context,
            b.pinned ? Icons.push_pin_rounded : Icons.push_pin_outlined,
            b.pinned
                ? 'Unpin (allow the model to edit)'
                : 'Pin (the model will not change this block)',
            _togglePin,
            color: b.pinned ? scheme.primary : null,
          ),
          _tiny(context, Icons.edit_outlined, 'Edit block', _edit),
          _tiny(context, Icons.delete_outline_rounded, 'Delete block', _delete),
        ],
      ),
    );
    return Focus(
      onFocusChange: (f) => setState(() => _focused = f),
      child: MouseRegion(
        onEnter: (_) => setState(() => _hover = true),
        onExit: (_) => setState(() => _hover = false),
        child: Container(
          decoration: BoxDecoration(
            color: _hover ? scheme.surfaceContainerLow : null,
            borderRadius: BorderRadius.circular(6),
            border: b.pinned
                ? Border(left: BorderSide(color: scheme.primary, width: 2))
                : null,
          ),
          padding: EdgeInsets.only(left: b.pinned ? 8 : 0, right: 2),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Row(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Expanded(child: widget.child),
                  const SizedBox(width: 4),
                  toolbar,
                ],
              ),
              if (b.pinned)
                Padding(
                  padding: const EdgeInsets.only(top: 2, bottom: 2),
                  child: Text(
                    'Yours. The model will not touch this.',
                    style: theme.textTheme.labelSmall!.copyWith(
                      color: scheme.primary,
                    ),
                  ),
                ),
            ],
          ),
        ),
      ),
    );
  }

  Widget _tiny(
    BuildContext context,
    IconData icon,
    String tip,
    VoidCallback onTap, {
    Color? color,
  }) => Tooltip(
    message: tip,
    child: SizedBox(
      width: 32,
      height: 32,
      child: InkWell(
        onTap: onTap,
        borderRadius: BorderRadius.circular(6),
        child: Icon(
          icon,
          size: 16,
          color: color ?? Theme.of(context).colorScheme.onSurfaceVariant,
        ),
      ),
    ),
  );
}

// ---------------------------------------------------------------------------
// Note

class NoteInspector extends StatefulWidget {
  const NoteInspector({super.key, required this.detail, required this.onOpen});
  final ObjectDetail detail;
  final RefTap onOpen;
  @override
  State<NoteInspector> createState() => _NoteInspectorState();
}

class _NoteInspectorState extends State<NoteInspector> {
  bool _editing = false;

  Future<void> _rename(AppState state, Note n) async {
    final t = await _promptText(context, title: 'Rename', initial: n.title);
    if (t == null || t.trim().isEmpty || t.trim() == n.title) return;
    if (!mounted) return;
    await _run(context, () => state.updateNote(n, {'title': t.trim()}));
  }

  Future<void> _menu(AppState state, Note n, String v) async {
    switch (v) {
      case 'edit_text':
        setState(() => _editing = true);
      case 'idea':
      case 'note':
        await _run(context, () => state.updateNote(n, {'kind': v}));
      case 'archive':
        final ok = await _confirm(
          context,
          'Archive this note?',
          'It disappears from views but stays in the data and can be restored.',
          action: 'Archive',
        );
        if (ok && mounted) {
          await _run(context, () => state.archive(n.id, n.meta.rev));
        }
      case 'unarchive':
        await _run(
          context,
          () => state.archive(n.id, n.meta.rev, unarchive: true),
        );
    }
  }

  @override
  Widget build(BuildContext context) {
    final n = widget.detail.note!;
    final state = context.read<AppState>();
    final theme = Theme.of(context);
    final scheme = theme.colorScheme;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        _Header(
          title: n.title,
          onEdit: () => _rename(state, n),
          menu: PopupMenuButton<String>(
            tooltip: 'Note actions',
            icon: const Icon(Icons.more_horiz_rounded),
            onSelected: (v) => _menu(state, n, v),
            itemBuilder: (_) => [
              const PopupMenuItem(
                value: 'edit_text',
                child: Text('Edit as Markdown'),
              ),
              PopupMenuItem(
                value: n.kind == 'idea' ? 'note' : 'idea',
                child: Text(
                  n.kind == 'idea' ? 'Make it a note' : 'Make it an idea',
                ),
              ),
              if (!n.meta.archived)
                const PopupMenuItem(value: 'archive', child: Text('Archive')),
              if (n.meta.archived)
                const PopupMenuItem(
                  value: 'unarchive',
                  child: Text('Unarchive'),
                ),
            ],
          ),
          chips: [
            Chip(
              avatar: Icon(
                n.kind == 'idea'
                    ? Icons.lightbulb_outline_rounded
                    : Icons.notes_rounded,
                size: 14,
              ),
              label: Text(n.kind),
            ),
            if (n.meta.archived)
              Chip(
                label: const Text('archived'),
                backgroundColor: scheme.errorContainer,
              ),
            Text(
              'updated ${timeAgo(n.meta.updatedAt)}',
              style: theme.textTheme.labelSmall,
            ),
            IdChip(n.id, rev: n.meta.rev),
            TopicChips(
              ids: widget.detail.topics.map((t) => t.id).toList(),
              names: widget.detail.topics.map((t) => t.title).toList(),
              onTap: widget.onOpen,
              dense: false,
            ),
          ],
        ),
        const SizedBox(height: 18),
        if (_editing)
          MarkdownEditor(
            initial: n.body.toMarkdown(),
            onCancel: () => setState(() => _editing = false),
            onSave: (md) async {
              if (md.trim() == n.body.toMarkdown().trim()) {
                setState(() => _editing = false);
                return;
              }
              await _run(context, () => state.setNoteMarkdown(n, md.trim()));
              if (mounted) setState(() => _editing = false);
            },
          )
        else
          EditableDoc(
            doc: n.body,
            onOpen: widget.onOpen,
            apply: (edits) => state.reviseNote(n, edits),
          ),
        _Origins(ids: n.origins, onOpen: widget.onOpen),
        _Backlinks(links: widget.detail.backlinks, onOpen: widget.onOpen),
        _History(receipts: widget.detail.receipts, onOpen: widget.onOpen),
      ],
    );
  }
}

// ---------------------------------------------------------------------------
// Task

class TaskInspector extends StatelessWidget {
  const TaskInspector({super.key, required this.detail, required this.onOpen});
  final ObjectDetail detail;
  final RefTap onOpen;

  Future<void> _editText(BuildContext context, AppState state, Task t) async {
    final v = await _promptText(context, title: 'Edit task', initial: t.text);
    if (v == null || v.trim().isEmpty || v.trim() == t.text) return;
    if (!context.mounted) return;
    await _run(context, () => state.updateTask(t, {'text': v.trim()}));
  }

  Future<void> _pickDue(BuildContext context, AppState state, Task t) async {
    final picked = await showDatePicker(
      context: context,
      initialDate: DateTime.tryParse(t.due) ?? DateTime.now(),
      firstDate: DateTime(2000),
      lastDate: DateTime(2100),
    );
    if (picked == null || !context.mounted) return;
    final s =
        '${picked.year.toString().padLeft(4, '0')}-${picked.month.toString().padLeft(2, '0')}-${picked.day.toString().padLeft(2, '0')}';
    await _run(context, () => state.updateTask(t, {'due': s}));
  }

  Future<void> _editEffort(BuildContext context, AppState state, Task t) async {
    final v = await _promptText(
      context,
      title: 'Effort in minutes',
      initial: t.effortMinutes > 0 ? '${t.effortMinutes}' : '',
    );
    final n = int.tryParse(v ?? '');
    if (n == null || !context.mounted) return;
    await _run(context, () => state.updateTask(t, {'effort_minutes': n}));
  }

  Future<void> _editWaiting(
    BuildContext context,
    AppState state,
    Task t,
  ) async {
    final v = await _promptText(
      context,
      title: 'Waiting on',
      initial: t.waitingOn,
    );
    if (v == null || !context.mounted) return;
    await _run(context, () => state.updateTask(t, {'waiting_on': v.trim()}));
  }

  @override
  Widget build(BuildContext context) {
    final t = detail.task!;
    final state = context.read<AppState>();
    final theme = Theme.of(context);
    final scheme = theme.colorScheme;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        _Header(
          title: t.text,
          strike: t.state == 'done',
          onEdit: () => _editText(context, state, t),
          menu: const SizedBox(width: 8),
          chips: [
            Chip(
              avatar: const Icon(Icons.check_circle_outline_rounded, size: 14),
              label: Text(t.state),
            ),
            Text(
              'created ${timeAgo(t.meta.createdAt)}',
              style: theme.textTheme.labelSmall,
            ),
            IdChip(t.id, rev: t.meta.rev),
          ],
        ),
        const SizedBox(height: 14),
        SegmentedButton<String>(
          segments: const [
            ButtonSegment(value: 'open', label: Text('Open')),
            ButtonSegment(value: 'waiting', label: Text('Waiting')),
            ButtonSegment(value: 'later', label: Text('Later')),
            ButtonSegment(value: 'done', label: Text('Done')),
          ],
          selected: {t.state},
          showSelectedIcon: false,
          style: ButtonStyle(
            visualDensity: VisualDensity.compact,
            textStyle: WidgetStatePropertyAll(theme.textTheme.labelMedium),
          ),
          onSelectionChanged: (s) =>
              _run(context, () => state.setTaskState(t, s.first)),
        ),
        const SectionLabel('Details'),
        Wrap(
          spacing: 8,
          runSpacing: 8,
          children: [
            ActionChip(
              avatar: const Icon(Icons.event_rounded, size: 14),
              label: Text(t.due.isEmpty ? 'Due…' : dueLabel(t.due)),
              onPressed: () => _pickDue(context, state, t),
            ),
            if (t.due.isNotEmpty)
              ActionChip(
                avatar: const Icon(Icons.close_rounded, size: 14),
                label: const Text('Clear date'),
                onPressed: () =>
                    _run(context, () => state.updateTask(t, {'due': ''})),
              ),
            ActionChip(
              avatar: const Icon(Icons.timer_outlined, size: 14),
              label: Text(
                t.effortMinutes > 0 ? '${t.effortMinutes} min' : 'Effort…',
              ),
              onPressed: () => _editEffort(context, state, t),
            ),
            PopupMenuButton<int>(
              tooltip: 'Importance',
              onSelected: (v) =>
                  _run(context, () => state.updateTask(t, {'importance': v})),
              itemBuilder: (_) => const [
                PopupMenuItem(value: 0, child: Text('Unset')),
                PopupMenuItem(value: 1, child: Text('Low')),
                PopupMenuItem(value: 2, child: Text('Normal')),
                PopupMenuItem(value: 3, child: Text('High')),
              ],
              child: Chip(
                avatar: Icon(
                  Icons.priority_high_rounded,
                  size: 14,
                  color: t.importance == 3 ? scheme.primary : null,
                ),
                label: Text(
                  const ['Importance…', 'Low', 'Normal', 'High'][t.importance
                      .clamp(0, 3)],
                ),
              ),
            ),
            if (t.state == 'waiting' || t.waitingOn.isNotEmpty)
              ActionChip(
                avatar: const Icon(Icons.hourglass_empty_rounded, size: 14),
                label: Text(
                  t.waitingOn.isEmpty
                      ? 'Waiting on…'
                      : 'Waiting on ${t.waitingOn}',
                ),
                onPressed: () => _editWaiting(context, state, t),
              ),
          ],
        ),
        if (t.reasons.isNotEmpty) ...[
          const SectionLabel('Attention'),
          Tooltip(
            message: 'Attention score ${t.score.toStringAsFixed(1)}',
            child: Text(
              '${t.reasons.join(' · ')}.',
              style: theme.textTheme.bodySmall,
            ),
          ),
        ],
        if (detail.topics.isNotEmpty) ...[
          const SectionLabel('Topics'),
          TopicChips(
            ids: detail.topics.map((x) => x.id).toList(),
            names: detail.topics.map((x) => x.title).toList(),
            onTap: onOpen,
            dense: false,
          ),
        ],
        if (t.notes.isNotEmpty)
          _Origins(ids: t.notes, onOpen: onOpen, label: 'Related notes'),
        _Origins(ids: t.origins, onOpen: onOpen),
        _Backlinks(links: detail.backlinks, onOpen: onOpen),
        _History(receipts: detail.receipts, onOpen: onOpen),
      ],
    );
  }
}

// ---------------------------------------------------------------------------
// Topic

class TopicInspector extends StatefulWidget {
  const TopicInspector({
    super.key,
    required this.detail,
    required this.page,
    required this.onOpen,
  });
  final ObjectDetail detail;
  final TopicPage? page;
  final RefTap onOpen;
  @override
  State<TopicInspector> createState() => _TopicInspectorState();
}

class _TopicInspectorState extends State<TopicInspector> {
  bool _editing = false;

  Future<void> _rename(AppState state, Topic t) async {
    final v = await _promptText(
      context,
      title: 'Rename topic',
      initial: t.name,
    );
    if (v == null || v.trim().isEmpty || v.trim() == t.name) return;
    if (!mounted) return;
    await _run(context, () => state.updateTopic(t, {'name': v.trim()}));
  }

  Future<void> _menu(AppState state, Topic t, String v) async {
    switch (v) {
      case 'summary_text':
        setState(() => _editing = true);
      case 'aliases':
        final a = await _promptText(
          context,
          title: 'Aliases (comma separated)',
          initial: t.aliases.join(', '),
        );
        if (a == null || !mounted) return;
        final aliases = a
            .split(',')
            .map((s) => s.trim())
            .where((s) => s.isNotEmpty)
            .toList();
        await _run(context, () => state.updateTopic(t, {'aliases': aliases}));
      case 'merge':
        final survivor = await _pickTopic(context, state, exclude: t.id);
        if (survivor == null || !mounted) return;
        final ok = await _confirm(
          context,
          'Merge “${t.name}” into “${survivor.name}”?',
          'Notes and tasks are relinked, “${t.name}” becomes an alias. This can be undone.',
          action: 'Merge',
        );
        if (ok && mounted) {
          await _run(context, () => state.mergeTopic(survivor, t));
        }
      case 'topic':
      case 'person':
      case 'project':
        await _run(context, () => state.updateTopic(t, {'kind': v}));
      case 'archive':
        final ok = await _confirm(
          context,
          'Archive this topic?',
          'Links from notes and tasks are kept; the topic disappears from lists.',
          action: 'Archive',
        );
        if (ok && mounted) {
          await _run(context, () => state.archive(t.id, t.meta.rev));
        }
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = widget.detail.topic!;
    final page = widget.page;
    final state = context.read<AppState>();
    final theme = Theme.of(context);
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        _Header(
          title: t.name,
          onEdit: () => _rename(state, t),
          menu: PopupMenuButton<String>(
            tooltip: 'Topic actions',
            icon: const Icon(Icons.more_horiz_rounded),
            onSelected: (v) => _menu(state, t, v),
            itemBuilder: (_) => [
              const PopupMenuItem(
                value: 'summary_text',
                child: Text('Edit summary as Markdown'),
              ),
              const PopupMenuItem(value: 'merge', child: Text('Merge into…')),
              const PopupMenuItem(
                value: 'aliases',
                child: Text('Edit aliases'),
              ),
              if (t.kind != 'topic')
                const PopupMenuItem(
                  value: 'topic',
                  child: Text('Mark as topic'),
                ),
              if (t.kind != 'person')
                const PopupMenuItem(
                  value: 'person',
                  child: Text('Mark as person'),
                ),
              if (t.kind != 'project')
                const PopupMenuItem(
                  value: 'project',
                  child: Text('Mark as project'),
                ),
              const PopupMenuItem(value: 'archive', child: Text('Archive')),
            ],
          ),
          chips: [
            Chip(
              avatar: const Icon(Icons.tag_rounded, size: 14),
              label: Text(t.kind),
            ),
            if (t.aliases.isNotEmpty)
              Text(
                'aka ${t.aliases.join(', ')}',
                style: theme.textTheme.labelSmall,
              ),
            IdChip(t.id, rev: t.meta.rev),
          ],
        ),
        const SectionLabel('Summary'),
        if (_editing)
          MarkdownEditor(
            initial: t.summary.toMarkdown(),
            onCancel: () => setState(() => _editing = false),
            onSave: (md) async {
              if (md.trim() == t.summary.toMarkdown().trim()) {
                setState(() => _editing = false);
                return;
              }
              await _run(context, () => state.setTopicSummary(t, md.trim()));
              if (mounted) setState(() => _editing = false);
            },
          )
        else
          EditableDoc(
            doc: t.summary,
            onOpen: widget.onOpen,
            compact: true,
            apply: (edits) => state.updateTopic(t, {'edits': edits}),
          ),
        if (page != null && page.notes.isNotEmpty) ...[
          SectionLabel(
            'Notes',
            trailing: Text('${page.notes.length}', style: monoStyle(context)),
          ),
          for (final n in page.notes)
            ListTile(
              contentPadding: const EdgeInsets.symmetric(horizontal: 4),
              leading: Icon(
                n.kind == 'idea'
                    ? Icons.lightbulb_outline_rounded
                    : Icons.notes_rounded,
                size: 18,
              ),
              title: Text(n.title, style: theme.textTheme.bodyMedium),
              subtitle:
                  n.preview.isEmpty ||
                      n.preview.trim().startsWith(n.title.trim())
                  ? null
                  : Text(
                      n.preview,
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                      style: theme.textTheme.bodySmall,
                    ),
              onTap: () => widget.onOpen(n.id),
            ),
        ],
        if (page != null && page.tasks.isNotEmpty) ...[
          SectionLabel(
            'Tasks',
            trailing: Text('${page.tasks.length}', style: monoStyle(context)),
          ),
          for (final task in page.tasks)
            ListTile(
              contentPadding: const EdgeInsets.symmetric(horizontal: 4),
              leading: Icon(
                task.state == 'done'
                    ? Icons.check_circle_rounded
                    : Icons.circle_outlined,
                size: 18,
                color: task.state == 'done'
                    ? theme.colorScheme.success
                    : theme.colorScheme.outline,
              ),
              title: Text(
                task.text,
                style: theme.textTheme.bodyMedium!.copyWith(
                  decoration: task.state == 'done'
                      ? TextDecoration.lineThrough
                      : null,
                ),
              ),
              subtitle: Text(
                [
                  task.state,
                  if (task.due.isNotEmpty) dueLabel(task.due),
                ].join(' · '),
                style: theme.textTheme.labelSmall,
              ),
              onTap: () => widget.onOpen(task.id),
            ),
        ],
        _History(receipts: widget.detail.receipts, onOpen: widget.onOpen),
      ],
    );
  }
}

/// A simple searchable topic picker.
Future<Topic?> _pickTopic(
  BuildContext context,
  AppState state, {
  required String exclude,
}) async {
  List<Topic> all;
  try {
    all = await state.api.topics();
  } catch (e) {
    if (context.mounted) showError(context, e);
    return null;
  }
  if (!context.mounted) return null;
  final filter = TextEditingController();
  return showDialog<Topic>(
    context: context,
    builder: (ctx) => StatefulBuilder(
      builder: (ctx, setState) {
        final q = filter.text.trim().toLowerCase();
        final items = all
            .where((t) => t.id != exclude && !t.meta.archived)
            .where(
              (t) =>
                  q.isEmpty ||
                  t.name.toLowerCase().contains(q) ||
                  t.aliases.any((a) => a.toLowerCase().contains(q)),
            )
            .toList();
        return AlertDialog(
          title: const Text('Merge into which topic?'),
          content: SizedBox(
            width: 460,
            height: 380,
            child: Column(
              children: [
                TextField(
                  controller: filter,
                  autofocus: true,
                  decoration: const InputDecoration(
                    hintText: 'Filter topics…',
                    isDense: true,
                  ),
                  onChanged: (_) => setState(() {}),
                ),
                const SizedBox(height: 8),
                Expanded(
                  child: items.isEmpty
                      ? const Center(child: Text('No other topics.'))
                      : ListView.builder(
                          itemCount: items.length,
                          itemBuilder: (_, i) => ListTile(
                            dense: true,
                            leading: Icon(switch (items[i].kind) {
                              'person' => Icons.person_outline_rounded,
                              'project' => Icons.folder_outlined,
                              _ => Icons.tag_rounded,
                            }, size: 18),
                            title: Text(items[i].name),
                            subtitle: items[i].aliases.isEmpty
                                ? null
                                : Text(items[i].aliases.join(', ')),
                            onTap: () => Navigator.pop(ctx, items[i]),
                          ),
                        ),
                ),
              ],
            ),
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.pop(ctx),
              child: const Text('Cancel'),
            ),
          ],
        );
      },
    ),
  );
}

// ---------------------------------------------------------------------------
// Capture

class CaptureInspector extends StatefulWidget {
  const CaptureInspector({
    super.key,
    required this.detail,
    required this.onOpen,
  });
  final ObjectDetail detail;
  final RefTap onOpen;
  @override
  State<CaptureInspector> createState() => _CaptureInspectorState();
}

class _CaptureInspectorState extends State<CaptureInspector> {
  final _answer = TextEditingController();
  bool _busy = false;

  @override
  void dispose() {
    _answer.dispose();
    super.dispose();
  }

  Future<void> _retry(AppState state, Capture c) async {
    if (c.status == 'processed' &&
        c.filingReceipt != null &&
        !c.filingReceipt!.isUndone) {
      final ok = await _confirm(
        context,
        'File again?',
        'This capture was already filed. Filing again runs the model once more and may create duplicates.',
        action: 'File again',
      );
      if (!ok || !mounted) return;
    }
    setState(() => _busy = true);
    try {
      await state.retryCapture(c, answer: _answer.text);
      _answer.clear();
    } catch (e) {
      if (mounted) showError(context, e);
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _accept(AppState state, Capture c) async {
    setState(() => _busy = true);
    try {
      final updated = await state.acceptCapture(c);
      final r = updated.filingReceipt;
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

  Future<void> _dismiss(AppState state, Capture c) async {
    try {
      await state.dismissCapture(c);
    } catch (e) {
      if (mounted) showError(context, e);
    }
  }

  @override
  Widget build(BuildContext context) {
    final c = widget.detail.capture!;
    final state = context.read<AppState>();
    final theme = Theme.of(context);
    final scheme = theme.colorScheme;
    final r = c.result;
    final filedAndKept =
        c.status == 'processed' &&
        c.filingReceipt != null &&
        !c.filingReceipt!.isUndone;
    final canRefile =
        c.status == 'needs_review' ||
        c.status == 'failed' ||
        c.status == 'dismissed' ||
        (c.status == 'processed' && !filedAndKept);
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Wrap(
          spacing: 10,
          runSpacing: 6,
          crossAxisAlignment: WrapCrossAlignment.center,
          children: [
            StatusDot(c.status),
            Text(
              captureStatusLabel(c.status),
              style: theme.textTheme.titleSmall,
            ),
            Text(
              '${captureSourceLabel(c.source)}  ·  ${shortDate(c.meta.createdAt)} ${shortTime(c.meta.createdAt)}',
              style: theme.textTheme.labelSmall,
            ),
            IdChip(c.id, rev: c.meta.rev),
          ],
        ),
        const SizedBox(height: 14),
        Container(
          padding: const EdgeInsets.all(14),
          decoration: BoxDecoration(
            color: scheme.surfaceContainerLow,
            borderRadius: BorderRadius.circular(8),
            border: Border.all(color: scheme.outlineVariant),
          ),
          child: SelectableText(c.text, style: theme.textTheme.bodyLarge),
        ),
        Padding(
          padding: const EdgeInsets.only(top: 6),
          child: Text(
            'Original capture. Fundus never edits it.',
            style: theme.textTheme.labelSmall,
          ),
        ),
        if (c.answer.isNotEmpty) ...[
          const SectionLabel('Your answer'),
          Text(c.answer, style: theme.textTheme.bodyMedium),
        ],
        if (r != null) ...[
          const SectionLabel('Result'),
          _ResultView(capture: c, onOpen: widget.onOpen),
        ],
        if (canRefile) ...[
          const SizedBox(height: 14),
          TextField(
            controller: _answer,
            decoration: InputDecoration(
              hintText: c.status == 'needs_review'
                  ? 'Answer, or add context and file again…'
                  : 'Add context and file again…',
              isDense: true,
            ),
            onChanged: (_) => setState(() {}),
            onSubmitted: (_) => _retry(state, c),
          ),
          const SizedBox(height: 8),
          Wrap(
            spacing: 6,
            runSpacing: 6,
            children: [
              if (c.canAccept) ...[
                FilledButton(
                  onPressed: _busy ? null : () => _accept(state, c),
                  child: const Text('Accept'),
                ),
                FilledButton.tonal(
                  onPressed: _busy || _answer.text.trim().isEmpty
                      ? null
                      : () => _retry(state, c),
                  child: const Text('Answer'),
                ),
              ] else
                FilledButton(
                  onPressed: _busy ? null : () => _retry(state, c),
                  child: Text(c.status == 'processed' ? 'File again' : 'File'),
                ),
              if (c.status != 'dismissed' && c.status != 'processed')
                TextButton(
                  style: TextButton.styleFrom(
                    foregroundColor: scheme.onSurfaceVariant,
                  ),
                  onPressed: _busy ? null : () => _dismiss(state, c),
                  child: const Text('Dismiss'),
                ),
            ],
          ),
        ],
        _History(receipts: widget.detail.receipts, onOpen: widget.onOpen),
      ],
    );
  }
}

class _ConversationInspector extends StatelessWidget {
  const _ConversationInspector({required this.detail});
  final ObjectDetail detail;
  @override
  Widget build(BuildContext context) {
    final c = detail.conversation!;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(
          c.title.isEmpty ? 'Conversation' : c.title,
          style: Theme.of(context).textTheme.headlineMedium,
        ),
        Text(
          '${c.messageCount} messages',
          style: Theme.of(context).textTheme.labelSmall,
        ),
        const SizedBox(height: 8),
        IdChip(c.id, rev: c.meta.rev),
      ],
    );
  }
}

/// The filing result, worded by result.reason.
class _ResultView extends StatelessWidget {
  const _ResultView({required this.capture, required this.onOpen});
  final Capture capture;
  final RefTap onOpen;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final scheme = theme.colorScheme;
    final c = capture;
    final r = c.result!;
    final pct = (r.confidence * 100).round();
    final children = <Widget>[];
    if (r.classification.isNotEmpty) {
      children.add(
        Wrap(
          spacing: 8,
          runSpacing: 6,
          crossAxisAlignment: WrapCrossAlignment.center,
          children: [
            Chip(label: Text(r.classification)),
            if (r.confidence > 0)
              Text('$pct% sure', style: theme.textTheme.labelSmall),
            if (r.model.isNotEmpty || r.provider.isNotEmpty)
              Text(
                modelLabel(r.provider, r.model),
                style: theme.textTheme.labelSmall,
              ),
          ],
        ),
      );
    }
    Widget para(String text, {TextStyle? style}) => Padding(
      padding: const EdgeInsets.only(top: 6),
      child: Text(text, style: style ?? theme.textTheme.bodyMedium),
    );
    if (c.status == 'needs_review') {
      switch (r.reason) {
        case 'unclear':
          children.add(
            Padding(
              padding: const EdgeInsets.only(top: 6),
              child: Row(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Icon(
                    Icons.help_outline_rounded,
                    size: 16,
                    color: scheme.warning,
                  ),
                  const SizedBox(width: 6),
                  Expanded(
                    child: Text(
                      r.question.isEmpty
                          ? 'Fundus needs an answer before filing this.'
                          : r.question,
                      style: FundusTheme.weight(
                        theme.textTheme.bodyMedium!,
                        550,
                      ),
                    ),
                  ),
                ],
              ),
            ),
          );
        case 'low_confidence':
          children.add(
            para('Fundus was unsure ($pct%) and did not file this.'),
          );
          if (r.summary.isNotEmpty && r.proposal.isEmpty) {
            children.add(para(r.summary, style: theme.textTheme.bodySmall));
          }
        case 'proposal':
          children.add(para('Waiting for your approval.'));
        case 'undone':
          children.add(para('Filing was undone.'));
        case 'discard':
          children.add(para('Fundus suggests discarding this.'));
        default:
          if (r.question.isNotEmpty) {
            children.add(
              para(
                r.question,
                style: FundusTheme.weight(theme.textTheme.bodyMedium!, 550),
              ),
            );
          } else if (r.summary.isNotEmpty) {
            children.add(para(r.summary));
          }
      }
    } else if (r.summary.isNotEmpty) {
      children.add(para(r.summary));
    }
    if (r.error.isNotEmpty) {
      children.add(
        Padding(
          padding: const EdgeInsets.only(top: 6),
          child: r.retryable && c.status == 'failed'
              ? Text(
                  'The model provider was unavailable. Fundus will retry automatically. ${r.error}',
                  style: theme.textTheme.bodySmall,
                )
              : SelectableText(
                  r.error,
                  style: theme.textTheme.bodySmall!.copyWith(
                    color: scheme.error,
                  ),
                ),
        ),
      );
    }
    if (r.proposal.isNotEmpty) {
      children.add(const SectionLabel('Proposal'));
      children.add(ProposalView(ops: r.proposal, onOpen: onOpen));
    }
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: children,
    );
  }
}
