import { LoaderCircleIcon } from "lucide-react"

import { cn } from "@/lib/utils"

/** Pending indicator; inside a button it takes the place of the leading icon. */
export function Spinner({ className, ...props }: React.ComponentProps<typeof LoaderCircleIcon>) {
  return <LoaderCircleIcon className={cn("animate-spin", className)} aria-hidden="true" {...props} />
}
