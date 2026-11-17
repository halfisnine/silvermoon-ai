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
import { useId } from 'react'
import { useTranslation } from 'react-i18next'

import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group'
import { useThemeCustomization } from '@/context/theme-customization-provider'
import { THEME_PRESETS, type ThemePreset } from '@/lib/theme-customization'

/** A visual package chooser composed from the shared radio control. */
export function ThemePicker() {
  const { t } = useTranslation()
  const { customization, setPreset } = useThemeCustomization()
  const id = useId()

  return (
    <section aria-labelledby={`${id}-title`}>
      <h3 id={`${id}-title`} className='mb-3 text-sm font-semibold'>
        {t('Theme')}
      </h3>
      <RadioGroup
        aria-labelledby={`${id}-title`}
        value={customization.preset}
        onValueChange={(value) => setPreset(value as ThemePreset)}
        className='grid grid-cols-1 gap-3 sm:grid-cols-2'
      >
        {THEME_PRESETS.map((preset) => (
          <label
            key={preset.value}
            className='silvermoon-theme-option'
            data-selected={customization.preset === preset.value || undefined}
          >
            <span
              className='silvermoon-theme-preview'
              data-preview={preset.value}
              aria-hidden='true'
            >
              <span className='silvermoon-theme-preview-sidebar'>
                <i />
                <i />
                <i />
              </span>
              <span className='silvermoon-theme-preview-main'>
                <span>
                  <img src='/silvermoon/moonlight.webp' alt='' loading='lazy' />
                </span>
                <span>
                  <i />
                  <i />
                  <i />
                </span>
              </span>
            </span>
            <span className='flex items-center gap-2 px-3 pt-3 text-sm font-medium'>
              <RadioGroupItem
                value={preset.value}
                aria-labelledby={`${id}-${preset.value}-name`}
                aria-describedby={`${id}-${preset.value}`}
              />
              <span id={`${id}-${preset.value}-name`}>{t(preset.name)}</span>
            </span>
            <span
              id={`${id}-${preset.value}`}
              className='text-muted-foreground block px-3 pt-2 pb-3 text-xs leading-relaxed'
            >
              {t(preset.description)}
            </span>
          </label>
        ))}
      </RadioGroup>
    </section>
  )
}
