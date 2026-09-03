import 'package:flutter/widgets.dart';

/// Supplies titles for object ids to [RefChip]s below it, without coupling
/// the block renderer to application state.
abstract class RefLabelSource implements Listenable {
  String? labelFor(String id);
  void request(Iterable<String> ids);
}

class RefLabels extends InheritedNotifier<Listenable> {
  const RefLabels({super.key, required this.source, required super.child})
    : super(notifier: source);

  final RefLabelSource source;

  static RefLabelSource? maybeOf(BuildContext context) =>
      context.dependOnInheritedWidgetOfExactType<RefLabels>()?.source;
}
