// Only same-app paths: a `next` pointing anywhere else is ignored, so a
// crafted sign-in link can't bounce someone off to another site.
export function safeNext(next: string | null): string {
  return next && next.startsWith("/") && !next.startsWith("//") ? next : "/applications";
}
