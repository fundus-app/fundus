import 'dart:async';
import 'dart:typed_data';

import 'package:fundus_app/api/client.dart';
import 'package:fundus_app/api/models.dart';

/// In-memory API for widget tests.
class FakeApi implements FundusApi {
  final eventBus = StreamController<ServerEvent>.broadcast();
  final List<String> captured = [];
  int counter = 0;

  Capture _cap(String text, String status) => Capture(
    meta: Meta(
      id: 'cap_${++counter}',
      type: 'capture',
      rev: 1,
      createdAt: DateTime.now(),
    ),
    text: text,
    source: 'app',
    status: status,
  );

  @override
  Future<Capture> capture(
    String text, {
    String source = 'app',
    String? conversationId,
    int waitMs = 0,
  }) async {
    captured.add(text);
    return _cap(text, 'pending');
  }

  @override
  Future<Capture> getCapture(String id) async => Capture(
    meta: Meta(id: id, type: 'capture', rev: 3),
    text: captured.isEmpty ? '' : captured.last,
    status: 'processed',
    receipts: const [
      Receipt(
        txnId: 'txn_1',
        actor: 'llm:triage/fake/heuristic',
        summary: 'Created task "x". No due date.',
      ),
    ],
  );

  @override
  Future<List<Capture>> inbox() async => [];
  @override
  Future<List<Capture>> captures({String status = '', int limit = 50}) async =>
      [];
  @override
  Future<Capture> retryCapture(String id, {String answer = ''}) async =>
      _cap('', 'pending');
  @override
  Future<Capture> dismissCapture(String id) async => _cap('', 'dismissed');
  @override
  Future<Capture> acceptCapture(
    String id, {
    List<Map<String, dynamic>>? operations,
  }) async => _cap('', 'processed');
  @override
  Future<List<LinkRef>> resolve(List<String> ids) async => [
    for (final id in ids)
      LinkRef(id: id, type: id.split('_').first, title: 'Title of $id'),
  ];
  @override
  Future<List<Receipt>> changesAfter(int after) async => [];
  @override
  Future<Receipt> change(String txnId) async => Receipt(
    txnId: txnId,
    summary: 'Receipt $txnId',
    actor: 'llm:chat/fake/heuristic',
  );
  @override
  String backupUrl() => 'http://test/backup';

  // --- setup / settings mocks -------------------------------------------
  bool setupNeeded = true;
  final Map<String, dynamic> serverSettings = {
    'path': '/home/u/.config/fundus/config.toml',
    'listen': '127.0.0.1:7433',
    'timezone': 'Europe/Berlin',
    'token_set': false,
    'setup_needed': true,
    'triage': <String, dynamic>{'provider': '', 'model': ''},
    'chat': <String, dynamic>{'provider': '', 'model': ''},
    'dictation': <String, dynamic>{'provider': '', 'model': ''},
    'autonomy': <String, dynamic>{
      'min_confidence': 0.6,
      'auto_create': true,
      'max_ops_per_capture': 12,
      'max_new_topics_per_capture': 2,
    },
    'providers': <String, dynamic>{
      'openai': <String, dynamic>{
        'type': 'openai',
        'base_url': 'https://api.openai.com/v1',
        'key_status': 'unset',
        'key_hint': '',
        'local': false,
        'oauth': false,
        'transcription': 'audio',
      },
      'anthropic': <String, dynamic>{
        'type': 'openai',
        'base_url': 'https://api.anthropic.com/v1',
        'key_status': 'unset',
        'local': false,
        'oauth': false,
      },
      'gemini': <String, dynamic>{
        'type': 'openai',
        'base_url': 'https://generativelanguage.googleapis.com/v1beta/openai',
        'key_status': 'unset',
        'local': false,
        'oauth': false,
        'transcription': 'chat',
      },
      'openrouter': <String, dynamic>{
        'type': 'openai',
        'base_url': 'https://openrouter.ai/api/v1',
        'key_status': 'unset',
        'local': false,
        'oauth': true,
      },
      'ollama': <String, dynamic>{
        'type': 'openai',
        'base_url': 'http://127.0.0.1:11434/v1',
        'key_status': 'unset',
        'local': true,
        'oauth': false,
        'transcription': 'none',
      },
      'fake': <String, dynamic>{'type': 'fake'},
    },
  };
  final List<Map<String, dynamic>> settingsPatches = [];
  final List<String> tested = [];
  final List<String> modelsRequested = [];
  final List<String> oauthStarted = [];
  bool probeOk = true;

  /// Simulates an unreachable daemon (autostart tests).
  bool healthFails = false;
  bool dictationOn = true;
  final List<int> transcribed = [];
  String transcript = 'call the dentist tomorrow';

  @override
  Future<Health> health() async {
    if (healthFails) throw const ApiException(0, 'sse', 'Connection refused');
    return Health(
      ok: true,
      version: 'test',
      triage: 'fake/heuristic',
      chat: 'fake/heuristic',
      setupNeeded: setupNeeded,
      configuredTriage: setupNeeded ? '' : 'openai/gpt-5.6-luna',
      dictation: dictationOn,
    );
  }

  @override
  Future<ServerSettings> settings() async =>
      ServerSettings.fromJson(serverSettings);

