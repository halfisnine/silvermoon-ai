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
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'

import { AccountingRecords } from './accounting-records'
import { useLogsViewScope } from './usage-logs-provider'

export function AccountingQueryDialog() {
  const { t } = useTranslation()
  const { isAdminView } = useLogsViewScope()
  const [open, setOpen] = useState(false)
  const [resource, setResource] = useState('accounting')
  const [userId, setUserId] = useState('')
  const [requestId, setRequestId] = useState('')
  const [state, setState] = useState('review')
  const [submitted, setSubmitted] = useState<Record<string, string | number>>({
    state: 'review',
  })
  const validUser =
    userId === '' ||
    (/^[1-9]\d*$/.test(userId) && Number.isSafeInteger(Number(userId)))
  if (!isAdminView) return null
  return (
    <Dialog
      open={open}
      onOpenChange={setOpen}
      title={t('Accounting lookup')}
      contentClassName='sm:max-w-4xl'
      contentHeight='min(75dvh, 800px)'
      description={t(
        'Read-only accounting records remain available independently of usage logs.'
      )}
      trigger={
        <Button variant='outline' size='sm'>
          {t('Accounting lookup')}
        </Button>
      }
    >
      <div className='flex h-full min-h-0 flex-col gap-3'>
        <Tabs
          value={resource}
          onValueChange={(value) => {
            setResource(String(value))
            setSubmitted({})
            setState('')
            setRequestId('')
          }}
        >
          <TabsList className='h-auto flex-wrap'>
            <TabsTrigger value='accounting'>{t('Model usage')}</TabsTrigger>
            <TabsTrigger value='payments'>{t('Verified payments')}</TabsTrigger>
            <TabsTrigger value='cash-refunds'>{t('Cash refunds')}</TabsTrigger>
          </TabsList>
        </Tabs>
        <form
          className='grid gap-3 sm:grid-cols-3'
          onSubmit={(event) => {
            event.preventDefault()
            if (!validUser) return
            const filters: Record<string, string | number> = {}
            if (userId) filters.user_id = Number(userId)
            if (resource === 'accounting') {
              filters.state = state
              if (requestId.trim()) filters.request_id = requestId.trim()
            }
            setSubmitted(filters)
          }}
        >
          <div className='space-y-1'>
            <Label htmlFor='accounting-user'>{t('User ID')}</Label>
            <Input
              id='accounting-user'
              inputMode='numeric'
              value={userId}
              aria-invalid={!validUser}
              onChange={(event) => setUserId(event.target.value)}
              placeholder={t('Optional')}
            />
            {!validUser && (
              <p className='text-destructive text-xs'>
                {t('Enter a valid user ID')}
              </p>
            )}
          </div>
          {resource === 'accounting' && (
            <>
              <div className='space-y-1'>
                <Label htmlFor='accounting-request'>{t('Request ID')}</Label>
                <Input
                  id='accounting-request'
                  value={requestId}
                  onChange={(event) => setRequestId(event.target.value)}
                  placeholder={t('Optional')}
                />
              </div>
              <div className='space-y-1'>
                <Label htmlFor='accounting-state'>{t('Status')}</Label>
                <NativeSelect
                  id='accounting-state'
                  value={state}
                  onChange={(event) => setState(event.target.value)}
                >
                  <NativeSelectOption value=''>{t('All')}</NativeSelectOption>
                  <NativeSelectOption value='review'>
                    {t('Needs reconciliation')}
                  </NativeSelectOption>
                  <NativeSelectOption value='active'>
                    {t('Settling')}
                  </NativeSelectOption>
                  <NativeSelectOption value='reserved'>
                    {t('Request in progress')}
                  </NativeSelectOption>
                  <NativeSelectOption value='settled'>
                    {t('Settled')}
                  </NativeSelectOption>
                  <NativeSelectOption value='released'>
                    {t('Released without consumption')}
                  </NativeSelectOption>
                </NativeSelect>
              </div>
            </>
          )}
          <Button className='w-fit' disabled={!validUser} type='submit'>
            {t('Search')}
          </Button>
        </form>
        {open && (
          <AccountingRecords
            key={resource + JSON.stringify(submitted)}
            resource={resource}
            filters={submitted}
          />
        )}
      </div>
    </Dialog>
  )
}
