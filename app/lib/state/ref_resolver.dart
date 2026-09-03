import 'dart:async';

import 'package:flutter/foundation.dart';

import '../api/client.dart';
import '../api/models.dart';
import '../ui/blocks/ref_labels.dart';

/// Resolves object ids to titles in batches (GET /v1/objects?ids=…) and
/// caches them, so citation chips and provenance chips show names.
class RefResolver extends ChangeNotifier implements RefLabelSource {
  RefResolver(this.api);
  final FundusApi api;
  final Map<String, LinkRef> _cache = {};
  final Set<String> _pending = {};
  Timer? _flush;

  LinkRef? get(String id) => _cache[id];

  /// Title for chips. Captures show the start of their text plus how long
  /// ago they were captured, since they have no title of their own.
  @override
  String? labelFor(String id) {
    final r = _cache[id];
    if (r == null) return null;
    if (r.type == 'capture') {
      final text = (r.preview.isNotEmpty ? r.preview : r.title)
          .replaceAll(RegExp(r'\s+'), ' ')
          .trim();
      final head = text.length > 40 ? '${text.substring(0, 39)}…' : text;
      final when = _ago(r.createdAt);
      return when.isEmpty ? head : '$head · $when';
    }
    return r.title;
  }

  static String _ago(DateTime? t) {
    if (t == null) return '';
    final d = DateTime.now().difference(t);
    if (d.inMinutes < 1) return 'just now';
    if (d.inMinutes < 60) {
      return '${d.inMinutes} minute${d.inMinutes == 1 ? '' : 's'} ago';
    }
    if (d.inHours < 24) {
      return '${d.inHours} hour${d.inHours == 1 ? '' : 's'} ago';
    }
    if (d.inDays < 7) return '${d.inDays} day${d.inDays == 1 ? '' : 's'} ago';
    return '${t.year}-${t.month.toString().padLeft(2, '0')}-${t.day.toString().padLeft(2, '0')}';
  }

  /// Queues ids for resolution; unknown ones are fetched in one request.
  @override
  void request(Iterable<String> ids) {
    var added = false;
    for (final id in ids) {
      if (id.isEmpty || _cache.containsKey(id) || _pending.contains(id)) {
        continue;
      }
      _pending.add(id);
      added = true;
    }
    if (added) {
      _flush ??= Timer(const Duration(milliseconds: 30), _run);
    }
  }

  /// Forgets a cached title (after a rename) so it is fetched again.
  void invalidate(String id) {
    _cache.remove(id);
    notifyListeners();
  }

  /// Applies a fresh title without a round trip.
  void put(LinkRef ref) {
    _cache[ref.id] = ref;
    notifyListeners();
  }

  Future<void> _run() async {
    _flush = null;
    final batch = _pending.take(200).toList();
    _pending.removeAll(batch);
    if (batch.isEmpty) return;
    try {
      for (final r in await api.resolve(batch)) {
        _cache[r.id] = r;
      }
      for (final id in batch) {
        _cache.putIfAbsent(
          id,
          () => LinkRef(id: id, type: id.split('_').first, title: ''),
        );
      }
      notifyListeners();
    } catch (_) {
      // leave unresolved; a later request retries
    }
    if (_pending.isNotEmpty) {
      _flush ??= Timer(const Duration(milliseconds: 30), _run);
    }
  }

  @override
  void dispose() {
    _flush?.cancel();
    super.dispose();
  }
}
