import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:provider/provider.dart';

import '../../api/models.dart';
import '../../state/app_state.dart';
import '../../state/chat_state.dart';
import '../../state/settings.dart';
import '../blocks/block_renderer.dart';
import '../widgets/common.dart';

/// The conversation pane: thread, working trail, receipts, composer.
class ConversationScreen extends StatefulWidget {
  const ConversationScreen({super.key, required this.onOpen, this.focusNode});
  final RefTap onOpen;
  final FocusNode? focusNode;

  @override
  State<ConversationScreen> createState() => _ConversationScreenState();
}

class _ConversationScreenState extends State<ConversationScreen> {
  final _ctrl = TextEditingController();
  late final FocusNode _focus = widget.focusNode ?? FocusNode();
  final _scroll = ScrollController();

  @override
  void initState() {
    super.initState();
    final chat = context.read<ChatState>();
    chat.loadConversations().then((_) {
      if (chat.current == null && chat.conversations.isNotEmpty) {
        chat.open(chat.conversations.first.id);
      }
    });
  }

  @override
  void dispose() {
    _ctrl.dispose();
    if (widget.focusNode == null) _focus.dispose();
    _scroll.dispose();
    super.dispose();
  }

  Future<void> _send() async {
    final text = _ctrl.text.trim();
    if (text.isEmpty) return;
    _ctrl.clear();
    _focus.requestFocus();
    await context.read<ChatState>().send(text);
    _scrollDown();
  }

  Future<void> _rename(ChatState chat, Conversation conv) async {
    final ctrl = TextEditingController(text: conv.title);
    final v = await showDialog<String>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('Rename conversation'),
        content: TextField(
          controller: ctrl,
          autofocus: true,
          onSubmitted: (v) => Navigator.pop(ctx, v),
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
    if (v != null && v.trim().isNotEmpty && v.trim() != conv.title) {
      await chat.rename(v);
    }
  }

  void _scrollDown() {
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (_scroll.hasClients) {
        _scroll.animateTo(
          _scroll.position.maxScrollExtent,
          duration: const Duration(milliseconds: 250),
          curve: Curves.easeOut,
        );
      }
    });
  }

  @override
  Widget build(BuildContext context) {
    final chat = context.watch<ChatState>();
    final theme = Theme.of(context);
    final scheme = theme.colorScheme;
    final conv = chat.current;
    if (chat.sending) _scrollDown();
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Padding(
          padding: const EdgeInsets.fromLTRB(4, 18, 4, 8),
          child: Row(
            children: [
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    EditableTitle(
                      text: conv == null || conv.title.isEmpty
                          ? 'New conversation'
                          : conv.title,
                      style: theme.textTheme.headlineMedium!,
                      tooltip: 'Rename',
                      onEdit: conv == null ? () {} : () => _rename(chat, conv),
                    ),
                    const SizedBox(height: 2),
                    Text(
                      AppView.conversation.hint,
                      style: theme.textTheme.bodySmall,
                    ),
                  ],
                ),
              ),
              _ConversationPicker(chat: chat),
              const SizedBox(width: 4),
              FilledButton.tonalIcon(
                style: FilledButton.styleFrom(
                  visualDensity: VisualDensity.compact,
                ),
                onPressed: chat.sending ? null : () => chat.startNew(),
                icon: const Icon(Icons.add_rounded, size: 16),
                label: const Text('New'),
              ),
            ],
          ),
        ),
        Expanded(
          child: conv == null || conv.messages.isEmpty
              ? _Intro(
                  onPick: (s) {
                    _ctrl.text = s;
                    _focus.requestFocus();
                  },
                )
              : ListView.builder(
                  controller: _scroll,
                  padding: const EdgeInsets.fromLTRB(4, 4, 4, 16),
                  itemCount: conv.messages.length + (chat.sending ? 1 : 0),
                  itemBuilder: (context, i) {
                    if (i == conv.messages.length) {
                      return _WorkingTrail(
                        steps: chat.liveSteps,
                        live: true,
                        onOpen: widget.onOpen,
                      );
                    }
                    final m = conv.messages[i];
                    if (m.interrupted) {
                      String? previous;
                      for (var j = i - 1; j >= 0; j--) {
                        if (conv.messages[j].role == 'user') {
                          previous = conv.messages[j].text;
                          break;
                        }
                      }
                      return _InterruptedNote(
                        previousText: previous,
                        onResend: chat.sending || previous == null
                            ? null
                            : () => chat.resend(previous!),
                      );
                    }
                    return _MessageTile(
                      message: m,
                      steps: chat.stepsByMessage[i] ?? const [],
                      receipts: chat.receiptsFor(m.txnIds),
                      onOpen: widget.onOpen,
                    );
                  },
                ),
        ),
        if (chat.busy)
          Padding(
            padding: const EdgeInsets.only(bottom: 6),
            child: Row(
              children: [
                SizedBox(
                  width: 12,
                  height: 12,
                  child: CircularProgressIndicator(
                    strokeWidth: 1.5,
                    color: scheme.tertiary,
                  ),
                ),
                const SizedBox(width: 8),
                Text(
                  'Still working on the previous turn…',
                  style: theme.textTheme.bodySmall,
                ),
              ],
            ),
          ),
        if (chat.error != null)
          Padding(
            padding: const EdgeInsets.only(bottom: 6),
            child: Row(
              children: [
                Expanded(
                  child: Text(
                    describeError(
                      chat.error!,
                      serverUrl: context.read<Settings?>()?.serverUrl,
                    ),
                    style: theme.textTheme.bodySmall!.copyWith(
                      color: scheme.error,
                    ),
                  ),
                ),
                TextButton(
                  style: TextButton.styleFrom(
                    visualDensity: VisualDensity.compact,
                  ),
                  onPressed: chat.sending ? null : chat.retryLast,
                  child: const Text('Retry'),
                ),
              ],
            ),
          ),
        CallbackShortcuts(
          bindings: {
            const SingleActivator(LogicalKeyboardKey.enter): _send,
            const SingleActivator(LogicalKeyboardKey.numpadEnter): _send,
          },
          child: TextField(
            key: const Key('chat-field'),
            controller: _ctrl,
            focusNode: _focus,
            minLines: 1,
            maxLines: 6,
            keyboardType: TextInputType.multiline,
            textInputAction: TextInputAction.newline,
            enabled: !chat.sending,
            decoration: InputDecoration(
              hintText: chat.sending
                  ? 'Working…'
                  : 'Ask, recall, or tell Fundus something…',
              prefixIcon: Icon(Icons.forum_outlined, color: scheme.primary),
              suffixIcon: IconButton(
                tooltip: 'Send',
                icon: const Icon(Icons.send_rounded, size: 18),
                onPressed: chat.sending ? null : _send,
              ),
            ),
          ),
        ),
        const SizedBox(height: 8),
      ],
    );
  }
}

