/// Data models mirroring the Fundus JSON API (internal/api in the Go core).
///
/// Parsing is tolerant: missing fields fall back to sensible defaults so a
/// slightly older or newer daemon does not crash the client.
library;

DateTime? _date(dynamic v) {
  if (v is String && v.isNotEmpty) return DateTime.tryParse(v)?.toLocal();
  return null;
}

String _str(dynamic v, [String def = '']) => v is String ? v : def;
int _int(dynamic v, [int def = 0]) =>
    v is int ? v : (v is num ? v.toInt() : def);
double _dbl(dynamic v, [double def = 0]) => v is num ? v.toDouble() : def;
bool _bool(dynamic v) => v == true;
List<String> _strs(dynamic v) =>
    v is List ? v.whereType<String>().toList() : const [];

/// One unit of a typed document.
class Block {
  final String id;
  final String
  type; // heading paragraph list quote code callout task_ref source_ref
  final String text;
  final int level;
  final List<String> items;
  final bool ordered;
  final String lang;
  final String kind;
  final String ref;
  final List<String> sources;
  final bool pinned;

  const Block({
    required this.id,
    required this.type,
    this.text = '',
    this.level = 1,
    this.items = const [],
    this.ordered = false,
    this.lang = '',
    this.kind = '',
    this.ref = '',
    this.sources = const [],
    this.pinned = false,
  });

  factory Block.fromJson(Map<String, dynamic> j) => Block(
    id: _str(j['id']),
    type: _str(j['type'], 'paragraph'),
    text: _str(j['text']),
    level: _int(j['level'], 1),
    items: _strs(j['items']),
    ordered: _bool(j['ordered']),
    lang: _str(j['lang']),
    kind: _str(j['kind']),
    ref: _str(j['ref']),
    sources: _strs(j['sources']),
    pinned: _bool(j['pinned']),
  );

  /// Renders the block back to the Markdown subset (for editing).
  String toMarkdown() {
    switch (type) {
      case 'heading':
        return '${'#' * level.clamp(1, 3)} $text';
      case 'list':
        return items
            .asMap()
            .entries
            .map((e) => ordered ? '${e.key + 1}. ${e.value}' : '- ${e.value}')
            .join('\n');
      case 'quote':
        return text.split('\n').map((l) => '> $l').join('\n');
      case 'callout':
        final lines = text.split('\n');
        final first = '> [!$kind] ${lines.first}';
        return [first, ...lines.skip(1).map((l) => '> $l')].join('\n');
      case 'code':
        return '```$lang\n$text\n```';
      case 'task_ref':
        return '[[$ref]]';
      case 'source_ref':
        return text.isEmpty ? '[[$ref]]' : '[[$ref]] $text';
      default:
        return text;
    }
  }
}

/// A list of blocks.
class Doc {
  final List<Block> blocks;
  const Doc(this.blocks);
  static const empty = Doc([]);

  factory Doc.fromJson(dynamic j) {
    if (j is Map<String, dynamic> && j['blocks'] is List) {
      return Doc(
        (j['blocks'] as List)
            .whereType<Map<String, dynamic>>()
            .map(Block.fromJson)
            .toList(),
      );
    }
    return Doc.empty;
  }

  String toMarkdown() => blocks.map((b) => b.toMarkdown()).join('\n\n');
  String plainText() => blocks
      .map((b) => b.type == 'list' ? b.items.join(' ') : b.text)
      .join(' ');
}

/// Shared object header.
class Meta {
  final String id;
  final String type;
  final int rev;
  final DateTime? createdAt;
  final DateTime? updatedAt;
  final bool archived;
  const Meta({
    required this.id,
    required this.type,
    this.rev = 0,
    this.createdAt,
    this.updatedAt,
    this.archived = false,
  });
  factory Meta.fromJson(Map<String, dynamic> j) => Meta(
    id: _str(j['id']),
    type: _str(j['type']),
    rev: _int(j['rev']),
    createdAt: _date(j['created_at']),
    updatedAt: _date(j['updated_at']),
    archived: _bool(j['archived']),
  );
}

class ReceiptLine {
  final String op;
  final String objectId;
  final String objectType;
  final String text;
  const ReceiptLine({
    required this.op,
    required this.objectId,
    required this.objectType,
    required this.text,
  });
  factory ReceiptLine.fromJson(Map<String, dynamic> j) => ReceiptLine(
    op: _str(j['op']),
    objectId: _str(j['object_id']),
    objectType: _str(j['object_type']),
    text: _str(j['text']),
  );
}

/// What a transaction actually did.
class Receipt {
  final String txnId;
  final int seq;
  final DateTime? at;
  final String actor;
  final String causeKind;
  final String causeId;
  final List<ReceiptLine> lines;
  final String summary;
  final bool undoable;
  final String undoOf;
  final String undoneBy;
  final bool quiet;
  final List<String> touched;

