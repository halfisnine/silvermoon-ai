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
import { Palette } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { useThemeCustomization } from '@/context/theme-customization-provider'
import { THEME_PRESETS, type ThemePreset } from '@/lib/theme-customization'

export function ThemeSwitch() {
  const { t } = useTranslation()
  const { customization, setPreset } = useThemeCustomization()
  return (
    <DropdownMenu modal={false}>
      <DropdownMenuTrigger
        render={<Button variant='ghost' size='icon' className='size-11' />}
      >
        <Palette className='size-5' aria-hidden='true' />
        <span className='sr-only'>{t('Toggle theme')}</span>
      </DropdownMenuTrigger>
      <DropdownMenuContent align='end'>
        <DropdownMenuRadioGroup
          value={customization.preset}
          onValueChange={(value) => setPreset(value as ThemePreset)}
        >
          {THEME_PRESETS.map((preset) => (
            <DropdownMenuRadioItem
              key={preset.value}
              value={preset.value}
              className='min-h-11'
            >
              {t(preset.name)}
            </DropdownMenuRadioItem>
          ))}
        </DropdownMenuRadioGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
