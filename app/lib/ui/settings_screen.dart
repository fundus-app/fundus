import 'package:flutter/material.dart';

import 'settings/about.dart';
import 'settings/appearance.dart';
import 'settings/connection.dart';
import 'settings/filing.dart';
import 'settings/models.dart';
import 'settings/providers.dart';
import 'settings/research.dart';
import 'settings_maintenance.dart';

export 'settings/rows.dart' show showSaved;

/// The sections of the settings surface, in rail order. Deep links use the
/// enum name: `settings/research`.
enum SettingsSection {
  connection('Connection'),
  models('Models'),
  providers('Providers'),
  research('Research'),
  maintenance('Maintenance'),
  filing('Filing'),
  appearance('Appearance'),
  about('About');

  const SettingsSection(this.label);
  final String label;

  static SettingsSection? fromName(String? name) {
    if (name == null) return null;
    final n = name.startsWith('settings/') ? name.substring(9) : name;
    return values.where((s) => s.name == n).firstOrNull;
  }
}

/// Settings: a 960 px surface (full screen on narrow windows) with a left
/// rail of sections and one scrolling page per section. Esc closes.
class SettingsScreen extends StatefulWidget {
  const SettingsScreen({super.key, this.initial = SettingsSection.connection});
  final SettingsSection initial;

  static Future<void> show(
    BuildContext context, {
    SettingsSection section = SettingsSection.connection,
  }) {
    final size = MediaQuery.sizeOf(context);
    final narrow = size.width < 720;
    return showDialog<void>(
      context: context,
      builder: (_) => narrow
          ? Dialog.fullscreen(child: SettingsScreen(initial: section))
          : Dialog(
              insetPadding: const EdgeInsets.all(24),
              clipBehavior: Clip.antiAlias,
              child: SizedBox(
                width: 960,
                height: size.height * 0.9,
                child: SettingsScreen(initial: section),
              ),
            ),
    );
  }

  @override
  State<SettingsScreen> createState() => _SettingsScreenState();
}

class _SettingsScreenState extends State<SettingsScreen> {
  late SettingsSection _section = widget.initial;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final scheme = theme.colorScheme;
    final narrow = MediaQuery.sizeOf(context).width < 720;
    final page = switch (_section) {
      SettingsSection.connection => const ConnectionSection(),
      SettingsSection.models => const ModelsSection(),
      SettingsSection.providers => const ProvidersSection(),
      SettingsSection.research => const ResearchSection(),
      SettingsSection.maintenance => const MaintenanceSection(),
      SettingsSection.filing => const FilingSection(),
      SettingsSection.appearance => const AppearanceSection(),
      SettingsSection.about => const AboutSection(),
    };
    final rail = Container(
      width: narrow ? null : 200,
      color: scheme.surfaceContainerLow,
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Padding(
            padding: const EdgeInsets.fromLTRB(20, 22, 12, 10),
            child: Text(
              'Settings',
              style: theme.textTheme.headlineMedium!.copyWith(
                fontSize: 22,
                height: 1.2,
              ),
            ),
          ),
          for (final s in SettingsSection.values)
            _NavItem(
              key: Key('settings-nav-${s.name}'),
              label: s.label,
              selected: s == _section,
              onTap: () => setState(() => _section = s),
            ),
        ],
      ),
    );
    if (narrow) {
      return Scaffold(
        appBar: AppBar(
          title: Text(_section.label),
          leading: IconButton(
            tooltip: 'Close (Esc)',
            icon: const Icon(Icons.close_rounded),
            onPressed: () => Navigator.of(context).maybePop(),
          ),
        ),
        body: Column(
          children: [
            SizedBox(
              height: 44,
              child: ListView(
                scrollDirection: Axis.horizontal,
                padding: const EdgeInsets.symmetric(horizontal: 8),
                children: [
                  for (final s in SettingsSection.values)
                    Padding(
                      padding: const EdgeInsets.symmetric(
                        horizontal: 4,
                        vertical: 6,
                      ),
                      child: ChoiceChip(
                        label: Text(s.label),
                        selected: s == _section,
                        onSelected: (_) => setState(() => _section = s),
                      ),
                    ),
                ],
              ),
            ),
            Expanded(child: page),
          ],
        ),
      );
    }
    return Material(
      color: scheme.surface,
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          rail,
          Expanded(
            child: Stack(
              children: [
                Positioned.fill(child: page),
                Positioned(
                  top: 12,
                  right: 12,
                  child: IconButton(
                    tooltip: 'Close (Esc)',
                    icon: const Icon(Icons.close_rounded),
                    onPressed: () => Navigator.of(context).maybePop(),
                  ),
                ),
              ],
            ),
          ),
        ],
      ),
    );
  }
}

class _NavItem extends StatelessWidget {
  const _NavItem({
    super.key,
    required this.label,
    required this.selected,
    required this.onTap,
  });
  final String label;
  final bool selected;
  final VoidCallback onTap;
  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final scheme = theme.colorScheme;
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 1),
      child: Material(
        color: selected ? scheme.surfaceContainerHighest : Colors.transparent,
        borderRadius: BorderRadius.circular(6),
        child: InkWell(
          onTap: onTap,
          borderRadius: BorderRadius.circular(6),
          child: Padding(
            padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
            child: Text(
              label,
              style: theme.textTheme.bodyMedium!.copyWith(
                fontWeight: selected ? FontWeight.w600 : null,
                color: selected ? scheme.onSurface : scheme.onSurfaceVariant,
              ),
            ),
          ),
        ),
      ),
    );
  }
}