class _ConversationPicker extends StatelessWidget {
  const _ConversationPicker({required this.chat});
  final ChatState chat;
  @override
  Widget build(BuildContext context) {
    if (chat.conversations.isEmpty) return const SizedBox.shrink();
    return PopupMenuButton<String>(
      tooltip: 'Conversations',
      onSelected: chat.open,
      itemBuilder: (_) => [
        for (final c in chat.conversations.take(20))
          PopupMenuItem(
            value: c.id,
            child: Row(
              children: [
                Expanded(
                  child: Text(
                    c.title.isEmpty ? 'Conversation' : c.title,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                  ),
                ),
                const SizedBox(width: 12),
                Text(
                  timeAgo(c.updatedAt),
                  style: Theme.of(context).textTheme.labelSmall,
                ),
              ],
            ),
          ),
      ],
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 6),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(
              Icons.history_rounded,
              size: 16,
              color: Theme.of(context).colorScheme.onSurfaceVariant,
            ),
            const SizedBox(width: 4),
            Text(
              'Conversations',
              style: Theme.of(context).textTheme.labelMedium,
            ),
            Icon(
              Icons.arrow_drop_down_rounded,
              size: 18,
              color: Theme.of(context).colorScheme.onSurfaceVariant,
            ),
          ],
        ),
      ),
    );
  }
}

class _Intro extends StatelessWidget {
  const _Intro({required this.onPick});
  final void Function(String) onPick;
  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final samples = [
      'What were my thoughts on a notes system of my own?',
      'Which ideas have I not followed up on lately?',
      'I have one hour. What could I sensibly do?',
      'Remember: the heating gateway listens on port 8899.',
    ];
    return Center(
      child: ConstrainedBox(
        constraints: const BoxConstraints(maxWidth: 520),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(
              'Ask your knowledge base.',
              style: theme.textTheme.headlineSmall,
            ),
            const SizedBox(height: 6),
            Text(
              'Fundus searches your notes and tasks before answering, cites what it used, and files anything new you tell it. Every change comes with a receipt and undo.',
              style: theme.textTheme.bodySmall,
            ),
            const SizedBox(height: 16),
            for (final s in samples)
              Padding(
                padding: const EdgeInsets.only(bottom: 6),
                child: ActionChip(label: Text(s), onPressed: () => onPick(s)),
              ),
          ],
        ),
      ),
    );
  }
}

