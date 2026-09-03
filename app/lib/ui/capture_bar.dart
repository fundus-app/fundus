import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:provider/provider.dart';

import '../state/app_state.dart';
import 'widgets/common.dart';

/// The omnipresent capture field plus the pills of recent captures.
class CaptureBar extends StatefulWidget {
  const CaptureBar({
    super.key,
    required this.focusNode,
    this.onOpen,
    this.autofocus = false,
  });
  final FocusNode focusNode;
  final void Function(String id)? onOpen;
  final bool autofocus;

  @override
  State<CaptureBar> createState() => _CaptureBarState();
}

class _CaptureBarState extends State<CaptureBar> {
  final _ctrl = TextEditingController();
  bool _busy = false;

  @override
  void dispose() {
    _ctrl.dispose();
    super.dispose();
  }

  Future<void> _submit() async {
    final text = _ctrl.text.trim();
    if (text.isEmpty || _busy) return;
    setState(() => _busy = true);
    final state = context.read<AppState>();
    try {
      await state.capture(text);
      _ctrl.clear();
    } catch (e) {
      if (mounted) showError(context, e);
    } finally {
      if (mounted) setState(() => _busy = false);
      widget.focusNode.requestFocus();
    }
  }

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    final state = context.watch<AppState>();
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        CallbackShortcuts(
          bindings: {
            const SingleActivator(LogicalKeyboardKey.enter): _submit,
            const SingleActivator(LogicalKeyboardKey.numpadEnter): _submit,
          },
          child: TextField(
            key: const Key('capture-field'),
            controller: _ctrl,
            focusNode: widget.focusNode,
            autofocus: widget.autofocus,
            minLines: 1,
            maxLines: 6,
            keyboardType: TextInputType.multiline,
            textInputAction: TextInputAction.newline,
            style: Theme.of(context).textTheme.bodyLarge,
            decoration: InputDecoration(
              hintText: 'Capture a thought, task, question…',
              prefixIcon: Icon(
                Icons.add_circle_outline_rounded,
                color: scheme.primary,
              ),
              suffixIcon: _busy
                  ? const Padding(
                      padding: EdgeInsets.all(12),
                      child: SizedBox(
                        width: 16,
                        height: 16,
                        child: CircularProgressIndicator(strokeWidth: 2),
                      ),
                    )
                  : Padding(
                      padding: const EdgeInsets.only(right: 8),
                      child: Row(
                        mainAxisSize: MainAxisSize.min,
                        children: [
                          const KeyHint('↵'),
                          const SizedBox(width: 6),
                          IconButton(
                            tooltip: 'Capture',
                            icon: const Icon(Icons.send_rounded, size: 18),
                            onPressed: _submit,
                          ),
                        ],
                      ),
                    ),
            ),
          ),
        ),
        for (final p in state.pending)
          _PendingPill(pending: p, onOpen: widget.onOpen),
      ],
    );
  }
}

class _PendingPill extends StatefulWidget {
  const _PendingPill({required this.pending, this.onOpen});
  final PendingCapture pending;
  final void Function(String id)? onOpen;

  @override
  State<_PendingPill> createState() => _PendingPillState();
}

class _PendingPillState extends State<_PendingPill> {
  Timer? _auto;

  @override
  void didUpdateWidget(covariant _PendingPill old) {
    super.didUpdateWidget(old);
    _armAutoDismiss();
  }

  @override
  void initState() {
    super.initState();
    _armAutoDismiss();
  }

  void _armAutoDismiss() {
    final c = widget.pending.capture;
    if (c.status == 'processed' && _auto == null) {
      _auto = Timer(const Duration(seconds: 25), () {
        if (mounted) context.read<AppState>().dismissPending(widget.pending);
      });
    }
  }

  @override
  void dispose() {
    _auto?.cancel();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final scheme = theme.colorScheme;
    final state = context.read<AppState>();
    final c = widget.pending.capture;
    final receipt = c.filingReceipt;
    String text;
    Widget? action;
    switch (c.status) {
      case 'pending':
      case 'processing':
        text = 'Filing “${_short(c.text)}”…';
      case 'processed':
        text = receipt?.summary ?? 'Filed.';
        final conf = c.result?.confidence ?? 1;
        if (receipt != null && conf > 0 && conf < 0.85) {
          text = 'Filed, ${(conf * 100).round()}% sure · $text';
        }
        if (receipt != null && !receipt.isUndone) {
          action = TextButton(
            style: TextButton.styleFrom(visualDensity: VisualDensity.compact),
            onPressed: () => undoWithConfirm(context, state, receipt.txnId),
            child: const Text('Undo'),
          );
        } else if (receipt != null && receipt.isUndone) {
          text = 'Undone: ${receipt.summary}';
        }
      case 'needs_review':
        text = c.result?.question.isNotEmpty == true
            ? 'Parked: ${c.result!.question}'
            : 'Parked in inbox for review.';
        action = TextButton(
          style: TextButton.styleFrom(visualDensity: VisualDensity.compact),
          onPressed: () {
            state.setView(AppView.inbox);
            widget.onOpen?.call(c.id);
          },
          child: const Text('Answer'),
        );
      case 'failed':
        text = c.isRetrying
            ? 'Provider unavailable, will retry automatically.'
            : 'Failed: ${c.result?.error ?? 'unknown error'}';
        action = TextButton(
          style: TextButton.styleFrom(visualDensity: VisualDensity.compact),
          onPressed: () => state.retryCapture(c),
          child: const Text('Retry'),
        );
      default:
        text = c.status;
    }
    return Padding(
      padding: const EdgeInsets.only(top: 6),
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 6),
        decoration: BoxDecoration(
          color: scheme.surfaceContainerLow,
          borderRadius: BorderRadius.circular(8),
          border: Border.all(color: scheme.outlineVariant),
        ),
        child: Row(
          children: [
            StatusDot(c.status),
            const SizedBox(width: 8),
            Expanded(
              child: InkWell(
                onTap: widget.onOpen == null
                    ? null
                    : () => widget.onOpen!(c.id),
                child: Text(
                  text,
                  style: theme.textTheme.bodySmall!.copyWith(
                    color: scheme.onSurface,
                  ),
                  maxLines: 2,
                  overflow: TextOverflow.ellipsis,
                ),
              ),
            ),
            ?action,
            IconButton(
              tooltip: 'Hide',
              visualDensity: VisualDensity.compact,
              icon: const Icon(Icons.close_rounded, size: 14),
              onPressed: () => state.dismissPending(widget.pending),
            ),
          ],
        ),
      ),
    );
  }

  String _short(String s) {
    final t = s.replaceAll(RegExp(r'\s+'), ' ');
    return t.length > 48 ? '${t.substring(0, 47)}…' : t;
  }
}

/// Accent-coloured inline hint shown under the capture bar on first run.
class CaptureHint extends StatelessWidget {
  const CaptureHint({super.key});
  @override
  Widget build(BuildContext context) {
    final t = Theme.of(context).textTheme.bodySmall!;
    return Padding(
      padding: const EdgeInsets.only(top: 6, left: 4),
      child: Row(
        children: [
          Expanded(
            child: Text(
              'Fundus files it as a note, idea or task, or answers a question.',
              style: t,
            ),
          ),
        ],
      ),
    );
  }
}
