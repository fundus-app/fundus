import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:http/http.dart' as http;
import 'package:http_parser/http_parser.dart' show MediaType;

import 'models.dart';

/// Abstract API so screens and tests can use a fake.
abstract class FundusApi {
  Future<Health> health();
  Future<Stats> stats();

  /// Files a capture. With [waitMs] > 0 the daemon waits up to that long
  /// for triage and returns the final capture with receipts.
  Future<Capture> capture(
    String text, {
    String source = 'app',
    String? conversationId,
    int waitMs = 0,
  });
  Future<Capture> getCapture(String id);
  Future<List<Capture>> inbox();
  Future<List<Capture>> captures({String status = '', int limit = 50});
  Future<Capture> retryCapture(String id, {String answer = ''});
  Future<Capture> dismissCapture(String id);

  /// Applies the proposal parked on a capture (or [operations] instead).
  Future<Capture> acceptCapture(
    String id, {
    List<Map<String, dynamic>>? operations,
  });

  /// Resolves ids to {id,type,title} in one round trip.
  Future<List<LinkRef>> resolve(List<String> ids);

  Future<ObjectDetail> object(String id);
  Future<List<Note>> notes({String kind = ''});
  Future<List<Task>> tasks({List<String> states = const []});
  Future<List<Task>> relevant({int limit = 15});
  Future<List<Topic>> topics();
  Future<TopicPage> topic(String id);
  Future<List<SearchHit>> search(String query, {int limit = 20});

  Future<List<Receipt>> changes({int limit = 50, bool all = false});

  /// Receipts with seq > [after], oldest first (resume after a reconnect).
  Future<List<Receipt>> changesAfter(int after);

  /// One receipt by transaction id.
  Future<Receipt> change(String txnId);
  Future<Receipt> undo(String txnId, {bool force = false});
  Future<Receipt> commands(List<Map<String, dynamic>> ops);

  Future<List<ConversationSummary>> conversations();
  Future<Conversation> createConversation({String title = ''});
  Future<Conversation> conversation(String id);

  /// Sends a turn. [id] is a client-generated capture id ("cap_…") that makes
  /// the turn idempotent across retries.
  Future<ChatReply> sendMessage(
    String conversationId,
    String text, {
    String? id,
  });

  /// Streams server events until cancelled. Implementations reconnect.
  Stream<ServerEvent> events();

  /// Absolute URL for exports (opened in a browser tab / launcher).
  String exportUrl(String format);

  /// Absolute URL of the backup zip.
  String backupUrl();

  // Setup and settings (0.3.2).
  Future<ServerSettings> settings();

  /// PUT /v1/settings with only the changed keys; returns the new settings.
  Future<ServerSettings> updateSettings(Map<String, dynamic> patch);

  /// POST /v1/settings/test: probes a provider (and model) with the stored key.
  Future<ProbeResult> testProvider(String provider, {String? model});

  /// Lists models. With [apiKey] the key is sent (POST /v1/setup/models) and
  /// never stored; without it the stored key is used (GET).
  Future<ModelList> setupModels(
    String provider, {
    String? apiKey,
    String? baseUrl,
  });

  /// POST /v1/setup/oauth/start → the URL to open externally.
  Future<String> oauthStart(String provider);

  /// POST /v1/transcribe: a WAV recording (≤ 25 MB) → its transcript.
  Future<String> transcribe(Uint8List wav, {String? language});
}

/// HTTP implementation.
class HttpFundusApi implements FundusApi {
  HttpFundusApi({required this.baseUrl, this.token = '', http.Client? client})
    : _client = client ?? http.Client();

  final String baseUrl;
  final String token;
  final http.Client _client;
  final _sseController = StreamController<ServerEvent>.broadcast();
  bool _sseRunning = false;
  bool _closed = false;

