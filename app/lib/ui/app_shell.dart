import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:provider/provider.dart';

import '../state/app_state.dart';
import '../state/settings.dart';
import 'blocks/ref_labels.dart';
import 'capture_bar.dart';
import 'conversation/conversation_screen.dart';
import 'inspector/inspector.dart';
import 'settings_screen.dart';
import 'setup/setup_wizard.dart';
import 'theme.dart';
import 'views/list_views.dart';
import 'widgets/common.dart';
import 'widgets/toasts.dart';

const _views = AppView.values;

IconData _iconFor(AppView v) => switch (v) {
  AppView.inbox => Icons.inbox_outlined,
  AppView.relevant => Icons.bolt_outlined,
  AppView.open => Icons.circle_outlined,
  AppView.ideas => Icons.lightbulb_outline_rounded,
  AppView.notes => Icons.notes_rounded,
  AppView.topics => Icons.tag_rounded,
  AppView.waiting => Icons.hourglass_empty_rounded,
  AppView.later => Icons.snooze_outlined,
  AppView.done => Icons.check_circle_outline_rounded,
  AppView.changes => Icons.history_rounded,
  AppView.conversation => Icons.forum_outlined,
};

/// Responsive shell: rail | list | inspector.
class AppShell extends StatefulWidget {
  const AppShell({
    super.key,
    this.initialView,
    this.initialOpen,
    this.openSettings = false,
  });
  final String? initialView;
  final String? initialOpen;
  final bool openSettings;
  @override
  State<AppShell> createState() => _AppShellState();
}

