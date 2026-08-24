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
import { AlertTriangle, Save } from 'lucide-react'
import {
  forwardRef,
  useCallback,
  useEffect,
  useImperativeHandle,
  useMemo,
  useState,
} from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import { sideDrawerContentClassName } from '@/components/drawer-layout'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  Field,
  FieldDescription,
  FieldGroup,
  FieldLabel,
} from '@/components/ui/field'
import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import {
  InputGroup,
  InputGroupAddon,
  InputGroupInput,
} from '@/components/ui/input-group'
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { Switch } from '@/components/ui/switch'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { cn } from '@/lib/utils'

import {
  EMPTY_LANE_ENABLED,
  EMPTY_LANE_PRICES,
  buildPreviewRows,
  createInitialLaneState,
  createModelPricingSchema,
  hasValue,
  laneConfigs,
  numericDraftRegex,
  ratioFieldByLane,
  toNumberOrNull,
  type LaneKey,
  type ModelPricingFormValues,
  type ModelRatioData,
  type PricingMode,
  type ScheduledDiscountConfig,
  type TaskResolution,
} from './model-pricing-core'
import { PriceInput, PriceLane } from './model-pricing-inputs'
import { formatPricingNumber } from './pricing-format'
import { TieredPricingEditor } from './tiered-pricing-editor'

export type { ModelRatioData } from './model-pricing-core'

type ModelPricingSheetProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  editData?: ModelRatioData | null
  onSave?: () => void | Promise<void>
  isSaving?: boolean
}

const taskResolutions: TaskResolution[] = [
  '480p',
  '720p',
  '768p',
  '1080p',
  '1440p',
  '4k',
]

type ParsedTaskBillingPricing = {
  mode: 'per-request' | 'per-second'
  defaultPrice: string
  resolutionPrices: Record<TaskResolution, string>
}

function emptyTaskResolutionPrices(): Record<TaskResolution, string> {
  return {
    '480p': '',
    '768p': '',
    '720p': '',
    '1080p': '',
    '1440p': '',
    '4k': '',
  }
}

function formatTaskResolutionLabel(resolution: TaskResolution): string {
  if (resolution === '4k') return '4K'
  if (resolution === '1440p') return '2K'
  if (resolution === '768p') return '768P'
  return resolution
}

function parseTaskBillingPricing(
  value: string | undefined
): ParsedTaskBillingPricing | null {
  if (!value) return null
  try {
    const parsed: unknown = JSON.parse(value)
    if (!parsed || typeof parsed !== 'object') return null
    const record = parsed as Record<string, unknown>
    if (record.mode !== 'per-request' && record.mode !== 'per-second') {
      return null
    }
    const resolutionPrices = emptyTaskResolutionPrices()
    if (
      record.resolution_prices &&
      typeof record.resolution_prices === 'object'
    ) {
      const source = record.resolution_prices as Record<string, unknown>
      taskResolutions.forEach((resolution) => {
        const price = source[resolution]
        if (typeof price === 'number' && Number.isFinite(price)) {
          resolutionPrices[resolution] = String(price)
        } else if (typeof price === 'string' && price.trim() !== '') {
          resolutionPrices[resolution] = price
        }
      })
    }
    let defaultPrice = ''
    if (
      typeof record.default_price === 'number' &&
      Number.isFinite(record.default_price)
    ) {
      defaultPrice = String(record.default_price)
    } else if (typeof record.default_price === 'string') {
      defaultPrice = record.default_price
    }
    return {
      mode: record.mode,
      defaultPrice,
      resolutionPrices,
    }
  } catch {
    return null
  }
}

type ParsedScheduledDiscount = ScheduledDiscountConfig & {
  discountPercent: string
}

function parseScheduledDiscount(
  value: string | undefined
): ParsedScheduledDiscount | null {
  if (!value) return null
  try {
    const parsed: unknown = JSON.parse(value)
    if (!parsed || typeof parsed !== 'object') return null
    const record = parsed as Record<string, unknown>
    const discount = Number(record.discount)
    if (
      typeof record.enabled !== 'boolean' ||
      typeof record.start !== 'string' ||
      typeof record.end !== 'string' ||
      !Number.isFinite(discount)
    ) {
      return null
    }
    return {
      enabled: record.enabled,
      start: record.start,
      end: record.end,
      discount,
      discountPercent: String(
        Math.max(0, Number(((1 - discount) * 100).toFixed(2)))
      ),
    }
  } catch {
    return null
  }
}

