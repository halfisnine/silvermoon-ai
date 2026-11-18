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
import type { ITheme } from '@visactor/vchart'

import type { ThemePreset } from '@/lib/theme-customization'

export interface DashboardChartAppearance {
  palette: string[]
  textColor: string
  theme: ITheme
}

// Canvas marks need concrete colors; CSS variables alone cannot recolor them.
// Keep each package's chart settings together and share them across chart types.
const packages: Record<
  ThemePreset,
  {
    palette: string[]
    text: string
    muted: string
    grid: string
    panel: string
  }
> = {
  default: {
    palette: [
      '#78618f',
      '#557e9b',
      '#438b80',
      '#b58a48',
      '#b56883',
      '#6471aa',
      '#b77754',
      '#7d8b52',
      '#955f72',
      '#668b9a',
    ],
    text: '#35415f',
    muted: '#686079',
    grid: '#77738e26',
    panel: '#fcfaff',
  },
  'silvermoon-dream': {
    palette: [
      '#8b6caf',
      '#658da6',
      '#b66e96',
      '#ae814b',
      '#668d80',
      '#6f73ae',
      '#ba7c69',
      '#858b50',
      '#9a5f7c',
      '#458b94',
    ],
    text: '#453a59',
    muted: '#71647f',
    grid: '#87729d29',
    panel: '#fcf8ff',
  },
  'crimson-clouds': {
    palette: [
      '#a95379',
      '#6b729d',
      '#bf8051',
      '#708c88',
      '#9569a7',
      '#b66a60',
      '#537f9c',
      '#92854e',
      '#7c5575',
      '#5d8c70',
    ],
    text: '#44364d',
    muted: '#775c6b',
    grid: '#9f72872b',
    panel: '#fff9f7',
  },
}

export const DASHBOARD_CHART_THEMES = Object.fromEntries(
  Object.entries(packages).map(([preset, colors]) => {
    const axis = {
      label: { style: { fill: colors.muted } },
      title: { style: { fill: colors.text } },
      grid: { style: { stroke: colors.grid, lineDash: [3, 4] } },
      domainLine: { style: { stroke: colors.grid } },
      tick: { style: { stroke: colors.grid } },
    }
    const theme: ITheme = {
      type: 'light',
      background: 'transparent',
      colorScheme: { default: colors.palette },
      markByName: { label: { style: { fill: colors.text } } },
      component: {
        axis,
        axisBand: axis,
        axisLinear: axis,
        title: {
          textStyle: { fill: colors.text },
          subtextStyle: { fill: colors.muted },
        },
        discreteLegend: {
          item: {
            label: {
              style: { fill: colors.text },
              state: { unSelected: { fill: colors.muted, opacity: 0.55 } },
            },
          },
        },
        tooltip: {
          panel: {
            backgroundColor: colors.panel,
            border: { color: colors.grid, width: 1, radius: 10 },
            shadow: { x: 0, y: 6, blur: 20, spread: 0, color: '#3325401f' },
          },
          titleLabel: { fill: colors.text, fontColor: colors.text },
          keyLabel: { fill: colors.muted, fontColor: colors.muted },
          valueLabel: { fill: colors.text, fontColor: colors.text },
        },
      },
    }
    return [preset, { palette: colors.palette, textColor: colors.text, theme }]
  })
) as Record<ThemePreset, DashboardChartAppearance>
