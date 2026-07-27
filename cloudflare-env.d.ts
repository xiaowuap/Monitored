declare module "cloudflare:workers" {
  export const env: {
    DB?: D1Database;
    ASSETS?: Fetcher;
  };
}

declare global {
  interface Env {
    DB: D1Database;
    ASSETS: Fetcher;
  }
}

export {};
