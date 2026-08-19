/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { Check, Copy, FileJson } from 'lucide-react'
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { ScrollArea } from '@/components/ui/scroll-area'
import { useCopyToClipboard } from '@/hooks/use-copy-to-clipboard'

function formatRequestBody(value: unknown): string {
  if (value == null || value === '') return ''
  if (typeof value === 'string') {
    try {
      return JSON.stringify(JSON.parse(value), null, 2)
    } catch {
      return value
    }
  }
  try {
    return JSON.stringify(value, null, 2)
  } catch {
    return String(value)
  }
}

interface RequestBodyDialogProps {
  requestBody: unknown
  requestBodyComplete?: boolean
  open: boolean
  onOpenChange: (open: boolean) => void
}

export function RequestBodyDialog({
  requestBody,
  requestBodyComplete = false,
  open,
  onOpenChange,
}: RequestBodyDialogProps) {
  const { t } = useTranslation()
  const { copiedText, copyToClipboard } = useCopyToClipboard({ notify: false })
  const formattedBody = useMemo(
    () => formatRequestBody(requestBody),
    [requestBody]
  )

  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      title={t('Request Body')}
      description={t('View the complete request body for this task')}
      contentClassName='sm:max-w-3xl'
      contentHeight='auto'
      bodyClassName='space-y-4'
    >
      <ScrollArea className='max-h-[min(70vh,700px)] pr-4'>
        <div className='space-y-2 py-4'>
          <Label className='text-sm font-semibold'>{t('Request Body')}</Label>
          {formattedBody ? (
            <>
              {!requestBodyComplete && (
                <p className='text-muted-foreground text-xs'>
                  {t(
                    'The original request body exceeded the storage limit, so a normalized request was recorded instead.'
                  )}
                </p>
              )}
              <div className='bg-muted/50 relative overflow-hidden rounded-md border'>
                <Button
                  variant='ghost'
                  size='sm'
                  className='absolute top-2 right-2 z-10 h-8 w-8 p-0'
                  onClick={() => copyToClipboard(formattedBody)}
                  title={t('Copy to clipboard')}
                >
                  {copiedText === formattedBody ? (
                    <Check className='size-4 text-green-600' />
                  ) : (
                    <Copy className='size-4' />
                  )}
                </Button>
                <pre className='max-h-[min(65vh,640px)] overflow-auto p-4 pr-12 font-mono text-xs leading-relaxed break-words whitespace-pre-wrap'>
                  {formattedBody}
                </pre>
              </div>
            </>
          ) : (
            <div className='text-muted-foreground flex items-center gap-2 rounded-md border p-4 text-sm'>
              <FileJson className='size-4' />
              {t('No request body recorded')}
            </div>
          )}
        </div>
      </ScrollArea>
    </Dialog>
  )
}