class _AppShellState extends State<AppShell> {
  final _captureFocus = FocusNode();
  final _captureBar = GlobalKey<CaptureBarState>();
  final _chatFocus = FocusNode();
  final _searchFocus = FocusNode();
  final _searchCtrl = TextEditingController();
  bool _searchMode = false;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) {
      final state = context.read<AppState>();
      final v = AppView.values
          .where((v) => v.name == widget.initialView)
          .firstOrNull;
      if (v != null) state.view = v;
      state.refreshView();
      final open = widget.initialOpen;
      if (open != null && open.isNotEmpty) state.select(open);
      if (widget.openSettings) {
        Future<void>.delayed(const Duration(milliseconds: 600), () {
          if (mounted) SettingsScreen.show(context);
        });
      }
      // Capture first: the field has focus the moment the app is up.
      if (state.view == AppView.conversation) {
        _chatFocus.requestFocus();
      } else {
        _captureFocus.requestFocus();
      }
    });
  }

  @override
  void dispose() {
    _captureFocus.dispose();
    _chatFocus.dispose();
    _searchFocus.dispose();
    _searchCtrl.dispose();
    super.dispose();
  }

  void _focusCapture() {
    final state = context.read<AppState>();
    setState(() => _searchMode = false);
    if (state.view == AppView.conversation) {
      _chatFocus.requestFocus();
    } else {
      _captureFocus.requestFocus();
    }
  }

  Future<void> _undoLatest() async {
    final state = context.read<AppState>();
    try {
      final r = await state.undoLatest();
      if (!mounted) return;
      if (r == null) {
        showToast(context, 'Nothing to undo.', key: 'undo-latest');
        return;
      }
      showReceiptSnack(context, r, actionLabel: 'Redo', undo: true);
    } catch (e) {
      if (mounted) showError(context, e);
    }
  }

  void _toggleSearch() {
    setState(() => _searchMode = !_searchMode);
    if (_searchMode) {
      _searchFocus.requestFocus();
    } else {
      _searchCtrl.clear();
      context.read<AppState>().search('');
    }
  }

  Future<void> _toggleDictation() async {
    final state = context.read<AppState>();
    if (!state.dictationAvailable) return;
    if (state.view == AppView.conversation) state.setView(AppView.inbox);
    setState(() => _searchMode = false);
    final d = state.dictation;
    final text = await d.toggle();
    if (!mounted) return;
    if (d.lastError != null) {
      showError(context, d.lastError!);
    } else if (text.isNotEmpty) {
      _captureBar.currentState?.insertText(text);
    }
  }

  void _escape() {
    final d = context.read<AppState>().dictation;
    if (d.isRecording) {
      d.stop().then((text) {
        if (mounted && text.isNotEmpty) {
          _captureBar.currentState?.insertText(text);
        }
      });
      return;
    }
    if (_searchMode) {
      _toggleSearch();
      return;
    }
    context.read<AppState>().clearSelection();
  }

  Future<void> _open(String id) async {
    final state = context.read<AppState>();
    final width = MediaQuery.sizeOf(context).width;
    await state.select(id);
    if (!mounted) return;
    if (width < 1000) {
      _showInspectorSheet(context);
    }
  }

  void _showInspectorSheet(BuildContext context) {
    final state = context.read<AppState>();
    showModalBottomSheet<void>(
      context: context,
      isScrollControlled: true,
      useSafeArea: true,
      showDragHandle: true,
      backgroundColor: Theme.of(context).colorScheme.surface,
      builder: (ctx) => ChangeNotifierProvider.value(
        value: state,
        child: FractionallySizedBox(
          heightFactor: 0.92,
          child: Inspector(
            onOpen: (id) => state.select(id),
            onClose: () => Navigator.of(ctx).pop(),
          ),
        ),
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    final state = context.watch<AppState>();
    final width = MediaQuery.sizeOf(context).width;
    final wide = width >= 1000;
    final narrow = width < 600;

    final bindings = <ShortcutActivator, VoidCallback>{
      const SingleActivator(LogicalKeyboardKey.keyK, control: true):
          _focusCapture,
      const SingleActivator(LogicalKeyboardKey.keyK, meta: true): _focusCapture,
      const SingleActivator(LogicalKeyboardKey.keyN, control: true):
          _focusCapture,
      const SingleActivator(LogicalKeyboardKey.keyN, meta: true): _focusCapture,
      const SingleActivator(
        LogicalKeyboardKey.keyK,
        control: true,
        shift: true,
      ): _toggleDictation,
      const SingleActivator(LogicalKeyboardKey.keyK, meta: true, shift: true):
          _toggleDictation,
      const SingleActivator(LogicalKeyboardKey.keyF, control: true):
          _toggleSearch,
      const SingleActivator(LogicalKeyboardKey.keyF, meta: true): _toggleSearch,
      const SingleActivator(LogicalKeyboardKey.escape): _escape,
      const SingleActivator(LogicalKeyboardKey.keyZ, control: true):
          _undoLatest,
      const SingleActivator(LogicalKeyboardKey.keyZ, meta: true): _undoLatest,
      const SingleActivator(LogicalKeyboardKey.digit0, control: true): () =>
          state.setView(AppView.conversation),
      const SingleActivator(LogicalKeyboardKey.keyJ, control: true): () =>
          state.setView(AppView.conversation),
      const SingleActivator(LogicalKeyboardKey.comma, control: true): () =>
          SettingsScreen.show(context),
      for (var i = 0; i < 9 && i < _views.length; i++)
        SingleActivator(
          LogicalKeyboardKey(LogicalKeyboardKey.digit1.keyId + i),
          control: true,
        ): () =>
            state.setView(_views[i]),
    };

    final listPane = _ListPane(
      captureFocus: _captureFocus,
      captureBarKey: _captureBar,
      chatFocus: _chatFocus,
      searchFocus: _searchFocus,
      searchCtrl: _searchCtrl,
      searchMode: _searchMode,
      onToggleSearch: _toggleSearch,
      onOpen: _open,
    );

    if (state.starting) {
      return const _StartingView();
    }
    if (state.startError != null && !state.reachable) {
      return const _StartFailedView();
    }
    if (state.setupNeeded) {
      return SetupWizard(onSkip: state.skipSetup, onDone: () {});
    }
    Widget body;
    if (narrow) {
      body = Scaffold(
        body: SafeArea(
          child: Column(
            children: [
              const _OfflineBanner(),
              Expanded(child: listPane),
            ],
          ),
        ),
        bottomNavigationBar: NavigationBar(
          selectedIndex: _narrowIndex(state.view),
          onDestinationSelected: (i) {
            if (i < 4) {
              state.setView(
                const [
                  AppView.inbox,
                  AppView.relevant,
                  AppView.notes,
                  AppView.conversation,
                ][i],
              );
            } else {
              _showMoreMenu(context, state);
            }
          },
          destinations: [
            for (final v in const [
              AppView.inbox,
              AppView.relevant,
              AppView.notes,
              AppView.conversation,
            ])
              NavigationDestination(icon: Icon(_iconFor(v)), label: v.label),
            const NavigationDestination(
              icon: Icon(Icons.more_horiz_rounded),
              label: 'More',
            ),
          ],
        ),
      );
    } else {
      body = Scaffold(
        body: Column(
          children: [
            const _OfflineBanner(),
            Expanded(
              child: Row(
                children: [
                  _Rail(
                    view: state.view,
                    onSelect: state.setView,
                    inboxCount: state.inboxAttention,
                  ),
                  const VerticalDivider(),
                  Expanded(
                    flex: 5,
                    child: ConstrainedBox(
                      constraints: const BoxConstraints(minWidth: 360),
                      child: listPane,
                    ),
                  ),
                  if (wide) ...[
                    const VerticalDivider(),
                    Expanded(
                      flex: 6,
                      child: Container(
                        color: Theme.of(context)
                            .colorScheme
                            .surfaceContainerLowest,
                        child: Inspector(
                          onOpen: _open,
                          onClose: state.clearSelection,
                        ),
                      ),
                    ),
                  ],
                ],
              ),
            ),
          ],
        ),
      );
    }

    // Toasts sit bottom-right of the content area; on phones bottom-centre
    // above the navigation bar.
    final toasts = toastControllerOf(context);
    if (toasts != null) {
      final inset = narrow ? 64 + MediaQuery.paddingOf(context).bottom : 0.0;
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (mounted) toasts.place(bottomInset: inset, centered: narrow);
      });
    }
    return RefLabels(
      source: state.refs,
      child: CallbackShortcuts(
        bindings: bindings,
        child: Focus(autofocus: true, child: body),
      ),
    );
  }

  int _narrowIndex(AppView v) => switch (v) {
    AppView.inbox => 0,
    AppView.relevant => 1,
    AppView.notes => 2,
    AppView.conversation => 3,
    _ => 4,
  };

  void _showMoreMenu(BuildContext context, AppState state) {
    showModalBottomSheet<void>(
      context: context,
      showDragHandle: true,
      builder: (ctx) => SafeArea(
        child: Wrap(
          children: [
            for (final v in _views)
              ListTile(
                leading: Icon(_iconFor(v)),
                title: Text(v.label),
                subtitle: Text(
                  v.hint,
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                ),
                selected: v == state.view,
                onTap: () {
                  Navigator.pop(ctx);
                  state.setView(v);
                },
              ),
            ListTile(
              leading: const Icon(Icons.settings_outlined),
              title: const Text('Settings'),
              onTap: () {
                Navigator.pop(ctx);
                SettingsScreen.show(context);
              },
            ),
          ],
        ),
      ),
    );
  }
}

