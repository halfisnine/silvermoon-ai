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
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { THEME_STORAGE_KEYS as keys } from '@/lib/theme-storage'

const html = readFileSync(
  resolve(import.meta.dirname, '../../../index.html'),
  'utf8'
)
const scripts = [...html.matchAll(/<script>([\s\S]*?)<\/script>/g)].map(
  (match) => match[1]
)
function boot() {
  for (const script of scripts) new Function(script)()
}
beforeEach(() => {
  localStorage.clear()
  document.head.innerHTML = '<meta name="theme-color" content="#fff">'
  document.documentElement.className = 'dark'
  vi.spyOn(window, 'matchMedia').mockImplementation(() => {
    throw new Error('Packages must not consult OS appearance')
  })
})
afterEach(() => {
  vi.restoreAllMocks()
  document.documentElement.className = ''
  delete document.documentElement.dataset.themePackage
  delete document.body.dataset.themePreset
  delete document.body.dataset.themeFont
})
describe('theme before React mounts', () => {
  it('restores dream on html and body before content renders', () => {
    localStorage.setItem(keys.package, 'silvermoon-dream')
    boot()
    expect(document.documentElement).toHaveAttribute(
      'data-theme-package',
      'silvermoon-dream'
    )
    expect(document.body).toHaveAttribute(
      'data-theme-preset',
      'silvermoon-dream'
    )
    expect(document.documentElement).toHaveClass('light')
    expect(document.documentElement).not.toHaveClass('dark')
    expect(document.querySelector('meta[name="theme-color"]')).toHaveAttribute(
      'content',
      '#f1ecf8'
    )
  })
  it.each([null, 'unknown', 'dark', 'rose-garden'])(
    'uses default for %j, ignoring legacy dark and OS mode',
    (value) => {
      if (value !== null) localStorage.setItem(keys.package, value)
      localStorage.setItem(keys.mode, 'dark')
      boot()
      expect(document.documentElement).toHaveAttribute(
        'data-theme-package',
        'default'
      )
      expect(document.body).not.toHaveAttribute('data-theme-preset')
      expect(document.documentElement).toHaveClass('light')
    }
  )
  it('renders default even when storage is blocked', () => {
    vi.spyOn(localStorage, 'getItem').mockImplementation(() => {
      throw new Error('blocked')
    })
    boot()
    expect(document.documentElement).toHaveAttribute(
      'data-theme-package',
      'default'
    )
    expect(document.documentElement).toHaveClass('light')
  })
})
