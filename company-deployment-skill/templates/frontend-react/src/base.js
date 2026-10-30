// The app's own root on the platform, set by index.html's <base>:
// /{application}/, /run/{application}/frontend/, or / locally.
export const APP_ROOT = new URL(document.baseURI).pathname

// The router's basename: the same, without its trailing slash.
export const ROUTER_BASENAME = APP_ROOT.replace(/\/$/, '') || '/'