class _Rail extends StatelessWidget {
  const _Rail({
    required this.view,
    required this.onSelect,
    required this.inboxCount,
  });
  final AppView view;
  final void Function(AppView) onSelect;
  final int inboxCount;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return LayoutBuilder(
      builder: (context, constraints) {
        final short = constraints.maxHeight < 700;
        return SingleChildScrollView(
          child: ConstrainedBox(
            constraints: BoxConstraints(minHeight: constraints.maxHeight),
            child: IntrinsicHeight(child: _rail(context, theme, short)),
          ),
        );
      },
    );
  }

  Widget _rail(BuildContext context, ThemeData theme, bool short) {
    return NavigationRail(
      selectedIndex: _views.indexOf(view),
      onDestinationSelected: (i) => onSelect(_views[i]),
      minWidth: 72,
      groupAlignment: -0.9,
      labelType: short
          ? NavigationRailLabelType.selected
          : NavigationRailLabelType.all,
      leading: Padding(
        padding: const EdgeInsets.only(top: 14, bottom: 8),
        child: const Tooltip(message: 'Fundus', child: FundusMark(size: 28)),
      ),
      trailing: Expanded(
        child: Align(
          alignment: Alignment.bottomCenter,
          child: Padding(
            padding: const EdgeInsets.only(bottom: 12),
            child: IconButton(
              tooltip: 'Settings (Ctrl ,)',
              icon: const Icon(Icons.settings_outlined),
              onPressed: () => SettingsScreen.show(context),
            ),
          ),
        ),
      ),
      destinations: [
        for (final v in _views)
          NavigationRailDestination(
            icon: v == AppView.inbox && inboxCount > 0
                ? Badge(label: Text('$inboxCount'), child: Icon(_iconFor(v)))
                : Icon(_iconFor(v)),
            label: Text(v.label),
          ),
      ],
    );
  }
}

