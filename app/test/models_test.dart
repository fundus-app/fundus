import 'dart:convert';

import 'package:fundus_app/api/models.dart';
import 'package:flutter_test/flutter_test.dart';

const captureJson = '''
{"id":"cap_01M1J4MFGVY4MD9AST55N8WTA3","type":"capture","rev":3,"created_at":"2026-09-03T00:43:01.5Z","updated_at":"2026-09-03T00:43:02Z",
 "text":"Ich muss beim Deye noch prüfen, warum der zweite String manchmal keinen Strom liefert.","source":"cli","status":"processed","attempts":1,
 "result":{"classification":"task","confidence":0.75,"summary":"Created a task.","provider":"fake","model":"heuristic","processed_at":"2026-09-03T00:43:02Z"},
 "receipts":[
  {"txn_id":"txn_1","seq":1,"at":"2026-09-03T00:43:01.5Z","actor":"user:cli","cause":{"kind":"user"},"lines":[{"op":"capture.create","object_id":"cap_01M1J4MFGVY4MD9AST55N8WTA3","object_type":"capture","text":"Captured \\"Ich muss…\\"."}],"summary":"Captured.","undoable":true},
  {"txn_id":"txn_2","seq":2,"at":"2026-09-03T00:43:01.7Z","actor":"system","cause":{"kind":"capture","id":"cap_01M1J4MFGVY4MD9AST55N8WTA3"},"lines":[],"summary":"No visible changes.","undoable":true,"quiet":true},
  {"txn_id":"txn_3","seq":3,"at":"2026-09-03T00:43:02Z","actor":"llm:triage/fake/heuristic","cause":{"kind":"capture","id":"cap_01M1J4MFGVY4MD9AST55N8WTA3"},"lines":[{"op":"task.create","object_id":"task_1","object_type":"task","text":"Created task \\"Deye prüfen\\". No due date."}],"summary":"Created task \\"Deye prüfen\\". No due date.","undoable":true}
 ]}
''';

const taskJson = '''
{"id":"task_1","type":"task","rev":1,"created_at":"2026-09-03T00:43:02Z","updated_at":"2026-09-03T00:43:02Z","text":"Deye prüfen","state":"open","due":"2026-09-10",
 "topics":["topic_1"],"origins":["cap_1"],"score":4.5,"reasons":["due this week","captured recently"],"topic_names":["Deye"]}
''';

const noteJson = '''
{"id":"note_1","type":"note","rev":2,"created_at":"2026-09-03T00:43:02Z","updated_at":"2026-09-03T00:43:02Z","kind":"idea","title":"LLM remote",
 "body":{"blocks":[{"id":"b_1","type":"heading","level":2,"text":"Plan"},{"id":"b_2","type":"paragraph","text":"Hardware **later**.","sources":["cap_1"],"pinned":true},
 {"id":"b_3","type":"list","items":["a","b"],"ordered":true},{"id":"b_4","type":"callout","kind":"warning","text":"careful"},{"id":"b_5","type":"code","lang":"go","text":"x := 1"},{"id":"b_6","type":"task_ref","ref":"task_1"}]},
 "topics":["topic_1"],"topic_names":["Hardware"],"preview":"Plan Hardware later."}
''';

const detailJson =
    '''
{"object":$noteJson,"receipts":[],"backlinks":[{"id":"task_1","type":"task","title":"Deye prüfen"}],"topics":[{"id":"topic_1","type":"topic","title":"Hardware"}],"markdown":"## Plan"}
''';

const replyJson = '''
{"conversation_id":"conv_1","message":{"id":"msg_2","type":"message","rev":1,"created_at":"2026-09-03T01:00:00Z","conversation_id":"conv_1","index":1,"role":"assistant","text":"Done [[note_1]]","blocks":{"blocks":[{"id":"b","type":"paragraph","text":"Done [[note_1]]"}]},"txn_ids":["txn_9"],"refs":["note_1"]},
 "receipts":[{"txn_id":"txn_9","summary":"Created note \\"X\\".","actor":"llm:chat/openai/gpt-5.5","lines":[]}],
 "steps":[{"kind":"tool_call","tool":"search","summary":"Searching for \\"x\\""},{"kind":"receipt","tool":"apply_operations","summary":"Created note","receipt":{"txn_id":"txn_9","summary":"Created note \\"X\\".","actor":"llm:chat/openai/gpt-5.5"}}],
 "usage":{"input_tokens":10,"output_tokens":5}}
''';

