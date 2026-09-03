import 'dart:io' show Platform;

import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:provider/provider.dart';
import 'package:window_manager/window_manager.dart';

import 'api/client.dart';
import 'desktop/daemon_launcher.dart';
import 'state/app_state.dart';
import 'state/chat_state.dart';
import 'state/settings.dart';
import 'ui/app_shell.dart';
import 'ui/quick_capture.dart';
import 'ui/theme.dart';

bool _isLoopback(String url) {
  final h = Uri.tryParse(url)?.host ?? '';
  return h == '127.0.0.1' || h == 'localhost' || h == '::1';
}

bool get _isDesktop =>
    !kIsWeb && (Platform.isLinux || Platform.isMacOS || Platform.isWindows);

Future<void> main(List<String> args) async {
  WidgetsFlutterBinding.ensureInitialized();
  final settings = await Settings.load();
  final quick = args.contains('--quick-capture');

  if (_isDesktop) {
    await windowManager.ensureInitialized();
    final opts = quick
        ? const WindowOptions(
            size: Size(560, 220),
            minimumSize: Size(420, 180),
            center: true,
            title: 'Fundus capture',
            alwaysOnTop: true,
            skipTaskbar: true,
          )
        : const WindowOptions(
            size: Size(1320, 860),
            minimumSize: Size(420, 480),
            center: true,
            title: 'Fundus',
          );
    await windowManager.waitUntilReadyToShow(opts, () async {
      await windowManager.show();
      await windowManager.focus();
    });
  }

  if (quick) {
    final api = HttpFundusApi(
      baseUrl: settings.serverUrl,
      token: settings.token,
    );
    runApp(QuickCaptureApp(api: api, themeMode: settings.themeMode));
    return;
  }

  String? arg(String name) {
    for (final a in args) {
      if (a.startsWith('--$name=')) return a.substring(name.length + 3);
    }
    return null;
  }

  // `--server=http://host:port` pins the daemon address for this run
  // (integration tests, kiosk setups); it is not persisted.
  final server = arg('server');
  if (server != null && server.isNotEmpty) settings.overrideServer(server);

  runApp(
    FundusApp(
      settings: settings,
      initialView: arg('view'),
      initialOpen: arg('open'),
      daemonPath: arg('daemon-path'),
    ),
  );
}

/// Root: rebuilds the API + state when the server settings change.
class FundusApp extends StatelessWidget {
  const FundusApp({
    super.key,
    required this.settings,
    this.initialView,
    this.initialOpen,
    this.daemonPath,
  });
  final Settings settings;

  /// `--view=relevant`: start in a view (inbox, relevant, open, ideas, notes, topics, waiting, later, changes, conversation).
  final String? initialView;

  /// `--open=<id>`: start with an object selected.
  final String? initialOpen;

  /// `--daemon-path=/path/to/fundus`: binary to start when no daemon answers.
  final String? daemonPath;

  @override
  Widget build(BuildContext context) {
    return ChangeNotifierProvider<Settings>.value(
      value: settings,
      child: Consumer<Settings>(
        builder: (context, s, _) {
          final api = HttpFundusApi(baseUrl: s.serverUrl, token: s.token);
          return MultiProvider(
            key: ValueKey('${s.serverUrl}|${s.token}'),
            providers: [
              ChangeNotifierProvider<AppState>(
                create: (_) {
                  final launcher = _isDesktop
                      ? DaemonLauncher(override: daemonPath)
                      : null;
                  return AppState(
                    api,
                    // Only a local Fundus can be started for the user.
                    daemonStarter: launcher != null && _isLoopback(s.serverUrl)
                        ? launcher.start
                        : null,
                    daemonLogPath: launcher?.logPath,
                    instanceStore: s,
                  );
                },
              ),
              ChangeNotifierProvider<ChatState>(create: (_) => ChatState(api)),
            ],
            child: MaterialApp(
              title: 'Fundus',
              debugShowCheckedModeBanner: false,
              theme: FundusTheme.light(),
              darkTheme: FundusTheme.dark(),
              themeMode: s.themeMode,
              home: AppShell(
                initialView: initialView,
                initialOpen: initialOpen,
              ),
            ),
          );
        },
      ),
    );
  }
}
