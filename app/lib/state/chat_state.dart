import 'dart:async';

import 'package:flutter/foundation.dart';

import '../api/client.dart';
import '../api/models.dart';
import '../api/ulid.dart';

/// Conversation state: list of threads, the open thread, in-flight steps.
class ChatState extends ChangeNotifier {
  ChatState(this.api) {
    _sub = api.events().listen(_onEvent, onError: (_) {});
  }

  final FundusApi api;
  StreamSubscription<ServerEvent>? _sub;

  List<ConversationSummary> conversations = [];
  Conversation? current;
  bool sending = false;

  /// The daemon is still working on an earlier turn (409 busy); we poll.
  bool busy = false;
  List<ChatStep> liveSteps = [];
  Object? error;

  /// Idempotency key of the turn in flight (reused on retry).
  String? _turnId;
  String? _turnText;

  /// Steps that belong to a finished assistant message, keyed by message index.
  final Map<int, List<ChatStep>> stepsByMessage = {};

  /// Receipts by transaction id, resolved for reopened conversations.
  final Map<String, Receipt> receipts = {};
  final Set<String> _loadingTxns = {};

  /// Receipts for a message's txn_ids that are known so far.
  List<Receipt> receiptsFor(List<String> txnIds) => [
    for (final t in txnIds)
      if (receipts[t] != null) receipts[t]!,
  ];

  /// Fetches unknown receipts (GET /v1/changes/{txn}) and notifies.
  void loadReceipts(Iterable<String> txnIds) {
    final missing = txnIds
        .where((t) => !receipts.containsKey(t) && !_loadingTxns.contains(t))
        .toList();
    if (missing.isEmpty) return;
    _loadingTxns.addAll(missing);
    Future(() async {
      for (final t in missing) {
        try {
          receipts[t] = await api.change(t);
        } catch (_) {}
        _loadingTxns.remove(t);
      }
      notifyListeners();
    });
  }

  /// Renames the current conversation (conversation.update).
  Future<void> rename(String title) async {
    final c = current;
    if (c == null) return;
    try {
      await api.commands([
        {
          'op': 'conversation.update',
          'id': c.id,
          'expected_rev': c.meta.rev,
          'title': title.trim(),
        },
      ]);
      current = await api.conversation(c.id);
      await loadConversations();
    } catch (e) {
      error = e;
    }
    notifyListeners();
  }

  @override
  void dispose() {
    _sub?.cancel();
    super.dispose();
  }

  void _onEvent(ServerEvent ev) {
    if (ev.type != 'chat.step') return;
    final convId = ev.payload['conversation_id'];
    if (current == null || convId != current!.id) return;
    final step = ev.payload['step'];
    if (step is Map<String, dynamic>) {
      liveSteps = [...liveSteps, ChatStep.fromJson(step)];
      notifyListeners();
    }
  }

  Future<void> loadConversations() async {
    try {
      conversations = await api.conversations();
      error = null;
    } catch (e) {
      error = e;
    }
    notifyListeners();
  }

  Future<void> open(String id) async {
    try {
      current = await api.conversation(id);
      stepsByMessage.clear();
      liveSteps = [];
      error = null;
      loadReceipts([for (final m in current!.messages) ...m.txnIds]);
    } catch (e) {
      error = e;
    }
    notifyListeners();
  }

  Future<void> startNew() async {
    try {
      current = await api.createConversation();
      stepsByMessage.clear();
      liveSteps = [];
      error = null;
      await loadConversations();
    } catch (e) {
      error = e;
    }
    notifyListeners();
  }

  void closeCurrent() {
    current = null;
    liveSteps = [];
    notifyListeners();
  }

  Future<void> send(String text) async {
    final t = text.trim();
    if (t.isEmpty || sending) return;
    if (current == null) await startNew();
    if (current == null) return;
    // A fresh idempotency key per turn; a retry of the same text reuses it.
    if (_turnId == null || _turnText != t) {
      _turnId = newCaptureId();
      _turnText = t;
    }
    await _run(current!, t, _turnId!);
  }

  /// Re-sends a user message whose reply never arrived.
  Future<void> resend(String text) async {
    _turnId = newCaptureId();
    _turnText = text;
    await send(text);
  }

  /// Retries the last failed turn with the same idempotency key.
  Future<void> retryLast() async {
    if (_turnText == null) return;
    await send(_turnText!);
  }

  Future<void> _run(Conversation conv, String t, String id) async {
    // Optimistic user message.
    current = Conversation(
      meta: conv.meta,
      title: conv.title,
      messageCount: conv.messageCount,
      lastMessageAt: conv.lastMessageAt,
      messages: [
        ...conv.messages,
        Message(
          meta: Meta(id: '', type: 'message', createdAt: DateTime.now()),
          role: 'user',
          text: t,
          captureId: id,
        ),
      ],
    );
    sending = true;
    busy = false;
    liveSteps = [];
    error = null;
    notifyListeners();
    try {
      final reply = await api.sendMessage(conv.id, t, id: id);
      final refreshed = await api.conversation(conv.id);
      current = refreshed;
      stepsByMessage[refreshed.messages.length - 1] = reply.steps;
      for (final r in reply.receipts) {
        receipts[r.txnId] = r;
      }
      liveSteps = [];
      _turnId = null;
      _turnText = null;
      await loadConversations();
    } on ApiException catch (e) {
      if (e.code == 'busy') {
        await _pollUntilAnswered(conv);
      } else {
        error = e;
        try {
          current = await api.conversation(conv.id);
        } catch (_) {}
      }
    } catch (e) {
      error = e;
      try {
        current = await api.conversation(conv.id);
      } catch (_) {}
    }
    sending = false;
    busy = false;
    notifyListeners();
  }

  /// 409 busy: another turn is running. Poll until a new reply appears.
  Future<void> _pollUntilAnswered(Conversation conv) async {
    busy = true;
    notifyListeners();
    final before = conv.messageCount;
    final deadline = DateTime.now().add(const Duration(seconds: 90));
    while (DateTime.now().isBefore(deadline)) {
      await Future<void>.delayed(const Duration(seconds: 2));
      try {
        final c = await api.conversation(conv.id);
        if (c.messageCount > before &&
            c.messages.isNotEmpty &&
            c.messages.last.role == 'assistant') {
          current = c;
          liveSteps = [];
          await loadConversations();
          return;
        }
      } catch (_) {}
    }
    error = 'Still working. Try again in a moment.';
    try {
      current = await api.conversation(conv.id);
    } catch (_) {}
  }
}
