import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'app_state.dart' show InstanceStore;

/// Persisted user settings.
class Settings extends ChangeNotifier implements InstanceStore {
  Settings._(this._prefs);

  static Future<Settings> load() async =>
      Settings._(await SharedPreferences.getInstance());

  /// In-memory settings for tests, optionally pinned to a server.
  Settings.memory({String? serverUrl}) : _prefs = null, _override = serverUrl;

  final SharedPreferences? _prefs;
  String? _override;

  /// Pins the server address for this process (launch arg `--server=`).
  void overrideServer(String url) {
    _override = url;
    notifyListeners();
  }

  String get serverUrl =>
      _override ?? _prefs?.getString('server_url') ?? defaultServerUrl();
  String get token => _prefs?.getString('token') ?? '';
  ThemeMode? _themeOverride;

  /// Forces a theme for this process (launch arg `--theme=`).
  void overrideTheme(ThemeMode m) {
    _themeOverride = m;
    notifyListeners();
  }

  ThemeMode get themeMode {
    if (_themeOverride != null) return _themeOverride!;
    switch (_prefs?.getString('theme')) {
      case 'light':
        return ThemeMode.light;
      case 'dark':
        return ThemeMode.dark;
      default:
        return ThemeMode.system;
    }
  }

  /// On the web the daemon that served the page is the server; on desktop the
  /// local daemon on its default port.
  static String defaultServerUrl() {
    if (kIsWeb) {
      final origin = Uri.base.origin;
      if (origin.startsWith('http')) return origin;
    }
    return 'http://127.0.0.1:7433';
  }

  Future<void> setServer(String url, String token) async {
    var u = url.trim();
    while (u.endsWith('/')) {
      u = u.substring(0, u.length - 1);
    }
    if (u.isEmpty) u = defaultServerUrl();
    await _prefs?.setString('server_url', u);
    await _prefs?.setString('token', token.trim());
    notifyListeners();
  }

  final Map<String, String> _memInstance = {};

  /// The Fundus instance id first seen at [url] (empty when unknown).
  @override
  String instanceFor(String url) =>
      _prefs?.getString('instance:$url') ?? _memInstance[url] ?? '';

  @override
  Future<void> setInstance(String url, String id) async {
    _memInstance[url] = id;
    await _prefs?.setString('instance:$url', id);
  }

  Future<void> setThemeMode(ThemeMode m) async {
    await _prefs?.setString('theme', m.name);
    notifyListeners();
  }
}
