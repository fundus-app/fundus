// The inspector follows the event stream: a committed transaction that
// touches members of the open topic, or the open note itself, refetches
// the open detail (debounced) without the user reopening it.
import 'package:fake_async/fake_async.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:fundus_app/api/models.dart';
import 'package:fundus_app/state/app_state.dart';

import 'fake_api.dart';

ServerEvent _committed(
  List<String> touched, {
  String cause = 'capture',
  List<String> affected = const [],
}) => ServerEvent('txn.committed', {
  'txn_id': 'txn1',
  'seq': 7,
  'cause': {'kind': cause},
  'touched': touched,
  'affected': affected,
  'lines': [
    for (final id in touched)
      {'text': 'Linked note to Fundus', 'object_id': id},
  ],
}, 7);

void main() {
  test('txn.committed touching a note reloads the open topic page', () {
    fakeAsync((async) {
      final api = FakeApi()..objectTypes['t1'] = 'topic';
      final state = AppState(api);
      async.flushMicrotasks();
      state.select('t1');
      async.flushMicrotasks();
      expect(state.topicPage?.topic.meta.id, 't1');
      final before = api.topicRequests.length;

      // The daemon links two earlier notes to the topic: two events, one GET.
      api.eventBus.add(_committed(['n1']));
      api.eventBus.add(_committed(['n2']));
      async.flushMicrotasks();
      expect(api.topicRequests.length, before, reason: 'debounced');
      async.elapse(const Duration(milliseconds: 300));
      expect(api.topicRequests.length, before + 1);
      expect(api.objectRequests.last, 't1');
      state.dispose();
    });
  });

  test('txn.committed touching the open note reloads it', () {
    fakeAsync((async) {
      final api = FakeApi();
      final state = AppState(api);
      async.flushMicrotasks();
      state.select('n1');
      async.flushMicrotasks();
      final before = api.objectRequests.length;
      api.eventBus.add(_committed(['n1']));
      async.elapse(const Duration(milliseconds: 300));
      expect(api.objectRequests.length, before + 1);
      expect(api.objectRequests.last, 'n1');
      state.dispose();
    });
  });

  test('object.changed for a member of the open topic reloads the page', () {
    fakeAsync((async) {
      final api = FakeApi()..objectTypes['t1'] = 'topic';
      final state = AppState(api);
      async.flushMicrotasks();
      state.select('t1');
      async.flushMicrotasks();
      final before = api.topicRequests.length;
      api.eventBus.add(const ServerEvent('object.changed', {'id': 'n1'}));
      async.elapse(const Duration(milliseconds: 300));
      expect(api.topicRequests.length, before + 1);
      state.dispose();
    });
  });

  test('a pure conversation turn leaves the open detail alone', () {
    fakeAsync((async) {
      final api = FakeApi();
      final state = AppState(api);
      async.flushMicrotasks();
      state.select('n1');
      async.flushMicrotasks();
      final before = api.objectRequests.length;
      api.eventBus.add(_committed(const [], cause: 'conversation'));
      async.elapse(const Duration(milliseconds: 300));
      expect(api.objectRequests.length, before);
      state.dispose();
    });
  });

  test('affected topic in txn.committed reloads the open topic page', () {
    fakeAsync((async) {
      final api = FakeApi()..objectTypes['t1'] = 'topic';
      final state = AppState(api);
      async.flushMicrotasks();
      state.select('t1');
      async.flushMicrotasks();
      final before = api.topicRequests.length;
      // A conversation turn that linked a note into the topic.
      api.eventBus.add(
        _committed(['n1'], cause: 'conversation', affected: ['t1']),
      );
      async.elapse(const Duration(milliseconds: 300));
      expect(api.topicRequests.length, before + 1);
      state.dispose();
    });
  });

  test('object.changed with members: true reloads the open topic page', () {
    fakeAsync((async) {
      final api = FakeApi()..objectTypes['t1'] = 'topic';
      final state = AppState(api);
      async.flushMicrotasks();
      state.select('t1');
      async.flushMicrotasks();
      final before = api.topicRequests.length;
      api.eventBus.add(
        const ServerEvent('object.changed', {
          'id': 't1',
          'members': true,
          'rev': 3,
        }),
      );
      async.elapse(const Duration(milliseconds: 300));
      expect(api.topicRequests.length, before + 1);
      expect(state.removedNotice, isNull);
      state.dispose();
    });
  });

  test('Receipt parses affected topics', () {
    final r = Receipt.fromJson({
      'txn_id': 'x',
      'touched': ['n1'],
      'affected': ['t1', 't2'],
    });
    expect(r.touched, ['n1']);
    expect(r.affected, ['t1', 't2']);
    expect(Receipt.fromJson({'txn_id': 'y'}).affected, isEmpty);
  });
}
