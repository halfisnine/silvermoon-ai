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
import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { ThemePicker } from '@/components/theme-picker'
import { ThemeSwitch } from '@/components/theme-switch'
import {
  ThemeCustomizationProvider,
  useThemeCustomization,
} from '@/context/theme-customization-provider'
import { ThemeProvider, useTheme } from '@/context/theme-provider'
import { initializeFrontendCache } from '@/lib/frontend-cache'
import { THEME_STORAGE_KEYS as keys } from '@/lib/theme-storage'

function Controls() {
  const { resolvedTheme } = useTheme()
  const { setScale, setContentLayout, resetCustomization } =
    useThemeCustomization()
  return (
    <>
      <output aria-label='Surface mode'>{resolvedTheme}</output>
      <input aria-label='Unsaved work' defaultValue='keep my input' />
      <ThemePicker />
      <ThemeSwitch />
      <button
        type='button'
        onClick={() => {
          setScale('lg')
          setContentLayout('centered')
        }}
      >
        Reading
      </button>
      <button type='button' onClick={resetCustomization}>
        Reset
      </button>
    </>
  )
}
function Fixture() {
  return (
    <ThemeProvider>
      <ThemeCustomizationProvider>
        <Controls />
      </ThemeCustomizationProvider>
    </ThemeProvider>
  )
}
beforeEach(() => localStorage.clear())
afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  localStorage.clear()
  document.documentElement.className = ''
  delete document.documentElement.dataset.themePackage
  for (const name of document.body.getAttributeNames()) {
    if (name.startsWith('data-theme-')) document.body.removeAttribute(name)
  }
})

describe('complete theme packages', () => {
  it('retires old dark/palette/font/radius combinations but preserves reading settings', () => {
    for (const [key, value] of [
      [keys.mode, 'dark'],
      [keys.preset, 'rose-garden'],
      [keys.font, 'serif'],
      [keys.radius, 'xl'],
      [keys.scale, 'lg'],
      [keys.contentLayout, 'centered'],
    ]) {
      localStorage.setItem(key, value)
    }
    document.documentElement.classList.add('dark')
    render(<Fixture />)
    expect(document.documentElement).toHaveClass('light')
    expect(document.documentElement).not.toHaveClass('dark')
    expect(document.body).not.toHaveAttribute('data-theme-preset')
    expect(document.body).toHaveAttribute('data-theme-font', 'system')
    expect(document.body).not.toHaveAttribute('data-theme-radius')
    expect(document.body).toHaveAttribute('data-theme-scale', 'lg')
    expect(document.body).toHaveAttribute(
      'data-theme-content-layout',
      'centered'
    )
    for (const key of [keys.mode, keys.preset, keys.font, keys.radius]) {
      expect(localStorage.getItem(key)).toBeNull()
    }
  })
  it('switches the whole package through shared controls without losing form state, then restores on remount', async () => {
    const user = userEvent.setup()
    const first = render(<Fixture />)
    await user.click(screen.getByRole('button', { name: 'Reading' }))
    await user.click(screen.getByRole('radio', { name: 'Silvermoon Dream' }))
    expect(document.body).toHaveAttribute(
      'data-theme-preset',
      'silvermoon-dream'
    )
    expect(screen.getByLabelText('Unsaved work')).toHaveValue('keep my input')
    await user.click(screen.getByRole('button', { name: 'Toggle theme' }))
    expect(
      screen.getByRole('menuitemradio', { name: 'Silvermoon Dream' })
    ).toHaveAttribute('aria-checked', 'true')
    await user.click(screen.getByRole('menuitemradio', { name: 'Default' }))
    expect(document.body).not.toHaveAttribute('data-theme-preset')
    expect(document.body).toHaveAttribute('data-theme-scale', 'lg')
    await user.click(screen.getByRole('radio', { name: 'Silvermoon Dream' }))
    first.unmount()
    render(<Fixture />)
    expect(
      screen.getByRole('radio', { name: 'Silvermoon Dream' })
    ).toHaveAttribute('aria-checked', 'true')
    expect(document.body).toHaveAttribute(
      'data-theme-content-layout',
      'centered'
    )
    expect(screen.getByLabelText('Surface mode')).toHaveTextContent('light')
  })
  it('persists reset and leaves unrelated preferences alone', async () => {
    localStorage.setItem(keys.package, 'silvermoon-dream')
    localStorage.setItem('unrelated', 'keep')
    const user = userEvent.setup()
    const first = render(<Fixture />)
    await user.click(screen.getByRole('button', { name: 'Reset' }))
    first.unmount()
    render(<Fixture />)
    expect(screen.getByRole('radio', { name: 'Default' })).toHaveAttribute(
      'aria-checked',
      'true'
    )
    expect(localStorage.getItem(keys.package)).toBeNull()
    expect(localStorage.getItem('unrelated')).toBe('keep')
  })
  it.each(['', 'unknown', 'rose-garden'])(
    'falls back for invalid package %j',
    (value) => {
      localStorage.setItem(keys.package, value)
      render(<Fixture />)
      expect(document.body).not.toHaveAttribute('data-theme-preset')
    }
  )
  it('remains usable when storage reads/writes fail', async () => {
    for (const method of ['getItem', 'setItem', 'removeItem'] as const) {
      vi.spyOn(localStorage, method).mockImplementation(() => {
        throw new DOMException('Unavailable', 'SecurityError')
      })
    }
    const user = userEvent.setup()
    render(<Fixture />)
    expect(document.body).not.toHaveAttribute('data-theme-preset')
    await user.click(screen.getByRole('radio', { name: 'Silvermoon Dream' }))
    expect(document.body).toHaveAttribute(
      'data-theme-preset',
      'silvermoon-dream'
    )
    await user.click(screen.getByRole('button', { name: 'Reset' }))
    expect(document.body).not.toHaveAttribute('data-theme-preset')
  })
  it('preserves a selected package during frontend cache initialization', () => {
    localStorage.setItem(keys.package, 'silvermoon-dream')
    localStorage.setItem(keys.scale, 'lg')
    localStorage.setItem('stale-ui-cache', 'old')
    initializeFrontendCache()
    initializeFrontendCache()
    render(<Fixture />)
    expect(document.body).toHaveAttribute(
      'data-theme-preset',
      'silvermoon-dream'
    )
    expect(document.body).toHaveAttribute('data-theme-scale', 'lg')
    expect(localStorage.getItem('stale-ui-cache')).toBeNull()
  })
})
