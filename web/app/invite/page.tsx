import type { Metadata } from "next"

import { InviteAccept } from "@/components/auth/invite-accept"
import { AuthShell } from "@/components/layout/auth-shell"

export const metadata: Metadata = { title: "Invitation" }

// /invite#token=<token>: the token stays in the fragment so it never reaches
// server or proxy logs; it is read on the client only.
export default function InvitePage() {
  return (
    <AuthShell
      aside={
        <>
          Someone made this <em>for you.</em>
        </>
      }
    >
      <InviteAccept />
    </AuthShell>
  )
}