  /// Topics whose member list changed (created in, linked, unlinked).
  final List<String> affected;

  const Receipt({
    required this.txnId,
    this.seq = 0,
    this.at,
    this.actor = '',
    this.causeKind = '',
    this.causeId = '',
    this.lines = const [],
    this.summary = '',
    this.undoable = true,
    this.undoOf = '',
    this.undoneBy = '',
    this.quiet = false,
    this.touched = const [],
    this.affected = const [],
  });

  factory Receipt.fromJson(Map<String, dynamic> j) {
    final cause = j['cause'];
    return Receipt(
      txnId: _str(j['txn_id']),
      seq: _int(j['seq']),
      at: _date(j['at']),
      actor: _str(j['actor']),
      causeKind: cause is Map ? _str(cause['kind']) : '',
      causeId: cause is Map ? _str(cause['id']) : '',
      lines: (j['lines'] is List)
          ? (j['lines'] as List)
                .whereType<Map<String, dynamic>>()
                .map(ReceiptLine.fromJson)
                .toList()
          : const [],
      summary: _str(j['summary']),
      undoable: j['undoable'] is bool ? j['undoable'] as bool : true,
      undoOf: _str(j['undo_of']),
      undoneBy: _str(j['undone_by']),
      quiet: _bool(j['quiet']),
      touched: _strs(j['touched']),
      affected: _strs(j['affected']),
    );
  }

  bool get isModel => actor.startsWith('llm:');
  bool get isUser => actor.startsWith('user:');
  bool get isUndone => undoneBy.isNotEmpty;
  bool get isUndo => undoOf.isNotEmpty;

  /// Short actor label: "you", "triage", "chat", "system".
  String get actorLabel {
    if (actor.startsWith('user:')) return 'you';
    if (actor.startsWith('llm:triage')) return 'triage';
    if (actor.startsWith('llm:chat')) return 'chat';
    if (actor.startsWith('llm:')) return 'model';
    return actor.isEmpty ? 'system' : actor;
  }

  String get modelName {
    final parts = actor.split('/');
    final m = parts.length >= 3 ? parts.sublist(2).join('/') : '';
    return m == 'heuristic' ? 'rules' : m;
  }
}

class CaptureResult {
  final String classification;
  final double confidence;
  final String summary;
  final String question;
  final String error;
  final String provider;
  final String model;
  final bool retryable;
  final List<ProposalOp> proposal;

  /// Why a capture is parked: unclear | low_confidence | proposal | discard | undone.
  final String reason;
  const CaptureResult({
    this.classification = '',
    this.confidence = 0,
    this.summary = '',
    this.question = '',
    this.error = '',
    this.provider = '',
    this.model = '',
    this.retryable = false,
    this.proposal = const [],
    this.reason = '',
  });
  factory CaptureResult.fromJson(Map<String, dynamic> j) => CaptureResult(
    classification: _str(j['classification']),
    confidence: _dbl(j['confidence']),
    summary: _str(j['summary']),
    question: _str(j['question']),
    error: _str(j['error']),
    provider: _str(j['provider']),
    model: _str(j['model']),
    retryable: _bool(j['retryable']),
    reason: _str(j['reason']),
    proposal: j['proposal'] is List
        ? (j['proposal'] as List)
              .whereType<Map<String, dynamic>>()
              .map(ProposalOp.fromJson)
              .toList()
        : const [],
  );
}

/// One operation of a parked proposal, in the model's vocabulary.
class ProposalOp {
  final String op;
  final String kind;
  final String title;
  final String markdown;
  final String noteId;
  final String text;
  final String taskId;
  final String state;
  final String due;
  final int? effortMinutes;
  final int? importance;
  final String waitingOn;
  final List<String> topics;
  final String name;
  final List<String> aliases;
  const ProposalOp({
    required this.op,
    this.kind = '',
    this.title = '',
    this.markdown = '',
    this.noteId = '',
    this.text = '',
    this.taskId = '',
    this.state = '',
    this.due = '',
    this.effortMinutes,
    this.importance,
    this.waitingOn = '',
    this.topics = const [],
    this.name = '',
    this.aliases = const [],
  });
  factory ProposalOp.fromJson(Map<String, dynamic> j) => ProposalOp(
    op: _str(j['op']),
    kind: _str(j['kind']),
    title: _str(j['title']),
    markdown: _str(j['markdown']),
    noteId: _str(j['note_id']),
    text: _str(j['text']),
    taskId: _str(j['task_id']),
    state: _str(j['state']),
    due: _str(j['due']),
    effortMinutes: j['effort_minutes'] is num
        ? (j['effort_minutes'] as num).toInt()
        : null,
    importance: j['importance'] is num
        ? (j['importance'] as num).toInt()
        : null,
    waitingOn: _str(j['waiting_on']),
    topics: _strs(j['topics']),
    name: _str(j['name']),
    aliases: _strs(j['aliases']),
  );

