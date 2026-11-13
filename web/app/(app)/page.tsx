import { LayersIcon } from "lucide-react"

import { McpEndpoint } from "@/components/home/mcp-endpoint"
import { QuickOpen } from "@/components/home/quick-open"
import { Container, Stack } from "@/components/layout/container"
import { EmptyState } from "@/components/layout/empty-state"
import { PageHeader } from "@/components/layout/page-header"

export default function SitesPage() {
  return (
    <Container>
      <Stack gap="loose">
        <PageHeader
          eyebrow="Workspace"
          title="Your sites"
          description="Everything your agents have published, and everything shared with you."
        />
        <EmptyState icon={<LayersIcon />} title="Nothing on the press yet">
          <p>
            Connect an MCP client to this server and ask it to publish a site.
            It will appear here with a link you can share.
          </p>
          <McpEndpoint className="mt-5" />
        </EmptyState>
        <QuickOpen />
      </Stack>
    </Container>
  )
}
