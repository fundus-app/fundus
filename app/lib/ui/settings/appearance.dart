import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../../state/settings.dart';
import 'rows.dart';

/// Appearance: the theme.
class AppearanceSection extends StatelessWidget {
  const AppearanceSection({super.key});
  @override
  Widget build(BuildContext context) {
    final settings = context.watch<Settings>();
    return SettingsPage(
      title: 'Appearance',
      children: [
        SettingsRow(
          label: 'Theme',
          hint: 'Paper is light, Ink is dark.',
          trailing: SegmentedButton<ThemeMode>(
            key: const Key('appearance-theme'),
            segments: const [
              ButtonSegment(value: ThemeMode.system, label: Text('System')),
              ButtonSegment(value: ThemeMode.light, label: Text('Paper')),
              ButtonSegment(value: ThemeMode.dark, label: Text('Ink')),
            ],
            selected: {settings.themeMode},
            showSelectedIcon: false,
            style: const ButtonStyle(visualDensity: VisualDensity.compact),
            onSelectionChanged: (s) async {
              await settings.setThemeMode(s.first);
              if (context.mounted) showSaved(context);
            },
          ),
        ),
      ],
    );
  }
}