  Map<String, dynamic> toJson() => {
    'op': op,
    if (kind.isNotEmpty) 'kind': kind,
    if (title.isNotEmpty) 'title': title,
    if (markdown.isNotEmpty) 'markdown': markdown,
    if (noteId.isNotEmpty) 'note_id': noteId,
    if (text.isNotEmpty) 'text': text,
    if (taskId.isNotEmpty) 'task_id': taskId,
    if (state.isNotEmpty) 'state': state,
    if (due.isNotEmpty) 'due': due,
    if (effortMinutes != null) 'effort_minutes': effortMinutes,
    if (importance != null) 'importance': importance,
    if (waitingOn.isNotEmpty) 'waiting_on': waitingOn,
    if (topics.isNotEmpty) 'topics': topics,
    if (name.isNotEmpty) 'name': name,
    if (aliases.isNotEmpty) 'aliases': aliases,
  };

  /// Human rendering, e.g. "create idea “X” linked to Solaranlage".
  /// [label] resolves topic ids to names; unknown ids are shown as given.
  String describe([String Function(String id)? label]) {
    String tps() {
      if (topics.isEmpty) return '';
      final names = topics
          .map((t) {
            if (t.startsWith('topic_')) return _name(label, t, 'a topic');
            return 'new topic “$t”';
          })
          .join(', ');
      return ' linked to $names';
    }

    switch (op) {
      case 'note.create':
        return 'create ${kind.isEmpty ? 'note' : kind} “$title”${tps()}';
      case 'note.append':
        return 'add to ${_name(label, noteId, 'the note')}${tps()}';
      case 'task.create':
        final extras = [
          if (state.isNotEmpty && state != 'open') state,
          if (due.isNotEmpty) 'due $due',
          if (effortMinutes != null) '$effortMinutes min',
          if (importance == 3) 'important',
          if (waitingOn.isNotEmpty) 'waiting on $waitingOn',
        ];
        return 'create task “$text”${extras.isEmpty ? '' : ' (${extras.join(', ')})'}${tps()}';
      case 'task.complete':
        return 'complete ${_name(label, taskId, 'the task')}';
      case 'task.mention':
        return 'note another mention of ${_name(label, taskId, 'the task')}';
      case 'task.update':
        return 'update ${_name(label, taskId, 'the task')}${text.isEmpty ? '' : ' to “$text”'}${due.isEmpty ? '' : ', due $due'}${state.isEmpty ? '' : ', $state'}${tps()}';
      case 'topic.create':
        return 'create ${kind.isEmpty ? 'topic' : kind} “$name”';
      default:
        return op;
    }
  }
}

/// Resolved title in quotes, or a generic noun; never a raw id.
String _name(String Function(String id)? label, String id, String fallback) {
  final l = label?.call(id);
  if (l == null || l.isEmpty || l == id) return fallback;
  return '“$l”';
}

/// "Would create idea “X”; would create task “Y”."
String describeProposal(
  List<ProposalOp> ops, [
  String Function(String id)? label,
]) {
  if (ops.isEmpty) return '';
  final parts = ops.map((o) => o.describe(label)).toList();
  final s = parts.join('; would ');
  return 'Would ${s[0]}${s.substring(1)}.';
}

/// Raw user input with its processing state.
class Capture {
  final Meta meta;
  final String text;
  final String source;
  final String
  status; // pending processing processed needs_review failed dismissed
  final int attempts;
  final CaptureResult? result;
  final String conversationId;
  final String answer;
  final List<Receipt> receipts;

  const Capture({
    required this.meta,
    required this.text,
    this.source = '',
    this.status = 'pending',
    this.attempts = 0,
    this.result,
    this.conversationId = '',
    this.answer = '',
    this.receipts = const [],
  });

  String get id => meta.id;

  factory Capture.fromJson(Map<String, dynamic> j) => Capture(
    meta: Meta.fromJson(j),
    text: _str(j['text']),
    source: _str(j['source']),
    status: _str(j['status'], 'pending'),
    attempts: _int(j['attempts']),
    result: j['result'] is Map<String, dynamic>
        ? CaptureResult.fromJson(j['result'] as Map<String, dynamic>)
        : null,
    conversationId: _str(j['conversation_id']),
    answer: _str(j['answer']),
    receipts: receiptsFromJson(j['receipts']),
  );

  Capture copyWith({
    String? status,
    CaptureResult? result,
    List<Receipt>? receipts,
  }) => Capture(
    meta: meta,
    text: text,
    source: source,
    status: status ?? this.status,
    attempts: attempts,
    result: result ?? this.result,
    conversationId: conversationId,
    answer: answer,
    receipts: receipts ?? this.receipts,
  );

