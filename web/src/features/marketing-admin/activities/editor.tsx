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
import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useRef, useState } from 'react'
import { useForm, useWatch } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Dialog } from '@/components/dialog'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { FieldGroup } from '@/components/ui/field'
import { Form } from '@/components/ui/form'
import { handleServerError } from '@/lib/handle-server-error'
import { getServerErrorMessage } from '@/lib/server-error-message'

import { saveActivity } from '../api'
import type { ActivityDetail, MarketingActivity } from '../types'
import { CheckinFields } from './checkin-fields'
import { ActivityField } from './fields'
import { ExclusiveGroupField } from './group-field'
import { RewardsEditor } from './rewards-editor'
import {
  activitySchema,
  changeActivityCondition,
  defaultActivity,
} from './schema'
import { ActivitySummary } from './summary'

export function ActivityEditor(props: {
  currency: string
  moneyRevision: string
  original?: ActivityDetail
  copy?: boolean
  onClose: () => void
}) {
  const { t } = useTranslation()
  const client = useQueryClient()
  const [snapshot] = useState(() => ({
    currency: props.currency,
    moneyRevision: props.moneyRevision,
    original: props.original ? structuredClone(props.original) : undefined,
  }))
  let initial =
    snapshot.original?.activity ?? defaultActivity(snapshot.currency)
  if (props.copy)
    {initial = {
      ...structuredClone(initial),
      name: `${initial.name} ${t('Copy')}`,
      starts_at: 0,
      ends_at: 0,
    }}
  const form = useForm<MarketingActivity>({
    resolver: zodResolver(activitySchema),
    defaultValues: initial,
  })
  const activity = useWatch({ control: form.control }) as MarketingActivity
  const request = useRef({ payload: '', key: '' })
  const mutation = useMutation({
    mutationFn: async (values: MarketingActivity) => {
      const payload = JSON.stringify(values)
      if (request.current.payload !== payload)
        {request.current = { payload, key: crypto.randomUUID() }}
      return saveActivity(
        values,
        request.current.key,
        snapshot.moneyRevision,
        props.copy ? undefined : snapshot.original
      )
    },
    onSuccess: async () => {
      await client.invalidateQueries({ queryKey: ['marketing-admin'] })
      toast.success(t('Saved successfully'))
      props.onClose()
    },
    onError: (error) => {
      handleServerError(error)
      form.setError('root.server', {
        message: getServerErrorMessage(error) || t('Unable to save changes'),
      })
    },
  })
  const change = (kind: MarketingActivity['kind'], condition: string) =>
    form.reset(
      changeActivityCondition(
        form.getValues(),
        kind,
        condition,
        snapshot.currency
      )
    )
  const conditionOptions =
    activity.kind === 'recharge'
      ? [
          {
            value: 'first_payment',
            label: t('First-ever payment reaches the threshold'),
          },
          {
            value: 'single_payment',
            label: t('Single payment during this activity'),
          },
          {
            value: 'cumulative_payments',
            label: t('Cumulative payments during this activity'),
          },
        ]
      : [
          { value: 'friend_registration', label: t('Friend registers') },
          {
            value: 'friend_cumulative',
            label: t('Friend’s cumulative payments reach the threshold'),
          },
          {
            value: 'friend_ratio',
            label: t('Percentage of friend’s actual payment'),
          },
        ]
  const hasFrequency = [
    'single_payment',
    'cumulative_payments',
    'friend_cumulative',
  ].includes(activity.condition)
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !mutation.isPending) props.onClose()
      }}
      title={props.copy ? t('Copy activity') : t('Configure activity')}
      contentClassName='sm:max-w-5xl'
      footer={
        <>
          <Button
            variant='outline'
            disabled={mutation.isPending}
            onClick={props.onClose}
          >
            {t('Cancel')}
          </Button>
          <Button
            form='marketing-activity-form'
            type='submit'
            disabled={mutation.isPending}
          >
            {mutation.isPending ? t('Saving...') : t('Save draft')}
          </Button>
        </>
      }
    >
      <Form {...form}>
        <form
          id='marketing-activity-form'
          className='flex flex-col gap-6'
          onSubmit={form.handleSubmit((values) => {
            form.clearErrors('root')
            mutation.mutate(values)
          })}
        >
          <fieldset
            disabled={mutation.isPending}
            className='flex min-w-0 flex-col gap-6'
          >
            <FieldGroup className='grid gap-4 sm:grid-cols-2'>
              <ActivityField name='name' label={t('Activity name')} />
              <ActivityField
                name='kind'
                label={t('Activity type')}
                options={[
                  { value: 'registration', label: t('Registration gifts') },
                  { value: 'checkin', label: t('Check-in rewards') },
                  { value: 'recharge', label: t('Recharge rewards') },
                  { value: 'invitation', label: t('Invitation rewards') },
                ]}
                onChange={(value) => {
                  const defaults: Record<MarketingActivity['kind'], string> = {
                    registration: 'registration',
                    checkin: 'checkin',
                    recharge: 'first_payment',
                    invitation: 'friend_registration',
                  }
                  const kind = value as MarketingActivity['kind']
                  change(kind, defaults[kind])
                }}
              />
              <ActivityField
                name='starts_at'
                type='date'
                label={t('Starts at')}
              />
              <ActivityField
                name='ends_at'
                type='date'
                label={t('Ends at')}
                description={t('Leave empty for an ongoing activity.')}
              />
              <ActivityField
                name='published'
                type='check'
                label={t('Show in activity center after activation')}
              />
            </FieldGroup>
            {(activity.kind === 'recharge' ||
              activity.kind === 'invitation') && (
              <FieldGroup className='grid gap-4 sm:grid-cols-2'>
                <ActivityField
                  name='condition'
                  label={t('Reward trigger')}
                  options={conditionOptions}
                  onChange={(v) => change(activity.kind, v)}
                />
                {hasFrequency && (
                  <ActivityField
                    name='frequency'
                    label={t('Reward frequency')}
                    options={[
                      {
                        value: 'once',
                        label: t('Once per participant in this activity'),
                      },
                      {
                        value: 'every',
                        label: t(
                          'Every qualifying payment or cumulative threshold'
                        ),
                      },
                    ]}
                  />
                )}
                {(activity.kind === 'recharge' ||
                  activity.condition === 'friend_cumulative') && (
                  <ActivityField
                    name='threshold_cny'
                    label={t('Actual payment threshold (CNY)')}
                    description={t(
                      'Count actual CNY payments only. Gifts never count toward thresholds.'
                    )}
                  />
                )}
              </FieldGroup>
            )}
            {activity.kind === 'invitation' && (
              <p className='text-muted-foreground text-sm'>
                {t(
                  'Only friends invited during this activity qualify. Payments must also be posted during the activity; each friend is counted separately.'
                )}
              </p>
            )}
            {activity.kind === 'checkin' ? (
              <CheckinFields currency={snapshot.currency} />
            ) : (
              <RewardsEditor
                name='rewards'
                currency={snapshot.currency}
                title={t('Rewards')}
              />
            )}
            <details>
              <summary className='cursor-pointer'>
                {t('Stacking and priority')}
              </summary>
              <div className='mt-4 grid gap-4 sm:grid-cols-2'>
                <ExclusiveGroupField />
                <ActivityField
                  name='priority'
                  type='number'
                  label={t('Priority (higher wins)')}
                />
              </div>
              <p className='text-muted-foreground mt-2 text-sm'>
                {t(
                  'No group means stacking. Only qualifying activities in the same group compete; the higher priority wins.'
                )}
              </p>
            </details>
            <ActivitySummary activity={activity} />
          </fieldset>
          {form.formState.errors.root?.server && (
            <Alert variant='destructive'>
              <AlertDescription>
                {form.formState.errors.root.server.message}
              </AlertDescription>
            </Alert>
          )}
        </form>
      </Form>
    </Dialog>
  )
}
