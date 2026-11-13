import { McpEndpoint } from "@/components/home/mcp-endpoint"
import { QuickOpen } from "@/components/home/quick-open"
import { SiteList } from "@/components/home/site-list"
import { Container, Stack } from "@/components/layout/container"
import { PageHeader } from "@/components/layout/page-header"

export default function SitesPage() {
  return (
    <Container>
      <Stack gap="loose">
        <PageHeader
          eyebrow="Workspace"
          title="Your sites"
          description="Everything your agents have published, and everything shared with you."
          actions={<McpEndpoint className="hidden md:inline-flex" />}
        />
        <SiteList />
        <QuickOpen />
      </Stack>
    </Container>
  )
}
