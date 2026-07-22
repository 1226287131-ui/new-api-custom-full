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
import { Video } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { IconBadge } from '@/components/ui/icon-badge'

interface VideoPreviewDialogProps {
  videoUrl: string
  taskId?: string
  open: boolean
  onOpenChange: (open: boolean) => void
}

export function VideoPreviewDialog({
  videoUrl,
  taskId,
  open,
  onOpenChange,
}: VideoPreviewDialogProps) {
  const { t } = useTranslation()
  const [hasError, setHasError] = useState(false)

  const handleOpenChange = (nextOpen: boolean) => {
    if (nextOpen) setHasError(false)
    onOpenChange(nextOpen)
  }

  return (
    <Dialog
      open={open}
      onOpenChange={handleOpenChange}
      title={
        <>
          <IconBadge tone='chart-4' size='sm'>
            <Video />
          </IconBadge>
          {t('Video Preview')}
        </>
      }
      description={taskId ? `${t('Task ID:')} ${taskId}` : undefined}
      contentClassName='sm:max-w-4xl'
      contentHeight='auto'
      bodyClassName='p-0'
      titleClassName='flex items-center gap-2'
    >
      <div className='flex aspect-video w-full items-center justify-center overflow-hidden rounded-md bg-black'>
        {hasError ? (
          <p className='text-sm text-white/80'>{t('Failed to load video')}</p>
        ) : (
          <video
            src={videoUrl}
            controls
            preload='metadata'
            className='h-full w-full object-contain'
            aria-label={t('Generated video')}
            onError={() => setHasError(true)}
          />
        )}
      </div>
    </Dialog>
  )
}