  Map<String, String> get _headers => {
    'X-Fundus-Client': 'app',
    'Accept': 'application/json',
    if (token.isNotEmpty) 'Authorization': 'Bearer $token',
  };

  Uri _u(String path, [Map<String, String>? q]) =>
      Uri.parse('$baseUrl$path')
          .replace(queryParameters: q == null || q.isEmpty ? null : q);

  Future<dynamic> _get(String path, [Map<String, String>? q]) async {
    final res = await _client
        .get(_u(path, q), headers: _headers)
        .timeout(const Duration(seconds: 30));
    return _decode(res);
  }

  Future<dynamic> _post(String path, [Object? body]) async {
    final res = await _client
        .post(
          _u(path),
          headers: {..._headers, 'Content-Type': 'application/json'},
          body: jsonEncode(body ?? const {}),
        )
        .timeout(const Duration(minutes: 10));
    return _decode(res);
  }

  dynamic _decode(http.Response res) {
    final text = utf8.decode(res.bodyBytes);
    dynamic body;
    try {
      body = text.isEmpty ? null : jsonDecode(text);
    } catch (_) {
      body = null;
    }
    if (res.statusCode >= 400) {
      if (body is Map && body['error'] is Map) {
        final e = body['error'] as Map;
        throw ApiException(
          res.statusCode,
          '${e['code'] ?? 'error'}',
          '${e['message'] ?? ''}',
          e['details'] is Map
              ? Map<String, dynamic>.from(e['details'] as Map)
              : null,
        );
      }
      throw ApiException(res.statusCode, 'http_${res.statusCode}', text.trim());
    }
    return body;
  }

  List<Map<String, dynamic>> _list(dynamic v) =>
      v is List ? v.whereType<Map<String, dynamic>>().toList() : const [];

  Map<String, dynamic> _map(dynamic v) =>
      v is Map<String, dynamic> ? v : <String, dynamic>{};

  @override
  Future<Health> health() async =>
      Health.fromJson(_map(await _get('/v1/health')));

  @override
  Future<Stats> stats() async => Stats.fromJson(_map(await _get('/v1/stats')));

  @override
  Future<Capture> capture(
    String text, {
    String source = 'app',
    String? conversationId,
    int waitMs = 0,
  }) async => Capture.fromJson(
    _map(
      await _post(waitMs > 0 ? '/v1/captures?wait=$waitMs' : '/v1/captures', {
        'text': text,
        'source': source,
        'conversation_id': ?conversationId,
      }),
    ),
  );

  @override
  Future<Capture> getCapture(String id) async =>
      Capture.fromJson(_map(await _get('/v1/captures/$id')));

  @override
  Future<List<Capture>> inbox() async =>
      _list(await _get('/v1/inbox')).map(Capture.fromJson).toList();

  @override
  Future<List<Capture>> captures({String status = '', int limit = 50}) async =>
      _list(
        await _get('/v1/captures', {
          if (status.isNotEmpty) 'status': status,
          'limit': '$limit',
        }),
      ).map(Capture.fromJson).toList();

  @override
  Future<Capture> retryCapture(String id, {String answer = ''}) async =>
      Capture.fromJson(
        _map(await _post('/v1/captures/$id/retry', {'answer': answer})),
      );

  @override
  Future<Capture> dismissCapture(String id) async =>
      Capture.fromJson(_map(await _post('/v1/captures/$id/dismiss')));

  @override
  Future<Capture> acceptCapture(
    String id, {
    List<Map<String, dynamic>>? operations,
  }) async => Capture.fromJson(
    _map(await _post('/v1/captures/$id/accept', {'operations': ?operations})),
  );

  @override
  Future<List<LinkRef>> resolve(List<String> ids) async {
    if (ids.isEmpty) return const [];
    return _list(await _get('/v1/objects', {'ids': ids.take(200).join(',')}))
        .map(LinkRef.fromJson)
        .toList();
  }

