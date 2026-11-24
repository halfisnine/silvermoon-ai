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
import { useFieldArray, useFormContext, useWatch } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { FieldGroup, FieldSet, FieldLegend } from '@/components/ui/field'
import { Separator } from '@/components/ui/separator'

import type { MarketingActivity } from '../types'
import { ActivityField } from './fields'
import { RewardsEditor } from './rewards-editor'
import { defaultReward } from './schema'

export function CheckinFields(props: { currency: string }) {
  const { t } = useTranslation()
  const form = useFormContext<MarketingActivity>()
  const checkin = useWatch({ control: form.control, name: 'checkin' })
  const milestones = useFieldArray({
    control: form.control,
    name: 'checkin.milestones',
  })
  if (!checkin) return null
  return (
    <>
      <FieldSet>
        <FieldLegend>{t('Check-in eligibility')}</FieldLegend>
        <FieldGroup className='grid items-start gap-4 sm:grid-cols-2'>
          <ActivityField
            name='checkin.condition'
            label={t('Eligibility')}
            options={[
              { value: 'none', label: t('No recharge required') },
              { value: 'today_paid', label: t('Paid today') },
              {
                value: 'previous_day_paid',
                label: t('Paid on the previous day'),
              },
            ]}
            onChange={(v) => {
              form.setValue(
                'checkin.condition',
                v as 'none' | 'today_paid' | 'previous_day_paid'
              )
              form.setValue('checkin.threshold_cny', '')
            }}
          />
          {checkin.condition !== 'none' && (
            <ActivityField
              name='checkin.threshold_cny'
              label={t('Actual payment threshold (CNY)')}
              description={t(
                'Add successful payments within the qualifying day. Gifts do not count.'
              )}
            />
          )}
          <ActivityField
            name='checkin.cycle_days'
            type='number'
            label={t('Streak cycle (days, 0 means once per milestone)')}
          />
          <ActivityField
            name='checkin.makeup_days'
            type='number'
            label={t('Make-up window (previous days)')}
          />
          <ActivityField
            name='checkin.makeup_limit'
            type='number'
            label={t('Make-ups per user for this activity')}
            description={t(
              'Set both make-up values to 0 to disable. This allowance does not reset monthly.'
            )}
          />
          <ActivityField
            name='checkin.makeup_daily'
            type='check'
            label={t('Grant the missed daily reward on make-up')}
          />
        </FieldGroup>
      </FieldSet>
      <RewardsEditor
        name='rewards'
        currency={props.currency}
        title={t('Daily rewards')}
      />
      <FieldSet>
        <FieldLegend>{t('Extra streak rewards')}</FieldLegend>
        <p className='text-muted-foreground text-sm'>
          {t(
            'Daily and streak rewards are added together. Make-ups cannot grant a milestone twice.'
          )}
        </p>
        {milestones.fields.map((milestone, i) => (
          <div key={milestone.id} className='flex flex-col gap-4'>
            <ActivityField
              name={`checkin.milestones.${i}.days`}
              type='number'
              label={t('Consecutive check-in days')}
            />
            <RewardsEditor
              name={`checkin.milestones.${i}.rewards`}
              currency={props.currency}
              title={t('Milestone rewards')}
            />
            <Button
              type='button'
              variant='outline'
              onClick={() => milestones.remove(i)}
            >
              {t('Remove milestone')}
            </Button>
            <Separator />
          </div>
        ))}
        <Button
          type='button'
          variant='outline'
          disabled={milestones.fields.length >= 100}
          onClick={() =>
            milestones.append({
              days: 1,
              rewards: [defaultReward(props.currency)],
            })
          }
        >
          {t('Add streak milestone')}
        </Button>
      </FieldSet>
    </>
  )
}
