import 'package:flutter/material.dart';

/// Fundus's visual language: warm paper in light mode, deep ink in dark
/// mode, one ochre accent, editorial type (Fraunces for titles, Inter for
/// text, JetBrains Mono for identifiers).
class FundusTheme {
  static const body = 'Inter';
  static const display = 'Fraunces';
  static const mono = 'JetBrainsMono';

  static const accentLight = Color(0xFFB8620E);
  static const accentDark = Color(0xFFE9A653);

  static ThemeData light() => _build(Brightness.light);
  static ThemeData dark() => _build(Brightness.dark);

  static ThemeData _build(Brightness b) {
    final isDark = b == Brightness.dark;
    final accent = isDark ? accentDark : accentLight;
    final scheme = ColorScheme(
      brightness: b,
      primary: accent,
      onPrimary: isDark ? const Color(0xFF1A1206) : Colors.white,
      secondary: isDark ? const Color(0xFF8FB8B0) : const Color(0xFF2E6B63),
      onSecondary: isDark ? const Color(0xFF0E1A18) : Colors.white,
      error: isDark ? const Color(0xFFE28B8B) : const Color(0xFFA33A3A),
      onError: Colors.white,
      surface: isDark ? const Color(0xFF15161B) : const Color(0xFFFCFAF5),
      onSurface: isDark ? const Color(0xFFECEAE3) : const Color(0xFF1E1C19),
      surfaceContainerLowest: isDark
          ? const Color(0xFF101115)
          : const Color(0xFFFFFFFF),
      surfaceContainerLow: isDark
          ? const Color(0xFF1A1B21)
          : const Color(0xFFF8F5EE),
      surfaceContainer: isDark
          ? const Color(0xFF1F2127)
          : const Color(0xFFF2EEE5),
      surfaceContainerHigh: isDark
          ? const Color(0xFF262830)
          : const Color(0xFFECE7DC),
      surfaceContainerHighest: isDark
          ? const Color(0xFF2D3039)
          : const Color(0xFFE4DED1),
      onSurfaceVariant: isDark
          ? const Color(0xFF9E9B93)
          : const Color(0xFF6A665E),
      outline: isDark ? const Color(0xFF3A3D47) : const Color(0xFFD8D2C5),
      outlineVariant: isDark
          ? const Color(0xFF2A2C34)
          : const Color(0xFFE9E4D9),
      inverseSurface: isDark
          ? const Color(0xFFECEAE3)
          : const Color(0xFF2A2825),
      onInverseSurface: isDark
          ? const Color(0xFF1E1C19)
          : const Color(0xFFF8F5EE),
      primaryContainer: isDark
          ? const Color(0xFF3D2A12)
          : const Color(0xFFFBE7CF),
      onPrimaryContainer: isDark
          ? const Color(0xFFF3C58A)
          : const Color(0xFF5A2E04),
      secondaryContainer: isDark
          ? const Color(0xFF1E3330)
          : const Color(0xFFD9ECE8),
      onSecondaryContainer: isDark
          ? const Color(0xFFB8DAD3)
          : const Color(0xFF15403A),
      tertiary: isDark ? const Color(0xFFB9A7E0) : const Color(0xFF5B4B8A),
      onTertiary: Colors.white,
      tertiaryContainer: isDark
          ? const Color(0xFF2B2440)
          : const Color(0xFFE6DFF5),
      onTertiaryContainer: isDark
          ? const Color(0xFFD9CDF5)
          : const Color(0xFF2E2350),
      errorContainer: isDark
          ? const Color(0xFF3F1F1F)
          : const Color(0xFFF8DADA),
      onErrorContainer: isDark
          ? const Color(0xFFF0B3B3)
          : const Color(0xFF5A1C1C),
      shadow: Colors.black,
      scrim: Colors.black,
      surfaceTint: accent,
    );

    final text = _textTheme2(scheme);
    final base = ThemeData(
      useMaterial3: true,
      brightness: b,
      colorScheme: scheme,
      scaffoldBackgroundColor: scheme.surface,
      fontFamily: body,
      textTheme: text,
      visualDensity: VisualDensity.standard,
      splashFactory: InkSparkle.splashFactory,
    );
    return base.copyWith(
      appBarTheme: AppBarTheme(
        backgroundColor: scheme.surface,
        foregroundColor: scheme.onSurface,
        elevation: 0,
        scrolledUnderElevation: 0,
        centerTitle: false,
        titleTextStyle: text.titleLarge,
      ),
      dividerTheme: DividerThemeData(
        color: scheme.outlineVariant,
        thickness: 1,
        space: 1,
      ),
      cardTheme: CardThemeData(
        color: scheme.surfaceContainerLowest,
        elevation: 0,
        margin: EdgeInsets.zero,
        shape: RoundedRectangleBorder(
          borderRadius: BorderRadius.circular(10),
          side: BorderSide(color: scheme.outlineVariant),
        ),
      ),
      chipTheme: ChipThemeData(
        backgroundColor: scheme.surfaceContainer,
        side: BorderSide.none,
        labelStyle: text.labelMedium,
        padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
        shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(6)),
      ),
      inputDecorationTheme: InputDecorationTheme(
        filled: true,
        fillColor: scheme.surfaceContainerLowest,
        border: OutlineInputBorder(
          borderRadius: BorderRadius.circular(8),
          borderSide: BorderSide(color: scheme.outline),
        ),
        enabledBorder: OutlineInputBorder(
          borderRadius: BorderRadius.circular(8),
          borderSide: BorderSide(color: scheme.outline),
        ),
        focusedBorder: OutlineInputBorder(
          borderRadius: BorderRadius.circular(8),
          borderSide: BorderSide(color: scheme.primary, width: 1.5),
        ),
        contentPadding: const EdgeInsets.symmetric(
          horizontal: 12,
          vertical: 10,
        ),
        hintStyle: text.bodyMedium?.copyWith(color: scheme.onSurfaceVariant),
      ),
      filledButtonTheme: FilledButtonThemeData(
        style: FilledButton.styleFrom(
          shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(8)),
          textStyle: weight(text.labelLarge!, 600),
        ),
      ),
      outlinedButtonTheme: OutlinedButtonThemeData(
        style: OutlinedButton.styleFrom(
          shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(8)),
          side: BorderSide(color: scheme.outline),
        ),
      ),
      textButtonTheme: TextButtonThemeData(
        style: TextButton.styleFrom(
          shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(8)),
          foregroundColor: scheme.primary,
        ),
      ),
      listTileTheme: ListTileThemeData(
        selectedTileColor: scheme.surfaceContainer,
        shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(8)),
        dense: true,
      ),
      navigationRailTheme: NavigationRailThemeData(
        backgroundColor: scheme.surfaceContainerLow,
        indicatorColor: scheme.primaryContainer,
        selectedIconTheme: IconThemeData(
          color: scheme.onPrimaryContainer,
          size: 20,
        ),
        unselectedIconTheme: IconThemeData(
          color: scheme.onSurfaceVariant,
          size: 20,
        ),
        selectedLabelTextStyle: weight(
          text.labelSmall!,
          600,
        ).copyWith(color: scheme.onSurface),
        unselectedLabelTextStyle: text.labelSmall!.copyWith(
          color: scheme.onSurfaceVariant,
        ),
        labelType: NavigationRailLabelType.all,
        useIndicator: true,
      ),
      navigationBarTheme: NavigationBarThemeData(
        backgroundColor: scheme.surfaceContainerLow,
        indicatorColor: scheme.primaryContainer,
        labelTextStyle: WidgetStatePropertyAll(text.labelSmall),
        height: 64,
      ),
      dialogTheme: DialogThemeData(
        backgroundColor: scheme.surfaceContainerLowest,
        shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(12)),
        titleTextStyle: text.titleLarge,
      ),
      tooltipTheme: TooltipThemeData(
        decoration: BoxDecoration(
          color: scheme.inverseSurface,
          borderRadius: BorderRadius.circular(6),
        ),
        textStyle: text.bodySmall?.copyWith(color: scheme.onInverseSurface),
        waitDuration: const Duration(milliseconds: 400),
      ),
      progressIndicatorTheme: ProgressIndicatorThemeData(color: scheme.primary),
      iconTheme: IconThemeData(color: scheme.onSurfaceVariant, size: 18),
    );
  }

  /// Sets weight on both the classic fontWeight and the variable-font axis.
  static TextStyle weight(TextStyle s, double w) => s.copyWith(
    fontWeight: FontWeight.values[((w / 100).round().clamp(1, 9)) - 1],
    fontVariations: [FontVariation('wght', w)],
  );

  static TextTheme _textTheme2(ColorScheme c) => TextTheme(
    displayLarge: weight(
      TextStyle(
        fontFamily: display,
        fontSize: 40,
        height: 1.1,
        color: c.onSurface,
        letterSpacing: -0.5,
      ),
      500,
    ),
    displayMedium: weight(
      TextStyle(
        fontFamily: display,
        fontSize: 32,
        height: 1.15,
        color: c.onSurface,
        letterSpacing: -0.3,
      ),
      500,
    ),
    displaySmall: weight(
      TextStyle(
        fontFamily: display,
        fontSize: 26,
        height: 1.2,
        color: c.onSurface,
      ),
      500,
    ),
    headlineLarge: weight(
      TextStyle(
        fontFamily: display,
        fontSize: 24,
        height: 1.25,
        color: c.onSurface,
      ),
      500,
    ),
    headlineMedium: weight(
      TextStyle(
        fontFamily: display,
        fontSize: 21,
        height: 1.3,
        color: c.onSurface,
      ),
      500,
    ),
    headlineSmall: weight(
      TextStyle(
        fontFamily: display,
        fontSize: 18,
        height: 1.3,
        color: c.onSurface,
      ),
      500,
    ),
    titleLarge: weight(
      TextStyle(
        fontFamily: body,
        fontSize: 17,
        height: 1.3,
        color: c.onSurface,
      ),
      600,
    ),
    titleMedium: weight(
      TextStyle(
        fontFamily: body,
        fontSize: 15,
        height: 1.35,
        color: c.onSurface,
      ),
      600,
    ),
    titleSmall: weight(
      TextStyle(
        fontFamily: body,
        fontSize: 13,
        height: 1.35,
        color: c.onSurface,
      ),
      600,
    ),
    bodyLarge: weight(
      TextStyle(
        fontFamily: body,
        fontSize: 15.5,
        height: 1.55,
        color: c.onSurface,
      ),
      400,
    ),
    bodyMedium: weight(
      TextStyle(
        fontFamily: body,
        fontSize: 14,
        height: 1.5,
        color: c.onSurface,
      ),
      400,
    ),
    bodySmall: weight(
      TextStyle(
        fontFamily: body,
        fontSize: 12.5,
        height: 1.45,
        color: c.onSurfaceVariant,
      ),
      400,
    ),
    labelLarge: weight(
      TextStyle(
        fontFamily: body,
        fontSize: 14,
        height: 1.3,
        color: c.onSurface,
      ),
      500,
    ),
    labelMedium: weight(
      TextStyle(
        fontFamily: body,
        fontSize: 12.5,
        height: 1.3,
        color: c.onSurface,
      ),
      500,
    ),
    labelSmall: weight(
      TextStyle(
        fontFamily: body,
        fontSize: 11,
        height: 1.3,
        color: c.onSurfaceVariant,
        letterSpacing: 0.2,
      ),
      500,
    ),
  );
}