function TaskBillingPricingFields(props: {
  enabled: boolean
  onEnabledChange: (enabled: boolean) => void
  prices: Record<TaskResolution, string>
  onPriceChange: (resolution: TaskResolution, value: string) => void
  mode: 'per-request' | 'per-second'
}) {
  const { t } = useTranslation()
  return (
    <div className='bg-muted/20 space-y-4 rounded-lg border p-3'>
      <div className='flex items-start justify-between gap-4'>
        <div className='min-w-0'>
          <FieldLabel>{t('Resolution-aware video task pricing')}</FieldLabel>
          <FieldDescription>
            {t(
              'Set prices by output resolution. The default price above is disabled while this is enabled, and blank resolutions are rejected.'
            )}
          </FieldDescription>
        </div>
        <Switch
          checked={props.enabled}
          onCheckedChange={props.onEnabledChange}
          aria-label={t('Enable resolution-aware video task pricing')}
        />
      </div>
      {props.enabled && (
        <div className='grid gap-3 sm:grid-cols-2'>
          {taskResolutions.map((resolution) => (
            <Field key={resolution}>
              <FieldLabel>{formatTaskResolutionLabel(resolution)}</FieldLabel>
              <InputGroup>
                <InputGroupAddon>$</InputGroupAddon>
                <InputGroupInput
                  inputMode='decimal'
                  placeholder={props.mode === 'per-second' ? '0.01' : '0.08'}
                  value={props.prices[resolution]}
                  onChange={(event) =>
                    props.onPriceChange(resolution, event.target.value)
                  }
                />
                <InputGroupAddon align='inline-end'>
                  {t(
                    props.mode === 'per-second' ? 'per second' : 'per request'
                  )}
                </InputGroupAddon>
              </InputGroup>
            </Field>
          ))}
        </div>
      )}
    </div>
  )
}

function ScheduledDiscountFields(props: {
  enabled: boolean
  onEnabledChange: (enabled: boolean) => void
  start: string
  end: string
  discountPercent: string
  onStartChange: (value: string) => void
  onEndChange: (value: string) => void
  onDiscountChange: (value: string) => void
}) {
  const { t } = useTranslation()
  return (
    <div className='bg-muted/20 space-y-4 rounded-lg border p-3'>
      <div className='flex items-start justify-between gap-4'>
        <div className='min-w-0'>
          <FieldLabel>{t('Scheduled model discount')}</FieldLabel>
          <FieldDescription>
            {t(
              'Apply a daily discount window using Beijing time. The discount is combined with the user group multiplier.'
            )}
          </FieldDescription>
        </div>
        <Switch
          checked={props.enabled}
          onCheckedChange={props.onEnabledChange}
          aria-label={t('Enable scheduled model discount')}
        />
      </div>
      {props.enabled && (
        <div className='grid gap-3 sm:grid-cols-3'>
          <Field>
            <FieldLabel>{t('Start time')}</FieldLabel>
            <Input
              type='time'
              step='60'
              value={props.start}
              onChange={(event) => props.onStartChange(event.target.value)}
            />
          </Field>
          <Field>
            <FieldLabel>{t('End time')}</FieldLabel>
            <Input
              type='time'
              step='60'
              value={props.end}
              onChange={(event) => props.onEndChange(event.target.value)}
            />
          </Field>
          <Field>
            <FieldLabel>{t('Discount percentage')}</FieldLabel>
            <InputGroup>
              <InputGroupInput
                inputMode='decimal'
                min='0'
                max='99.99'
                value={props.discountPercent}
                onChange={(event) => props.onDiscountChange(event.target.value)}
              />
              <InputGroupAddon align='inline-end'>%</InputGroupAddon>
            </InputGroup>
            <FieldDescription>
              {t('For example, 20 means the model is charged at 80% of its normal price.')}
            </FieldDescription>
          </Field>
        </div>
      )}
    </div>
  )
}

type ModelPricingEditorPanelProps = Omit<
  ModelPricingSheetProps,
  'open' | 'onOpenChange'
> & {
  className?: string
}

export type ModelPricingEditorPanelHandle = {
  commitDraft: () => Promise<ModelRatioData | null>
}

export const ModelPricingSheet = forwardRef<
  ModelPricingEditorPanelHandle,
  ModelPricingSheetProps