const conversationJson = '''
{"id":"conv_1","type":"conversation","rev":3,"created_at":"2026-09-02T23:24:11Z","updated_at":"2026-09-02T23:24:12Z","title":"hallo","message_count":3,"last_message_at":"2026-09-02T23:24:12Z",
 "messages":[
  {"id":"msg_1","type":"message","rev":1,"created_at":"2026-09-02T23:24:11Z","conversation_id":"conv_1","index":0,"role":"user","text":"hallo","blocks":{"blocks":[]},"capture_id":"cap_9"},
  {"id":"msg_2","type":"message","rev":1,"created_at":"2026-09-02T23:24:12Z","conversation_id":"conv_1","index":1,"role":"assistant","text":"_restarted_","blocks":{"blocks":[]},"interrupted":true},
  {"id":"msg_3","type":"message","rev":1,"created_at":"2026-09-02T23:24:13Z","conversation_id":"conv_1","index":2,"role":"assistant","text":"hi","blocks":{"blocks":[]},"refs":["note_1"]}
 ]}
''';

const parkedJson = '''
{"id":"cap_p","type":"capture","rev":2,"text":"Idee: Heizung mit Grafana","source":"cli","status":"needs_review",
 "result":{"classification":"unclear","reason":"unclear","confidence":0.4,"summary":"Could be either.","question":"Build it, or just a thought?",
  "proposal":[{"op":"note.create","kind":"idea","title":"Heizung mit Grafana","markdown":"x","topics":["topic_1","Solaranlage"]},{"op":"task.create","text":"Grafana-Panel bauen","due":"2026-09-10"}]},
 "receipts":[]}
''';

