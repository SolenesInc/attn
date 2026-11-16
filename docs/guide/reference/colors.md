# Colors

A color scheme sets every color Attn draws: the interface and the terminals,
once for dark and once for light. The `theme` setting picks dark, light or
system, and the scheme supplies the colors for whichever appearance is in
effect. Attn's own scheme, `attn`, is the default.

Overrides change single colors on top of the scheme. Your home daemon stores
both settings.

## Pick a scheme

Set `color_scheme` to a scheme's id:

```sh
attn settings set color_scheme <id>
```

Attn refuses an id it doesn't know, and the error lists the schemes it has. To
go back to Attn's colors, clear the setting:

```sh
attn settings set color_scheme ""
```

## Override colors

`color_overrides` holds JSON with the same shape as a scheme: an appearance
(`dark` or `light`), a group (`ui` for the interface, `terminal` for the
terminals), and color names with their colors. Give only the colors you want
to change. A `ui` color is `#rrggbb`, or `#rrggbbaa` with an alpha byte. A
`terminal` color is `#rrggbb`.

```sh
attn settings set color_overrides '{"dark":{"ui":{"bg_app":"#24273a"},"terminal":{"bg":"#24273a","bright_black":"#7f849c"}}}'
```

The overrides apply to whichever scheme is chosen, so they stay when you switch
schemes. Setting `color_overrides` replaces every override at once. To keep
the ones you have, read the current value with
`attn settings get color_overrides` and add to it.

Attn refuses a value it can't use, and the error says what to fix:

- An unknown color name. The error lists the names in that group.
- A color in the wrong format for its group. The error names the value.
- An appearance other than `dark` or `light`, or a group other than `ui` or
  `terminal`.

```console
$ attn settings set color_overrides '{"dark":{"ui":{"bg_ap":"#24273a"}}}'
settings: daemon error: color_overrides: dark.ui.bg_ap is not a color name; ui names: accent, annotation, bg_app, …
```

To remove every override, clear the setting:

```sh
attn settings set color_overrides ""
```
