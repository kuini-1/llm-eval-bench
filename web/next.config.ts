import type { NextConfig } from "next";

// The browser only talks to this app; /api is forwarded to the Go server,
// so there is one origin and no CORS to configure.
const apiURL = process.env.EVALBENCH_API_URL ?? "http://127.0.0.1:8097";

const nextConfig: NextConfig = {
  output: "standalone",
  turbopack: {
    rules: {
      "*.css": {
        loaders: ["@tailwindcss/turbopack"],
        as: "*.css",
      },
    },
  },
  async rewrites() {
    return [
      { source: "/api/:path*", destination: `${apiURL}/api/:path*` },
      { source: "/openapi.yaml", destination: `${apiURL}/openapi.yaml` },
    ];
  },
};

export default nextConfig;
