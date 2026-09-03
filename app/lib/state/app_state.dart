import 'dart:async';

import 'package:flutter/foundation.dart';

import '../api/client.dart';
import '../api/models.dart';
import 'ref_resolver.dart';

/// The navigable views.
enum AppView {
  inbox,
  relevant,
  open,
  ideas,
  notes,
  topics,
  waiting,
  later,
  changes,
  conversation,
}

extension AppViewInfo on AppView {
  String get label => switch (this) {
    AppView.inbox => 'Inbox',
    AppView.relevant => 'Relevant',
    AppView.open => 'Open',
    AppView.ideas => 'Ideas',
    AppView.notes => 'Notes',
    AppView.topics => 'Topics',
    AppView.waiting => 'Waiting',
    AppView.later => 'Later',
    AppView.changes => 'Changes',
    AppView.conversation => 'Conversation',
  };

  String get hint => switch (this) {
    AppView.inbox => 'Captures that still need attention: being processed, unclear, or failed.',
    AppView.relevant => 'Open tasks ranked by evidence: due dates, mentions, importance, active topics.',
    AppView.open => 'Every open task. Nothing here needs a review ritual.',
    AppView.ideas =>
      'Loose thoughts without a project. They may sit here for years.',
    AppView.notes =>
      'Information worth keeping, each traceable to its captures.',
    AppView.topics => 'Hubs that notes and tasks link to. Fundus creates them when a subject recurs.',
    AppView.waiting => 'Tasks blocked on something external.',
    AppView.later => 'Deliberately deferred. No reminders, no guilt.',
    AppView.changes => 'Every change, by you or the model, with undo.',
    AppView.conversation => 'Ask, recall, and file in natural language.',
  };
}

/// A capture the user just submitted, tracked until its receipt arrives.
class PendingCapture {
  PendingCapture(this.capture, {this.error});
  Capture capture;
  String? error;
  DateTime shownAt = DateTime.now();
}

/// The selected object was removed by an undo.
class RemovedNotice {
  const RemovedNotice(this.id, this.receipt);
  final String id;
  final Receipt? receipt;
}

/// Central application state: connection, current view, lists, selection.
/// Optional hook that starts the local daemon (desktop only).
typedef DaemonStarter = Future<String> Function();

/// Persists the first-seen instance id per server URL.
abstract class InstanceStore {
  String instanceFor(String url);
  Future<void> setInstance(String url, String id);
}

class AppState extends ChangeNotifier {
  AppState(
    this.api, {
    this.daemonStarter,
    this.instanceStore,
    this.daemonLogPath,
  }) : refs = RefResolver(api) {
    _sub = api.events().listen(_onEvent, onError: (_) {});
    _countAttention();
    if (api is HttpFundusApi) {
      _connSub = (api as HttpFundusApi).connected.stream.listen((c) {
        if (c != _sseConnected) {
          _sseConnected = c;
          if (c) _resume();
          notifyListeners();
        }
      });
    }
    checkHealth();
  }

  final FundusApi api;
  final RefResolver refs;
  final DaemonStarter? daemonStarter;

  /// Remembers the instance id per server (shared preferences); null in tests.
  final InstanceStore? instanceStore;

  /// Where an autostarted Fundus writes its log (desktop only).
  final String? daemonLogPath;

  /// True when the server answers with a different instance id than the one
  /// first seen at this address.
  bool instanceChanged = false;

  /// The app is starting the local daemon and waiting for it.
  bool starting = false;

  /// Set when autostart failed (the error state shows the command to run).
  Object? startError;
  bool _autostartTried = false;

  /// The user skipped the setup wizard for this session.
  bool setupSkipped = false;
  bool get setupNeeded => (health?.setupNeeded ?? false) && !setupSkipped;
  StreamSubscription<ServerEvent>? _sub;

  /// Seq of the last transaction seen on the event stream.
  int lastSeq = 0;

