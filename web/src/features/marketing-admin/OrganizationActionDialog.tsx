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
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { getServerErrorMessage } from '@/lib/server-error-message'

import { marketingWrite } from './api'

export type OrganizationAction = 'archive' | 'unarchive' | 'delete'
export function OrganizationActionDialog(props: {
  resource: 'activities' | 'pools'
  record: { id: number; revision: number; deletable: boolean }
  action: OrganizationAction
  onClose: () => void
}) {
  const { t } = useTranslation()
  const client = useQueryClient()
  const [operationKey] = useState(() => crypto.randomUUID())
  const [record] = useState(props.record)
  const mutation = useMutation({
    mutationFn: () =>
      marketingWrite(`/${props.resource}/${record.id}/${props.action}`, {
        operation_key: operationKey,
        revision: record.revision,
      }),
    onSuccess: async () => {
      await client.invalidateQueries({ queryKey: ['marketing-admin'] })
      toast.success(t('Saved successfully'))
      props.onClose()
    },
    meta: { errorToast: false },
  })
  let title = t('Archive')
  if (props.action === 'unarchive') title = t('Restore from archive')
  if (props.action === 'delete') title = t('Delete')
  let description = t(
    'This only organizes the admin list. Participation, availability, deadlines, and existing user rights remain unchanged.'
  )
  if (props.action === 'delete') {
    description = t(
      'Only drafts that have never been enabled and have no associated records can be deleted. This cannot be undone.'
    )
    if (props.resource === 'pools') {
      description = t(
        'Only prize pools that have not started, are not referenced by activities, and have no associated records can be deleted. This cannot be undone.'
      )
    }
  }
  return (
    <ConfirmDialog
      open
      onOpenChange={(open) => {
        if (!open && !mutation.isPending) props.onClose()
      }}
      title={title}
      desc={description}
      destructive={props.action === 'delete'}
      disabled={props.action === 'delete' && !record.deletable}
      confirmText={t('Confirm')}
      isLoading={mutation.isPending}
      handleConfirm={() => mutation.mutate()}
    >
      {mutation.isError && (
        <p role='alert' className='text-destructive text-sm'>
          {getServerErrorMessage(mutation.error)}
        </p>
      )}
    </ConfirmDialog>
  )
}
