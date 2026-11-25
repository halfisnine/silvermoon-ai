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
import timezone from 'dayjs/plugin/timezone'
import utc from 'dayjs/plugin/utc'
import { ChevronDownIcon } from 'lucide-react'
import * as React from 'react'
import { enUS, fr, ja, ru, vi, zhCN, zhTW } from 'react-day-picker/locale'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Calendar } from '@/components/ui/calendar'
import { Input } from '@/components/ui/input'
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from '@/components/ui/popover'
import { toIntlLocale } from '@/i18n/languages'
import dayjs from '@/lib/dayjs'
import { cn } from '@/lib/utils'

dayjs.extend(utc)
dayjs.extend(timezone)

const calendarLocales = {
  en: enUS,
  zhCN,
  zhTW,
  fr,
  ru,
  ja,
  vi,
} as const

interface DateTimePickerProps extends Omit<
  React.ComponentProps<typeof Button>,
  'value' | 'onChange' | 'children'
> {
  value?: Date
  onChange?: (date: Date | undefined) => void
  placeholder?: string
  className?: string
  timeZone?: string
  hideTime?: boolean
  compact?: boolean
  displayFormat?: string
}

export function DateTimePicker({
  value,
  onChange,
  placeholder,
  className,
  displayFormat,
  timeZone,
  hideTime = false,
  compact = false,
  disabled,
  ...triggerProps
}: DateTimePickerProps) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.language)
  const placeholderText = placeholder ?? t('Select date')
  const calendarLocale =
    calendarLocales[i18n.language as keyof typeof calendarLocales] ?? enUS
  const currentYear = new Date().getFullYear()
  const [open, setOpen] = React.useState(false)
  const [date, setDate] = React.useState<Date | undefined>(() =>
    pickerDate(value, timeZone)
  )
  const [month, setMonth] = React.useState<Date | undefined>(() =>
    pickerDate(value, timeZone)
  )
  const [time, setTime] = React.useState<string>('00:00:00')
  const selectedDateText = dateLabel(date, displayFormat, locale)

  React.useEffect(() => {
    const displayed = pickerDate(value, timeZone)
    setDate(displayed)
    setMonth(displayed)
    if (displayed) {
      setTime(dayjs(displayed).format('HH:mm:ss'))
    }
  }, [value, timeZone])

  const handleDateSelect = (selectedDate: Date | undefined) => {
    if (selectedDate) {
      const [hours, minutes, seconds = 0] = (hideTime ? '00:00:00' : time)
        .split(':')
        .map(Number)
      const newDate = new Date(selectedDate)
      newDate.setHours(hours, minutes, seconds, 0)
      setDate(newDate)
      setMonth(newDate)
      onChange?.(pickerInstant(newDate, timeZone))
      if (!compact || hideTime) setOpen(false)
    } else {
      setDate(undefined)
      setMonth(undefined)
      onChange?.(undefined)
    }
  }

  const handleTimeChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const newTime = e.target.value
    // The browser reports an emptied or half-cleared field as ''. Keep the
    // current time: the field shows it again and no invalid date goes out.
    if (!newTime) return
    setTime(newTime)

    if (date) {
      // A browser may omit :00 seconds from the value.
      const [hours, minutes, seconds = 0] = newTime.split(':').map(Number)
      const newDate = new Date(date)
      newDate.setHours(hours, minutes, seconds, 0)
      setDate(newDate)
      onChange?.(pickerInstant(newDate, timeZone))
    }
  }

  const handleClear = () => {
    setDate(undefined)
    setMonth(undefined)
    setTime('00:00:00')
    onChange?.(undefined)
  }

  const timeControl = !hideTime && (
    <Input
      type='time'
      step={1}
      value={time}
      aria-label={t('Time')}
      onChange={handleTimeChange}
      className='w-32 appearance-none [&::-webkit-calendar-picker-indicator]:hidden [&::-webkit-calendar-picker-indicator]:appearance-none'
      disabled={disabled || !date}
    />
  )
  const clearControl = date && (
    <Button
      type='button'
      variant='outline'
      size='icon'
      onClick={handleClear}
      className='shrink-0'
      aria-label={t('Clear')}
      disabled={disabled}
    >
      <span aria-hidden='true'>✕</span>
    </Button>
  )

  return (
    <div className={cn('flex gap-2', className)}>
      <Popover open={open} onOpenChange={setOpen}>
        <PopoverTrigger
          render={
            <Button
              {...triggerProps}
              type='button'
              disabled={disabled}
              variant='outline'
              className={cn(
                'min-w-0 flex-1 justify-between font-normal',
                !date && 'text-muted-foreground'
              )}
            />
          }
        >
          <span className='truncate'>
            {date ? selectedDateText : placeholderText}
            {compact && date && !hideTime && ` ${time}`}
          </span>
          <ChevronDownIcon className='h-4 w-4 shrink-0 opacity-50' />
        </PopoverTrigger>
        <PopoverContent className='w-auto overflow-hidden p-0' align='start'>
          <Calendar
            mode='single'
            selected={date}
            month={month}
            onMonthChange={setMonth}
            captionLayout='dropdown'
            onSelect={handleDateSelect}
            locale={calendarLocale}
            startMonth={new Date(currentYear - 100, 0)}
            endMonth={new Date(currentYear + 100, 11)}
          />
          {compact && (
            <div className='flex items-center gap-2 border-t p-3'>
              {timeControl}
              {clearControl}
              <Button
                type='button'
                className='ml-auto'
                onClick={() => setOpen(false)}
              >
                {t('Done')}
              </Button>
            </div>
          )}
        </PopoverContent>
      </Popover>
      {!compact && timeControl}
      {!compact && clearControl}
    </div>
  )
}

function dateLabel(
  value: Date | undefined,
  displayFormat: string | undefined,
  locale: Intl.LocalesArgument | undefined
): string {
  if (!value) return ''
  if (displayFormat) return dayjs(value).format(displayFormat)
  return new Intl.DateTimeFormat(locale, {
    year: 'numeric',
    month: 'long',
    day: 'numeric',
  }).format(value)
}

function pickerDate(
  value: Date | undefined,
  timeZone?: string
): Date | undefined {
  if (!value || !timeZone) return value
  const zoned = dayjs(value).tz(timeZone)
  return new Date(
    zoned.year(),
    zoned.month(),
    zoned.date(),
    zoned.hour(),
    zoned.minute(),
    zoned.second()
  )
}
function pickerInstant(value: Date, timeZone?: string): Date {
  if (!timeZone) return value
  return dayjs.tz(dayjs(value).format('YYYY-MM-DD HH:mm:ss'), timeZone).toDate()
}
