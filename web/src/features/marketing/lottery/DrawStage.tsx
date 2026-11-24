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
import { motion, useReducedMotion } from 'motion/react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'

import { GiftLines } from './GiftLines'
import type { LotteryDraw, LotteryPool } from './types'

import './lottery.css'

const palette = [
  '#bfdbfe',
  '#a7f3d0',
  '#fde68a',
  '#fbcfe8',
  '#ddd6fe',
  '#fed7aa',
]

export function DrawStage(props: {
  pool: LotteryPool
  result?: LotteryDraw | null
  disabled: boolean
  busy: boolean
  onDraw: () => void
  onCloseResult: () => void
}) {
  const { t } = useTranslation()
  const reduced = useReducedMotion()
  const [revealedId, setRevealedId] = useState<number | null>(null)
  const [closedId, setClosedId] = useState<number | null>(null)
  const result = props.result
  const revealed = !!result && (reduced || revealedId === result.id)
  const animating = props.busy || (!!result && !revealed)
  const reveal = () => {
    if (result) setRevealedId(result.id)
  }
  const index = props.pool.prizes.findIndex(
    (prize) => prize.id === result?.prize_id
  )
  const count = Math.max(1, props.pool.prizes.length)
  let labelSize = 14
  if (count > 20) labelSize = 7
  else if (count > 12) labelSize = 10
  const labelWidth = count > 12 ? 38 : 70
  const rotation =
    result && index >= 0 ? 1800 + (360 - ((index + 0.5) * 360) / count) : 0
  const duration = reduced ? 0 : 2.8
  const label =
    props.pool.style === 'wheel' ? t('Spin once') : t('Open mystery box')
  let resultMessage = t('All gifts from this draw have been credited.')
  if (result?.state === 'pending') {
    resultMessage = t(
      'Your result is saved. Gift delivery is still processing; do not draw again to recover it.'
    )
  } else if (result?.state === 'cleared') {
    resultMessage = t(
      'These rewards were cleared after your refund. They will not be restored.'
    )
  } else if (result?.no_prize) {
    resultMessage = t('Draw completed')
  }
  return (
    <>
      <div className='silvermoon-lottery-stage' aria-busy={animating}>
        {props.pool.style === 'wheel' ? (
          <div className='silvermoon-lottery-wheel'>
            <span className='silvermoon-lottery-pointer' aria-hidden='true' />
            <motion.div
              className='silvermoon-lottery-disc'
              key={result?.id ?? 'ready'}
              initial={{ rotate: 0 }}
              animate={{
                rotate: !result && props.busy && !reduced ? 360 : rotation,
              }}
              transition={
                !result && props.busy && !reduced
                  ? { duration: 1, ease: 'linear', repeat: Infinity }
                  : { duration, ease: [0.12, 0.72, 0.12, 1] }
              }
              onAnimationComplete={reveal}
            >
              <svg
                viewBox='0 0 360 360'
                role='img'
                aria-label={t('Prize wheel')}
              >
                <circle cx='180' cy='180' r='178' fill='#fff7ed' />
                {props.pool.prizes.map((prize, i) => {
                  const start = (i * Math.PI * 2) / count - Math.PI / 2
                  const end = ((i + 1) * Math.PI * 2) / count - Math.PI / 2
                  const mid = (start + end) / 2
                  const fill = prize.available
                    ? palette[i % palette.length]
                    : '#e2e8f0'
                  let shape = `M180 180 L${180 + 170 * Math.cos(start)} ${180 + 170 * Math.sin(start)} A170 170 0 ${count === 1 ? 1 : 0} 1 ${180 + 170 * Math.cos(end)} ${180 + 170 * Math.sin(end)} Z`
                  if (count === 1) shape = 'M180 10 A170 170 0 1 1 179.999 10 Z'
                  return (
                    <g key={prize.id}>
                      <path
                        d={shape}
                        fill={fill}
                        stroke='white'
                        strokeWidth='2'
                      />
                      <text
                        x={180 + 128 * Math.cos(mid)}
                        y={186 + 128 * Math.sin(mid)}
                        textAnchor='middle'
                        fontSize={labelSize}
                        textLength={
                          prize.name.length > 6 ? labelWidth : undefined
                        }
                        lengthAdjust='spacingAndGlyphs'
                        fill='#334155'
                        fontWeight='700'
                      >
                        {prize.name}
                      </text>
                    </g>
                  )
                })}
              </svg>
            </motion.div>
            <Button
              className='silvermoon-lottery-go'
              disabled={props.disabled || !!result}
              onClick={props.onDraw}
              aria-label={label}
            >
              {animating ? t('Drawing…') : label}
            </Button>
          </div>
        ) : (
          <div className='silvermoon-lottery-box-stage'>
            <Button
              className='silvermoon-lottery-box-button'
              variant='ghost'
              disabled={props.disabled || !!result}
              onClick={props.onDraw}
              aria-label={label}
            >
              <motion.svg
                className='size-full'
                viewBox='0 0 160 160'
                aria-hidden='true'
                animate={{
                  rotate: props.busy && !reduced ? [0, -5, 5, -3, 0] : 0,
                }}
                transition={{
                  duration: 0.6,
                  repeat: props.busy && !reduced ? Infinity : 0,
                }}
              >
                <ellipse
                  cx='80'
                  cy='141'
                  rx='55'
                  ry='10'
                  fill='#c4b5fd'
                  opacity='.35'
                />
                <path
                  d='M29 62L80 82L131 62V119L80 141L29 119Z'
                  fill='#a78bfa'
                />
                <path d='M80 82L131 62V119L80 141Z' fill='#7c3aed' />
                <path d='M72 78L87 84V137L72 130Z' fill='#fde68a' />
                {result && (
                  <motion.circle
                    key={`glow-${result.id}`}
                    cx='80'
                    cy='65'
                    r='28'
                    fill='#fde68a'
                    initial={{ opacity: 0, scale: 0 }}
                    animate={{
                      opacity: [0, 0, 0.9, 0.5],
                      scale: [0, 0, 1.3, 1],
                    }}
                    transition={{
                      duration: reduced ? 0 : 1.8,
                      times: [0, 0.4, 0.8, 1],
                    }}
                    style={{ transformOrigin: '80px 65px' }}
                  />
                )}
                <motion.g
                  key={result?.id ?? 'ready'}
                  initial={{ y: 0, rotate: 0 }}
                  animate={
                    result
                      ? { y: [0, -4, 0, -40], rotate: [0, -5, 5, -16] }
                      : { y: 0, rotate: 0 }
                  }
                  transition={{
                    duration: reduced ? 0 : 1.8,
                    times: [0, 0.2, 0.4, 1],
                    ease: 'easeInOut',
                  }}
                  style={{ transformOrigin: '80px 60px' }}
                  onAnimationComplete={reveal}
                >
                  <path d='M22 59L80 30L138 59L80 85Z' fill='#ddd6fe' />
                  <path d='M22 59L80 85V95L22 69Z' fill='#a78bfa' />
                  <path d='M80 85L138 59V69L80 95Z' fill='#8b5cf6' />
                  <path d='M72 34L87 26V88L72 82Z' fill='#fde68a' />
                  <path
                    d='M80 32C36 9 47 65 80 32C113 0 131 55 80 32Z'
                    fill='none'
                    stroke='#fbbf24'
                    strokeWidth='9'
                  />
                </motion.g>
              </motion.svg>
              <span>{animating ? t('Drawing…') : label}</span>
            </Button>
          </div>
        )}
        {props.busy && !result && (
          <p role='status' className='text-sm font-medium'>
            {t('Drawing…')}
          </p>
        )}
        {!!result && !revealed && (
          <Button variant='outline' onClick={reveal}>
            {t('Skip animation')}
          </Button>
        )}
        {!!result && revealed && closedId === result.id && (
          <Button variant='outline' onClick={() => setClosedId(null)}>
            {t('View draw result')}
          </Button>
        )}
        <p className='text-muted-foreground text-center text-sm'>
          {t('Each draw uses one chance. Gifts are credited automatically.')}
        </p>
        {props.pool.style === 'wheel' && (
          <p className='text-muted-foreground text-center text-xs'>
            {t('Wheel segments illustrate prizes, not their odds.')}
          </p>
        )}
      </div>
      {result && (
        <Dialog
          open={revealed && closedId !== result.id}
          title={result.no_prize ? t('No prize') : t('Your draw result')}
          onOpenChange={(open) => {
            if (!open) {
              setClosedId(result.id)
              props.onCloseResult()
            }
          }}
          footer={
            <Button
              onClick={() => {
                setClosedId(result.id)
                props.onCloseResult()
              }}
            >
              {t('Close')}
            </Button>
          }
        >
          <div className='space-y-4'>
            <h3 className='text-lg font-semibold break-words'>
              {result.prize_name}
            </h3>
            {result.no_prize ? (
              <p>{t('This draw used one chance. No gift was awarded.')}</p>
            ) : (
              <GiftLines gifts={result.rewards} awarded />
            )}
            <p role='status'>{resultMessage}</p>
          </div>
        </Dialog>
      )}
    </>
  )
}
