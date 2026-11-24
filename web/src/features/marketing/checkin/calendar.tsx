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
  CalendarCheck,
  Check,
  Circle,
  LockKeyhole,
  RotateCcw,
} from 'lucide-react'
import { createContext, useContext, type ComponentProps } from 'react'
import type { DayButton } from 'react-day-picker'
import { enUS, fr, ja, ru, vi, zhCN, zhTW } from 'react-day-picker/locale'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Calendar, CalendarDayButton } from '@/components/ui/calendar'
import { Card } from '@/components/ui/card'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'

import type { CheckinCalendar } from '../types'
import { civilDate, civilKey } from './dates'

const calendarLocales = { 'zh-CN': zhCN, 'zh-TW': zhTW, fr, ja, ru, vi }
const CalendarContext = createContext<{
  today: string
  signed: Set<string>
  makeup: Set<string>
  checkedIn: boolean
  canCheckin: boolean
} | null>(null)

// Keep the day component identity stable so refreshes preserve keyboard focus.
function CheckinDay(props: ComponentProps<typeof DayButton>) {
  const { t, i18n } = useTranslation()
  const state = useContext(CalendarContext)
  if (!state) throw new Error('CheckinDay requires its calendar provider')
  const date = civilKey(props.day.date)
  const outside = props.modifiers.outside
  const today = date === state.today
  const makeup = state.makeup.has(date)
  // Another activity may still allow an action on a date already recorded elsewhere.
  const signed =
    !makeup &&
    !(today && state.canCheckin) &&
    (state.signed.has(date) || (today && state.checkedIn))
  let label = t('Not checked in')
  let Icon = Circle
  if (date > state.today) {
    label = t('Not yet available')
    Icon = LockKeyhole
  }
  if (signed) {
    label = t('Checked in')
    Icon = Check
  } else if (makeup) {
    label = t('Make-up available')
    Icon = RotateCcw
  } else if (today) {
    label = state.canCheckin ? t('Check in now') : t('Today')
    Icon = CalendarCheck
  }
  const dayLabel = new Intl.DateTimeFormat(toIntlLocale(i18n.language), {
    year: 'numeric',
    month: 'long',
    day: 'numeric',
  }).format(props.day.date)
  return (
    <CalendarDayButton
      {...props}
      aria-label={`${dayLabel} · ${label}`}
      aria-current={today ? 'date' : undefined}
      className='marketing-checkin-day'
      data-signed={signed}
      data-makeup={makeup}
      data-today={today}
      data-outside={outside}
    >
      <span className='marketing-checkin-date'>
        {formatNumber(props.day.date.getDate(), toIntlLocale(i18n.language))}
      </span>
      {!outside && (
        <>
          <span className='marketing-checkin-icon'>
            <Icon className='size-4 sm:size-5' aria-hidden='true' />
          </span>
          <span className='marketing-checkin-day-label'>
            {today && state.canCheckin ? t('Check-in') : label}
          </span>
        </>
      )}
    </CalendarDayButton>
  )
}
const calendarComponents = { DayButton: CheckinDay }

export function CheckinCalendarView(props: {
  data: CheckinCalendar
  busy: boolean
  makeup?: string
  onMonthChange: (month: string) => void
  onCheckin: () => void
  onMakeup: (date: string) => void
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.language)
  const data = props.data
  const makeup = new Set(
    data.activities.flatMap((activity) => activity.makeup_dates)
  )
  const signed = new Set(
    data.activities.flatMap((activity) =>
      activity.records.map((record) => record.date)
    )
  )
  const calendarLocale =
    calendarLocales[locale as keyof typeof calendarLocales] ?? enUS
  const dayLabel = new Intl.DateTimeFormat(locale, {
    year: 'numeric',
    month: 'long',
    day: 'numeric',
  }).format(civilDate(data.today))
  return (
    <Card className='marketing-checkin-calendar-card gap-5 p-3 sm:p-5'>
      <div className='flex flex-wrap items-center justify-between gap-3'>
        <div className='space-y-1'>
          <h3 className='flex items-center gap-2 text-base font-semibold'>
            <CalendarCheck className='text-primary size-5' aria-hidden='true' />
            {t('Check-in calendar')}
          </h3>
          <p className='text-muted-foreground text-xs'>{dayLabel}</p>
        </div>
        <Button
          disabled={!data.can_checkin || props.busy}
          onClick={props.onCheckin}
          className='px-4'
        >
          {data.checked_in_today && !data.can_checkin
            ? t('Checked in today')
            : t('Check in now')}
        </Button>
      </div>
      <CalendarContext
        value={{
          today: data.today,
          signed,
          makeup,
          checkedIn: data.checked_in_today,
          canCheckin: data.can_checkin,
        }}
      >
        <Calendar
          className='marketing-checkin-calendar w-full max-w-full bg-transparent p-0'
          mode='single'
          locale={calendarLocale}
          weekStartsOn={1}
          fixedWeeks
          month={civilDate(`${data.month}-01`)}
          onMonthChange={(date) => {
            const month = civilKey(date).slice(0, 7)
            if (month !== data.month) props.onMonthChange(month)
          }}
          today={civilDate(data.today)}
          selected={props.makeup ? civilDate(props.makeup) : undefined}
          onSelect={(date) => {
            if (!date) return
            const key = civilKey(date)
            if (key === data.today && data.can_checkin) props.onCheckin()
            else if (makeup.has(key)) props.onMakeup(key)
          }}
          disabled={(date) =>
            props.busy ||
            (civilKey(date) === data.today
              ? !data.can_checkin
              : !makeup.has(civilKey(date)))
          }
          components={calendarComponents}
          classNames={{
            root: 'w-full',
            month: 'flex w-full min-w-0 flex-col gap-3',
            month_caption: 'flex h-9 items-center justify-center px-10',
            caption_label: 'text-base font-semibold',
            nav: 'absolute inset-x-0 top-0 flex items-center justify-between',
            button_previous: 'size-9',
            button_next: 'size-9',
            weekdays: 'grid grid-cols-7 gap-1 sm:gap-2',
            weekday:
              'text-muted-foreground py-2 text-center text-xs font-medium',
            week: 'mt-1 grid grid-cols-7 gap-1 sm:mt-2 sm:gap-2',
            day: 'group/day relative min-w-0 p-0',
            today: '',
            disabled: '',
            outside: '',
          }}
        />
      </CalendarContext>
      <div className='border-border/60 flex flex-wrap items-center justify-between gap-3 border-t pt-3 text-xs'>
        <div className='text-muted-foreground flex flex-wrap gap-x-4 gap-y-2'>
          <span className='flex items-center gap-1.5'>
            <Check className='text-primary size-3.5' aria-hidden='true' />
            {t('Checked in')}
          </span>
          <span className='flex items-center gap-1.5'>
            <RotateCcw className='text-primary size-3.5' aria-hidden='true' />
            {t('Make-up available')}
          </span>
          <span className='flex items-center gap-1.5'>
            <Circle className='text-primary size-3.5' aria-hidden='true' />
            {t('Today')}
          </span>
        </div>
        <p className='text-muted-foreground max-w-xl leading-relaxed'>
          {t(
            'Rewards are granted automatically under the eligible activity rules. Choose an available past date to make up a missed check-in.'
          )}
        </p>
      </div>
    </Card>
  )
}
