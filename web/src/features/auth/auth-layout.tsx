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
import { Link } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'

import { LanguageSwitcher } from '@/components/language-switcher'
import { ThemeSwitch } from '@/components/theme-switch'
import { Skeleton } from '@/components/ui/skeleton'
import { useThemeCustomization } from '@/context/theme-customization-provider'
import { useSystemConfig } from '@/hooks/use-system-config'
import { THEME_PRESETS } from '@/lib/theme-customization'

type AuthLayoutProps = {
  children: React.ReactNode
  illustrated?: boolean
}

export function AuthLayout({ children, illustrated = false }: AuthLayoutProps) {
  const { t } = useTranslation()
  const { systemName, logo, loading } = useSystemConfig()
  const { customization } = useThemeCustomization()
  const theme =
    THEME_PRESETS.find((preset) => preset.value === customization.preset) ??
    THEME_PRESETS[0]

  const brand = (
    <Link
      to='/'
      className={
        illustrated
          ? 'silvermoon-auth-brand'
          : 'absolute top-4 left-4 z-10 flex items-center gap-2 transition-opacity hover:opacity-80 sm:top-8 sm:left-8'
      }
    >
      <div className='relative h-8 w-8'>
        {loading ? (
          <Skeleton className='absolute inset-0 rounded-full' />
        ) : (
          <img
            src={logo}
            alt={t('Logo')}
            className='h-8 w-8 rounded-full object-cover'
          />
        )}
      </div>
      {loading ? (
        <Skeleton className='h-6 w-24' />
      ) : (
        <h1 className='text-xl font-medium'>{systemName}</h1>
      )}
    </Link>
  )

  if (illustrated) {
    return (
      <div className='silvermoon-auth' data-auth-theme={customization.preset}>
        <div className='silvermoon-auth-scene' aria-hidden='true'>
          <img
            src={theme.artwork}
            width={theme.width}
            height={theme.height}
            alt=''
            fetchPriority='high'
            className='silvermoon-auth-artwork'
          />
        </div>
        <header className='silvermoon-auth-header'>
          {brand}
          <div className='silvermoon-auth-tools'>
            <LanguageSwitcher />
            <ThemeSwitch />
          </div>
        </header>
        <main className='silvermoon-auth-main'>
          <div className='silvermoon-auth-welcome'>
            <p className='silvermoon-auth-title'>
              {t('Welcome back to {{name}}', { name: systemName })}
            </p>
            <p className='silvermoon-auth-description'>
              {t('Manage your API keys, usage and balance.')}
            </p>
          </div>
          <section className='silvermoon-auth-panel'>{children}</section>
        </main>
      </div>
    )
  }

  return (
    <div className='relative grid h-svh max-w-none'>
      {brand}
      <div className='container flex items-center pt-16 sm:pt-0'>
        <div className='mx-auto flex w-full flex-col justify-center space-y-2 px-4 py-8 sm:w-[480px] sm:p-8'>
          {children}
        </div>
      </div>
    </div>
  )
}