/// Shown while the app starts the local daemon.
class _StartingView extends StatelessWidget {
  const _StartingView();
  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Scaffold(
      body: Center(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            const SizedBox(
              width: 22,
              height: 22,
              child: CircularProgressIndicator(strokeWidth: 2),
            ),
            const SizedBox(height: 16),
            Text('Starting Fundus…', style: theme.textTheme.headlineSmall),
            const SizedBox(height: 6),
            Text(
              'Launching Fundus on this machine and waiting for it to answer.',
              style: theme.textTheme.bodySmall,
            ),
          ],
        ),
      ),
    );
  }
}

/// Autostart failed: the error, the command to run, and a retry.
class _StartFailedView extends StatelessWidget {
  const _StartFailedView();
  @override
  Widget build(BuildContext context) {
    final state = context.watch<AppState>();
    final theme = Theme.of(context);
    final scheme = theme.colorScheme;
    return Scaffold(
      body: Center(
        child: ConstrainedBox(
          constraints: const BoxConstraints(maxWidth: 520),
          child: Padding(
            padding: const EdgeInsets.all(32),
            child: Column(
              mainAxisSize: MainAxisSize.min,
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Icon(Icons.power_off_rounded, size: 28, color: scheme.error),
                const SizedBox(height: 12),
                Text(
                  'Fundus could not be started',
                  style: theme.textTheme.headlineSmall,
                ),
                const SizedBox(height: 8),
                Text(
                  describeError(state.startError!),
                  style: theme.textTheme.bodyMedium,
                ),
                const SizedBox(height: 12),
                Text(
                  'Start it yourself in a terminal:',
                  style: theme.textTheme.bodySmall,
                ),
                const SizedBox(height: 4),
                SelectableText(
                  'fundus serve',
                  style: monoStyle(context, size: 13, color: scheme.onSurface),
                ),
                if (state.daemonLogPath != null) ...[
                  const SizedBox(height: 10),
                  Text('See the log at', style: theme.textTheme.bodySmall),
                  SelectableText(
                    state.daemonLogPath!,
                    style: monoStyle(
                      context,
                      size: 12,
                      color: scheme.onSurface,
                    ),
                  ),
                ],
                const SizedBox(height: 18),
                Row(
                  children: [
                    FilledButton(
                      onPressed: state.retryAutostart,
                      child: const Text('Start again'),
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
        ),
      ),
    );
  }
}

class _OfflineBanner extends StatelessWidget {
  const _OfflineBanner();
  @override
  Widget build(BuildContext context) {
    final state = context.watch<AppState>();
    if (state.reachable) return const SizedBox.shrink();
    final scheme = Theme.of(context).colorScheme;
    return Material(
      color: scheme.errorContainer,
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
        child: Row(
          children: [
            Icon(
              Icons.cloud_off_rounded,
              size: 16,
              color: scheme.onErrorContainer,
            ),
            const SizedBox(width: 8),
            Expanded(
              child: Text(
                describeError(
                  state.lastError ?? 'unreachable',
                  serverUrl: context.read<Settings?>()?.serverUrl,
                ),
                style: Theme.of(context).textTheme.bodySmall!
                    .copyWith(color: scheme.onErrorContainer),
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
              ),
            ),
            TextButton(onPressed: state.refreshAll, child: const Text('Retry')),
            TextButton(
              onPressed: () => SettingsScreen.show(context),
              child: const Text('Settings'),
            ),
          ],
        ),
      ),
    );
  }
}

/// Daemon warnings (e.g. missing API key), dismissible per session.
class _WarningsBanner extends StatelessWidget {
  const _WarningsBanner();
  @override
  Widget build(BuildContext context) {
    final state = context.watch<AppState>();
    final warnings = state.warnings;
    if (warnings.isEmpty && !state.instanceChanged) {
      return const SizedBox.shrink();
    }
    final scheme = Theme.of(context).colorScheme;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        if (state.instanceChanged)
          Padding(
            padding: const EdgeInsets.only(top: 8),
            child: Row(
              children: [
                Icon(
                  Icons.swap_horiz_rounded,
                  size: 14,
                  color: scheme.onSurfaceVariant,
                ),
                const SizedBox(width: 6),
                Text(
                  'This is a different Fundus than before.',
                  style: Theme.of(context).textTheme.labelSmall,
                ),
              ],
            ),
          ),
        for (final w in warnings)
          Container(
            margin: const EdgeInsets.only(top: 8),
            padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 6),
            decoration: BoxDecoration(
              color: scheme.warning.withValues(alpha: 0.12),
              borderRadius: BorderRadius.circular(8),
              border: Border.all(color: scheme.warning.withValues(alpha: 0.4)),
            ),
            child: Row(
              children: [
                Icon(
                  Icons.warning_amber_rounded,
                  size: 15,
                  color: scheme.warning,
                ),
                const SizedBox(width: 8),
                Expanded(
                  child: Text(
                    w,
                    style: Theme.of(context).textTheme.bodySmall!
                        .copyWith(color: scheme.onSurface),
                  ),
                ),
                IconButton(
                  tooltip: 'Dismiss warning',
                  visualDensity: VisualDensity.compact,
                  icon: const Icon(Icons.close_rounded, size: 14),
                  onPressed: () => state.dismissWarning(w),
                ),
              ],
            ),
          ),
      ],
    );
  }
}

class _ListPane extends StatelessWidget {
  const _ListPane({
    required this.captureFocus,
    required this.captureBarKey,
    required this.chatFocus,
    required this.searchFocus,
    required this.searchCtrl,
    required this.searchMode,
    required this.onToggleSearch,
    required this.onOpen,
  });
  final FocusNode captureFocus;
  final GlobalKey<CaptureBarState> captureBarKey;
  final FocusNode chatFocus;
  final FocusNode searchFocus;
  final TextEditingController searchCtrl;
  final bool searchMode;
  final VoidCallback onToggleSearch;
  final void Function(String id) onOpen;

