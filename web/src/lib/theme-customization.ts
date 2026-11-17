/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
/** A package owns its palette, typography, radius and decorative surfaces. */
export const THEME_PRESETS = [
  {
    value: 'default',
    artwork: '/silvermoon/moonlight.webp',
    width: 1254,
    height: 1254,
    surface: '#eeedf6',
    name: 'Default',
    description: 'Moon-white canvas and a dusk-blue sidebar.',
  },
  {
    value: 'silvermoon-dream',
    artwork: '/silvermoon/moonlight.webp',
    width: 1254,
    height: 1254,
    surface: '#f1ecf8',
    name: 'Silvermoon Dream',
    description: 'Moonlit scenery, misty lilac and soft glass surfaces.',
  },
  {
    value: 'crimson-clouds',
    name: 'Crimson Moon, Flowing Clouds',
    description: 'Rose moonlight, peach-gold clouds and violet skies.',
    artwork: '/silvermoon/crimson-clouds.jpg',
    width: 2016,
    height: 1152,
    surface: '#f7eaf0',
  },
] as const

export type ThemePreset = (typeof THEME_PRESETS)[number]['value']
export type ThemeScale = 'default' | 'sm' | 'lg' | 'xl'
export type ContentLayout = 'full' | 'centered'
export type ThemeCustomization = {
  preset: ThemePreset
  // Kept for chart geometry consumers; radius is owned by the package CSS.
  radius: 'default'
  scale: ThemeScale
  contentLayout: ContentLayout
}
export const DEFAULT_THEME_CUSTOMIZATION: ThemeCustomization = {
  preset: 'default',
  radius: 'default',
  scale: 'default',
  contentLayout: 'full',
}
export const THEME_PRESET_VALUES: ReadonlySet<ThemePreset> = new Set(
  THEME_PRESETS.map((p) => p.value)
)
export const THEME_SCALE_VALUES: ReadonlySet<ThemeScale> = new Set([
  'default',
  'sm',
  'lg',
  'xl',
])
export const CONTENT_LAYOUT_VALUES: ReadonlySet<ContentLayout> = new Set([
  'full',
  'centered',
])