  bool get isOpen =>
      status == 'pending' ||
      status == 'processing' ||
      status == 'needs_review' ||
      status == 'failed';
  bool get isBusy => status == 'pending' || status == 'processing';
  bool get hasProposal => result?.proposal.isNotEmpty == true;
  bool get isRetrying => status == 'failed' && result?.retryable == true;
  bool get canAccept =>
      hasProposal && (status == 'needs_review' || status == 'failed');

  /// The receipt of the model's filing, if any and not undone.
  Receipt? get filingReceipt {
    for (final r in receipts.reversed) {
      if (r.isModel && !r.quiet) return r;
    }
    return null;
  }
}

List<Receipt> receiptsFromJson(dynamic v) => v is List
    ? v.whereType<Map<String, dynamic>>().map(Receipt.fromJson).toList()
    : const [];

/// Task with attention score.
class Task {
  final Meta meta;
  final String text;
  final String state; // open waiting later done
  final String due;
  final int effortMinutes;
  final int importance;
  final String waitingOn;
  final List<String> topics;
  final List<String> origins;
  final List<String> notes;
  final DateTime? completedAt;
  final int mentions;
  final double score;
  final List<String> reasons;
  final List<String> topicNames;

  const Task({
    required this.meta,
    required this.text,
    this.state = 'open',
    this.due = '',
    this.effortMinutes = 0,
    this.importance = 0,
    this.waitingOn = '',
    this.topics = const [],
    this.origins = const [],
    this.notes = const [],
    this.completedAt,
    this.mentions = 0,
    this.score = 0,
    this.reasons = const [],
    this.topicNames = const [],
  });

  String get id => meta.id;

  factory Task.fromJson(Map<String, dynamic> j) => Task(
    meta: Meta.fromJson(j),
    text: _str(j['text']),
    state: _str(j['state'], 'open'),
    due: _str(j['due']),
    effortMinutes: _int(j['effort_minutes']),
    importance: _int(j['importance']),
    waitingOn: _str(j['waiting_on']),
    topics: _strs(j['topics']),
    origins: _strs(j['origins']),
    notes: _strs(j['notes']),
    completedAt: _date(j['completed_at']),
    mentions: _int(j['mentions']),
    score: _dbl(j['score']),
    reasons: _strs(j['reasons']),
    topicNames: _strs(j['topic_names']),
  );
}

/// Note or idea.
class Note {
  final Meta meta;
  final String kind; // note | idea
  final String title;
  final Doc body;
  final List<String> topics;
  final List<String> origins;
  final List<String> related;
  final List<String> topicNames;
  final String preview;

  const Note({
    required this.meta,
    required this.kind,
    required this.title,
    this.body = Doc.empty,
    this.topics = const [],
    this.origins = const [],
    this.related = const [],
    this.topicNames = const [],
    this.preview = '',
  });

  String get id => meta.id;

  factory Note.fromJson(Map<String, dynamic> j) => Note(
    meta: Meta.fromJson(j),
    kind: _str(j['kind'], 'note'),
    title: _str(j['title']),
    body: Doc.fromJson(j['body']),
    topics: _strs(j['topics']),
    origins: _strs(j['origins']),
    related: _strs(j['related']),
    topicNames: _strs(j['topic_names']),
    preview: _str(j['preview']),
  );
}

/// Topic (also persons and projects).
class Topic {
  final Meta meta;
  final String kind; // topic | person | project
  final String name;
  final List<String> aliases;
  final Doc summary;
  final int noteCount;
  final int openTaskCount;
  final DateTime? lastActivity;

  const Topic({
    required this.meta,
    required this.kind,
    required this.name,
    this.aliases = const [],
    this.summary = Doc.empty,
    this.noteCount = 0,
    this.openTaskCount = 0,
    this.lastActivity,
  });

  String get id => meta.id;

  factory Topic.fromJson(Map<String, dynamic> j) => Topic(
    meta: Meta.fromJson(j),
    kind: _str(j['kind'], 'topic'),
    name: _str(j['name']),
    aliases: _strs(j['aliases']),
    summary: Doc.fromJson(j['summary']),
    noteCount: _int(j['note_count']),
    openTaskCount: _int(j['open_task_count']),
    lastActivity: _date(j['last_activity']),
  );
}

class TopicPage {
  final Topic topic;
  final List<Note> notes;

  /// Open, waiting and later tasks.
  final List<Task> tasks;

