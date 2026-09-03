import 'dart:math';

const _alphabet = '0123456789ABCDEFGHJKMNPQRSTVWXYZ';
final _rng = Random.secure();

/// A ULID (Crockford base32, 48-bit time + 80-bit randomness), matching the
/// ids the daemon generates. Used for client-side idempotency keys.
String ulid([DateTime? now]) {
  final ms = (now ?? DateTime.now()).toUtc().millisecondsSinceEpoch;
  final chars = List<String>.filled(26, '0');
  var t = ms;
  for (var i = 9; i >= 0; i--) {
    chars[i] = _alphabet[t & 31];
    t >>= 5;
  }
  for (var i = 10; i < 26; i++) {
    chars[i] = _alphabet[_rng.nextInt(32)];
  }
  return chars.join();
}

/// A fresh client-generated capture id (`cap_<ULID>`).
String newCaptureId() => 'cap_${ulid()}';
