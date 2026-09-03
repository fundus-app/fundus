// Screenshot driver, not part of the suite (no `_test` suffix, so
// `flutter test integration_test` skips it). Run it explicitly on a Wayland
// display with grim installed and a daemon seeded with a topic that has an
// open task:
//   FUNDUS_SHOT_URL=http://127.0.0.1:7435 FUNDUS_SHOT_TOPIC=<topic id> \
//   FUNDUS_SHOT_OUT=/tmp/toast.png \
//   flutter test integration_test/shots/toast_shot.dart -d linux
// It opens the topic page, completes the first task and captures the
// resulting Undo toast.
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:fundus_app/main.dart';
import 'package:fundus_app/state/settings.dart';
import 'package:integration_test/integration_test.dart';
import 'package:window_manager/window_manager.dart';

Future<void> pumpUntil(WidgetTester tester, Finder f) async {
  final end = DateTime.now().add(const Duration(seconds: 30));
  while (DateTime.now().isBefore(end)) {
    await tester.pump(const Duration(milliseconds: 200));
    if (f.evaluate().isNotEmpty) return;
  }
  throw TestFailure('timed out waiting for $f');
}

void main() {
  IntegrationTestWidgetsFlutterBinding.ensureInitialized();
  final env = Platform.environment;
  testWidgets('toast screenshot', (tester) async {
    await tester.pumpWidget(
      FundusApp(
        settings: Settings.memory(serverUrl: env['FUNDUS_SHOT_URL']!),
        initialView: 'topics',
        initialOpen: env['FUNDUS_SHOT_TOPIC'],
      ),
    );
    try {
      await windowManager.ensureInitialized();
      await windowManager.setSize(const Size(1440, 900));
      await windowManager.setPosition(Offset.zero);
      for (var i = 0; i < 10; i++) {
        await tester.pump(const Duration(milliseconds: 100));
        if (tester.view.physicalSize.width / tester.view.devicePixelRatio >=
            1000) {
          break;
        }
      }
    } catch (e) {
      // ignore: avoid_print
      print('[shot] window_manager: $e');
    }
    final logical = tester.view.physicalSize / tester.view.devicePixelRatio;
    // ignore: avoid_print
    print('[shot] surface $logical');
    if (logical.width < 1000) {
      tester.view.physicalSize =
          const Size(1440, 900) * tester.view.devicePixelRatio;
      await tester.pump();
    }
    try {
      await pumpUntil(tester, find.text('SUMMARY'));
    } catch (e) {
      final texts = find
          .byType(Text)
          .evaluate()
          .map((e) => (e.widget as Text).data)
          .whereType<String>()
          .take(40)
          .join(' | ');
      // ignore: avoid_print
      print('[shot] texts: $texts');
      rethrow;
    }
    await pumpUntil(tester, find.bySemanticsLabel('Complete task'));
    await tester.tap(find.bySemanticsLabel('Complete task').first);
    await pumpUntil(tester, find.text('Undo'));
    for (var i = 0; i < 6; i++) {
      await tester.pump(const Duration(milliseconds: 100));
    }
    await tester.runAsync(() async {
      await Future<void>.delayed(const Duration(seconds: 1));
      final r = await Process.run('grim', [env['FUNDUS_SHOT_OUT']!]);
      // ignore: avoid_print
      print('[shot] grim exit ${r.exitCode} ${r.stderr}');
    });
    expect(find.text('Undo'), findsWidgets);
  });
}
