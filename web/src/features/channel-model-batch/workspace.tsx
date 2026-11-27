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
  keepPreviousData,
  useMutation,
  useQuery,
  useQueryClient,
} from '@tanstack/react-query'
import { useBlocker } from '@tanstack/react-router'
import { useMemo, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { Combobox } from '@/components/ui/combobox'
import { Input } from '@/components/ui/input'
import {
  invalidateModelPricing,
  type ModelPricingConfig,
} from '@/features/model-pricing/api'
import type { PricingValues } from '@/features/model-pricing/pricing'
import { useDebounce } from '@/hooks/use-debounce'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'

import {
  getBatchPricing,
  getBatchSnapshot,
  saveBatch,
  previewBatchPricing,
  type BatchSnapshot,
  type DraftRow,
} from './api'
import { BatchDialog } from './batch-dialog'
import {
  buildChanges,
  createDrafts,
  isDirty,
  validateDrafts,
  draftEntry,
} from './draft'
import { endpointCatalog } from './endpoints'
import { ModelTable } from './model-table'
import { ReviewDialog } from './review-dialog'

export function BatchWorkspace(props: {
  channelId: number
  channelPicker?: ReactNode
}) {
  const query = useQuery({
    queryKey: ['channel-model-batch', props.channelId],
    queryFn: () => getBatchSnapshot(props.channelId),
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
    staleTime: Infinity,
  })
  if (query.isError) {
    return (
      <ErrorState
        description={query.error.message}
        onRetry={() => void query.refetch()}
      />
    )
  }
  if (!query.data) return <LoadingState />
  return (
    <DraftWorkspace
      key={query.data.version}
      channelPicker={props.channelPicker}
      snapshot={query.data}
      reload={() => query.refetch()}
    />
  )
}

function DraftWorkspace(props: {
  snapshot: BatchSnapshot
  channelPicker?: ReactNode
  reload: () => Promise<unknown>
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const client = useQueryClient()
  const endpointOptions = useMemo(
    () => endpointCatalog(props.snapshot),
    [props.snapshot]
  )
  const [rows, setRows] = useState(() => createDrafts(props.snapshot))
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [activeSource, setActiveSource] = useState<string | null>(null)
  const [search, setSearch] = useState('')
  const [filter, setFilter] = useState('all')
  const [editMode, setEditMode] = useState('keep')
  const [editRows, setEditRows] = useState<DraftRow[] | null>(null)
  const [review, setReview] = useState<ModelPricingConfig | null>(null)
  const [reviewEntries, setReviewEntries] = useState<
    Record<string, PricingValues>
  >({})
  const [discard, setDiscard] = useState(false)
  const dirty = rows.filter(isDirty)
  const blocker = useBlocker({
    shouldBlockFn: () => dirty.length > 0,
    enableBeforeUnload: dirty.length > 0,
    withResolver: true,
  })
  const names = useMemo(
    () =>
      [...new Set(rows.flatMap((row) => [row.name, row.source_name]))].sort(),
    [rows]
  )
  // Only names affect this query. Price/vendor edits must not restart its
  // debounce timer, and continuous typing must not reload the entire table.
  const namesKey = JSON.stringify(names)
  const settledNamesKey = useDebounce(namesKey, 300)
  const settledNames = JSON.parse(settledNamesKey) as string[]
  const pricing = useQuery({
    queryKey: ['model-pricing-config', 'batch', ...settledNames],
    queryFn: () => getBatchPricing(settledNames),
    refetchOnWindowFocus: false,
    placeholderData: keepPreviousData,
    staleTime: Infinity,
  })
  const validation = validateDrafts(rows, props.snapshot)
  const priceValidation = rows.some(
    (row) => row.priceError && row.priceError === validation
  )
  const isReady = (row: DraftRow) => {
    if (!row.vendorId || row.priceError || !pricing.data) return false
    const effective = draftEntry(row, pricing.data).effective
    return ['ModelPrice', 'ModelRatio', 'billing_setting.billing_expr'].some(
      (key) => Object.hasOwn(effective, key)
    )
  }
  const visible = rows.filter((row) => {
    const vendor = props.snapshot.vendors.find(
      (item) => item.id === row.vendorId
    )
    const matches = `${row.name} ${row.upstream_name} ${vendor?.name ?? ''}`
      .toLowerCase()
      .includes(search.toLowerCase())
    return (
      matches &&
      (row.source_name === activeSource ||
        filter === 'all' ||
        (filter === 'changed' && isDirty(row)) ||
        (filter === 'unassigned' && !row.vendorId) ||
        (filter === 'todo' && !isReady(row)) ||
        (filter === 'ready' && isReady(row)))
    )
  })
  const busy = pricing.isFetching || namesKey !== settledNamesKey
  const prepare = useMutation({
    mutationFn: async () => {
      if (validation || !pricing.data) {
        throw new Error(validation ?? t('Failed to load model pricing'))
      }
      const previews = await previewBatchPricing(dirty)
      setReviewEntries(
        Object.fromEntries(
          previews.flatMap((row) =>
            row.previewPricing ? [[row.source_name, row.previewPricing]] : []
          )
        )
      )
      // Freeze exactly the versions displayed while editing; a concurrent save
      // must conflict, never acquire fresh versions underneath an old draft.
      setReview(pricing.data)
    },
  })
  const save = useMutation({
    mutationFn: async () => {
      if (!review) return
      await saveBatch(
        props.snapshot.channel.id,
        props.snapshot.version,
        buildChanges(rows, review)
      )
    },
    onSuccess: async () => {
      setReview(null)
      setRows(createDrafts(props.snapshot))
      await Promise.all([
        invalidateModelPricing(client),
        client.invalidateQueries({ queryKey: ['channels'] }),
        client.invalidateQueries({ queryKey: ['vendors'] }),
        props.reload(),
      ])
      toast.success(t('Channel model batch saved'))
    },
  })
  const patch = (
    source: string,
    values: Partial<DraftRow>,
    expectedQuickPricing?: DraftRow['quickPricing']
  ) =>
    setRows((current) =>
      current.map((row) =>
        row.source_name === source &&
        (!expectedQuickPricing || row.quickPricing === expectedQuickPricing)
          ? {
              ...row,
              ...values,
              // A conversion can finish after a rename. Keep the version
              // bound to the current destination, not the conversion origin.
              ...(expectedQuickPricing
                ? { pricingVersion: row.pricingVersion }
                : {}),
            }
          : row
      )
    )
  if (pricing.isError) {
    return (
      <ErrorState
        description={pricing.error.message}
        onRetry={() => void pricing.refetch()}
      />
    )
  }

  return (
    <div className='batch-workspace'>
      <section className='batch-overview'>
        <div className='batch-overview-row'>
          <div className='batch-channel-picker'>
            <span className='batch-label'>{t('Select channel')}</span>
            {props.channelPicker ?? (
              <strong>{props.snapshot.channel.name}</strong>
            )}
          </div>
          <div className='batch-channel-info'>
            <span className='batch-label'>{t('Channel overview')}</span>
            <strong title={props.snapshot.channel.name}>
              {props.snapshot.channel.name}
            </strong>
            <span>
              {' '}
              · {t('Channel models')}: {formatNumber(rows.length, locale)}
            </span>
          </div>
          <div className='batch-pills'>
            <Button
              size='sm'
              variant={filter === 'todo' ? 'secondary' : 'ghost'}
              onClick={() => setFilter(filter === 'todo' ? 'all' : 'todo')}
            >
              {t('Incomplete')} {rows.filter((row) => !isReady(row)).length}
            </Button>
            <Button
              size='sm'
              variant={filter === 'ready' ? 'secondary' : 'ghost'}
              onClick={() => setFilter(filter === 'ready' ? 'all' : 'ready')}
            >
              {t('Configured')} {rows.filter(isReady).length}
            </Button>
          </div>
        </div>
      </section>
      <section className='batch-models'>
        <div className='batch-toolbar'>
          <div className='batch-toolbar-left'>
            <span className='text-muted-foreground text-xs'>
              {t('Channel models')}: <b>{visible.length}</b>
            </span>
            <Button
              size='sm'
              variant='outline'
              disabled={!selected.size || busy}
              onClick={() => {
                setEditRows(rows.filter((row) => selected.has(row.source_name)))
                setEditMode('keep')
              }}
            >
              {t('Batch settings')}
            </Button>
            <Button
              size='sm'
              variant='ghost'
              onClick={() =>
                setSelected(
                  (current) =>
                    new Set([
                      ...current,
                      ...visible.map((row) => row.source_name),
                    ])
                )
              }
            >
              {t('Select visible models')}
            </Button>
            <Button
              size='sm'
              variant='ghost'
              disabled={!selected.size}
              onClick={() => setSelected(new Set())}
            >
              {t('Clear selection')}
            </Button>
          </div>
          <div className='batch-toolbar-right'>
            <Input
              className='min-w-48 flex-1'
              aria-label={t('Search models')}
              placeholder={t('Search models')}
              value={search}
              onChange={(event) => setSearch(event.target.value)}
            />
            <Combobox
              className='w-48'
              aria-label={t('Filter models')}
              value={filter}
              onValueChange={(value) => setFilter(value ?? 'all')}
              options={[
                { value: 'all', label: t('All models') },
                { value: 'changed', label: t('Changed models') },
                { value: 'unassigned', label: t('Missing vendor') },
                { value: 'todo', label: t('Incomplete') },
                { value: 'ready', label: t('Configured') },
              ]}
            />
          </div>
        </div>
        {validation && !priceValidation && (
          <p role='alert' className='text-destructive text-sm'>
            {validation}
          </p>
        )}
        {!pricing.data ? (
          <LoadingState />
        ) : (
          <ModelTable
            rows={visible}
            snapshot={props.snapshot}
            endpointOptions={endpointOptions}
            onActiveRowChange={setActiveSource}
            selected={selected}
            onSelectionChange={setSelected}
            pricing={pricing.data}
            busy={busy}
            onEdit={(row) => {
              setEditMode('uniform')
              setEditRows([row])
            }}
            onPatch={patch}
          />
        )}
      </section>
      <footer className='batch-footer'>
        <span className='text-muted-foreground text-sm'>
          {t('Selected models: {{count}}', { count: selected.size })} ·{' '}
          {t('Unsaved models')}: {dirty.length}
        </span>
        <div className='flex flex-wrap gap-2'>
          <Button
            variant='outline'
            disabled={!dirty.length || save.isPending}
            onClick={() => setDiscard(true)}
          >
            {t('Discard changes')}
          </Button>
          <Button
            disabled={
              !dirty.length ||
              Boolean(validation) ||
              busy ||
              prepare.isPending ||
              save.isPending
            }
            onClick={() => prepare.mutate()}
          >
            {prepare.isPending ? t('Loading...') : t('Review and save')}
          </Button>
        </div>
      </footer>
      {editRows && pricing.data && (
        <BatchDialog
          initialMode={editMode}
          rows={editRows}
          vendors={props.snapshot.vendors}
          snapshot={props.snapshot}
          endpointOptions={endpointOptions}
          pricing={pricing.data}
          onClose={() => setEditRows(null)}
          onApply={(updates) =>
            setRows((current) =>
              current.map(
                (row) =>
                  updates.find(
                    (update) => update.source_name === row.source_name
                  ) ?? row
              )
            )
          }
        />
      )}
      {review && (
        <ReviewDialog
          snapshot={props.snapshot}
          rows={dirty}
          pricing={review}
          effective={reviewEntries}
          isSaving={save.isPending}
          error={save.error?.message}
          onClose={() => setReview(null)}
          onSave={() => save.mutate()}
          onReload={() => setDiscard(true)}
        />
      )}
      <ConfirmDialog
        open={discard || blocker.status === 'blocked'}
        onOpenChange={(open) => {
          if (!open) {
            setDiscard(false)
            if (blocker.status === 'blocked') blocker.reset()
          }
        }}
        title={t('Discard changes')}
        desc={t('Discard unsaved model changes?')}
        confirmText={t('Discard changes')}
        handleConfirm={() => {
          setRows(createDrafts(props.snapshot))
          setSelected(new Set())
          setDiscard(false)
          void invalidateModelPricing(client)
          void props.reload()
          if (blocker.status === 'blocked') blocker.proceed()
        }}
      />
    </div>
  )
}