  @override
  Future<ServerSettings> updateSettings(Map<String, dynamic> patch) async {
    settingsPatches.add(patch);
    final provs = serverSettings['providers'] as Map<String, dynamic>;
    if (patch['providers'] is Map) {
      (patch['providers'] as Map).forEach((name, v) {
        final p = Map<String, dynamic>.from(
          provs['$name'] as Map? ?? {'type': 'openai'},
        );
        if (v is Map &&
            v['api_key'] is String &&
            (v['api_key'] as String).isNotEmpty) {
          p['key_status'] = 'set';
          final k = v['api_key'] as String;
          p['key_hint'] = '…${k.length > 4 ? k.substring(k.length - 4) : k}';
        }
        if (v is Map && v['base_url'] is String) p['base_url'] = v['base_url'];
        provs['$name'] = p;
      });
    }
    for (final role in ['triage', 'chat', 'dictation']) {
      if (patch[role] is Map) {
        serverSettings[role] = Map<String, dynamic>.from(patch[role] as Map);
      }
    }
    if (patch['autonomy'] is Map) {
      serverSettings['autonomy'] = {
        ...(serverSettings['autonomy'] as Map),
        ...(patch['autonomy'] as Map),
      };
    }
    if (patch['timezone'] is String) {
      serverSettings['timezone'] = patch['timezone'];
    }
    final tri = serverSettings['triage'] as Map;
    if ((tri['provider'] as String).isNotEmpty) {
      setupNeeded = false;
      serverSettings['setup_needed'] = false;
    }
    return ServerSettings.fromJson(serverSettings);
  }

  @override
  Future<ProbeResult> testProvider(String provider, {String? model}) async {
    tested.add(provider);
    return ProbeResult(
      reachable: probeOk,
      structured: probeOk,
      tools: probeOk,
      german: probeOk,
      latency: const Duration(milliseconds: 420),
      errors: probeOk ? const [] : const ['reachability: 401 invalid key'],
      mode: 'json_schema',
    );
  }

  final List<String?> modelsKeys = [];

  @override
  Future<ModelList> setupModels(
    String provider, {
    String? apiKey,
    String? baseUrl,
  }) async {
    modelsRequested.add(provider);
    modelsKeys.add(apiKey);
    if (apiKey != null && apiKey.startsWith('bad')) {
      return const ModelList(error: 'The key was rejected.');
    }
    if (provider == 'ollama') {
      return const ModelList(
        error: 'nothing is listening at http://127.0.0.1:11434/v1 (is Ollama running?)',
      );
    }
    if (provider == 'gemini') {
      return const ModelList(
        models: ['gemini-3.5-flash-lite', 'gemini-3.8-flash', 'gemini-3.8-pro'],
        suggestedTriage: 'gemini-3.5-flash-lite',
        suggestedChat: 'gemini-3.8-flash',
        suggestedTranscribe: 'gemini-3.8-flash',
      );
    }
    if (provider == 'anthropic') {
      return const ModelList(
        models: ['claude-sonnet-5', 'claude-opus-5'],
        suggestedTriage: 'claude-sonnet-5',
        suggestedChat: 'claude-opus-5',
      );
    }
    return const ModelList(
      models: [
        'gpt-5.6-luna',
        'gpt-5.6-terra',
        'gpt-5.4-nano',
        'gpt-transcribe',
      ],
      suggestedTriage: 'gpt-5.6-luna',
      suggestedChat: 'gpt-5.6-terra',
      suggestedTranscribe: 'gpt-transcribe',
    );
  }

  @override
  Future<String> transcribe(Uint8List wav, {String? language}) async {
    transcribed.add(wav.length);
    if (!dictationOn) {
      throw const ApiException(
        503,
        'dictation_unavailable',
        'No dictation model is connected.',
      );
    }
    return transcript;
  }

  @override
  Future<String> oauthStart(String provider) async {
    oauthStarted.add(provider);
    return 'https://openrouter.ai/auth?callback=test';
  }

  @override
  Future<Stats> stats() async => const Stats();
  @override
  Future<ObjectDetail> object(String id) async => ObjectDetail(
    meta: Meta(id: id, type: 'note'),
    raw: const {},
  );
  @override
  Future<List<Note>> notes({String kind = ''}) async => [];
  @override
  Future<List<Task>> tasks({List<String> states = const []}) async => [];
  @override
  Future<List<Task>> relevant({int limit = 15}) async => [];
  @override
  Future<List<Topic>> topics() async => [];
  @override
  Future<TopicPage> topic(String id) async => TopicPage(
    topic: Topic(
      meta: Meta(id: id, type: 'topic'),
      kind: 'topic',
      name: 'T',
    ),
  );
  @override
  Future<List<SearchHit>> search(String query, {int limit = 20}) async => [];
  @override
  Future<List<Receipt>> changes({int limit = 50, bool all = false}) async => [];
  @override
  Future<Receipt> undo(String txnId, {bool force = false}) async =>
      Receipt(txnId: 'txn_u', undoOf: txnId, summary: 'Undid.');
  @override
  Future<Receipt> commands(List<Map<String, dynamic>> ops) async =>
      const Receipt(txnId: 'txn_c', summary: 'ok');
  @override
  Future<List<ConversationSummary>> conversations() async => [];
  @override
  Future<Conversation> createConversation({String title = ''}) async =>
      const Conversation(
        meta: Meta(id: 'conv_1', type: 'conversation'),
      );
  @override
  Future<Conversation> conversation(String id) async => Conversation(
    meta: Meta(id: id, type: 'conversation'),
  );
  @override
  Future<ChatReply> sendMessage(
    String conversationId,
    String text, {
    String? id,
  }) async => ChatReply(
    conversationId: conversationId,
    message: const Message(role: 'assistant', text: 'ok'),
  );
  @override
  Stream<ServerEvent> events() => eventBus.stream;
  @override
  String exportUrl(String format) => 'http://test/export?format=$format';
}
