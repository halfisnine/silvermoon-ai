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
import { useId, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import {
  Field,
  FieldDescription,
  FieldGroup,
  FieldLabel,
  FieldLegend,
  FieldSet,
} from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'

import {
  deriveModelMappingPairs,
  type ModelMappingDirection,
  type ModelMappingPair,
  type ModelMappingRule,
} from '../lib'
import {
  ModelMappingConfirmation,
  ModelMappingPreview,
} from './model-mapping-confirmation'
import { UpstreamModelSelection } from './upstream-model-selection'

/** Where the selectable names come from. */
export type ModelMappingBatchSource = 'upstream' | 'channel'

export type ModelMappingBatchResult = {
  pairs: ModelMappingPair[]
  /** Publish the request names and drop the raw upstream names from the model list. */
  syncModels: boolean
}

type ModelMappingBatchDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  upstreamModels?: string[]
  channelModels: string[]
  initialSource?: ModelMappingBatchSource
  currentMapping?: string
  onApply: (result: ModelMappingBatchResult) => boolean | void
}

export function ModelMappingBatchDialog(props: ModelMappingBatchDialogProps) {
  const { t } = useTranslation()
  const id = useId()
  const upstreamModels = props.upstreamModels ?? []
  const hasUpstream = upstreamModels.length > 0
  const [source, setSource] = useState<ModelMappingBatchSource>(() =>
    hasUpstream ? (props.initialSource ?? 'upstream') : 'channel'
  )
  // Opening a batch action never opts models into a change on the user's behalf.
  const [selected, setSelected] = useState<string[]>([])
  const [direction, setDirection] = useState<ModelMappingDirection>(
    source === 'upstream' ? 'upstream' : 'request'
  )
  const [ruleType, setRuleType] = useState<ModelMappingRule['type']>(
    direction === 'upstream' ? 'strip-suffix' : 'add-suffix'
  )
  const [affix, setAffix] = useState('')
  const [find, setFind] = useState('')
  const [replaceWith, setReplaceWith] = useState('')
  const [syncModels, setSyncModels] = useState(true)
  const [confirmation, setConfirmation] = useState(false)
  const [draft, setDraft] = useState<
    Array<ModelMappingPair & { publish: boolean }>
  >([])
  const [failed, setFailed] = useState(false)
  const applying = useRef(false)
  const candidates =
    source === 'upstream' ? upstreamModels : props.channelModels
  const rule = useMemo<ModelMappingRule>(() => {
    if (ruleType === 'strip-prefix' || ruleType === 'strip-suffix') {
      return { type: ruleType, values: affix }
    }
    if (ruleType === 'add-prefix' || ruleType === 'add-suffix') {
      return { type: ruleType, value: affix }
    }
    return { type: 'replace', find, replaceWith }
  }, [ruleType, affix, find, replaceWith])
  const derivation = useMemo(
    () => deriveModelMappingPairs(selected, direction, rule),
    [selected, direction, rule]
  )
  // Later batches replace earlier entries with the same request name.
  // Nothing reaches the parent form until the final confirmation.
  const pending = useMemo(() => {
    const mappings = new Map(draft.map((pair) => [pair.from, pair]))
    for (const pair of derivation.pairs) {
      mappings.set(pair.from, { ...pair, publish: direction === 'upstream' })
    }
    return [...mappings.values()]
  }, [draft, derivation.pairs, direction])
  const pendingPairs = pending.map(({ from, to }) => ({ from, to }))
  const canSyncModels = pending.some((pair) => pair.publish)
  const hasRule =
    ruleType === 'replace' ? find.length > 0 : affix.trim().length > 0
  const isPrefix = ruleType === 'add-prefix' || ruleType === 'strip-prefix'
  const isStrip = ruleType === 'strip-prefix' || ruleType === 'strip-suffix'
  const ruleOptions: Array<{ value: ModelMappingRule['type']; label: string }> =
    [
      { value: 'strip-suffix', label: t('Strip suffix') },
      { value: 'strip-prefix', label: t('Strip prefix') },
      { value: 'add-suffix', label: t('Add suffix') },
      { value: 'add-prefix', label: t('Add prefix') },
      { value: 'replace', label: t('Replace text') },
    ]

  return (
    <Dialog
      open={props.open}
      onOpenChange={props.onOpenChange}
      title={t('Batch add mappings')}
      description={t(
        'Users call the model on the left. The platform forwards the request to the upstream model on the right.'
      )}
      contentClassName='sm:max-w-4xl'
      bodyClassName='flex flex-col gap-5'
      footerClassName='flex-wrap'
      footer={
        <>
          <p
            className='text-muted-foreground w-full text-sm sm:mr-auto sm:w-auto'
            aria-live='polite'
          >
            {t('Pending mappings: {{count}}', {
              count: derivation.pairs.length,
            })}
          </p>
          <Button
            type='button'
            variant='outline'
            onClick={() => props.onOpenChange(false)}
          >
            {t('Cancel')}
          </Button>
          <Button
            type='button'
            variant='outline'
            disabled={pending.length === 0}
            onClick={() => {
              applying.current = false
              setFailed(false)
              setConfirmation(true)
            }}
          >
            {t('Finish adding')}
          </Button>
          <Button
            type='button'
            disabled={!hasRule || derivation.pairs.length === 0}
            onClick={() => {
              setDraft(pending)
              setSelected([])
            }}
          >
            {t('Continue adding')}
          </Button>
        </>
      }
    >
      {draft.length > 0 && (
        <section
          aria-label={t('Draft mappings: {{count}}', { count: draft.length })}
          className='space-y-3 rounded-lg border p-3'
        >
          <p role='status' className='text-sm font-medium'>
            {t('Draft mappings: {{count}}', { count: draft.length })}
          </p>
          <p className='text-muted-foreground text-xs'>
            {t(
              'Mappings stay in this dialog until you finish and confirm. Later batches replace earlier mappings with the same request name.'
            )}
          </p>
          <ModelMappingPreview pairs={draft} />
        </section>
      )}
      <FieldSet>
        <FieldLegend id={`${id}-task`} variant='label'>
          {t('What do you want to change?')}
        </FieldLegend>
        <RadioGroup
          aria-labelledby={`${id}-task`}
          value={direction}
          className='sm:grid-cols-2'
          onValueChange={(value) => {
            if (value !== 'upstream' && value !== 'request') return
            setDirection(value)
            setRuleType(value === 'upstream' ? 'strip-suffix' : 'add-suffix')
            setAffix('')
            setFind('')
            setReplaceWith('')
          }}
        >
          <FieldLabel htmlFor={`${id}-aliases`}>
            <Field orientation='horizontal'>
              <RadioGroupItem
                id={`${id}-aliases`}
                value='upstream'
                aria-labelledby={`${id}-aliases-label`}
                aria-describedby={`${id}-aliases-hint`}
              />
              <div className='min-w-0'>
                <span id={`${id}-aliases-label`}>
                  {t('Create aliases for users')}
                </span>
                <FieldDescription id={`${id}-aliases-hint`}>
                  {t('Keep upstream names; choose what users call.')}
                </FieldDescription>
              </div>
            </Field>
          </FieldLabel>
          <FieldLabel htmlFor={`${id}-upstream`}>
            <Field orientation='horizontal'>
              <RadioGroupItem
                id={`${id}-upstream`}
                value='request'
                aria-labelledby={`${id}-upstream-label`}
                aria-describedby={`${id}-upstream-hint`}
              />
              <div className='min-w-0'>
                <span id={`${id}-upstream-label`}>
                  {t('Change upstream model names')}
                </span>
                <FieldDescription id={`${id}-upstream-hint`}>
                  {t(
                    'Keep the names users call; change what is sent upstream.'
                  )}
                </FieldDescription>
              </div>
            </Field>
          </FieldLabel>
        </RadioGroup>
      </FieldSet>
      <div className='grid gap-6 md:grid-cols-2'>
        <section
          aria-labelledby={`${id}-models`}
          className='flex min-w-0 flex-col gap-3'
        >
          <h3 id={`${id}-models`} className='text-sm font-semibold'>
            {direction === 'upstream'
              ? t('1. Select upstream models')
              : t('1. Select models users call')}
          </h3>
          {hasUpstream && (
            <Tabs
              value={source}
              onValueChange={(value) => {
                if (value !== 'channel' && value !== 'upstream') return
                setSource(value)
                setSelected([])
              }}
            >
              <TabsList
                variant='line'
                className='grid w-full grid-cols-2 group-data-horizontal/tabs:h-auto'
                aria-label={t('Select models')}
              >
                <TabsTrigger
                  value='upstream'
                  className='h-auto min-h-8 whitespace-normal'
                >
                  <span>
                    {t('Upstream model list')} ({upstreamModels.length})
                  </span>
                </TabsTrigger>
                <TabsTrigger
                  value='channel'
                  className='h-auto min-h-8 whitespace-normal'
                >
                  <span>
                    {t('Channel models')} ({props.channelModels.length})
                  </span>
                </TabsTrigger>
              </TabsList>
            </Tabs>
          )}
          <UpstreamModelSelection
            key={source}
            models={candidates}
            selected={selected}
            existingModels={candidates}
            onChange={setSelected}
            showChanges={false}
            summaryText={
              source === 'channel'
                ? t('Current models: {{count}}', { count: candidates.length })
                : undefined
            }
          />
        </section>
        <section
          aria-labelledby={`${id}-naming`}
          className='flex min-w-0 flex-col gap-3'
        >
          <h3 id={`${id}-naming`} className='text-sm font-semibold'>
            {direction === 'upstream'
              ? t('2. Set names users call')
              : t('2. Set upstream model names')}
          </h3>
          <FieldGroup>
            <Field>
              <FieldLabel htmlFor={`${id}-rule`}>{t('Rule')}</FieldLabel>
              <Select
                items={ruleOptions}
                value={ruleType}
                onValueChange={(value) => {
                  const option = ruleOptions.find(
                    (item) => item.value === value
                  )
                  if (option) {
                    setRuleType(option.value)
                    setAffix('')
                  }
                }}
              >
                <SelectTrigger id={`${id}-rule`} className='w-full'>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {ruleOptions.map((option) => (
                    <SelectItem key={option.value} value={option.value}>
                      {option.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
            {ruleType === 'replace' ? (
              <div className='grid gap-3 sm:grid-cols-2'>
                <Field>
                  <FieldLabel htmlFor={`${id}-find`}>{t('Find')}</FieldLabel>
                  <Input
                    id={`${id}-find`}
                    placeholder='.'
                    value={find}
                    onChange={(event) => setFind(event.target.value)}
                  />
                </Field>
                <Field>
                  <FieldLabel htmlFor={`${id}-replacement`}>
                    {t('Replace with')}
                  </FieldLabel>
                  <Input
                    id={`${id}-replacement`}
                    placeholder='-'
                    value={replaceWith}
                    onChange={(event) => setReplaceWith(event.target.value)}
                  />
                </Field>
              </div>
            ) : (
              <Field>
                <FieldLabel htmlFor={`${id}-affix`}>
                  {isPrefix ? t('Prefix') : t('Suffix')}
                </FieldLabel>
                <Input
                  id={`${id}-affix`}
                  placeholder={isPrefix ? 'openai/' : '-all'}
                  value={affix}
                  onChange={(event) => setAffix(event.target.value)}
                />
                {isStrip && (
                  <FieldDescription>
                    {t('Separate several values with commas')}
                  </FieldDescription>
                )}
              </Field>
            )}
          </FieldGroup>
          <section
            aria-labelledby={`${id}-preview`}
            className='mt-2 flex min-w-0 flex-col gap-3 rounded-lg border p-3'
          >
            <h3 id={`${id}-preview`} className='text-sm font-semibold'>
              {t('3. Review mappings')}
            </h3>
            {derivation.pairs.length > 0 && hasRule && (
              <ModelMappingPreview pairs={derivation.pairs} />
            )}
            {(!hasRule || selected.length === 0) && (
              <p className='text-muted-foreground text-sm'>
                {selected.length === 0
                  ? t('Select at least one model to preview mappings.')
                  : t('Enter a rule to preview mappings.')}
              </p>
            )}
            {selected.length === 0 && (
              <p className='text-muted-foreground text-xs'>
                {t('Example: users call gpt-4o; upstream receives gpt-4o-all.')}
              </p>
            )}
            {hasRule && derivation.unchanged.length > 0 && (
              <p className='text-muted-foreground text-xs'>
                {t('{{count}} model(s) unchanged by the rule were skipped', {
                  count: derivation.unchanged.length,
                })}
              </p>
            )}
            {hasRule && derivation.conflicts.length > 0 && (
              <p className='text-warning text-xs'>
                {t(
                  '{{count}} model(s) skipped because another model derives the same request name',
                  { count: derivation.conflicts.length }
                )}
              </p>
            )}
          </section>
        </section>
      </div>
      <ModelMappingConfirmation
        open={confirmation}
        onOpenChange={setConfirmation}
        pairs={pendingPairs}
        currentMapping={props.currentMapping ?? ''}
        showSync={canSyncModels}
        syncModels={syncModels}
        onSyncChange={setSyncModels}
        failed={failed}
        onConfirm={() => {
          if (!confirmation || applying.current) return
          applying.current = true
          try {
            const applied = props.onApply({
              pairs: pendingPairs,
              syncModels: canSyncModels && syncModels,
            })
            if (applied === false) {
              applying.current = false
              setFailed(true)
              return
            }
          } catch {
            applying.current = false
            setFailed(true)
            return
          }
          setConfirmation(false)
          props.onOpenChange(false)
        }}
      />
    </Dialog>
  )
}
