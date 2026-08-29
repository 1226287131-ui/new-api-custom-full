import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Search, Trash2, Users } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { StaticDataTable } from '@/components/data-table/static/static-data-table'
import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { searchUsers } from '@/features/users/api'
import type { User } from '@/features/users/types'

import {
  deleteGroupUserRatio,
  getGroupUserRatios,
  upsertGroupUserRatio,
} from '../api'
import type { GroupUserRatioEntry } from '../types'

type GroupUserRatioDialogProps = { group: string }

export function GroupUserRatioDialog({ group }: GroupUserRatioDialogProps) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [open, setOpen] = useState(false)
  const [keyword, setKeyword] = useState('')
  const [selectedUser, setSelectedUser] = useState<User | null>(null)
  const [ratio, setRatio] = useState('1')
  const [deleteTarget, setDeleteTarget] = useState<GroupUserRatioEntry | null>(
    null
  )

  const ratioQuery = useQuery({
    queryKey: ['group-user-ratios', group],
    queryFn: () => getGroupUserRatios(group),
    enabled: open,
  })
  const userQuery = useQuery({
    queryKey: ['group-user-ratio-users', keyword],
    queryFn: () => searchUsers({ keyword, page_size: 8, p: 1 }),
    enabled: open && keyword.trim().length > 0,
  })
  const saveMutation = useMutation({
    mutationFn: async () => {
      if (!selectedUser) throw new Error(t('Select a user first'))
      if (ratio.trim() === '') {
        throw new Error(t('Ratio must be between 0 and 1000'))
      }
      const value = Number(ratio)
      if (!Number.isFinite(value) || value < 0 || value > 1000) {
        throw new Error(t('Ratio must be between 0 and 1000'))
      }
      return upsertGroupUserRatio(group, {
        user_id: selectedUser.id,
        ratio: value,
      })
    },
    onSuccess: (data) => {
      if (!data.success) {
        toast.error(data.message || t('Failed to save user-specific ratio'))
        return
      }
      toast.success(t('User-specific ratio saved'))
      setSelectedUser(null)
      setKeyword('')
      setRatio('1')
      queryClient.invalidateQueries({ queryKey: ['group-user-ratios', group] })
    },
    onError: (error: Error) => toast.error(error.message),
  })
  const deleteMutation = useMutation({
    mutationFn: (entry: GroupUserRatioEntry) =>
      deleteGroupUserRatio(group, entry.user_id),
    onSuccess: (data) => {
      if (!data.success) {
        toast.error(data.message || t('Failed to delete user-specific ratio'))
        return
      }
      toast.success(t('User-specific ratio deleted'))
      setDeleteTarget(null)
      queryClient.invalidateQueries({ queryKey: ['group-user-ratios', group] })
    },
    onError: (error: Error) => toast.error(error.message),
  })

  const handleOpenChange = (nextOpen: boolean) => {
    setOpen(nextOpen)
    if (!nextOpen) {
      setKeyword('')
      setSelectedUser(null)
      setRatio('1')
      setDeleteTarget(null)
    }
  }

  const users = userQuery.data?.data?.items ?? []
  const entries = ratioQuery.data?.data ?? []

  return (
    <>
      <Dialog
        open={open}
        onOpenChange={handleOpenChange}
        trigger={
          <Button
            variant='ghost'
            size='sm'
            aria-label={t('Manage user-specific ratios')}
            title={t('Manage user-specific ratios')}
          >
            <Users className='h-4 w-4' />
          </Button>
        }
        title={t('User-specific ratio')}
        description={t(
          'Set a personal billing ratio for selected users in group {{group}}.',
          { group }
        )}
        contentClassName='sm:max-w-3xl'
        footer={
          <Button
            type='button'
            onClick={() => saveMutation.mutate()}
            disabled={!selectedUser || saveMutation.isPending}
          >
            {saveMutation.isPending ? t('Saving...') : t('Save ratio')}
          </Button>
        }
      >
        <div className='space-y-5'>
          <div className='space-y-2'>
            <label
              className='text-sm font-medium'
              htmlFor={`ratio-user-${group}`}
            >
              {t('User')}
            </label>
            <div className='relative'>
              <Search className='text-muted-foreground absolute top-2.5 left-2.5 h-4 w-4' />
              <Input
                id={`ratio-user-${group}`}
                value={
                  selectedUser
                    ? selectedUser.email || selectedUser.username
                    : keyword
                }
                onChange={(event) => {
                  setSelectedUser(null)
                  setKeyword(event.target.value)
                }}
                placeholder={t('Search by email, username, or user ID')}
                className='pl-8'
              />
            </div>
            {!selectedUser && keyword.trim() && (
              <div className='max-h-40 overflow-y-auto rounded-md border'>
                {users.length === 0 ? (
                  <p className='text-muted-foreground p-3 text-sm'>
                    {userQuery.isFetching
                      ? t('Searching...')
                      : t('No users found')}
                  </p>
                ) : (
                  users.map((user) => (
                    <button
                      type='button'
                      key={user.id}
                      className='hover:bg-muted flex w-full items-center justify-between px-3 py-2 text-left text-sm'
                      onClick={() => setSelectedUser(user)}
                    >
                      <span className='truncate'>
                        {user.email || user.username}
                      </span>
                      <span className='text-muted-foreground'>#{user.id}</span>
                    </button>
                  ))
                )}
              </div>
            )}
          </div>
          <div className='space-y-2'>
            <label
              className='text-sm font-medium'
              htmlFor={`ratio-value-${group}`}
            >
              {t('Ratio')}
            </label>
            <Input
              id={`ratio-value-${group}`}
              type='number'
              min={0}
              max={1000}
              step={0.01}
              value={ratio}
              onChange={(event) => setRatio(event.target.value)}
            />
            <p className='text-muted-foreground text-xs'>
              {t(
                'This rule overrides the user group ratio and group base ratio. Allowed range: 0-1000.'
              )}
            </p>
          </div>
          <div className='space-y-2'>
            <h3 className='text-sm font-medium'>{t('Configured users')}</h3>
            <StaticDataTable
              data={entries}
              getRowKey={(entry) => String(entry.user_id)}
              emptyContent={t('No user-specific ratios configured.')}
              columns={[
                {
                  id: 'user',
                  header: t('User'),
                  cell: (entry) =>
                    entry.email || entry.username || `#${entry.user_id}`,
                },
                {
                  id: 'ratio',
                  header: t('Ratio'),
                  cell: (entry) => entry.ratio,
                },
                {
                  id: 'actions',
                  header: t('Actions'),
                  className: 'text-right',
                  cellClassName: 'text-right',
                  cell: (entry) => (
                    <Button
                      variant='ghost'
                      size='sm'
                      aria-label={t('Delete')}
                      onClick={() => setDeleteTarget(entry)}
                    >
                      <Trash2 className='h-4 w-4' />
                    </Button>
                  ),
                },
              ]}
            />
          </div>
        </div>
      </Dialog>
      <ConfirmDialog
        open={deleteTarget !== null}
        onOpenChange={(value) => !value && setDeleteTarget(null)}
        title={t('Delete user-specific ratio?')}
        desc={t('This user will fall back to the normal group ratio.')}
        destructive
        isLoading={deleteMutation.isPending}
        handleConfirm={() =>
          deleteTarget && deleteMutation.mutate(deleteTarget)
        }
        confirmText={t('Delete')}
      />
    </>
  )
}
