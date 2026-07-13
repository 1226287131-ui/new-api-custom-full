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
import { Plus, Save, Trash2 } from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import {
  InputGroup,
  InputGroupAddon,
  InputGroupInput,
} from '@/components/ui/input-group'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'

import { safeJsonParse } from '../utils/json-parser'

type ResolutionTier = '1K' | '2K' | '4K'

type ResolutionPrices = Record<ResolutionTier, number>

type ResolutionPriceMap = Record<string, ResolutionPrices>

const tiers: ResolutionTier[] = ['1K', '2K', '4K']

function ResolutionPriceInput(props: {
  model: string
  tier: ResolutionTier
  value: number
  onCommit: (value: number) => void
}) {
  const [draft, setDraft] = useState(String(props.value))

  useEffect(() => {
    setDraft(String(props.value))
  }, [props.value])

  const commit = () => {
    const next = Number(draft)
    if (!Number.isFinite(next) || next < 0) {
      setDraft(String(props.value))
      return
    }
    props.onCommit(next)
  }

  return (
    <InputGroup className='h-10 min-w-0'>
      <InputGroupAddon>$</InputGroupAddon>
      <InputGroupInput
        type='number'
        min='0'
        step='any'
        inputMode='decimal'
        value={draft}
        aria-label={`${props.model} ${props.tier}`}
        onChange={(event) => setDraft(event.target.value)}
        onBlur={commit}
        onKeyDown={(event) => {
          if (event.key === 'Enter') event.currentTarget.blur()
        }}
      />
      <InputGroupAddon align='inline-end'>/ img</InputGroupAddon>
    </InputGroup>
  )
}

export function ImageResolutionPricingEditor(props: {
  value: string
  onChange: (value: string) => void
  onSave: () => void | Promise<void>
  isSaving: boolean
}) {
  const { t } = useTranslation()
  const [newModel, setNewModel] = useState('')
  const prices = useMemo(
    () =>
      safeJsonParse<ResolutionPriceMap>(props.value, {
        fallback: {},
        silent: true,
      }),
    [props.value]
  )
  const modelNames = useMemo(
    () => Object.keys(prices).sort((a, b) => a.localeCompare(b)),
    [prices]
  )
  const normalizedNewModel = newModel.trim()
  const canAdd =
    normalizedNewModel !== '' && prices[normalizedNewModel] === undefined

  const updatePrices = (next: ResolutionPriceMap) => {
    const sorted = Object.fromEntries(
      Object.entries(next).sort(([left], [right]) => left.localeCompare(right))
    )
    props.onChange(JSON.stringify(sorted, null, 2))
  }

  const addModel = () => {
    if (!canAdd) return
    updatePrices({
      ...prices,
      [normalizedNewModel]: { '1K': 0, '2K': 0, '4K': 0 },
    })
    setNewModel('')
  }

  return (
    <section className='space-y-3' aria-labelledby='image-resolution-pricing'>
      <div className='flex flex-wrap items-center justify-between gap-2'>
        <h3 id='image-resolution-pricing' className='text-sm font-medium'>
          {t('Image resolution pricing')}
        </h3>
        <Button
          type='button'
          size='sm'
          onClick={props.onSave}
          disabled={props.isSaving}
        >
          <Save data-icon='inline-start' />
          {props.isSaving ? t('Saving...') : t('Save changes')}
        </Button>
      </div>

      <div className='grid gap-2 sm:grid-cols-[minmax(0,1fr)_auto]'>
        <Input
          value={newModel}
          placeholder={t('Model name')}
          aria-label={t('Model name')}
          onChange={(event) => setNewModel(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === 'Enter') {
              event.preventDefault()
              addModel()
            }
          }}
        />
        <Button
          type='button'
          variant='outline'
          disabled={!canAdd}
          onClick={addModel}
        >
          <Plus data-icon='inline-start' />
          {t('Add model')}
        </Button>
      </div>

      <div className='overflow-hidden rounded-md border'>
        <div className='bg-muted/40 hidden min-h-10 grid-cols-[minmax(180px,1fr)_repeat(3,minmax(132px,180px))_44px] items-center gap-2 border-b px-3 text-sm font-medium md:grid'>
          <span>{t('Model name')}</span>
          {tiers.map((tier) => (
            <span key={tier}>{tier}</span>
          ))}
          <span className='sr-only'>{t('Delete')}</span>
        </div>

        {modelNames.length === 0 ? (
          <div className='text-muted-foreground px-3 py-8 text-center text-sm'>
            {t('No data')}
          </div>
        ) : (
          modelNames.map((model) => (
            <div
              key={model}
              className='grid gap-3 border-b p-3 last:border-b-0 md:grid-cols-[minmax(180px,1fr)_repeat(3,minmax(132px,180px))_44px] md:items-center md:gap-2'
            >
              <div className='flex min-w-0 items-center justify-between gap-2'>
                <code className='min-w-0 truncate text-sm' title={model}>
                  {model}
                </code>
                <div className='md:hidden'>
                  <Tooltip>
                    <TooltipTrigger
                      render={
                        <Button
                          type='button'
                          variant='ghost'
                          size='icon'
                          className='size-10'
                          aria-label={`${t('Delete')} ${model}`}
                          onClick={() => {
                            const next = { ...prices }
                            delete next[model]
                            updatePrices(next)
                          }}
                        >
                          <Trash2 />
                        </Button>
                      }
                    />
                    <TooltipContent>{t('Delete')}</TooltipContent>
                  </Tooltip>
                </div>
              </div>

              {tiers.map((tier) => (
                <label
                  key={tier}
                  className='grid gap-1.5 sm:grid-cols-[40px_1fr] sm:items-center md:block'
                >
                  <span className='text-muted-foreground text-xs font-medium md:sr-only'>
                    {tier}
                  </span>
                  <ResolutionPriceInput
                    model={model}
                    tier={tier}
                    value={prices[model][tier]}
                    onCommit={(value) =>
                      updatePrices({
                        ...prices,
                        [model]: { ...prices[model], [tier]: value },
                      })
                    }
                  />
                </label>
              ))}

              <div className='hidden md:block'>
                <Tooltip>
                  <TooltipTrigger
                    render={
                      <Button
                        type='button'
                        variant='ghost'
                        size='icon'
                        className='size-10'
                        aria-label={`${t('Delete')} ${model}`}
                        onClick={() => {
                          const next = { ...prices }
                          delete next[model]
                          updatePrices(next)
                        }}
                      >
                        <Trash2 />
                      </Button>
                    }
                  />
                  <TooltipContent>{t('Delete')}</TooltipContent>
                </Tooltip>
              </div>
            </div>
          ))
        )}
      </div>
    </section>
  )
}