  /// Daemon warnings the user has not dismissed.
  List<String> get warnings => (health?.warnings ?? const [])
      .where((w) => !_dismissedWarnings.contains(w))
      .toList();
  final Set<String> _dismissedWarnings = {};
  StreamSubscription<bool>? _connSub;

  Health? health;
  Stats? stats;
  bool _sseConnected = false;
  bool reachable = false;
  Object? lastError;
  bool get connected => reachable && (_sseConnected || api is! HttpFundusApi);

  AppView view = AppView.inbox;
  bool loading = false;

  /// The error of the last view refresh, if it failed.
  Object? viewError;

  /// Captures that need a decision (needs_review + failed), for the badge.
  int inboxAttention = 0;

  /// Set when the selected object was removed by an undo.
  RemovedNotice? removedNotice;

  /// Most recent receipt per touched object id (from the event stream).
  final Map<String, Receipt> lastReceiptFor = {};

  List<Capture> inbox = [];
  List<Task> tasks = [];
  List<Note> notes = [];
  List<Topic> topics = [];
  List<Receipt> changes = [];

  String? selectedId;
  ObjectDetail? detail;
  TopicPage? topicPage;
  bool detailLoading = false;

  final List<PendingCapture> pending = [];

  String searchQuery = '';
  List<SearchHit> searchHits = [];
  bool searching = false;

  Timer? _refreshDebounce;
  Timer? _healthTimer;

  @override
  void dispose() {
    _sub?.cancel();
    _connSub?.cancel();
    _refreshDebounce?.cancel();
    _healthTimer?.cancel();
    refs.dispose();
    if (api is HttpFundusApi) (api as HttpFundusApi).close();
    super.dispose();
  }

  void dismissWarning(String w) {
    _dismissedWarnings.add(w);
    notifyListeners();
  }

  /// After a reconnect: catch up on transactions missed while offline.
  Future<void> _resume() async {
    if (lastSeq > 0) {
      try {
        final missed = await api.changesAfter(lastSeq);
        for (final r in missed) {
          if (r.seq > lastSeq) lastSeq = r.seq;
          for (final id in r.touched) {
            refs.invalidate(id);
          }
        }
      } catch (_) {}
    }
    await refreshAll();
  }

  // ---------------------------------------------------------------------
  // Connection

  Future<void> checkHealth() async {
    try {
      health = await api.health();
      _checkInstance();
      reachable = true;
      lastError = null;
      stats = await api.stats();
    } catch (e) {
      reachable = false;
      lastError = e;
      if (!_autostartTried && daemonStarter != null) {
        _autostartTried = true;
        notifyListeners();
        await _autostart();
        return;
      }
    }
    notifyListeners();
    _healthTimer ??= Timer.periodic(
      const Duration(seconds: 20),
      (_) => _pollHealth(),
    );
  }

  /// One-command start: launch `fundus serve` and wait up to 5 s for it.
  Future<void> _autostart() async {
    starting = true;
    startError = null;
    notifyListeners();
    try {
      await daemonStarter!();
      final deadline = DateTime.now().add(const Duration(seconds: 5));
      while (DateTime.now().isBefore(deadline)) {
        await Future<void>.delayed(const Duration(milliseconds: 400));
        try {
          health = await api.health();
          _checkInstance();
          reachable = true;
          lastError = null;
          break;
        } catch (_) {}
      }
      if (!reachable) {
        startError = 'Fundus was started but did not answer within 5 seconds.';
      }
    } catch (e) {
      startError = e;
    }
    starting = false;
    notifyListeners();
    if (reachable) {
      try {
        stats = await api.stats();
      } catch (_) {}
      await refreshView();
    }
    _healthTimer ??= Timer.periodic(
      const Duration(seconds: 20),
      (_) => _pollHealth(),
    );
  }