  /// Completed tasks, most recently finished first.
  final List<Task> doneTasks;
  const TopicPage({
    required this.topic,
    this.notes = const [],
    this.tasks = const [],
    this.doneTasks = const [],
  });
  factory TopicPage.fromJson(Map<String, dynamic> j) => TopicPage(
    topic: Topic.fromJson((j['topic'] as Map<String, dynamic>?) ?? const {}),
    notes: (j['notes'] is List)
        ? (j['notes'] as List)
              .whereType<Map<String, dynamic>>()
              .map(Note.fromJson)
              .toList()
        : const [],
    tasks: (j['tasks'] is List)
        ? (j['tasks'] as List)
              .whereType<Map<String, dynamic>>()
              .map(Task.fromJson)
              .toList()
        : const [],
    doneTasks: (j['done_tasks'] is List)
        ? (j['done_tasks'] as List)
              .whereType<Map<String, dynamic>>()
              .map(Task.fromJson)
              .toList()
        : const [],
  );
}

class LinkRef {
  final String id;
  final String type;
  final String title;
  final String preview;
  final String state;
  final DateTime? createdAt;
  const LinkRef({
    required this.id,
    required this.type,
    required this.title,
    this.preview = '',
    this.state = '',
    this.createdAt,
  });
  factory LinkRef.fromJson(Map<String, dynamic> j) => LinkRef(
    id: _str(j['id']),
    type: _str(j['type']),
    title: _str(j['title']),
    preview: _str(j['preview']),
    state: _str(j['state']),
    createdAt: _date(j['created_at']),
  );
}

/// GET /v1/objects/{id}: a typed object plus history and links.
class ObjectDetail {
  final Meta meta;
  final Map<String, dynamic> raw;
  final Note? note;
  final Task? task;
  final Topic? topic;
  final Capture? capture;
  final Conversation? conversation;
  final List<Receipt> receipts;
  final List<LinkRef> backlinks;
  final List<LinkRef> topics;
  final String markdown;

  const ObjectDetail({
    required this.meta,
    required this.raw,
    this.note,
    this.task,
    this.topic,
    this.capture,
    this.conversation,
    this.receipts = const [],
    this.backlinks = const [],
    this.topics = const [],
    this.markdown = '',
  });

  String get id => meta.id;
  String get type => meta.type;

  String get title {
    if (note != null) return note!.title;
    if (task != null) return task!.text;
    if (topic != null) return topic!.name;
    if (capture != null) return capture!.text;
    if (conversation != null) return conversation!.title;
    return meta.id;
  }

  factory ObjectDetail.fromJson(Map<String, dynamic> j) {
    final obj = (j['object'] as Map<String, dynamic>?) ?? const {};
    final meta = Meta.fromJson(obj);
    final receipts = receiptsFromJson(j['receipts']);
    List<LinkRef> links(dynamic v) => v is List
        ? v.whereType<Map<String, dynamic>>().map(LinkRef.fromJson).toList()
        : <LinkRef>[];
    return ObjectDetail(
      meta: meta,
      raw: obj,
      note: meta.type == 'note' ? Note.fromJson(obj) : null,
      task: meta.type == 'task' ? Task.fromJson(obj) : null,
      topic: meta.type == 'topic' ? Topic.fromJson(obj) : null,
      capture: meta.type == 'capture'
          ? Capture.fromJson({...obj, 'receipts': j['receipts']})
          : null,
      conversation: meta.type == 'conversation'
          ? Conversation.fromJson(obj)
          : null,
      receipts: receipts,
      backlinks: links(j['backlinks']),
      topics: links(j['topics']),
      markdown: _str(j['markdown']),
    );
  }
}

class SearchHit {
  final String id;
  final String type;
  final String title;
  final double score;
  final String preview;
  final String kind;
  final String state;
  const SearchHit({
    required this.id,
    required this.type,
    required this.title,
    this.score = 0,
    this.preview = '',
    this.kind = '',
    this.state = '',
  });
  factory SearchHit.fromJson(Map<String, dynamic> j) => SearchHit(
    id: _str(j['id']),
    type: _str(j['type']),
    title: _str(j['title']),
    score: _dbl(j['score']),
    preview: _str(j['preview']),
    kind: _str(j['kind']),
    state: _str(j['state']),
  );
}

/// One turn of a conversation. Since 0.3.1 messages are objects of their own.
class Message {
  final Meta meta;
  final String conversationId;
  final int index;
  final String role;
  final String text;
  final Doc blocks;
  final String captureId;
  final List<String> txnIds;
  final List<String> refs;

  /// A system-inserted marker: the daemon restarted before answering.
  final bool interrupted;
  const Message({
    this.meta = const Meta(id: '', type: 'message'),
    this.conversationId = '',
    this.index = 0,
    required this.role,
    this.text = '',
    this.blocks = Doc.empty,
    this.captureId = '',
    this.txnIds = const [],
    this.refs = const [],
    this.interrupted = false,
  });
  String get id => meta.id;
  DateTime? get at => meta.createdAt;
  factory Message.fromJson(Map<String, dynamic> j) => Message(
    meta: Meta.fromJson(j),
    conversationId: _str(j['conversation_id']),
    index: _int(j['index']),
    role: _str(j['role'], 'assistant'),
    text: _str(j['text']),
    blocks: Doc.fromJson(j['blocks']),
    captureId: _str(j['capture_id']),
    txnIds: _strs(j['txn_ids']),
    refs: _strs(j['refs']),
    interrupted: _bool(j['interrupted']),
  );
}

