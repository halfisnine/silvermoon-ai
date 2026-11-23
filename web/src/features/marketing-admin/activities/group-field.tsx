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
import { useQuery } from '@tanstack/react-query'
import { useState } from 'react'
import { useFormContext, useWatch } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { Button } from '@/components/ui/button'
import { ComboboxInput } from '@/components/ui/combobox-input'
import {
  FormField,
  FormItem,
  FormLabel,
  FormControl,
  FormMessage,
} from '@/components/ui/form'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'

import { marketingGet } from '../api'
import type { ActivityDetail, MarketingPage, MarketingActivity } from '../types'

export function ExclusiveGroupField() {
  const { t, i18n } = useTranslation()
  const form = useFormContext<MarketingActivity>()
  const group = useWatch({ control: form.control, name: 'exclusive_group' })
  const [page, setPage] = useState(1)
  const [memberPage, setMemberPage] = useState(1)
  const groups = useQuery({
    queryKey: ['marketing-admin', 'groups', page, group],
    queryFn: () =>
      marketingGet<MarketingPage<{ name: string; count: number }>>('/groups', {
        page,
        page_size: 20,
        keyword: group,
      }),
  })
  const members = useQuery({
    queryKey: ['marketing-admin', 'group-members', group, memberPage],
    queryFn: () =>
      marketingGet<MarketingPage<ActivityDetail>>('/activities', {
        page: memberPage,
        page_size: 20,
        exclusive_group: group,
      }),
    enabled: !!group,
  })
  const locale = toIntlLocale(i18n.language)
  return (
    <div className='flex flex-col gap-2'>
      <FormField
        control={form.control}
        name='exclusive_group'
        render={({ field }) => (
          <FormItem>
            <FormLabel>{t('Exclusive group (optional)')}</FormLabel>
            <FormControl>
              <ComboboxInput
                value={field.value}
                allowCustomValue
                options={(groups.data?.items ?? []).map((g) => ({
                  value: g.name,
                  label: g.name,
                }))}
                onValueChange={(v) => {
                  field.onChange(v)
                  setPage(1)
                  setMemberPage(1)
                }}
                placeholder={t('Select a group or enter a new name')}
              />
            </FormControl>
            <FormMessage />
          </FormItem>
        )}
      />
      {groups.isError && <ErrorState onRetry={() => void groups.refetch()} />}
      <div className='flex gap-2'>
        <Button
          type='button'
          variant='ghost'
          size='sm'
          disabled={page === 1 || groups.isFetching}
          onClick={() => setPage(page - 1)}
        >
          {t('Previous groups')}
        </Button>
        <Button
          type='button'
          variant='ghost'
          size='sm'
          disabled={
            !groups.data || page * 20 >= groups.data.total || groups.isFetching
          }
          onClick={() => setPage(page + 1)}
        >
          {t('More groups')}
        </Button>
      </div>
      {!!group && (
        <>
          <p className='text-sm font-medium'>{t('Activities in this group')}</p>
          {members.isError ? (
            <ErrorState onRetry={() => void members.refetch()} />
          ) : (
            <ul className='text-muted-foreground text-sm'>
              {members.data?.items.map((m) => (
                <li key={m.id}>
                  {m.activity.name} · {t('Priority')}:{' '}
                  {formatNumber(m.activity.priority, locale)}
                </li>
              ))}
            </ul>
          )}
          <div className='flex gap-2'>
            <Button
              type='button'
              variant='ghost'
              size='sm'
              disabled={memberPage === 1 || members.isFetching}
              onClick={() => setMemberPage(memberPage - 1)}
            >
              {t('Previous')}
            </Button>
            <Button
              type='button'
              variant='ghost'
              size='sm'
              disabled={
                !members.data ||
                memberPage * 20 >= members.data.total ||
                members.isFetching
              }
              onClick={() => setMemberPage(memberPage + 1)}
            >
              {t('Next')}
            </Button>
          </div>
        </>
      )}
    </div>
  )
}
