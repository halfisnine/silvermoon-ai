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
import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useLayoutEffect,
  useMemo,
  useState,
} from 'react'

import {
  CONTENT_LAYOUT_VALUES,
  type ContentLayout,
  DEFAULT_THEME_CUSTOMIZATION,
  THEME_PRESET_VALUES,
  THEME_SCALE_VALUES,
  type ThemeCustomization,
  type ThemePreset,
  type ThemeScale,
} from '@/lib/theme-customization'
import {
  readThemePreference,
  THEME_STORAGE_KEYS,
  writeThemePreference,
} from '@/lib/theme-storage'

function applyAttribute(name: string, value: string | null) {
  if (typeof document === 'undefined') return
  const body = document.body
  if (!body) return
  if (value === null) {
    body.removeAttribute(name)
  } else {
    body.setAttribute(name, value)
  }
}

type ThemeCustomizationContextType = {
  defaults: ThemeCustomization
  customization: ThemeCustomization
  setPreset: (preset: ThemePreset) => void
  setScale: (scale: ThemeScale) => void
  setContentLayout: (contentLayout: ContentLayout) => void
  resetCustomization: () => void
}

// Fallback used when a consumer renders outside the provider (e.g. an error
// route mounted before providers are ready, or stale HMR boundaries). Keeping
// it permissive prevents the whole tree from crashing — the UI just behaves
// like the defaults until the real provider re-mounts.
const FALLBACK_CONTEXT: ThemeCustomizationContextType = {
  defaults: DEFAULT_THEME_CUSTOMIZATION,
  customization: DEFAULT_THEME_CUSTOMIZATION,
  setPreset: () => {},
  setScale: () => {},
  setContentLayout: () => {},
  resetCustomization: () => {},
}

const ThemeCustomizationContext =
  createContext<ThemeCustomizationContextType>(FALLBACK_CONTEXT)

export function ThemeCustomizationProvider(props: {
  children: React.ReactNode
}) {
  const [preset, _setPreset] = useState<ThemePreset>(() =>
    readThemePreference<ThemePreset>(
      THEME_STORAGE_KEYS.package,
      THEME_PRESET_VALUES,
      DEFAULT_THEME_CUSTOMIZATION.preset
    )
  )
  const [scale, _setScale] = useState<ThemeScale>(() =>
    readThemePreference<ThemeScale>(
      THEME_STORAGE_KEYS.scale,
      THEME_SCALE_VALUES,
      DEFAULT_THEME_CUSTOMIZATION.scale
    )
  )
  const [contentLayout, _setContentLayout] = useState<ContentLayout>(() =>
    readThemePreference<ContentLayout>(
      THEME_STORAGE_KEYS.contentLayout,
      CONTENT_LAYOUT_VALUES,
      DEFAULT_THEME_CUSTOMIZATION.contentLayout
    )
  )

  useLayoutEffect(() => {
    const value = preset === 'default' ? null : preset
    applyAttribute('data-theme-preset', value)
    document.documentElement.dataset.themePackage = preset
    applyAttribute('data-theme-font', 'system')
    applyAttribute('data-theme-radius', null)
    document
      .querySelector("meta[name='theme-color']")
      ?.setAttribute(
        'content',
        preset === 'silvermoon-dream' ? '#f1ecf8' : '#eeedf6'
      )
  }, [preset])

  useEffect(() => {
    // Old mix-and-match settings are intentionally retired. Reading preferences survive.
    for (const key of [
      THEME_STORAGE_KEYS.mode,
      THEME_STORAGE_KEYS.preset,
      THEME_STORAGE_KEYS.font,
      THEME_STORAGE_KEYS.radius,
    ]) {
      writeThemePreference(key, null)
    }
  }, [])

  useEffect(() => {
    applyAttribute(
      'data-theme-scale',
      scale === DEFAULT_THEME_CUSTOMIZATION.scale ? null : scale
    )
  }, [scale])

  useEffect(() => {
    applyAttribute('data-theme-content-layout', contentLayout)
  }, [contentLayout])

  const setPreset = useCallback((value: ThemePreset) => {
    _setPreset(value)
    writeThemePreference(
      THEME_STORAGE_KEYS.package,
      value === DEFAULT_THEME_CUSTOMIZATION.preset ? null : value
    )
  }, [])

  const setScale = useCallback((value: ThemeScale) => {
    _setScale(value)
    writeThemePreference(
      THEME_STORAGE_KEYS.scale,
      value === DEFAULT_THEME_CUSTOMIZATION.scale ? null : value
    )
  }, [])

  const setContentLayout = useCallback((value: ContentLayout) => {
    _setContentLayout(value)
    writeThemePreference(
      THEME_STORAGE_KEYS.contentLayout,
      value === DEFAULT_THEME_CUSTOMIZATION.contentLayout ? null : value
    )
  }, [])

  const resetCustomization = useCallback(() => {
    setPreset(DEFAULT_THEME_CUSTOMIZATION.preset)
    setScale(DEFAULT_THEME_CUSTOMIZATION.scale)
    setContentLayout(DEFAULT_THEME_CUSTOMIZATION.contentLayout)
  }, [setPreset, setScale, setContentLayout])

  const value = useMemo<ThemeCustomizationContextType>(
    () => ({
      defaults: DEFAULT_THEME_CUSTOMIZATION,
      customization: { preset, radius: 'default', scale, contentLayout },
      setPreset,
      setScale,
      setContentLayout,
      resetCustomization,
    }),
    [
      preset,
      scale,
      contentLayout,
      setPreset,
      setScale,
      setContentLayout,
      resetCustomization,
    ]
  )

  return (
    <ThemeCustomizationContext.Provider value={value}>
      {props.children}
    </ThemeCustomizationContext.Provider>
  )
}

// eslint-disable-next-line react-refresh/only-export-components
export function useThemeCustomization() {
  return useContext(ThemeCustomizationContext)
}
