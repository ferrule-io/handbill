import { CircleAlertIcon } from "lucide-react"

import { cn } from "@/lib/utils"

/**
 * The one inline error style: under a form or an action, announced to
 * screen readers. Renders nothing without children. Pass id to point a
 * field's aria-describedby at it.
 */
export function FormError({ children, className, id }: { children?: React.ReactNode; className?: string; id?: string }) {
  if (!children) return null
  return (
    <div
      id={id}
      role="alert"
      className={cn(
        "flex gap-2.5 rounded-lg border border-destructive/25 bg-destructive/8 px-3 py-2.5 text-sm leading-snug text-destructive dark:bg-destructive/12",
        className
      )}
    >
      <CircleAlertIcon className="mt-px size-4 shrink-0" aria-hidden="true" />
      <div>{children}</div>
    </div>
  )
}