>(function ModelPricingSheet(
  { open, onOpenChange, editData, onSave, isSaving },
  ref
) {
  const { t } = useTranslation()
  const title = editData ? t('Edit model pricing') : t('Add model pricing')
  const description = editData?.name || t('New model')

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent
        side='right'
        className={sideDrawerContentClassName('sm:max-w-2xl')}
      >
        <SheetHeader className='sr-only'>
          <SheetTitle>{title}</SheetTitle>
          <SheetDescription>{description}</SheetDescription>
        </SheetHeader>
        <ModelPricingEditorPanel
          ref={ref}
          editData={editData}
          onSave={onSave}
          isSaving={isSaving}
          className='h-full rounded-none border-0'
        />
      </SheetContent>
    </Sheet>
  )
})

export const ModelPricingEditorPanel = forwardRef<
  ModelPricingEditorPanelHandle,
  ModelPricingEditorPanelProps
>(function ModelPricingEditorPanel(
  { editData, className, onSave, isSaving },
  ref
) {
  const { t } = useTranslation()
  const [pricingMode, setPricingMode] = useState<PricingMode>('per-token')
  const [promptPrice, setPromptPrice] = useState('')
  const [lanePrices, setLanePrices] = useState<Record<LaneKey, string>>({
    ...EMPTY_LANE_PRICES,
  })
  const [laneEnabled, setLaneEnabled] = useState<Record<LaneKey, boolean>>({
    ...EMPTY_LANE_ENABLED,
  })
  const [billingExpr, setBillingExpr] = useState('')
  const [requestRuleExpr, setRequestRuleExpr] = useState('')
  const [taskPricingEnabled, setTaskPricingEnabled] = useState(false)
  const [taskResolutionPrices, setTaskResolutionPrices] = useState<
    Record<TaskResolution, string>
  >(emptyTaskResolutionPrices)
  const [scheduledDiscountEnabled, setScheduledDiscountEnabled] =
    useState(false)
  const [scheduledDiscountStart, setScheduledDiscountStart] = useState('')
  const [scheduledDiscountEnd, setScheduledDiscountEnd] = useState('')
  const [scheduledDiscountPercent, setScheduledDiscountPercent] = useState('')
  const [editorReloadToken, setEditorReloadToken] = useState(0)
  const isEditMode = !!editData

  const form = useForm<ModelPricingFormValues>({
    resolver: zodResolver(createModelPricingSchema(t)),
    defaultValues: {
      name: '',
      price: '',
      ratio: '',
      cacheRatio: '',
      createCacheRatio: '',
      completionRatio: '',
      imageRatio: '',
      audioRatio: '',
      audioCompletionRatio: '',
    },
  })

  useEffect(() => {
    const nextLaneState = createInitialLaneState(editData)

    if (editData) {
      const taskPricing = parseTaskBillingPricing(editData.taskBillingPricing)
      const scheduledDiscount = parseScheduledDiscount(editData.scheduledDiscount)
      form.reset({
        name: editData.name,
        price: editData.price || taskPricing?.defaultPrice || '',
        ratio: editData.ratio || '',
        cacheRatio: editData.cacheRatio || '',
        createCacheRatio: editData.createCacheRatio || '',
        completionRatio: editData.completionRatio || '',
        imageRatio: editData.imageRatio || '',
        audioRatio: editData.audioRatio || '',
        audioCompletionRatio: editData.audioCompletionRatio || '',
      })
      let nextPricingMode: PricingMode = 'per-token'
      if (editData.billingMode === 'tiered_expr') {
        nextPricingMode = 'tiered_expr'
      } else if (editData.billingMode === 'per-second') {
        nextPricingMode = 'per-second'
      } else if (editData.billingMode === 'per-request' || editData.price) {
        nextPricingMode = 'per-request'
      }
      setPricingMode(nextPricingMode)
      setBillingExpr(editData.billingExpr || '')
      setRequestRuleExpr(editData.requestRuleExpr || '')
      setTaskPricingEnabled(Boolean(taskPricing))
      setTaskResolutionPrices(
        taskPricing
          ? {
              ...emptyTaskResolutionPrices(),
              ...taskPricing.resolutionPrices,
            }
          : emptyTaskResolutionPrices()
      )
      setScheduledDiscountEnabled(Boolean(scheduledDiscount?.enabled))
      setScheduledDiscountStart(scheduledDiscount?.start || '')
      setScheduledDiscountEnd(scheduledDiscount?.end || '')
      setScheduledDiscountPercent(scheduledDiscount?.discountPercent || '')
    } else {
      form.reset({
        name: '',
        price: '',
        ratio: '',
        cacheRatio: '',
        createCacheRatio: '',
        completionRatio: '',
        imageRatio: '',
        audioRatio: '',
        audioCompletionRatio: '',
      })
      setPricingMode('per-token')
      setBillingExpr('')
      setRequestRuleExpr('')
      setTaskPricingEnabled(false)
      setTaskResolutionPrices(emptyTaskResolutionPrices())
      setScheduledDiscountEnabled(false)
      setScheduledDiscountStart('')
      setScheduledDiscountEnd('')
      setScheduledDiscountPercent('')
    }

    setPromptPrice(nextLaneState.promptPrice)
    setLanePrices(nextLaneState.prices)
    setLaneEnabled(nextLaneState.enabled)
    setEditorReloadToken((token) => token + 1)
  }, [editData, form])

  const setFormValue = (field: keyof ModelPricingFormValues, value: string) => {
    form.setValue(field, value, {
      shouldDirty: true,
      shouldValidate: true,
    })
  }

  const deriveLaneRatio = (
    lane: LaneKey,
    price: string,
    nextPromptPrice = promptPrice,
    nextLanePrices = lanePrices
  ) => {
    const priceNumber = toNumberOrNull(price)
    if (priceNumber === null) return ''

    if (lane === 'audioOutput') {
      const audioInputPrice = toNumberOrNull(nextLanePrices.audioInput)
      if (audioInputPrice === null || audioInputPrice === 0) return ''
      return formatPricingNumber(priceNumber / audioInputPrice)
    }

    const inputPrice = toNumberOrNull(nextPromptPrice)
    if (inputPrice === null || inputPrice === 0) return ''
    return formatPricingNumber(priceNumber / inputPrice)
  }

  const syncLaneRatios = (
    nextPromptPrice = promptPrice,
    nextLanePrices = lanePrices,
    nextLaneEnabled = laneEnabled
  ) => {
    const inputPrice = toNumberOrNull(nextPromptPrice)
    setFormValue(
      'ratio',
      inputPrice !== null ? formatPricingNumber(inputPrice / 2) : ''
    )

    laneConfigs.forEach(({ key }) => {
      const ratioField = ratioFieldByLane[key]
      if (!nextLaneEnabled[key]) {
        setFormValue(ratioField, '')
        return
      }
      setFormValue(
        ratioField,
        deriveLaneRatio(
          key,
          nextLanePrices[key],
          nextPromptPrice,
          nextLanePrices
        )
      )
    })
  }

  const handlePromptPriceChange = (value: string) => {
    if (!numericDraftRegex.test(value)) return
    setPromptPrice(value)
    syncLaneRatios(value, lanePrices, laneEnabled)
  }

  const handleLanePriceChange = (lane: LaneKey, value: string) => {
    if (!numericDraftRegex.test(value)) return
    const nextLanePrices = { ...lanePrices, [lane]: value }
    setLanePrices(nextLanePrices)

    if (laneEnabled[lane]) {
      setFormValue(
        ratioFieldByLane[lane],
        deriveLaneRatio(lane, value, promptPrice, nextLanePrices)
      )
    }

    if (lane === 'audioInput' && laneEnabled.audioOutput) {
      setFormValue(
        'audioCompletionRatio',
        deriveLaneRatio(
          'audioOutput',
          nextLanePrices.audioOutput,
          promptPrice,
          nextLanePrices
        )
      )
    }
  }

  const handleLaneToggle = (lane: LaneKey, checked: boolean) => {
    const nextEnabled = { ...laneEnabled, [lane]: checked }
    let nextPrices = lanePrices

    if (!checked) {
      nextPrices = { ...nextPrices, [lane]: '' }
      setFormValue(ratioFieldByLane[lane], '')
      if (lane === 'audioInput') {
        nextEnabled.audioOutput = false
        nextPrices.audioOutput = ''
        setFormValue('audioCompletionRatio', '')
      }
    }

    setLaneEnabled(nextEnabled)
    setLanePrices(nextPrices)

    if (checked) {
      setFormValue(
        ratioFieldByLane[lane],
        deriveLaneRatio(lane, nextPrices[lane], promptPrice, nextPrices)
      )
    }
  }

  const handleModeChange = (value: string) => {
    const nextMode = value as PricingMode
    setPricingMode(nextMode)
    if (nextMode === 'tiered_expr' && !billingExpr) {
      setBillingExpr('tier("base", p * 0 + c * 0)')
    }
  }

  const handleTaskResolutionPriceChange = (
    resolution: TaskResolution,
    value: string
  ) => {
    if (!numericDraftRegex.test(value)) return
    setTaskResolutionPrices((previous) => ({
      ...previous,
      [resolution]: value,
    }))
  }

  const hasTaskResolutionPrice = taskResolutions.some(
    (resolution) => toNumberOrNull(taskResolutionPrices[resolution]) !== null
  )

  const scheduledDiscountError = useMemo(() => {
    if (!scheduledDiscountEnabled) return ''
    if (!/^([01]\d|2[0-3]):[0-5]\d$/.test(scheduledDiscountStart)) {
      return t('Start time must use HH:MM.')
    }
    if (!/^([01]\d|2[0-3]):[0-5]\d$/.test(scheduledDiscountEnd)) {
      return t('End time must use HH:MM.')
    }
    if (scheduledDiscountStart === scheduledDiscountEnd) {
      return t('Start time and end time cannot be the same.')
    }
    const percent = Number(scheduledDiscountPercent)
    if (!Number.isFinite(percent) || percent < 0 || percent >= 100) {
      return t('Discount percentage must be between 0 and 99.99.')
    }
    return ''
  }, [
    scheduledDiscountEnabled,
    scheduledDiscountEnd,
    scheduledDiscountPercent,
    scheduledDiscountStart,
    t,
  ])

  const watchedValues = form.watch()
  const previewRows = useMemo(
    () =>
      buildPreviewRows(
        watchedValues,
        pricingMode,
        billingExpr,
        requestRuleExpr,
        promptPrice,
        lanePrices,
        laneEnabled,
        t,
        taskPricingEnabled,
        taskResolutionPrices
      ),
    [
      billingExpr,
      laneEnabled,
      lanePrices,
      pricingMode,
      promptPrice,
      requestRuleExpr,
      t,
      taskPricingEnabled,
      taskResolutionPrices,
      watchedValues,
    ]
  )

  const warnings = useMemo(() => {
    const nextWarnings: string[] = []
    const hasConflict =
      !!editData?.price &&
      [
        editData.ratio,
        editData.completionRatio,
        editData.cacheRatio,
        editData.createCacheRatio,
        editData.imageRatio,
        editData.audioRatio,
        editData.audioCompletionRatio,
      ].some(hasValue)

    if (hasConflict) {
      nextWarnings.push(
        t(
          'This model has both fixed-price and token-price settings. Saving the current mode will rewrite the conflicting fields.'
        )
      )
    }

    if (
      pricingMode === 'per-token' &&
      toNumberOrNull(promptPrice) === null &&
      laneConfigs.some(
        ({ key }) => laneEnabled[key] && hasValue(lanePrices[key])
      )
    ) {
      nextWarnings.push(
        t('Input price is required before saving dependent prices.')
      )
    }

    if (scheduledDiscountError) nextWarnings.push(scheduledDiscountError)

    if (
      taskPricingEnabled &&
      (pricingMode === 'per-request' || pricingMode === 'per-second') &&
      !hasTaskResolutionPrice
    ) {
      nextWarnings.push(t('Set at least one resolution price before saving.'))
    }

    if (
      pricingMode === 'per-token' &&
      laneEnabled.audioOutput &&
      !hasValue(lanePrices.audioInput)
    ) {
      nextWarnings.push(t('Audio output price requires an audio input price.'))
    }

    return nextWarnings
  }, [
    editData,
    hasTaskResolutionPrice,
    laneEnabled,
    lanePrices,
    pricingMode,
    promptPrice,
    t,
    taskPricingEnabled,
    scheduledDiscountError,
  ])

  const validatePricingValues = useCallback(() => {
    if (scheduledDiscountError) return false
    if (
      pricingMode === 'per-token' &&
      toNumberOrNull(promptPrice) === null &&
      laneConfigs.some(
        ({ key }) => laneEnabled[key] && hasValue(lanePrices[key])
      )
    ) {
      form.setError('ratio', {
        message: t('Input price is required before saving dependent prices.'),
      })
      return false
    }

    if (
      pricingMode === 'per-token' &&
      laneEnabled.audioOutput &&
      !hasValue(lanePrices.audioInput)
    ) {
      form.setError('audioRatio', {
        message: t('Audio output price requires an audio input price.'),
      })
      return false
    }

    if (
      taskPricingEnabled &&
      (pricingMode === 'per-request' || pricingMode === 'per-second') &&
      !hasTaskResolutionPrice
    ) {
      form.setError('price', {
        message: t('Set at least one resolution price before saving.'),
      })
      return false
    }

    return true
  }, [
    form,
    hasTaskResolutionPrice,
    laneEnabled,
    lanePrices,
    pricingMode,
    promptPrice,
    t,
    taskPricingEnabled,
    scheduledDiscountError,
  ])

  const buildSubmitData = useCallback(
    (values: ModelPricingFormValues) => {
      const data: ModelRatioData = {
        name: values.name.trim(),
        billingMode: pricingMode,
        price: values.price || '',
        ratio: values.ratio || '',
        cacheRatio: values.cacheRatio || '',
        createCacheRatio: values.createCacheRatio || '',
        completionRatio: values.completionRatio || '',
        imageRatio: values.imageRatio || '',
        audioRatio: values.audioRatio || '',
        audioCompletionRatio: values.audioCompletionRatio || '',
      }

      if (pricingMode === 'tiered_expr') {
        data.billingExpr = billingExpr
        data.requestRuleExpr = requestRuleExpr
      }

      if (
        taskPricingEnabled &&
        (pricingMode === 'per-request' || pricingMode === 'per-second')
      ) {
        const resolutionPrices: Record<string, number> = {}
        taskResolutions.forEach((resolution) => {
          const price = toNumberOrNull(taskResolutionPrices[resolution])
          if (price !== null) resolutionPrices[resolution] = price
        })
        const taskPricing: Record<string, unknown> = {
          mode: pricingMode,
          resolution_prices: resolutionPrices,
        }
        if (Object.keys(resolutionPrices).length > 0) {
          data.taskBillingPricing = JSON.stringify(taskPricing)
        }
      }

      if (
        scheduledDiscountEnabled &&
        !scheduledDiscountError
      ) {
        const percent = Number(scheduledDiscountPercent)
        data.scheduledDiscount = JSON.stringify({
          enabled: true,
          start: scheduledDiscountStart,
          end: scheduledDiscountEnd,
          discount: (100 - percent) / 100,
        })
      } else if (
        !scheduledDiscountEnabled &&
        (scheduledDiscountStart ||
          scheduledDiscountEnd ||
          scheduledDiscountPercent)
      ) {
        const percent = Number(scheduledDiscountPercent)
        if (
          scheduledDiscountStart &&
          scheduledDiscountEnd &&
          Number.isFinite(percent) &&
          percent >= 0 &&
          percent < 100
        ) {
          data.scheduledDiscount = JSON.stringify({
            enabled: false,
            start: scheduledDiscountStart,
            end: scheduledDiscountEnd,
            discount: (100 - percent) / 100,
          })
        }
      }

      return data
    },
    [
      billingExpr,
      pricingMode,
      requestRuleExpr,
      taskPricingEnabled,
      taskResolutionPrices,
      scheduledDiscountEnabled,
      scheduledDiscountEnd,
      scheduledDiscountPercent,
      scheduledDiscountStart,
      scheduledDiscountError,
    ]
  )

  useImperativeHandle(
    ref,
    () => ({
      commitDraft: async () => {
        const isValid = await form.trigger()
        if (!isValid || !validatePricingValues()) return null
        return buildSubmitData(form.getValues())
      },
    }),
    [form, validatePricingValues, buildSubmitData]
  )

  const showActions = Boolean(onSave)

  return (
    <div
      className={cn(
        'bg-background flex min-h-0 flex-1 flex-col overflow-hidden rounded-xl border',
        className
      )}
    >
      <div className='border-b p-4'>
        <div className='flex flex-wrap items-start justify-between gap-3'>
          <div className='min-w-0'>
            <h3 className='truncate text-base font-medium'>
              {isEditMode ? t('Edit model pricing') : t('Add model pricing')}
            </h3>
          </div>
        </div>
      </div>

      <Form {...form}>
        <form
          onSubmit={(event) => event.preventDefault()}
          className='flex min-h-0 flex-1 flex-col'
          autoComplete='off'
        >
          <div className='min-h-0 flex-1 overflow-y-auto p-4 pb-6'>
            <div className='grid items-start gap-4 xl:grid-cols-[minmax(0,1fr)_minmax(220px,260px)]'>
              <FieldGroup>
                {warnings.length > 0 && (
                  <Alert variant='destructive'>
                    <AlertTriangle data-icon='inline-start' />
                    <AlertDescription>
                      <div className='flex flex-col gap-1'>
                        {warnings.map((warning) => (
                          <span key={warning}>{warning}</span>
                        ))}
                      </div>
                    </AlertDescription>
                  </Alert>
                )}

                <FormField
                  control={form.control}
                  name='name'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Model name')}</FormLabel>
                      <FormControl>
                        <Input
                          placeholder={t('gpt-4')}
                          {...field}
                          disabled={isEditMode}
                        />
                      </FormControl>
                      <FormDescription>
                        {t(
                          'The exact model identifier as used in API requests.'
                        )}
                      </FormDescription>
                      <FormMessage />
                    </FormItem>
                  )}
                />

                <Tabs
                  value={pricingMode}
                  onValueChange={handleModeChange}
                  className='gap-4'
                >
                  <TabsList className='grid w-full grid-cols-2 sm:grid-cols-4'>
                    <TabsTrigger value='per-token'>
                      {t('Per-token')}
                    </TabsTrigger>
                    <TabsTrigger value='per-request'>
                      {t('Per-request')}
                    </TabsTrigger>
                    <TabsTrigger value='per-second'>
                      {t('Per-second')}
                    </TabsTrigger>
                    <TabsTrigger value='tiered_expr'>
                      {t('Expression')}
                    </TabsTrigger>
                  </TabsList>

                  <TabsContent value='per-token' className='pt-0'>
                    <FieldGroup className='gap-5'>
                      <Field>
                        <FieldLabel>{t('Input price')}</FieldLabel>
                        <PriceInput
                          value={promptPrice}
                          placeholder='3'
                          onChange={handlePromptPriceChange}
                        />
                        <FieldDescription>
                          {t('USD price per 1M input tokens.')}
                        </FieldDescription>
                      </Field>

                      <div className='grid gap-3 sm:grid-cols-[repeat(auto-fit,minmax(400px,1fr))]'>
                        {laneConfigs.map((lane) => {
                          const disabled =
                            lane.key === 'audioOutput' &&
                            (!laneEnabled.audioInput ||
                              !hasValue(lanePrices.audioInput))
                          return (
                            <PriceLane
                              key={lane.key}
                              title={t(lane.titleKey)}
                              description={t(lane.descriptionKey)}
                              placeholder={lane.placeholder}
                              value={lanePrices[lane.key]}
                              enabled={laneEnabled[lane.key]}
                              disabled={disabled}
                              onEnabledChange={(checked) =>
                                handleLaneToggle(lane.key, checked)
                              }
                              onChange={(value) =>
                                handleLanePriceChange(lane.key, value)
                              }
                            />
                          )
                        })}
                      </div>
                    </FieldGroup>
                  </TabsContent>

                  <TabsContent value='per-request' className='pt-0'>
                    <FieldGroup className='gap-5'>
                      <FormField
                        control={form.control}
                        name='price'
                        render={({ field }) => (
                          <FormItem className='contents'>
                            <Field
                              className={cn(taskPricingEnabled && 'opacity-60')}
                            >
                              <FieldLabel>{t('Fixed price')}</FieldLabel>
                              <FormControl>
                                <InputGroup>
                                  <InputGroupAddon>$</InputGroupAddon>
                                  <InputGroupInput
                                    inputMode='decimal'
                                    placeholder='0.01'
                                    {...field}
                                    disabled={taskPricingEnabled}
                                    onChange={(event) => {
                                      const value = event.target.value
                                      if (numericDraftRegex.test(value)) {
                                        field.onChange(value)
                                      }
                                    }}
                                  />
                                  <InputGroupAddon align='inline-end'>
                                    {t('per request')}
                                  </InputGroupAddon>
                                </InputGroup>
                              </FormControl>
                              <FieldDescription>
                                {t(
                                  taskPricingEnabled
                                    ? 'Disabled while resolution pricing is enabled. The default price is not used.'
                                    : 'Cost in USD per request, regardless of tokens used.'
                                )}
                              </FieldDescription>
                              <FormMessage />
                            </Field>
                          </FormItem>
                        )}
                      />
                    </FieldGroup>
                    <TaskBillingPricingFields
                      enabled={taskPricingEnabled}
                      onEnabledChange={setTaskPricingEnabled}
                      prices={taskResolutionPrices}
                      onPriceChange={handleTaskResolutionPriceChange}
                      mode='per-request'
                    />
                  </TabsContent>

                  <TabsContent value='per-second' className='pt-0'>
                    <FieldGroup className='gap-5'>
                      <FormField
                        control={form.control}
                        name='price'
                        render={({ field }) => (
                          <FormItem className='contents'>
                            <Field
                              className={cn(taskPricingEnabled && 'opacity-60')}
                            >
                              <FieldLabel>{t('Per-second price')}</FieldLabel>
                              <FormControl>
                                <InputGroup>
                                  <InputGroupAddon>$</InputGroupAddon>
                                  <InputGroupInput
                                    inputMode='decimal'
                                    placeholder='0.01'
                                    {...field}
                                    disabled={taskPricingEnabled}
                                    onChange={(event) => {
                                      const value = event.target.value
                                      if (numericDraftRegex.test(value)) {
                                        field.onChange(value)
                                      }
                                    }}
                                  />
                                  <InputGroupAddon align='inline-end'>
                                    {t('per second')}
                                  </InputGroupAddon>
                                </InputGroup>
                              </FormControl>
                              <FieldDescription>
                                {t(
                                  taskPricingEnabled
                                    ? 'Disabled while resolution pricing is enabled. The default price is not used.'
                                    : 'Cost in USD per second of generated video.'
                                )}
                              </FieldDescription>
                              <FormMessage />
                            </Field>
                          </FormItem>
                        )}
                      />
                    </FieldGroup>
                    <TaskBillingPricingFields
                      enabled={taskPricingEnabled}
                      onEnabledChange={setTaskPricingEnabled}
                      prices={taskResolutionPrices}
                      onPriceChange={handleTaskResolutionPriceChange}
                      mode='per-second'
                    />
                  </TabsContent>

                  <TabsContent value='tiered_expr' className='pt-0'>
                    <FieldGroup className='gap-5'>
                      <TieredPricingEditor
                        key={editorReloadToken}
                        modelName={watchedValues.name}
                        billingExpr={billingExpr}
                        requestRuleExpr={requestRuleExpr}
                        onBillingExprChange={setBillingExpr}
                        onRequestRuleExprChange={setRequestRuleExpr}
                      />
                    </FieldGroup>
                  </TabsContent>
                </Tabs>

                <ScheduledDiscountFields
                  enabled={scheduledDiscountEnabled}
                  onEnabledChange={setScheduledDiscountEnabled}
                  start={scheduledDiscountStart}
                  end={scheduledDiscountEnd}
                  discountPercent={scheduledDiscountPercent}
                  onStartChange={setScheduledDiscountStart}
                  onEndChange={setScheduledDiscountEnd}
                  onDiscountChange={(value) => {
                    if (numericDraftRegex.test(value)) {
                      setScheduledDiscountPercent(value)
                    }
                  }}
                />
              </FieldGroup>

              <aside className='bg-muted/20 sticky top-0 rounded-lg border'>
                <div className='border-b px-3 py-2'>
                  <div className='text-sm font-medium'>{t('Preview')}</div>
                </div>
                <div className='divide-y'>
                  {previewRows.map((row) => (
                    <div key={row.key} className='grid gap-1 px-3 py-2.5'>
                      <span className='text-muted-foreground text-xs'>
                        {row.label}
                      </span>
                      <span
                        className={cn(
                          'min-w-0 text-sm',
                          row.multiline
                            ? 'font-mono text-xs leading-5 break-words whitespace-pre-wrap'
                            : 'truncate'
                        )}
                      >
                        {row.value}
                      </span>
                    </div>
                  ))}
                  {scheduledDiscountEnabled && !scheduledDiscountError && (
                    <div className='grid gap-1 px-3 py-2.5'>
                      <span className='text-muted-foreground text-xs'>
                        {t('Scheduled discount')}
                      </span>
                      <span className='text-sm'>
                        {scheduledDiscountStart}-{scheduledDiscountEnd} · -
                        {scheduledDiscountPercent}%
                      </span>
                    </div>
                  )}
                </div>
              </aside>
            </div>
          </div>
          {showActions && (
            <div className='bg-background/95 supports-[backdrop-filter]:bg-background/80 shrink-0 border-t p-3 backdrop-blur'>
              <div className='flex flex-col-reverse gap-2 sm:flex-row sm:justify-end'>
                {onSave && (
                  <Button
                    type='button'
                    onClick={onSave}
                    disabled={isSaving}
                    className='w-full sm:w-auto'
                  >
                    <Save data-icon='inline-start' />
                    {isSaving ? t('Saving...') : t('Save model prices')}
                  </Button>
                )}
              </div>
            </div>
          )}
        </form>
      </Form>
    </div>
  )
})
