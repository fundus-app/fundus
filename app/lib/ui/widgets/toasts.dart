// One toast system for the whole app: a small stack of cards in the bottom
// corner of the content area. Informational toasts go after 4 s, ones with
// an action after 8 s; hovering pauses, Esc or × closes, a swipe down
// dismisses on touch. At most three are visible; a new toast with the same
// key replaces the old one.
import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:provider/provider.dart';

enum ToastKind { info, action, error }

class Toast {
  Toast({
    required this.id,
    required this.text,
    required this.kind,
    required this.duration,
    this.key,
    this.actionLabel,
    this.onAction,
    this.settledText = 'Undone.',
  });
  final int id;
  String text;
  ToastKind kind;
  Duration duration;

  /// Same key → the newer toast replaces the older one.
  final String? key;
  String? actionLabel;

  /// Runs the action; true when it went through (the toast then settles).
  Future<bool> Function()? onAction;
  final String settledText;
  bool busy = false;
}

class ToastController extends ChangeNotifier {
  static const maxVisible = 3;
  static const infoDuration = Duration(seconds: 4);
  static const actionDuration = Duration(seconds: 8);
  static const settledDuration = Duration(seconds: 2);

  final List<Toast> toasts = [];
  final _timers = <int, Timer>{};
  final _remaining = <int, Duration>{};
  final _startedAt = <int, DateTime>{};
  int _seq = 0;

  /// Where the stack sits: above the phone navigation bar, centred.
  double bottomInset = 0;
  bool centered = false;

  void place({required double bottomInset, required bool centered}) {
    if (bottomInset == this.bottomInset && centered == this.centered) return;
    this.bottomInset = bottomInset;
    this.centered = centered;
    notifyListeners();
  }

  int show(
    String text, {
    String? key,
    ToastKind kind = ToastKind.info,
    String? actionLabel,
    Future<bool> Function()? onAction,
    Duration? duration,
    String settledText = 'Undone.',
  }) {
    if (key != null) {
      for (final t in toasts.where((t) => t.key == key).toList()) {
        _cancel(t.id);
        toasts.remove(t);
      }
    }
    final k = kind == ToastKind.error
        ? ToastKind.error
        : (onAction != null ? ToastKind.action : ToastKind.info);
    final t = Toast(
      id: ++_seq,
      text: text,
      kind: k,
      key: key,
      actionLabel: actionLabel ?? (onAction == null ? null : 'Undo'),
      onAction: onAction,
      settledText: settledText,
      duration:
          duration ?? (k == ToastKind.info ? infoDuration : actionDuration),
    );
    toasts.add(t);
    while (toasts.length > maxVisible) {
      _cancel(toasts.first.id);
      toasts.removeAt(0);
    }
    _start(t.id, t.duration);
    notifyListeners();
    return t.id;
  }

  Toast? _find(int id) => toasts.where((t) => t.id == id).firstOrNull;

  void dismiss(int id) {
    final t = _find(id);
    if (t == null) return;
    _cancel(id);
    toasts.remove(t);
    notifyListeners();
  }

  /// Esc: closes the newest toast. False when there was none.
  bool dismissNewest() {
    if (toasts.isEmpty) return false;
    dismiss(toasts.last.id);
    return true;
  }

  void pause(int id) {
    final timer = _timers.remove(id);
    if (timer == null) return;
    timer.cancel();
    final started = _startedAt[id];
    final left =
        (_remaining[id] ?? Duration.zero) -
        (started == null ? Duration.zero : DateTime.now().difference(started));
    _remaining[id] = left > const Duration(milliseconds: 500)
        ? left
        : const Duration(milliseconds: 500);
  }

  void resume(int id) {
    if (_timers.containsKey(id) || _find(id) == null) return;
    final t = _find(id)!;
    if (t.busy) return;
    _start(id, _remaining[id] ?? t.duration);
  }

  /// The action went through: show a short confirmation instead.
  void settle(int id, [String? text]) {
    final t = _find(id);
    if (t == null) return;
    t.text = text ?? t.settledText;
    t.kind = ToastKind.info;
    t.onAction = null;
    t.actionLabel = null;
    t.busy = false;
    _cancel(id);
    _start(id, settledDuration);
    notifyListeners();
  }

