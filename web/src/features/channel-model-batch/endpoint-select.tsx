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
import { useTranslation } from 'react-i18next'

import { MultiSelect } from '@/components/multi-select'

import type { BatchSnapshot } from './api'
import {
  endpointCatalog,
  parseEndpointConfig,
  updateEndpointConfig,
  type EndpointConfig,
} from './endpoints'

export function EndpointSelect(props: {
  value: string
  snapshot: BatchSnapshot
  catalog?: EndpointConfig
  label: string
  disabled?: boolean
  onChange: (value: string) => void
}) {
  const { t } = useTranslation()
  const current = parseEndpointConfig(props.value)
  const selected = Object.keys(current)
  const catalog = { ...(props.catalog ?? endpointCatalog(props.snapshot)) }
  // Existing custom endpoints remain selectable and retain their saved paths.
  for (const [key, value] of Object.entries(current)) {
    catalog[key] = value ?? catalog[key] ?? null
  }
  const options = Object.entries(catalog).map(([key, config]) => {
    let hint: string | undefined
    if (typeof config === 'string') hint = config
    else if (config && typeof config === 'object' && 'path' in config) {
      hint = String(config.path)
    }
    return {
      value: key,
      label: key,
      hint,
      disabled: config == null && !selected.includes(key),
    }
  })
  return (
    <MultiSelect
      className='batch-endpoint-select flex-nowrap'
      popupClassName='w-96 max-w-[90vw]'
      options={options}
      selected={selected}
      placeholder={props.label}
      disabled={props.disabled}
      filter={(value, query) => {
        const option = options.find((item) => item.value === value)
        return `${value} ${option?.hint ?? ''}`
          .toLowerCase()
          .includes(query.toLowerCase())
      }}
      onChange={(values) =>
        props.onChange(
          updateEndpointConfig(
            props.value,
            values,
            props.snapshot,
            props.catalog
          )
        )
      }
      renderSelectedSummary={(values) => (
        <span
          className='flex min-w-0 items-center gap-1'
          title={values.join(', ') || t('Automatic by channel')}
        >
          <span className='truncate'>
            {values[0] || t('Automatic by channel')}
          </span>
          {values.length > 1 && (
            <span className='shrink-0'>+{values.length - 1}</span>
          )}
        </span>
      )}
    />
  )
}
