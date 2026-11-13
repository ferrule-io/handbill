"use client"

import { useState } from "react"

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog"
import { FormError } from "@/components/ui/form-error"
import { Spinner } from "@/components/ui/spinner"
import { errorMessage } from "@/lib/api"

/**
 * The one confirmation pattern for consequential actions. The dialog stays
 * open while `onConfirm` runs (buttons disabled, action shows `pending`),
 * closes when it resolves, and shows the error inline when it throws.
 * Pass `trigger`, or control it with `open`/`onOpenChange`.
 */
export function ConfirmDialog({
  trigger,
  open: controlledOpen,
  onOpenChange,
  title,
  description,
  action,
  pending,
  tone = "destructive",
  onConfirm,
  testId,
}: {
  trigger?: React.ReactNode
  open?: boolean
  onOpenChange?: (open: boolean) => void
  title: React.ReactNode
  description: React.ReactNode
  action: string
  pending: string
  tone?: "destructive" | "brand"
  onConfirm: () => Promise<void>
  testId?: string
}) {
  const [ownOpen, setOwnOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const open = controlledOpen ?? ownOpen

  function setOpen(next: boolean) {
    if (busy) return
    if (next) setError(null)
    setOwnOpen(next)
    onOpenChange?.(next)
  }

  async function confirm(e: React.MouseEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      await onConfirm()
      setBusy(false)
      setOwnOpen(false)
      onOpenChange?.(false)
    } catch (err) {
      setError(errorMessage(err))
      setBusy(false)
    }
  }

  return (
    <AlertDialog open={open} onOpenChange={setOpen}>
      {trigger && <AlertDialogTrigger asChild>{trigger}</AlertDialogTrigger>}
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{title}</AlertDialogTitle>
          <AlertDialogDescription>{description}</AlertDialogDescription>
        </AlertDialogHeader>
        <FormError>{error}</FormError>
        <AlertDialogFooter>
          <AlertDialogCancel disabled={busy}>Cancel</AlertDialogCancel>
          <AlertDialogAction variant={tone} onClick={confirm} disabled={busy} data-testid={testId}>
            {busy && <Spinner />}
            {busy ? pending : action}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
