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
import { createContext, useContext, useLayoutEffect } from 'react'

// Charts and toasts still consume a resolved surface mode. Both complete
// packages use light UI surfaces; OS preferences must not split a package.
const ThemeContext = createContext<{ resolvedTheme: 'light' | 'dark' }>({
  resolvedTheme: 'light',
})
const lightSurface = { resolvedTheme: 'light' as const }

export function ThemeProvider(props: { children: React.ReactNode }) {
  useLayoutEffect(() => {
    document.documentElement.classList.remove('dark')
    document.documentElement.classList.add('light')
  }, [])
  return <ThemeContext value={lightSurface}>{props.children}</ThemeContext>
}

// eslint-disable-next-line react-refresh/only-export-components
export const useTheme = () => useContext(ThemeContext)