  void _checkInstance() {
    final id = health?.instance ?? '';
    final store = instanceStore;
    if (id.isEmpty || store == null) return;
    final url = api is HttpFundusApi ? (api as HttpFundusApi).baseUrl : '';
    final seen = store.instanceFor(url);
    if (seen.isEmpty) {
      store.setInstance(url, id);
      instanceChanged = false;
    } else {
      instanceChanged = seen != id;
    }
  }

  /// "Start again" after a failed autostart.
  Future<void> retryAutostart() async {
    _autostartTried = false;
    startError = null;
    notifyListeners();
    await checkHealth();
  }

  /// Skip the wizard for now (captures still work; they wait as pending).
  void skipSetup() {
    setupSkipped = true;
    notifyListeners();
  }

  /// Called after settings changed: re-read health so setup_needed clears.
  Future<void> settingsChanged() async {
    setupSkipped = false;
    await checkHealth();
    await refreshView();
  }

  Future<void> _pollHealth() async {
    final wasReachable = reachable;
    try {
      health = await api.health();
      _checkInstance();
      reachable = true;
      lastError = null;
    } catch (e) {
      reachable = false;
      lastError = e;
    }
    if (reachable != wasReachable) {
      if (reachable) refreshAll();
      notifyListeners();
    }
  }

  // ---------------------------------------------------------------------
  // Events

  void _onEvent(ServerEvent ev) {
    switch (ev.type) {
      case 'capture.changed':
        final cap = Capture.fromJson(ev.payload);
        final idx = pending.indexWhere((p) => p.capture.id == cap.id);
        if (idx >= 0) {
          if (ev.payload.containsKey('receipts')) {
            // The event carries receipts: the pill renders from it alone.
            pending[idx].capture = cap;
            notifyListeners();
          } else {
            // Older daemons omit receipts from the event: fetch them.
            api
                .getCapture(cap.id)
                .then((full) {
                  final i = pending.indexWhere((p) => p.capture.id == cap.id);
                  if (i >= 0) {
                    pending[i].capture = full;
                    notifyListeners();
                  }
                })
                .catchError((_) {});
          }
        }
        if (selectedId == cap.id) select(cap.id, force: true);
        _scheduleRefresh();
      case 'txn.committed':
        _scheduleRefresh();
        final r = Receipt.fromJson(ev.payload);
        if (ev.id != null && ev.id! > lastSeq) lastSeq = ev.id!;
        if (r.seq > lastSeq) lastSeq = r.seq;
        for (final id in r.touched) {
          lastReceiptFor[id] = r;
        }
        if (selectedId != null &&
            (r.lines.any((l) => l.objectId == selectedId) ||
                r.touched.contains(selectedId)) &&
            r.causeKind != 'conversation') {
          select(selectedId!, force: true);
        }
      case 'object.changed':
        final id = ev.payload['id'];
        if (id is String) {
          refs.invalidate(id);
          if (id == selectedId && ev.payload['removed'] == true) {
            removedNotice = RemovedNotice(id, lastReceiptFor[id]);
            detail = null;
            topicPage = null;
            notifyListeners();
          }
        }
      case 'hello':
        _sseConnected = true;
        final seq = ev.payload['seq'];
        if (seq is int && lastSeq == 0) lastSeq = seq;
        notifyListeners();
    }
  }

  void _scheduleRefresh() {
    _refreshDebounce?.cancel();
    _refreshDebounce = Timer(const Duration(milliseconds: 250), () {
      refreshView();
      api
          .stats()
          .then((s) {
            stats = s;
            notifyListeners();
          })
          .catchError((_) {});
      _countAttention();
    });
  }

  Future<void> _countAttention() async {
    try {
      final items = await api.inbox();
      inboxAttention = items.where((c) => !c.isBusy).length;
      notifyListeners();
    } catch (_) {}
  }

  // ---------------------------------------------------------------------
  // Views

  Future<void> setView(AppView v) async {
    view = v;
    notifyListeners();
    await refreshView();
  }