class Conversation {
  final Meta meta;
  final String title;
  final int messageCount;
  final DateTime? lastMessageAt;
  final List<Message> messages;
  const Conversation({
    required this.meta,
    this.title = '',
    this.messageCount = 0,
    this.lastMessageAt,
    this.messages = const [],
  });
  String get id => meta.id;
  factory Conversation.fromJson(Map<String, dynamic> j) => Conversation(
    meta: Meta.fromJson(j),
    title: _str(j['title']),
    messageCount: _int(j['message_count']),
    lastMessageAt: _date(j['last_message_at']),
    messages: (j['messages'] is List)
        ? (j['messages'] as List)
              .whereType<Map<String, dynamic>>()
              .map(Message.fromJson)
              .toList()
        : const [],
  );
}

class ConversationSummary {
  final String id;
  final String title;
  final int messages;
  final DateTime? updatedAt;
  const ConversationSummary({
    required this.id,
    this.title = '',
    this.messages = 0,
    this.updatedAt,
  });
  factory ConversationSummary.fromJson(Map<String, dynamic> j) =>
      ConversationSummary(
        id: _str(j['id']),
        title: _str(j['title']),
        messages: _int(j['messages']),
        updatedAt: _date(j['updated_at']),
      );
}

class ChatStep {
  final String kind; // tool_call tool_result receipt error
  final String tool;
  final String summary;
  final Receipt? receipt;
  const ChatStep({
    required this.kind,
    this.tool = '',
    this.summary = '',
    this.receipt,
  });
  factory ChatStep.fromJson(Map<String, dynamic> j) => ChatStep(
    kind: _str(j['kind']),
    tool: _str(j['tool']),
    summary: _str(j['summary']),
    receipt: j['receipt'] is Map<String, dynamic>
        ? Receipt.fromJson(j['receipt'] as Map<String, dynamic>)
        : null,
  );
}

class ChatReply {
  final String conversationId;
  final Message message;
  final List<Receipt> receipts;
  final List<ChatStep> steps;
  const ChatReply({
    required this.conversationId,
    required this.message,
    this.receipts = const [],
    this.steps = const [],
  });
  factory ChatReply.fromJson(Map<String, dynamic> j) => ChatReply(
    conversationId: _str(j['conversation_id']),
    message: Message.fromJson(
      (j['message'] as Map<String, dynamic>?) ?? const {},
    ),
    receipts: receiptsFromJson(j['receipts']),
    steps: (j['steps'] is List)
        ? (j['steps'] as List)
              .whereType<Map<String, dynamic>>()
              .map(ChatStep.fromJson)
              .toList()
        : const [],
  );
}

class Health {
  final bool ok;
  final String version;
  final int seq;
  final String triage;
  final String chat;
  final String timezone;
  final bool ui;
  final List<String> warnings;

  /// True until a model provider has been configured.
  final bool setupNeeded;
  final String configuredTriage;
  final String configuredChat;

  /// Id of the data directory this Fundus serves.
  final String instance;

  /// Whether POST /v1/transcribe is available (a dictation model is set).
  final bool dictation;
  const Health({
    this.ok = false,
    this.version = '',
    this.seq = 0,
    this.triage = '',
    this.chat = '',
    this.timezone = '',
    this.ui = false,
    this.warnings = const [],
    this.setupNeeded = false,
    this.configuredTriage = '',
    this.configuredChat = '',
    this.instance = '',
    this.dictation = false,
  });
  factory Health.fromJson(Map<String, dynamic> j) => Health(
    ok: _bool(j['ok']),
    version: _str(j['version']),
    seq: _int(j['seq']),
    triage: _str(j['triage']),
    chat: _str(j['chat']),
    timezone: _str(j['timezone']),
    ui: _bool(j['ui']),
    warnings: _strs(j['warnings']),
    setupNeeded: _bool(j['setup_needed']),
    instance: _str(j['instance']),
    dictation: _bool(j['dictation']),
    configuredTriage: j['configured'] is Map
        ? _str((j['configured'] as Map)['triage'])
        : _str(j['triage']),
    configuredChat: j['configured'] is Map
        ? _str((j['configured'] as Map)['chat'])
        : _str(j['chat']),
  );
}

// ---------------------------------------------------------------------------
// Settings and setup

/// A role binding: which provider and model triage or chat use.
class RoleRef {
  final String provider;
  final String model;
  const RoleRef({this.provider = '', this.model = ''});
  factory RoleRef.fromJson(dynamic j) => j is Map
      ? RoleRef(provider: _str(j['provider']), model: _str(j['model']))
      : const RoleRef();
  Map<String, dynamic> toJson() => {'provider': provider, 'model': model};
  String get label => provider.isEmpty ? '' : '$provider/$model';
}

