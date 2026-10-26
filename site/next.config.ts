import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  // The pictures in public/ are already sized and compressed as WebP.
  images: { unoptimized: true },
};

export default nextConfig;