/// Monospace style for identifiers.
TextStyle monoStyle(BuildContext context, {double? size, Color? color}) =>
    TextStyle(
      fontFamily: FundusTheme.mono,
      fontSize: size ?? 12,
      color: color ?? Theme.of(context).colorScheme.onSurfaceVariant,
      fontVariations: const [FontVariation('wght', 450)],
    );

/// Semantic colours for capture / receipt states.
extension FundusColors on ColorScheme {
  Color get success => brightness == Brightness.dark
      ? const Color(0xFF7FC59B)
      : const Color(0xFF2E7D4F);
  Color get warning => brightness == Brightness.dark
      ? const Color(0xFFE9C46A)
      : const Color(0xFF9A6B00);
  Color get modelTint => tertiary;
}

/// The Fundus mark (three sheets settling into a bowl). Ink follows the
/// theme: the dark variant has the ink recoloured to warm paper.
class FundusMark extends StatelessWidget {
  const FundusMark({super.key, this.size = 28, this.semanticLabel = 'Fundus'});
  final double size;
  final String semanticLabel;

  @override
  Widget build(BuildContext context) {
    final dark = Theme.of(context).brightness == Brightness.dark;
    return Image.asset(
      dark ? 'assets/icon/mark-dark.png' : 'assets/icon/mark.png',
      width: size,
      height: size,
      filterQuality: FilterQuality.medium,
      semanticLabel: semanticLabel,
    );
  }
}
