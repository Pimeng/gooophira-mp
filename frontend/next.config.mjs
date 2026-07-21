/** @type {import('next').NextConfig} */
const nextConfig = {
  images: {
    unoptimized: true,
  },
  async rewrites() {
    return [{
      source: "/api/:path*",
      destination: "http://127.0.0.1:12347/api/:path*",
    }]
  },
}

export default nextConfig
