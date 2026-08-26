import { AlertTriangle } from 'lucide-react'
import { Dialog } from './dialog'
import { Button } from './button'

interface ConfirmDialogProps {
  open: boolean
  title: string
  message: string
  confirmText?: string
  cancelText?: string
  variant?: 'danger' | 'primary'
  onConfirm: () => void
  onClose: () => void
}

export function ConfirmDialog({
  open,
  title,
  message,
  confirmText = '确认',
  cancelText = '取消',
  variant = 'danger',
  onConfirm,
  onClose,
}: ConfirmDialogProps) {
  return (
    <Dialog open={open} onClose={onClose} title={title} className="max-w-md">
      <div className="space-y-4">
        <div className="flex items-start gap-3">
          {variant === 'danger' && (
            <div className="mt-0.5 flex h-9 w-9 shrink-0 items-center justify-center rounded-full bg-destructive/10">
              <AlertTriangle className="h-5 w-5 text-destructive" />
            </div>
          )}
          <p className="pt-1 text-sm text-muted-foreground">{message}</p>
        </div>
        <div className="flex justify-end gap-2">
          <Button variant="outline" onClick={onClose}>{cancelText}</Button>
          <Button
            variant={variant === 'danger' ? 'ghost' : 'default'}
            className={variant === 'danger' ? 'text-destructive hover:text-destructive' : ''}
            onClick={onConfirm}
          >
            {confirmText}
          </Button>
        </div>
      </div>
    </Dialog>
  )
}