  @override
  Widget build(BuildContext context) {
    final state = context.watch<AppState>();
    final view = state.view;
    if (view == AppView.conversation) {
      return Padding(
        padding: const EdgeInsets.symmetric(horizontal: 16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            const _WarningsBanner(),
            Expanded(
              child: ConversationScreen(onOpen: onOpen, focusNode: chatFocus),
            ),
          ],
        ),
      );
    }
    final searchButton = IconButton(
      tooltip: searchMode ? 'Close search (Esc)' : 'Search (Ctrl F)',
      icon: Icon(searchMode ? Icons.close_rounded : Icons.search_rounded),
      onPressed: onToggleSearch,
    );
    Widget list;
    final err = state.viewError;
    if (!searchMode && err != null && !state.loading) {
      list = ErrorState(error: err, onRetry: state.refreshView);
    } else if (searchMode) {
      list = SearchResults(
        hits: state.searchHits,
        onOpen: onOpen,
        query: searchCtrl.text,
      );
    } else {
      list = switch (view) {
        AppView.inbox => InboxList(captures: state.inbox, onOpen: onOpen),
        AppView.relevant => TaskList(
          tasks: state.tasks,
          onOpen: onOpen,
          showReasons: true,
          emptyTitle: 'Nothing pressing',
          emptyHint:
              'Open tasks with due dates, mentions or importance show up here.',
        ),
        AppView.open => TaskList(
          tasks: state.tasks,
          onOpen: onOpen,
          emptyTitle: 'No open tasks',
        ),
        AppView.waiting => TaskList(
          tasks: state.tasks,
          onOpen: onOpen,
          emptyTitle: 'Nothing waiting',
          emptyHint: 'Tasks blocked on someone or something land here.',
        ),
        AppView.later => TaskList(
          tasks: state.tasks,
          onOpen: onOpen,
          emptyTitle: 'Nothing deferred',
          emptyHint:
              'Push a task to “later” and it rests here without nagging.',
        ),
        AppView.done => DoneList(tasks: state.tasks, onOpen: onOpen),
        AppView.ideas => NoteList(
          notes: state.notes,
          onOpen: onOpen,
          kind: 'idea',
        ),
        AppView.notes => NoteList(
          notes: state.notes,
          onOpen: onOpen,
          kind: 'note',
        ),
        AppView.topics => TopicList(topics: state.topics, onOpen: onOpen),
        AppView.changes => ChangesList(receipts: state.changes, onOpen: onOpen),
        AppView.conversation => const SizedBox.shrink(),
      };
    }
    final count = switch (view) {
      AppView.inbox => state.inbox.length,
      AppView.relevant ||
      AppView.open ||
      AppView.waiting ||
      AppView.later ||
      AppView.done => state.tasks.length,
      AppView.ideas || AppView.notes => state.notes.length,
      AppView.topics => state.topics.length,
      AppView.changes => state.changes.length,
      _ => null,
    };
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 16),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          const _WarningsBanner(),
          const SizedBox(height: 14),
          if (searchMode)
            TextField(
              controller: searchCtrl,
              focusNode: searchFocus,
              decoration: InputDecoration(
                hintText: 'Search notes, tasks, topics…',
                prefixIcon: const Icon(Icons.search_rounded),
                suffixIcon: state.searching
                    ? const Padding(
                        padding: EdgeInsets.all(12),
                        child: SizedBox(
                          width: 16,
                          height: 16,
                          child: CircularProgressIndicator(strokeWidth: 2),
                        ),
                      )
                    : null,
              ),
              onChanged: (q) => state.search(q),
            )
          else
            CaptureBar(
              key: captureBarKey,
              focusNode: captureFocus,
              onOpen: onOpen,
            ),
          if (!searchMode && (state.stats?.captures ?? 1) == 0)
            const CaptureHint(),
          ViewHeader(view: view, count: count, trailing: searchButton),
          if (state.loading)
            const LinearProgressIndicator(minHeight: 2)
          else
            const SizedBox(height: 2),
          Expanded(child: list),
        ],
      ),
    );
  }
}
