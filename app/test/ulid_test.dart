import 'package:fundus_app/api/ulid.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test('ulid shape and ordering', () {
    final a = ulid(DateTime.utc(2026, 9, 3, 0, 0, 0));
    final b = ulid(DateTime.utc(2026, 9, 3, 0, 0, 1));
    expect(a.length, 26);
    expect(RegExp(r'^[0-9A-HJKMNP-TV-Z]{26}$').hasMatch(a), isTrue);
    expect(a.substring(0, 10).compareTo(b.substring(0, 10)) < 0, isTrue);
    expect(newCaptureId(), startsWith('cap_'));
    expect(newCaptureId(), isNot(newCaptureId()));
  });
}
