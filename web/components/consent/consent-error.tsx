import { UnplugIcon } from "lucide-react"

import { AuthHeading } from "@/components/auth/parts"
import { FormError } from "@/components/ui/form-error"

// The heading says the request can't continue; this says what to do.
const RETRY = "Go back to your app and try connecting again."

/** Fixed messages for /consent?error=<code>; the page never shows request text. */
const extra: Record<string, string> = {
  client_metadata_unavailable: "The app’s registration couldn’t be fetched: try again in a minute.",
}

/** Shown when a connection request can't go on. */
export function ConsentError({ code, message }: { code?: string; message?: string }) {
  return (
    <div className="flex flex-col gap-6" data-testid="consent-error">
      <AuthHeading icon={<UnplugIcon />} tone="muted" eyebrow="Connect an app" title="This connection can’t continue">
        <p>{message ?? RETRY}</p>
        <p>Nothing was shared, and you can close this tab.</p>
      </AuthHeading>
      {code && extra[code] && <FormError>{extra[code]}</FormError>}
    </div>
  )
}