  Future<void> runAction(int id) async {
    final t = _find(id);
    final action = t?.onAction;
    if (t == null || action == null || t.busy) return;
    t.busy = true;
    pause(id);
    notifyListeners();
    var ok = false;
    try {
      ok = await action();
    } catch (_) {
      ok = false;
    }
    if (_find(id) == null) return;
    if (ok) {
      settle(id);
    } else {
      t.busy = false;
      resume(id);
      notifyListeners();
    }
  }

  void _start(int id, Duration d) {
    _remaining[id] = d;
    _startedAt[id] = DateTime.now();
    _timers[id] = Timer(d, () => dismiss(id));
  }

  void _cancel(int id) {
    _timers.remove(id)?.cancel();
    _remaining.remove(id);
    _startedAt.remove(id);
  }

  @override
  void dispose() {
    for (final t in _timers.values) {
      t.cancel();
    }
    _timers.clear();
    super.dispose();
  }
}

/// Shows a toast from any context below a [ToastScope]. Returns its id, or
/// null when there is no scope (plain widget tests).
int? showToast(
  BuildContext context,
  String text, {
  String? key,
  bool error = false,
  String? actionLabel,
  Future<bool> Function()? onAction,
  Duration? duration,
  String settledText = 'Undone.',
}) {
  final c = toastControllerOf(context);
  if (c == null) return null;
  return c.show(
    text,
    key: key,
    kind: error ? ToastKind.error : ToastKind.info,
    actionLabel: actionLabel,
    onAction: onAction,
    duration: duration,
    settledText: settledText,
  );
}

ToastController? toastControllerOf(BuildContext context) {
  try {
    return Provider.of<ToastController>(context, listen: false);
  } on ProviderNotFoundException {
    return null;
  }
}

/// For `MaterialApp.builder`: hosts the toast layer above every route.
Widget toastBuilder(BuildContext context, Widget? child) =>
    ToastScope(child: child ?? const SizedBox.shrink());

class ToastScope extends StatefulWidget {
  const ToastScope({super.key, required this.child, this.controller});
  final Widget child;

  /// Supply one to observe it from tests; otherwise the scope owns one.
  final ToastController? controller;
  @override
  State<ToastScope> createState() => _ToastScopeState();
}

class _ToastScopeState extends State<ToastScope> {
  late final ToastController _own = ToastController();
  ToastController get _c => widget.controller ?? _own;

  @override
  void initState() {
    super.initState();
    HardwareKeyboard.instance.addHandler(_onKey);
  }

  @override
  void dispose() {
    HardwareKeyboard.instance.removeHandler(_onKey);
    _own.dispose();
    super.dispose();
  }

  bool _onKey(KeyEvent e) {
    if (e is KeyDownEvent &&
        e.logicalKey == LogicalKeyboardKey.escape &&
        _c.toasts.isNotEmpty) {
      _c.dismissNewest();
      return true;
    }
    return false;
  }

  // The layer lives above the Navigator, so it brings its own Overlay for
  // tooltips and its own Material for buttons.
  @override
  Widget build(BuildContext context) =>
      ChangeNotifierProvider<ToastController>.value(
        value: _c,
        child: Stack(
          children: [
            widget.child,
            Positioned.fill(
              child: Overlay.wrap(
                child: const Material(
                  type: MaterialType.transparency,
                  child: _ToastLayer(),
                ),
              ),
            ),
          ],
        ),
      );
}

class _ToastLayer extends StatelessWidget {
  const _ToastLayer();
  @override
  Widget build(BuildContext context) {
    final c = context.watch<ToastController>();
    if (c.toasts.isEmpty) return const SizedBox.shrink();
    final column = Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: c.centered
          ? CrossAxisAlignment.center
          : CrossAxisAlignment.end,
      children: [
        for (final t in c.toasts) ...[
          if (t != c.toasts.first) const SizedBox(height: 8),
          _Appear(
            key: ValueKey(t.id),
            child: ToastCard(toast: t, controller: c),
          ),
        ],
      ],
    );
    return Padding(
      padding: EdgeInsets.fromLTRB(16, 0, 16, 16 + c.bottomInset),
      child: Align(
        alignment: c.centered ? Alignment.bottomCenter : Alignment.bottomRight,
        child: column,
      ),
    );
  }
}

