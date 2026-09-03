import 'dart:io';

import 'package:fundus_app/api/models.dart';
import 'package:fundus_app/ui/widgets/common.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test('describeError maps failures to plain sentences', () {
    expect(
      describeError(
        const SocketException('refused'),
        serverUrl: 'http://127.0.0.1:7433',
      ),
      "Fundus is not running at http://127.0.0.1:7433.",
    );
    expect(
      describeError(const ApiException(401, 'unauthorized', 'x')),
      'The token was rejected.',
    );
    expect(
      describeError(const ApiException(409, 'conflict', 'x')),
      'This item changed meanwhile. Reloaded.',
    );
    expect(
      describeError(const ApiException(409, 'busy', 'x')),
      'Fundus is still working on the previous turn.',
    );
    expect(
      describeError(const ApiException(409, 'processing', 'x')),
      'Still being processed, wait a moment.',
    );
    expect(
      describeError(const ApiException(404, 'not_found', 'x')),
      'That item no longer exists.',
    );
    expect(
      describeError(const ApiException(400, 'invalid', 'topic name required')),
      'Topic name required.',
    );
    expect(
      describeError(const ApiException(500, 'internal', 'boom')),
      'Fundus hit an internal error: Boom.',
    );
    expect(
      describeError(const ApiException(0, 'sse', 'x')),
      "Fundus is not running or cannot be reached.",
    );
    expect(describeError(StateError('raw')), 'Something went wrong.');
    expect(describeError('Already a sentence.'), 'Already a sentence.');
  });

  test('dayLabel groups by day', () {
    final now = DateTime.now();
    expect(dayLabel(now), 'Today');
    expect(dayLabel(now.subtract(const Duration(days: 1))), 'Yesterday');
    expect(timeAgo(now.subtract(const Duration(days: 3))), '3 days ago');
    expect(timeAgo(now.subtract(const Duration(hours: 1))), '1 hour ago');
    final past = now.subtract(const Duration(days: 3));
    expect(
      dueLabel(
        '${past.year}-${past.month.toString().padLeft(2, '0')}-${past.day.toString().padLeft(2, '0')}',
      ),
      '3 days overdue',
    );
    expect(dayLabel(DateTime(2020, 1, 2)), isNot('Today'));
  });
}