  Future<void> refreshAll() async {
    await checkHealth();
    await refreshView();
    if (selectedId != null) await select(selectedId!, force: true);
  }

  Future<void> refreshView() async {
    loading = true;
    notifyListeners();
    try {
      switch (view) {
        case AppView.inbox:
          inbox = await api.inbox();
        case AppView.relevant:
          tasks = await api.relevant(limit: 25);
        case AppView.open:
          tasks = await api.tasks(states: const ['open']);
        case AppView.waiting:
          tasks = await api.tasks(states: const ['waiting']);
        case AppView.later:
          tasks = await api.tasks(states: const ['later']);
        case AppView.ideas:
          notes = await api.notes(kind: 'idea');
        case AppView.notes:
          notes = await api.notes(kind: 'note');
        case AppView.topics:
          topics = await api.topics();
        case AppView.changes:
          changes = await api.changes(limit: 100);
        case AppView.conversation:
          break;
      }
      reachable = true;
      lastError = null;
      viewError = null;
      if (view == AppView.inbox) {
        inboxAttention = inbox.where((c) => !c.isBusy).length;
      }
    } catch (e) {
      lastError = e;
      viewError = e;
      if (e is! ApiException) reachable = false;
    }
    loading = false;
    notifyListeners();
  }

  // ---------------------------------------------------------------------
  // Selection / inspector

  Future<void> select(String id, {bool force = false}) async {
    if (!force && selectedId == id && detail != null) return;
    selectedId = id;
    if (removedNotice?.id != id) removedNotice = null;
    detailLoading = detail?.id != id;
    notifyListeners();
    try {
      final d = await api.object(id);
      if (selectedId != id) return;
      detail = d;
      topicPage = d.type == 'topic' ? await api.topic(id) : null;
      lastError = null;
    } catch (e) {
      lastError = e;
    }
    detailLoading = false;
    notifyListeners();
  }

  void clearSelection() {
    selectedId = null;
    detail = null;
    topicPage = null;
    removedNotice = null;
    notifyListeners();
  }

  /// Undoes the most recent visible change (yours or the model's).
  /// Returns the compensating receipt, or null when nothing is undoable.
  Future<Receipt?> undoLatest() async {
    final recent = await api.changes(limit: 20);
    for (final r in recent) {
      if (r.undoable && !r.isUndone && !r.quiet) {
        return undo(r.txnId);
      }
    }
    return null;
  }

  // ---------------------------------------------------------------------
  // Actions

  Future<PendingCapture?> capture(String text) async {
    final t = text.trim();
    if (t.isEmpty) return null;
    try {
      final cap = await api.capture(t);
      final p = PendingCapture(cap);
      pending.insert(0, p);
      if (pending.length > 6) pending.removeLast();
      notifyListeners();
      return p;
    } catch (e) {
      lastError = e;
      notifyListeners();
      rethrow;
    }
  }

  void dismissPending(PendingCapture p) {
    pending.remove(p);
    notifyListeners();
  }

  /// Undo; returns the conflict details when the server refuses without force.
  Future<Receipt> undo(String txnId, {bool force = false}) async {
    final r = await api.undo(txnId, force: force);
    // Update pending captures referencing this txn.
    for (final p in pending) {
      if (p.capture.receipts.any((x) => x.txnId == txnId)) {
        try {
          p.capture = await api.getCapture(p.capture.id);
        } catch (_) {}
      }
    }
    await refreshView();
    if (selectedId != null) await select(selectedId!, force: true);
    return r;
  }

  Future<Receipt> setTaskState(Task t, String state) async {
    final r = await api.commands([
      {
        'op': 'task.update',
        'id': t.id,
        'expected_rev': t.meta.rev,
        'state': state,
      },
    ]);
    await refreshView();
    if (selectedId == t.id) await select(t.id, force: true);
    return r;
  }