class _MessageTile extends StatelessWidget {
  const _MessageTile({
    required this.message,
    required this.steps,
    required this.receipts,
    required this.onOpen,
  });
  final Message message;
  final List<ChatStep> steps;

  /// Receipts resolved for message.txn_ids (reopened threads).
  final List<Receipt> receipts;
  final RefTap onOpen;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final scheme = theme.colorScheme;
    final isUser = message.role == 'user';
    if (isUser) {
      return Padding(
        padding: const EdgeInsets.only(top: 14, bottom: 6, left: 48),
        child: Align(
          alignment: Alignment.centerRight,
          child: Container(
            padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 10),
            decoration: BoxDecoration(
              color: scheme.primaryContainer.withValues(alpha: 0.55),
              borderRadius: const BorderRadius.only(
                topLeft: Radius.circular(14),
                topRight: Radius.circular(14),
                bottomLeft: Radius.circular(14),
                bottomRight: Radius.circular(4),
              ),
            ),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.end,
              mainAxisSize: MainAxisSize.min,
              children: [
                SelectableText(message.text, style: theme.textTheme.bodyLarge),
                const SizedBox(height: 2),
                Row(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    Text(
                      shortTime(message.at),
                      style: theme.textTheme.labelSmall!.copyWith(fontSize: 12),
                    ),
                    if (message.captureId.isNotEmpty) ...[
                      Text(
                        ' · ',
                        style: theme.textTheme.labelSmall!.copyWith(
                          fontSize: 12,
                        ),
                      ),
                      LinkedText(
                        style: theme.textTheme.labelSmall!.copyWith(
                          fontSize: 12,
                        ),
                        parts: [
                          TextPart(
                            'filed',
                            onTap: () => onOpen(message.captureId),
                          ),
                        ],
                      ),
                    ],
                  ],
                ),
              ],
            ),
          ),
        ),
      );
    }
    final stepReceipts = steps
        .where((s) => s.receipt != null)
        .map((s) => s.receipt!)
        .toList();
    final receipts = stepReceipts.isNotEmpty ? stepReceipts : this.receipts;
    final pendingTxns = message.txnIds.length - receipts.length;
    return Padding(
      padding: const EdgeInsets.only(top: 10, bottom: 8, right: 24),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          if (steps.isNotEmpty)
            _WorkingTrail(steps: steps, live: false, onOpen: onOpen),
          Row(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Padding(
                padding: const EdgeInsets.only(top: 4, right: 10),
                child: Icon(
                  Icons.auto_awesome_rounded,
                  size: 16,
                  color: scheme.tertiary,
                ),
              ),
              Expanded(
                child: message.blocks.blocks.isNotEmpty
                    ? DocView(doc: message.blocks, onRef: onOpen)
                    : InlineText(
                        message.text,
                        style: theme.textTheme.bodyLarge,
                        onRef: onOpen,
                      ),
              ),
            ],
          ),
          if (receipts.isNotEmpty || pendingTxns > 0)
            Padding(
              padding: const EdgeInsets.only(left: 26, top: 6),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  for (final r in receipts)
                    _ReceiptCard(receipt: r, onOpen: onOpen),
                  if (pendingTxns > 0)
                    Text(
                      'Loading ${pendingTxns == 1 ? 'receipt' : '$pendingTxns receipts'}…',
                      style: theme.textTheme.labelSmall,
                    ),
                ],
              ),
            ),
          if (message.refs.isNotEmpty)
            Padding(
              padding: const EdgeInsets.only(left: 26, top: 8),
              child: Wrap(
                spacing: 6,
                runSpacing: 6,
                children: [
                  for (final id in message.refs) RefChip(id: id, onTap: onOpen),
                ],
              ),
            ),
        ],
      ),
    );
  }
}

/// System note: the daemon restarted before this turn was answered.
class _InterruptedNote extends StatelessWidget {
  const _InterruptedNote({required this.previousText, this.onResend});
  final String? previousText;
  final VoidCallback? onResend;
  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final scheme = theme.colorScheme;
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 10),
      child: Center(
        child: Container(
          padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
          decoration: BoxDecoration(
            color: scheme.surfaceContainer,
            borderRadius: BorderRadius.circular(999),
          ),
          child: Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              Icon(
                Icons.power_settings_new_rounded,
                size: 13,
                color: scheme.onSurfaceVariant,
              ),
              const SizedBox(width: 6),
              Text(
                'Fundus restarted before answering.',
                style: theme.textTheme.labelSmall,
              ),
              if (onResend != null) ...[
                const SizedBox(width: 4),
                TextButton(
                  style: TextButton.styleFrom(
                    visualDensity: VisualDensity.compact,
                    padding: const EdgeInsets.symmetric(horizontal: 8),
                  ),
                  onPressed: onResend,
                  child: const Text('Send again'),
                ),
              ],
            ],
          ),
        ),
      ),
    );
  }
}