void main() {
  test('conversation with message objects', () {
    final c = Conversation.fromJson(
      jsonDecode(conversationJson) as Map<String, dynamic>,
    );
    expect(c.messageCount, 3);
    expect(c.lastMessageAt, isNotNull);
    expect(c.messages.length, 3);
    expect(c.messages[0].id, 'msg_1');
    expect(c.messages[0].captureId, 'cap_9');
    expect(c.messages[0].at, isNotNull);
    expect(c.messages[1].interrupted, isTrue);
    expect(c.messages[2].refs, ['note_1']);
    expect(c.messages[2].index, 2);
  });

  test('parked capture with proposal', () {
    final c = Capture.fromJson(jsonDecode(parkedJson) as Map<String, dynamic>);
    expect(c.hasProposal, isTrue);
    expect(c.canAccept, isTrue);
    expect(c.result!.reason, 'unclear');
    expect(c.result!.proposal.length, 2);
    expect(
      const ProposalOp(
        op: 'note.create',
        kind: 'idea',
        title: 'X',
        topics: ['Solaranlage'],
      ).describe(),
      'create idea “X” linked to new topic “Solaranlage”',
    );
    expect(
      const ProposalOp(op: 'note.append', noteId: 'note_01X').describe(),
      'add to the note',
    );
    final text = describeProposal(
      c.result!.proposal,
      (id) => id == 'topic_1' ? 'Heizung' : id,
    );
    expect(
      text,
      'Would create idea “Heizung mit Grafana” linked to “Heizung”, new topic “Solaranlage”; would create task “Grafana-Panel bauen” (due 2026-09-10).',
    );
    expect(c.result!.proposal[0].toJson()['topics'], [
      'topic_1',
      'Solaranlage',
    ]);
  });

  test('parked reasons and retrying', () {
    const low = CaptureResult(
      reason: 'low_confidence',
      summary: 'Would create an idea.',
    );
    expect(low.reason, 'low_confidence');
    const q = CaptureResult(reason: 'unclear', question: 'Which?');
    expect(q.question, 'Which?');
    final retrying = Capture(
      meta: const Meta(id: 'cap_r', type: 'capture'),
      text: 't',
      status: 'failed',
      result: const CaptureResult(error: 'down', retryable: true),
    );
    expect(retrying.isRetrying, isTrue);
    expect(retrying.canAccept, isFalse);
    expect(captureStatusLabel('needs_review'), 'Waiting for you');
    expect(captureStatusLabel('processing'), 'Being filed');
    expect(captureSourceLabel('desktop'), 'from the desktop app');
    expect(modelLabel('fake', 'heuristic'), 'rules');
    expect(modelLabel('openai', 'gpt-5.4-mini'), 'gpt-5.4-mini');
    expect(
      const Receipt(txnId: 't', actor: 'llm:triage/fake/heuristic').modelName,
      'rules',
    );
  });

  test('link ref with preview and created_at', () {
    final r = LinkRef.fromJson(const {
      'id': 'cap_1',
      'type': 'capture',
      'title': 'Ich muss…',
      'preview': 'Ich muss die Solaranlage prüfen',
      'state': 'processed',
      'created_at': '2026-09-03T00:00:00Z',
    });
    expect(r.preview, 'Ich muss die Solaranlage prüfen');
    expect(r.state, 'processed');
    expect(r.createdAt, isNotNull);
  });

  test('health with warnings', () {
    final h = Health.fromJson(const {
      'ok': true,
      'version': 'dev',
      'timezone': 'Europe/Berlin',
      'ui': true,
      'warnings': ['no API key'],
    });
    expect(h.timezone, 'Europe/Berlin');
    expect(h.ui, isTrue);
    expect(h.warnings, ['no API key']);
    expect(Health.fromJson(const {}).warnings, isEmpty);
  });

  test('receipt touched', () {
    final r = Receipt.fromJson(const {
      'txn_id': 't',
      'touched': ['note_1', 'topic_2'],
    });
    expect(r.touched, ['note_1', 'topic_2']);
  });

  test('capture with receipts', () {
    final c = Capture.fromJson(jsonDecode(captureJson) as Map<String, dynamic>);
    expect(c.id, 'cap_01M1J4MFGVY4MD9AST55N8WTA3');
    expect(c.status, 'processed');
    expect(c.isOpen, isFalse);
    expect(c.result!.classification, 'task');
    expect(c.receipts.length, 3);
    expect(c.receipts[1].quiet, isTrue);
    final f = c.filingReceipt!;
    expect(f.txnId, 'txn_3');
    expect(f.isModel, isTrue);
    expect(f.actorLabel, 'triage');
    expect(f.modelName, 'rules');
    expect(f.causeId, c.id);
    expect(c.meta.createdAt, isNotNull);
  });

  test('task view', () {
    final t = Task.fromJson(jsonDecode(taskJson) as Map<String, dynamic>);
    expect(t.text, 'Deye prüfen');
    expect(t.score, 4.5);
    expect(t.reasons, ['due this week', 'captured recently']);
    expect(t.topicNames, ['Deye']);
    expect(t.due, '2026-09-10');
    expect(t.meta.rev, 1);
  });

  test('note with typed blocks and markdown round trip', () {
    final n = Note.fromJson(jsonDecode(noteJson) as Map<String, dynamic>);
    expect(n.kind, 'idea');
    expect(n.body.blocks.length, 6);
    expect(n.body.blocks[1].pinned, isTrue);
    expect(n.body.blocks[1].sources, ['cap_1']);
    expect(n.body.blocks[2].ordered, isTrue);
    expect(n.body.blocks[0].toMarkdown(), '## Plan');
    expect(n.body.blocks[2].toMarkdown(), '1. a\n2. b');
    expect(n.body.blocks[3].toMarkdown(), '> [!warning] careful');
    expect(n.body.blocks[4].toMarkdown(), '```go\nx := 1\n```');
    expect(n.body.blocks[5].toMarkdown(), '[[task_1]]');
    expect(n.topicNames, ['Hardware']);
  });

  test('object detail', () {
    final d = ObjectDetail.fromJson(
      jsonDecode(detailJson) as Map<String, dynamic>,
    );
    expect(d.type, 'note');
    expect(d.note!.title, 'LLM remote');
    expect(d.backlinks.single.title, 'Deye prüfen');
    expect(d.topics.single.id, 'topic_1');
    expect(d.markdown, '## Plan');
    expect(d.title, 'LLM remote');
  });

  test('chat reply with steps', () {
    final r = ChatReply.fromJson(jsonDecode(replyJson) as Map<String, dynamic>);
    expect(r.message.refs, ['note_1']);
    expect(r.message.blocks.blocks.length, 1);
    expect(r.steps.length, 2);
    expect(r.steps[1].receipt!.txnId, 'txn_9');
    expect(r.receipts.single.actorLabel, 'chat');
  });

  test('tolerates missing fields', () {
    final c = Capture.fromJson(const {'id': 'cap_x'});
    expect(c.text, '');
    expect(c.status, 'pending');
    expect(c.receipts, isEmpty);
    final r = Receipt.fromJson(const {});
    expect(r.undoable, isTrue);
    expect(r.actorLabel, 'system');
    expect(Doc.fromJson(null).blocks, isEmpty);
    expect(Topic.fromJson(const {'id': 'topic_x', 'name': 'X'}).kind, 'topic');
  });
}