/// Fades and slides a new toast in.
class _Appear extends StatelessWidget {
  const _Appear({super.key, required this.child});
  final Widget child;
  @override
  Widget build(BuildContext context) => TweenAnimationBuilder<double>(
    tween: Tween(begin: 0, end: 1),
    duration: const Duration(milliseconds: 160),
    curve: Curves.easeOut,
    builder: (_, v, child) => Opacity(
      opacity: v,
      child: Transform.translate(offset: Offset(0, (1 - v) * 8), child: child),
    ),
    child: child,
  );
}

class ToastCard extends StatelessWidget {
  const ToastCard({super.key, required this.toast, required this.controller});
  final Toast toast;
  final ToastController controller;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final scheme = theme.colorScheme;
    final t = toast;
    final textStyle = theme.textTheme.bodyMedium!.copyWith(
      fontSize: 13,
      height: 1.3,
    );
    return MouseRegion(
      onEnter: (_) => controller.pause(t.id),
      onExit: (_) => controller.resume(t.id),
      child: Dismissible(
        key: ValueKey('toast-swipe-${t.id}'),
        direction: DismissDirection.down,
        onDismissed: (_) => controller.dismiss(t.id),
        child: Semantics(
          liveRegion: true,
          child: Material(
            key: Key('toast-${t.id}'),
            color: scheme.surface,
            elevation: 3,
            shadowColor: Colors.black.withValues(alpha: 0.5),
            shape: RoundedRectangleBorder(
              borderRadius: BorderRadius.circular(8),
              side: BorderSide(color: scheme.outlineVariant),
            ),
            clipBehavior: Clip.antiAlias,
            child: ConstrainedBox(
              constraints: const BoxConstraints(maxWidth: 420, minHeight: 40),
              child: ClipRRect(
                borderRadius: BorderRadius.circular(7),
                child: Row(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    if (t.kind == ToastKind.error)
                      Container(width: 3, height: 40, color: scheme.error),
                    Flexible(
                      child: Padding(
                        padding: EdgeInsets.fromLTRB(
                          t.kind == ToastKind.error ? 10 : 12,
                          4,
                          2,
                          4,
                        ),
                        child: Row(
                          mainAxisSize: MainAxisSize.min,
                          children: [
                            Flexible(
                              child: Tooltip(
                                message: t.text,
                                waitDuration: const Duration(milliseconds: 600),
                                child: Text(
                                  t.text,
                                  style: textStyle,
                                  maxLines: 1,
                                  overflow: TextOverflow.ellipsis,
                                ),
                              ),
                            ),
                            if (t.onAction != null) ...[
                              const SizedBox(width: 6),
                              TextButton(
                                key: Key('toast-action-${t.id}'),
                                style: TextButton.styleFrom(
                                  visualDensity: VisualDensity.compact,
                                  padding: const EdgeInsets.symmetric(
                                    horizontal: 8,
                                  ),
                                  minimumSize: const Size(0, 30),
                                  textStyle: textStyle.copyWith(
                                    fontWeight: FontWeight.w600,
                                  ),
                                ),
                                onPressed: t.busy
                                    ? null
                                    : () => controller.runAction(t.id),
                                child: Text(t.actionLabel ?? 'Undo'),
                              ),
                            ],
                            IconButton(
                              key: Key('toast-close-${t.id}'),
                              tooltip: 'Close (Esc)',
                              visualDensity: VisualDensity.compact,
                              constraints: const BoxConstraints(
                                minWidth: 30,
                                minHeight: 30,
                              ),
                              padding: EdgeInsets.zero,
                              iconSize: 15,
                              icon: const Icon(Icons.close_rounded),
                              onPressed: () => controller.dismiss(t.id),
                            ),
                          ],
                        ),
                      ),
                    ),
                  ],
                ),
              ),
            ),
          ),
        ),
      ),
    );
  }
}