class _ReceiptCard extends StatelessWidget {
  const _ReceiptCard({required this.receipt, required this.onOpen});
  final Receipt receipt;
  final RefTap onOpen;
  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    return Container(
      margin: const EdgeInsets.only(bottom: 6),
      padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 2),
      decoration: BoxDecoration(
        color: scheme.surfaceContainerLow,
        borderRadius: BorderRadius.circular(8),
        border: Border.all(color: scheme.outlineVariant),
      ),
      child: ReceiptTile(
        receipt: receipt,
        onOpen: onOpen,
        dense: true,
        showActor: false,
      ),
    );
  }
}

/// The tool-step trail: live while the model works, collapsed afterwards.
class _WorkingTrail extends StatefulWidget {
  const _WorkingTrail({
    required this.steps,
    required this.live,
    required this.onOpen,
  });
  final List<ChatStep> steps;
  final bool live;
  final RefTap onOpen;
  @override
  State<_WorkingTrail> createState() => _WorkingTrailState();
}

class _WorkingTrailState extends State<_WorkingTrail> {
  bool _open = false;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final scheme = theme.colorScheme;
    final calls = widget.steps
        .where((s) => s.kind == 'tool_call' || s.kind == 'error')
        .toList();
    final expanded = widget.live || _open;
    final label = widget.live
        ? (calls.isEmpty ? 'Thinking…' : calls.last.summary)
        : '${calls.length} step${calls.length == 1 ? '' : 's'}';
    return Padding(
      padding: EdgeInsets.only(left: 26, bottom: 6, top: widget.live ? 12 : 0),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          InkWell(
            onTap: widget.live ? null : () => setState(() => _open = !_open),
            borderRadius: BorderRadius.circular(6),
            child: Padding(
              padding: const EdgeInsets.symmetric(vertical: 2),
              child: Row(
                mainAxisSize: MainAxisSize.min,
                children: [
                  if (widget.live)
                    SizedBox(
                      width: 12,
                      height: 12,
                      child: CircularProgressIndicator(
                        strokeWidth: 1.5,
                        color: scheme.tertiary,
                      ),
                    )
                  else
                    Icon(
                      _open
                          ? Icons.expand_less_rounded
                          : Icons.expand_more_rounded,
                      size: 14,
                      color: scheme.onSurfaceVariant,
                    ),
                  const SizedBox(width: 6),
                  Text(label, style: theme.textTheme.labelSmall),
                ],
              ),
            ),
          ),
          if (expanded)
            for (final s in widget.steps.where((s) => s.kind != 'tool_result'))
              Padding(
                padding: const EdgeInsets.only(left: 18, top: 2),
                child: Row(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Icon(
                      switch (s.kind) {
                        'receipt' => Icons.receipt_long_outlined,
                        'error' => Icons.error_outline_rounded,
                        _ => switch (s.tool) {
                          'search' => Icons.search_rounded,
                          'get' => Icons.article_outlined,
                          'list' => Icons.list_rounded,
                          'undo' => Icons.undo_rounded,
                          _ => Icons.edit_note_rounded,
                        },
                      },
                      size: 13,
                      color: s.kind == 'error'
                          ? scheme.error
                          : scheme.onSurfaceVariant,
                    ),
                    const SizedBox(width: 6),
                    Expanded(
                      child: Text(
                        _stepLabel(context, s),
                        style: theme.textTheme.labelSmall!.copyWith(
                          color: s.kind == 'error' ? scheme.error : null,
                        ),
                        maxLines: 2,
                        overflow: TextOverflow.ellipsis,
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

/// Step summaries come from the daemon and may name objects by id; show
/// titles (resolved through the app's title cache) instead.
String _stepLabel(BuildContext context, ChatStep s) {
  final refs = context.read<AppState>().refs;
  String titled(String id, String fallback) {
    final t = refs.labelFor(id);
    if (t == null) refs.request([id]);
    return t == null || t.isEmpty ? fallback : '“$t”';
  }

  final idRe = RegExp(r'\b(note|task|topic|cap|src|conv)_[0-9A-Za-z]+\b');
  if (s.kind == 'error') return s.summary.replaceAll(idRe, 'an item');
  switch (s.tool) {
    case 'get':
      final m = idRe.firstMatch(s.summary);
      return m == null
          ? 'Reading an item'
          : 'Reading ${titled(m.group(0)!, 'an item')}';
    case 'undo':
      return 'Undoing a change';
    default:
      return s.summary.replaceAllMapped(
        idRe,
        (m) => titled(m.group(0)!, 'an item'),
      );
  }
}
