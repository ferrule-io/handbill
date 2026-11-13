import type { NextConfig } from "next"

// The UI is a static export embedded in the Go binary (internal/webui).
// Every route is a fixed path; parameters travel in the query string or
// fragment, never in dynamic segments (docs/design.md, "Frontend").
const nextConfig: NextConfig = {
  output: "export",
  images: { unoptimized: true },
  turbopack: {
    rules: {
      "*.css": {
        loaders: ["@tailwindcss/turbopack"],
        as: "*.css",
      },
    },
  },
}

export default nextConfig