  @override
  Future<ObjectDetail> object(String id) async =>
      ObjectDetail.fromJson(_map(await _get('/v1/objects/$id')));

  @override
  Future<List<Note>> notes({String kind = ''}) async =>
      _list(await _get('/v1/notes', {if (kind.isNotEmpty) 'kind': kind}))
          .map(Note.fromJson)
          .toList();

  @override
  Future<List<Task>> tasks({List<String> states = const []}) async => _list(
    await _get('/v1/tasks', {if (states.isNotEmpty) 'state': states.join(',')}),
  ).map(Task.fromJson).toList();

  @override
  Future<List<Task>> relevant({int limit = 15}) async =>
      _list(await _get('/v1/relevant', {'limit': '$limit'}))
          .map(Task.fromJson)
          .toList();

  @override
  Future<List<Topic>> topics() async =>
      _list(await _get('/v1/topics')).map(Topic.fromJson).toList();

  @override
  Future<TopicPage> topic(String id) async =>
      TopicPage.fromJson(_map(await _get('/v1/topics/$id')));

  @override
  Future<List<SearchHit>> search(String query, {int limit = 20}) async =>
      _list(await _get('/v1/search', {'q': query, 'limit': '$limit'}))
          .map(SearchHit.fromJson)
          .toList();

  @override
  Future<List<Receipt>> changes({int limit = 50, bool all = false}) async =>
      _list(await _get('/v1/changes', {'limit': '$limit', if (all) 'all': '1'}))
          .map(Receipt.fromJson)
          .toList();

  @override
  Future<List<Receipt>> changesAfter(int after) async => _list(
    await _get('/v1/changes', {'after': '$after', 'all': '1', 'limit': '500'}),
  ).map(Receipt.fromJson).toList();

  @override
  Future<Receipt> change(String txnId) async {
    final j = _map(await _get('/v1/changes/$txnId'));
    return Receipt.fromJson(_map(j['receipt']));
  }

  @override
  Future<Receipt> undo(String txnId, {bool force = false}) async =>
      Receipt.fromJson(
        _map(await _post('/v1/changes/$txnId/undo', {'force': force})),
      );

  @override
  Future<Receipt> commands(List<Map<String, dynamic>> ops) async =>
      Receipt.fromJson(_map(await _post('/v1/commands', {'ops': ops})));

  @override
  Future<List<ConversationSummary>> conversations() async =>
      _list(await _get('/v1/conversations'))
          .map(ConversationSummary.fromJson)
          .toList();

  @override
  Future<Conversation> createConversation({String title = ''}) async =>
      Conversation.fromJson(
        _map(
          await _post('/v1/conversations', {
            if (title.isNotEmpty) 'title': title,
          }),
        ),
      );

  @override
  Future<Conversation> conversation(String id) async =>
      Conversation.fromJson(_map(await _get('/v1/conversations/$id')));

  @override
  Future<ChatReply> sendMessage(
    String conversationId,
    String text, {
    String? id,
  }) async => ChatReply.fromJson(
    _map(
      await _post('/v1/conversations/$conversationId/messages', {
        'text': text,
        'id': ?id,
      }),
    ),
  );

  @override
  String exportUrl(String format) =>
      _u('/v1/export', {'format': format}).toString();

  @override
  String backupUrl() => _u('/v1/backup').toString();

  @override
  Future<ServerSettings> settings() async =>
      ServerSettings.fromJson(_map(await _get('/v1/settings')));

  @override
  Future<ServerSettings> updateSettings(Map<String, dynamic> patch) async {
    final res = await _client
        .put(
          _u('/v1/settings'),
          headers: {..._headers, 'Content-Type': 'application/json'},
          body: jsonEncode(patch),
        )
        .timeout(const Duration(seconds: 60));
    return ServerSettings.fromJson(_map(_decode(res)));
  }

