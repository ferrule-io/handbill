import { FingerprintIcon, KeyRoundIcon, PlugIcon } from "lucide-react"
import type { Metadata } from "next"

import { GoogleIcon } from "@/components/auth/google-icon"
import { Container, Stack } from "@/components/layout/container"
import { PageHeader, Section } from "@/components/layout/page-header"
import { Button } from "@/components/ui/button"

export const metadata: Metadata = { title: "Security" }

export default function SecurityPage() {
  return (
    <Container>
      <Stack gap="loose">
        <PageHeader
          eyebrow="Settings"
          title="Security"
          description="How you sign in, how you get back in, and which agents can publish as you."
        />
        <Section title="Passkeys" description="Sign in with your device. No passwords, ever.">
          <Row icon={<FingerprintIcon />} title="No passkeys yet" detail="Add one from each device you use." action="Add passkey" />
        </Section>
        <Section title="Google" description="Link a Google account as a second way in.">
          <Row icon={<GoogleIcon />} title="Not linked" detail="Linking is always explicit; signing in with Google never merges accounts." action="Link Google" />
        </Section>
        <Section title="Recovery codes" description="Ten single-use codes for when every passkey is gone.">
          <Row icon={<KeyRoundIcon />} title="Codes are issued at sign-up" detail="Regenerating invalidates every earlier code." action="Regenerate" />
        </Section>
        <Section title="Connected agents" description="MCP clients you’ve authorized to publish on your behalf.">
          <Row icon={<PlugIcon />} title="No connected agents" detail="Authorize an MCP client and it will be listed here." />
        </Section>
      </Stack>
    </Container>
  )
}

function Row({
  icon,
  title,
  detail,
  action,
}: {
  icon: React.ReactNode
  title: string
  detail: string
  action?: string
}) {
  return (
    <div className="flex flex-col gap-4 rounded-2xl bg-card p-5 ring-1 ring-border sm:flex-row sm:items-center">
      <div className="grid size-10 shrink-0 place-items-center rounded-xl bg-muted text-foreground [&_svg]:size-4.5">
        {icon}
      </div>
      <div className="flex min-w-0 flex-col gap-0.5">
        <p className="text-sm font-medium">{title}</p>
        <p className="text-sm text-muted-foreground">{detail}</p>
      </div>
      {action && (
        <Button variant="outline" className="sm:ml-auto" disabled>
          {action}
        </Button>
      )}
    </div>
  )
}