  Future<Receipt> updateTask(Task t, Map<String, dynamic> patch) async {
    final r = await api.commands([
      {'op': 'task.update', 'id': t.id, 'expected_rev': t.meta.rev, ...patch},
    ]);
    await refreshView();
    if (selectedId == t.id) await select(t.id, force: true);
    return r;
  }

  Future<Receipt> reviseNote(Note n, List<Map<String, dynamic>> edits) async {
    final r = await api.commands([
      {
        'op': 'note.revise',
        'id': n.id,
        'expected_rev': n.meta.rev,
        'edits': edits,
      },
    ]);
    await select(n.id, force: true);
    await refreshView();
    return r;
  }

  Future<Receipt> updateNote(Note n, Map<String, dynamic> patch) async {
    final r = await api.commands([
      {'op': 'note.update', 'id': n.id, 'expected_rev': n.meta.rev, ...patch},
    ]);
    await select(n.id, force: true);
    await refreshView();
    return r;
  }

  Future<Receipt> updateTopic(Topic t, Map<String, dynamic> patch) async {
    final r = await api.commands([
      {'op': 'topic.update', 'id': t.id, 'expected_rev': t.meta.rev, ...patch},
    ]);
    await select(t.id, force: true);
    await refreshView();
    return r;
  }

  Future<Receipt> archive(String id, int rev, {bool unarchive = false}) async {
    final r = await api.commands([
      {
        'op': unarchive ? 'object.unarchive' : 'object.archive',
        'id': id,
        'expected_rev': rev,
      },
    ]);
    await refreshView();
    if (selectedId == id) await select(id, force: true);
    return r;
  }

  /// Re-queues a capture. Throws ApiException(code 'processing') while the
  /// daemon is still working on it.
  Future<void> retryCapture(Capture c, {String answer = ''}) async {
    await api.retryCapture(c.id, answer: answer);
    await refreshView();
  }

  Future<Capture> dismissCapture(Capture c) async {
    final r = await api.dismissCapture(c.id);
    await refreshView();
    if (selectedId == c.id) await select(c.id, force: true);
    return r;
  }

  /// Applies the model's parked proposal without a new model call.
  Future<Capture> acceptCapture(Capture c) async {
    final r = await api.acceptCapture(c.id);
    await refreshView();
    if (selectedId == c.id) await select(c.id, force: true);
    return r;
  }

  Future<Receipt> setNoteMarkdown(Note n, String markdown) async {
    final r = await api.commands([
      {
        'op': 'note.set_markdown',
        'id': n.id,
        'expected_rev': n.meta.rev,
        'markdown': markdown,
      },
    ]);
    await select(n.id, force: true);
    await refreshView();
    return r;
  }

  Future<Receipt> setTopicSummary(Topic t, String markdown) async {
    final r = await api.commands([
      {
        'op': 'topic.set_summary',
        'id': t.id,
        'expected_rev': t.meta.rev,
        'markdown': markdown,
      },
    ]);
    await select(t.id, force: true);
    await refreshView();
    return r;
  }

  /// Merges [from] into [survivor]; [survivor] keeps its id.
  Future<Receipt> mergeTopic(Topic survivor, Topic from) async {
    final r = await api.commands([
      {
        'op': 'topic.merge',
        'id': survivor.id,
        'expected_rev': survivor.meta.rev,
        'from': from.id,
      },
    ]);
    refs.invalidate(from.id);
    await select(survivor.id, force: true);
    await refreshView();
    return r;
  }

  Future<void> search(String q) async {
    searchQuery = q;
    if (q.trim().isEmpty) {
      searchHits = [];
      notifyListeners();
      return;
    }
    searching = true;
    notifyListeners();
    try {
      final hits = await api.search(q);
      if (searchQuery == q) searchHits = hits;
    } catch (e) {
      lastError = e;
    }
    searching = false;
    notifyListeners();
  }
}