/// One configured provider as reported by GET /v1/settings (no secrets).
class ProviderInfo {
  final String name;
  final String type; // openai | fake
  final String baseUrl;
  final String keyStatus; // set | env | unset
  final String keyHint;
  final bool local;
  final bool oauth;

  /// audio | chat | none — how this provider can transcribe dictation.
  final String transcription;
  const ProviderInfo({
    required this.name,
    this.type = 'openai',
    this.baseUrl = '',
    this.keyStatus = 'unset',
    this.keyHint = '',
    this.local = false,
    this.oauth = false,
    this.transcription = 'none',
  });
  factory ProviderInfo.fromJson(String name, Map<String, dynamic> j) =>
      ProviderInfo(
        name: name,
        type: _str(j['type'], 'openai'),
        baseUrl: _str(j['base_url']),
        keyStatus: _str(j['key_status'], 'unset'),
        keyHint: _str(j['key_hint']),
        local: _bool(j['local']),
        oauth: _bool(j['oauth']),
        transcription: _str(j['transcription'], 'none'),
      );
  bool get canTranscribe => transcription == 'audio' || transcription == 'chat';
  bool get hasKey => keyStatus == 'set' || keyStatus == 'env';
  bool get needsKey => type == 'openai' && !local;
}

/// The autonomy policy.
class Autonomy {
  final double minConfidence;
  final bool autoCreate;
  final int maxOpsPerCapture;
  final int maxNewTopicsPerCapture;
  const Autonomy({
    this.minConfidence = 0.6,
    this.autoCreate = true,
    this.maxOpsPerCapture = 12,
    this.maxNewTopicsPerCapture = 2,
  });
  factory Autonomy.fromJson(dynamic j) {
    if (j is! Map) return const Autonomy();
    dynamic pick(String snake, String pascal) =>
        j.containsKey(snake) ? j[snake] : j[pascal];
    final auto = pick('auto_create', 'AutoCreate');
    return Autonomy(
      minConfidence: _dbl(pick('min_confidence', 'MinConfidence'), 0.6),
      autoCreate: auto is bool ? auto : true,
      maxOpsPerCapture: _int(
        pick('max_ops_per_capture', 'MaxOpsPerCapture'),
        12,
      ),
      maxNewTopicsPerCapture: _int(
        pick('max_new_topics_per_capture', 'MaxNewTopicsPerCapture'),
        2,
      ),
    );
  }
  Map<String, dynamic> toJson() => {
    'min_confidence': minConfidence,
    'auto_create': autoCreate,
    'max_ops_per_capture': maxOpsPerCapture,
    'max_new_topics_per_capture': maxNewTopicsPerCapture,
  };
  Autonomy copyWith({
    double? minConfidence,
    bool? autoCreate,
    int? maxOpsPerCapture,
    int? maxNewTopicsPerCapture,
  }) => Autonomy(
    minConfidence: minConfidence ?? this.minConfidence,
    autoCreate: autoCreate ?? this.autoCreate,
    maxOpsPerCapture: maxOpsPerCapture ?? this.maxOpsPerCapture,
    maxNewTopicsPerCapture:
        maxNewTopicsPerCapture ?? this.maxNewTopicsPerCapture,
  );
}

/// GET /v1/settings.
class ServerSettings {
  final String path;
  final String listen;
  final String timezone;
  final bool tokenSet;
  final bool setupNeeded;
  final RoleRef triage;
  final RoleRef chat;

  /// Dictation model; an empty model means dictation is off.
  final RoleRef dictation;
  final Autonomy autonomy;
  final Map<String, ProviderInfo> providers;
  const ServerSettings({
    this.path = '',
    this.listen = '',
    this.timezone = '',
    this.tokenSet = false,
    this.setupNeeded = false,
    this.triage = const RoleRef(),
    this.chat = const RoleRef(),
    this.dictation = const RoleRef(),
    this.autonomy = const Autonomy(),
    this.providers = const {},
  });
  factory ServerSettings.fromJson(Map<String, dynamic> j) {
    final provs = <String, ProviderInfo>{};
    if (j['providers'] is Map) {
      (j['providers'] as Map).forEach((k, v) {
        if (v is Map<String, dynamic>) {
          provs['$k'] = ProviderInfo.fromJson('$k', v);
        }
      });
    }
    return ServerSettings(
      path: _str(j['path']),
      listen: _str(j['listen']),
      timezone: _str(j['timezone']),
      tokenSet: _bool(j['token_set']),
      setupNeeded: _bool(j['setup_needed']),
      triage: RoleRef.fromJson(j['triage']),
      chat: RoleRef.fromJson(j['chat']),
      dictation: RoleRef.fromJson(j['dictation']),
      autonomy: Autonomy.fromJson(j['autonomy']),
      providers: provs,
    );
  }
  ProviderInfo? provider(String name) => providers[name];
}

