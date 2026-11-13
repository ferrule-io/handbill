import { QuickOpen } from "@/components/home/quick-open"
import { SitesHome } from "@/components/home/sites-home"
import { Container, Stack } from "@/components/layout/container"

export default function SitesPage() {
  return (
    <Container>
      <Stack gap="loose">
        <SitesHome />
        <QuickOpen />
      </Stack>
    </Container>
  )
}