  @override
  Future<ProbeResult> testProvider(String provider, {String? model}) async =>
      ProbeResult.fromJson(
        _map(
          await _post('/v1/settings/test', {
            'provider': provider,
            'model': ?model,
          }),
        ),
      );

  @override
  Future<ModelList> setupModels(
    String provider, {
    String? apiKey,
    String? baseUrl,
  }) async {
    if (apiKey != null && apiKey.isNotEmpty) {
      return ModelList.fromJson(
        _map(
          await _post('/v1/setup/models', {
            'provider': provider,
            'api_key': apiKey,
            'base_url': ?baseUrl,
          }),
        ),
      );
    }
    return ModelList.fromJson(
      _map(await _get('/v1/setup/models', {'provider': provider})),
    );
  }

  @override
  Future<String> transcribe(Uint8List wav, {String? language}) async {
    final req = http.MultipartRequest('POST', _u('/v1/transcribe'))
      ..headers.addAll(_headers)
      ..files.add(
        http.MultipartFile.fromBytes(
          'audio',
          wav,
          filename: 'dictation.wav',
          contentType: MediaType('audio', 'wav'),
        ),
      );
    if (language != null && language.isNotEmpty) {
      req.fields['language'] = language;
    }
    final streamed = await _client
        .send(req)
        .timeout(const Duration(minutes: 3));
    final res = await http.Response.fromStream(streamed);
    final j = _map(_decode(res));
    return j['text'] is String ? j['text'] as String : '';
  }

  @override
  Future<String> oauthStart(String provider) async {
    final j = _map(
      await _post('/v1/setup/oauth/start', {'provider': provider}),
    );
    return j['url'] is String ? j['url'] as String : '';
  }

  @override
  Stream<ServerEvent> events() {
    if (!_sseRunning) {
      _sseRunning = true;
      _runSse();
    }
    return _sseController.stream;
  }

  /// Connection state of the event stream, as a broadcast of booleans.
  final connected = StreamController<bool>.broadcast();

  Future<void> _runSse() async {
    var backoff = const Duration(seconds: 1);
    while (!_closed) {
      try {
        final req = http.Request(
          'GET',
          _u('/v1/events', token.isNotEmpty ? {'token': token} : null),
        );
        req.headers.addAll({..._headers, 'Accept': 'text/event-stream'});
        final res = await _client.send(req);
        if (res.statusCode >= 400) {
          throw ApiException(res.statusCode, 'sse', 'event stream refused');
        }
        if (!_closed) connected.add(true);
        backoff = const Duration(seconds: 1);
        var event = '';
        int? frameId;
        final data = StringBuffer();
        await for (final line
            in res.stream
                .transform(utf8.decoder)
                .transform(const LineSplitter())) {
          if (_closed) break;
          if (line.isEmpty) {
            if (event.isNotEmpty && data.isNotEmpty) {
              try {
                final j = jsonDecode(data.toString());
                if (j is Map<String, dynamic>) {
                  final payload = j['payload'];
                  if (_closed) break;
                  _sseController.add(
                    ServerEvent(
                      event,
                      payload is Map<String, dynamic> ? payload : j,
                      frameId,
                    ),
                  );
                }
              } catch (_) {}
            }
            event = '';
            frameId = null;
            data.clear();
          } else if (line.startsWith('id:')) {
            frameId = int.tryParse(line.substring(3).trim());
          } else if (line.startsWith('event:')) {
            event = line.substring(6).trim();
          } else if (line.startsWith('data:')) {
            data.write(line.substring(5).trim());
          }
        }
      } catch (_) {
        // fall through to reconnect
      }
      if (!_closed) connected.add(false);
      if (_closed) break;
      await Future<void>.delayed(backoff);
      backoff = backoff * 2 > const Duration(seconds: 30)
          ? const Duration(seconds: 30)
          : backoff * 2;
    }
  }

  void close() {
    _closed = true;
    _client.close();
    _sseController.close();
    connected.close();
  }
}