/// POST /v1/settings/test result.
class ProbeResult {
  final bool reachable;
  final bool structured;
  final bool tools;
  final bool german;
  final Duration latency;
  final List<String> errors;
  final String mode;
  const ProbeResult({
    this.reachable = false,
    this.structured = false,
    this.tools = false,
    this.german = false,
    this.latency = Duration.zero,
    this.errors = const [],
    this.mode = '',
  });
  factory ProbeResult.fromJson(Map<String, dynamic> j) {
    final l = j['latency'];
    Duration lat = Duration.zero;
    if (l is num) {
      // Go marshals time.Duration as nanoseconds; tolerate milliseconds too.
      lat = l > 10000000
          ? Duration(microseconds: (l / 1000).round())
          : Duration(milliseconds: l.round());
    } else if (l is String) {
      final ms = int.tryParse(l.replaceAll(RegExp('[^0-9]'), '')) ?? 0;
      lat = Duration(milliseconds: ms);
    }
    return ProbeResult(
      reachable: _bool(j['reachable']),
      structured: _bool(j['structured']),
      tools: _bool(j['tools']),
      german: _bool(j['german']),
      latency: lat,
      errors: _strs(j['errors']),
      mode: _str(j['mode']),
    );
  }

  /// Good enough for triage: reachable and schema-valid JSON.
  bool get usable => reachable && structured;
}

/// GET /v1/setup/models.
class ModelList {
  final List<String> models;
  final String suggestedTriage;
  final String suggestedChat;

  /// Suggested dictation model; empty when the provider cannot transcribe.
  final String suggestedTranscribe;

  /// Set when the provider could not be listed (e.g. Ollama not running).
  final String error;
  const ModelList({
    this.models = const [],
    this.suggestedTriage = '',
    this.suggestedChat = '',
    this.suggestedTranscribe = '',
    this.error = '',
  });
  bool get ok => error.isEmpty;
  factory ModelList.fromJson(Map<String, dynamic> j) => ModelList(
    error: _str(j['error']),
    models: _strs(j['models']),
    suggestedTriage: j['suggested'] is Map
        ? _str((j['suggested'] as Map)['triage'])
        : '',
    suggestedChat: j['suggested'] is Map
        ? _str((j['suggested'] as Map)['chat'])
        : '',
    suggestedTranscribe: j['suggested'] is Map
        ? _str((j['suggested'] as Map)['transcribe'])
        : '',
  );
}

class Stats {
  final int captures, inbox, notes, ideas, openTasks, topics, conversations;
  const Stats({
    this.captures = 0,
    this.inbox = 0,
    this.notes = 0,
    this.ideas = 0,
    this.openTasks = 0,
    this.topics = 0,
    this.conversations = 0,
  });
  factory Stats.fromJson(Map<String, dynamic> j) => Stats(
    captures: _int(j['captures']),
    inbox: _int(j['inbox']),
    notes: _int(j['notes']),
    ideas: _int(j['ideas']),
    openTasks: _int(j['open_tasks']),
    topics: _int(j['topics']),
    conversations: _int(j['conversations']),
  );
}

/// A server-sent event from GET /v1/events.
class ServerEvent {
  final String type;
  final Map<String, dynamic> payload;

  /// The `id:` of the SSE frame (the transaction seq for txn.committed).
  final int? id;
  const ServerEvent(this.type, this.payload, [this.id]);
}

/// Error returned by the API.
class ApiException implements Exception {
  final int status;
  final String code;
  final String message;
  final Map<String, dynamic>? details;
  const ApiException(this.status, this.code, this.message, [this.details]);
  bool get isUndoConflict => code == 'undo_conflict';
  @override
  String toString() => message.isEmpty ? '$code ($status)' : message;
}

/// Readable capture status ("Waiting for you", "Filed", …).
String captureStatusLabel(String status) => switch (status) {
  'pending' || 'processing' => 'Being filed',
  'processed' => 'Filed',
  'needs_review' => 'Waiting for you',
  'failed' => 'Failed',
  'dismissed' => 'Dismissed',
  _ => status,
};

/// Readable capture source ("from the desktop app").
String captureSourceLabel(String source) => switch (source) {
  'desktop' => 'from the desktop app',
  'app' || 'web' => 'from this app',
  'cli' => 'from the command line',
  'chat' => 'from a conversation',
  'api' => 'from the API',
  'test' => 'from a test',
  '' => '',
  _ => 'from $source',
};

/// Model name for display: the rules provider has no model, and provider
/// slugs never appear in the UI.
String modelLabel(String provider, String model) {
  if (model == 'heuristic' || provider == 'fake') return 'rules';
  return model;
}
