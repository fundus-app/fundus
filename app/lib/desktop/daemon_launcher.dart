import 'dart:io';

import 'package:flutter/foundation.dart';

/// Finds the `fundus` daemon binary: an explicit override, then next to the
/// app executable, then on PATH (absolute directories only). Pure so it can
/// be unit-tested.
String? resolveDaemonPath({
  String? override,
  required String exeDir,
  required List<String> pathDirs,
  required bool Function(String path) exists,
  String name = 'fundus',
  bool windows = false,
}) {
  final sep = windows ? '\\' : '/';
  bool absolute(String d) => windows
      ? RegExp(r'^[A-Za-z]:[\\/]').hasMatch(d) || d.startsWith('\\\\')
      : d.startsWith('/');
  final candidates = <String>[
    if (override != null && override.isNotEmpty) override,
    '$exeDir$sep$name',
    if (windows) '$exeDir$sep$name.exe',
    for (final d in pathDirs)
      if (d.isNotEmpty && absolute(d)) ...[
        '$d$sep$name',
        if (windows) '$d$sep$name.exe',
      ],
  ];
  for (final c in candidates) {
    if (exists(c)) return c;
  }
  return null;
}

/// Where the daemon writes its log when started by the app.
String daemonLogPath({
  required Map<String, String> env,
  required String home,
  bool windows = false,
  bool macos = false,
}) {
  if (windows) {
    final base = env['LOCALAPPDATA'] ?? '$home\\AppData\\Local';
    return '$base\\fundus\\fundus.log';
  }
  if (macos) return '$home/Library/Logs/fundus.log';
  final state = env['XDG_STATE_HOME'];
  final base = state == null || state.isEmpty ? '$home/.local/state' : state;
  return '$base/fundus/fundus.log';
}

/// Starts the daemon detached so it outlives the app.
class DaemonLauncher {
  DaemonLauncher({this.override});
  final String? override;

  /// The binary that was started, once started.
  String? started;

  bool get supported =>
      !kIsWeb && (Platform.isLinux || Platform.isMacOS || Platform.isWindows);

  String get logPath => daemonLogPath(
    env: Platform.environment,
    home:
        Platform.environment['HOME'] ??
        Platform.environment['USERPROFILE'] ??
        '',
    windows: Platform.isWindows,
    macos: Platform.isMacOS,
  );

  String? resolve() {
    if (!supported) return null;
    final exe = File(Platform.resolvedExecutable);
    final pathVar = Platform.environment['PATH'] ?? '';
    return resolveDaemonPath(
      override: override,
      exeDir: exe.parent.path,
      pathDirs: pathVar.split(Platform.isWindows ? ';' : ':'),
      exists: (p) => File(p).existsSync(),
      windows: Platform.isWindows,
    );
  }

  /// Runs `fundus serve --log-file <log>` detached. Throws with a message
  /// that names what to run by hand when no binary is found.
  Future<String> start() async {
    final path = resolve();
    if (path == null) {
      throw StateError(
        'No fundus binary was found next to the app or on PATH. '
        'Start it yourself with fundus serve, or pass --daemon-path=/path/to/fundus.',
      );
    }
    final log = logPath;
    try {
      Directory(File(log).parent.path).createSync(recursive: true);
    } catch (_) {}
    await Process.start(path, [
      'serve',
      '--log-file',
      log,
    ], mode: ProcessStartMode.detached);
    started = path;
    return path;
  }
}
